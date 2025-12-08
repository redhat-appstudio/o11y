package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	RegistrySuccess    *prometheus.GaugeVec
	RegistryErrorCount *prometheus.CounterVec
	RegistryDuration   *prometheus.HistogramVec
	RegistryImageSize  *prometheus.GaugeVec
}

// Structures for registryMap
type RegistryConfig struct {
	URL         string
	Credentials Credentials
}
type Credentials struct {
	Username string
	Password string
}

// Structures for loading docker config
type DockerConfig struct {
	Auths map[string]AuthConfig `json:"auths"`
}
type AuthConfig struct {
	Auth string `json:"auth"`
}

// Max retries and timeouts for operations
const maxRetries = 5
const commandTimeout = 35 * time.Second

// Scrape interval == Time between each test execution
const scrapeInterval = 5 * time.Minute

const pullArtifactPath = "/mnt/storage/pull-artifact.txt"
const pullTag = ":pull"
const metadataTag = ":metadata"

var k8sNodeName = os.Getenv("NODE_NAME")

// Target file size in bytes (10MB)
const targetFileSize = 10 * 1024 * 1024

// InitMetrics initializes and registers Prometheus metrics.
func InitMetrics(reg prometheus.Registerer, registryMap map[string]RegistryConfig) *Metrics {
	m := &Metrics{
		RegistrySuccess: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "registry_exporter_success",
				Help: "Gauge indicating if a test for the registry was successful (1 if successful, 0 otherwise).",
			},
			[]string{"tested_registry", "node", "type"},
		),
		RegistryErrorCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_exporter_error_count",
				Help: "Total number of errors encountered during tests for the registry.",
			},
			[]string{"tested_registry", "node", "type", "error"},
		),
		RegistryDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "registry_exporter_duration_seconds",
				Help:    "Histogram of durations for tests for the registry in seconds.",
				Buckets: []float64{1, 1.5, 2, 4, 6, 8, 12, 16, 24, 32},
			},
			[]string{"tested_registry", "node", "type"},
		),
		RegistryImageSize: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "registry_exporter_image_size_mbytes",
				Help: "Gauge of image size for tests for the registry in megabytes.",
			},
			[]string{"tested_registry", "node", "type"},
		),
	}
	reg.MustRegister(m.RegistrySuccess)
	reg.MustRegister(m.RegistryErrorCount)
	reg.MustRegister(m.RegistryDuration)
	reg.MustRegister(m.RegistryImageSize)

	return m
}

// ExtractErrorReason extracts the reason for an error from the output of a command.
// It is used to set the error type for the metrics.
func ExtractErrorReason(output []byte, errorMessage string) string {
	outputStr := strings.ToLower(string(output))
	errorMessageStr := strings.ToLower(errorMessage)

	// Timeout errors
	if strings.Contains(errorMessageStr, "signal: killed") {
		return "TIMEOUT"
	}
	// Probably malformed request
	if strings.Contains(outputStr, "400") || strings.Contains(outputStr, "bad") {
		return "INVALID_REQUEST"
	}
	// Issue with credentials
	if strings.Contains(outputStr, "401") || strings.Contains(outputStr, "403") || strings.Contains(outputStr, "unauthorized") {
		return "AUTHENTICATION"
	}
	// Tag deleted or not found
	if strings.Contains(outputStr, "404") || strings.Contains(outputStr, "not found") {
		return "NOT_FOUND"
	}
	// Network errors
	if strings.Contains(outputStr, "no such host") {
		return "NETWORK_ERROR"
	}
	// Server errors typically from other registry issues
	if strings.Contains(outputStr, "500") || strings.Contains(outputStr, "502") || strings.Contains(outputStr, "503") || strings.Contains(outputStr, "504") {
		return "SERVER_ERROR"
	}
	return "UNKNOWN"
}

// setSuccessState sets the success state for a test operation.
func setSuccessState(metrics *Metrics, registryType string, testType string) {
	metrics.RegistrySuccess.WithLabelValues(registryType, k8sNodeName, testType).Set(1)
}

// setErrorState sets the error state for a test operation.
func setErrorState(metrics *Metrics, registryType string, testType string, output []byte, errorMessage string) {
	errorType := ExtractErrorReason(output, errorMessage)
	metrics.RegistrySuccess.WithLabelValues(registryType, k8sNodeName, testType).Set(0)
	metrics.RegistryErrorCount.WithLabelValues(registryType, k8sNodeName, testType, errorType).Inc()
}

// getFileSizeMB returns the size of a file in megabytes.
func getFileSizeMB(filePath string) (float64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	return float64(info.Size()) / float64(1024*1024), nil
}

// setImageSize sets the image size for a test operation.
func setImageSize(metrics *Metrics, registryType string, testType string, sizeMB float64) {
	metrics.RegistryImageSize.WithLabelValues(registryType, k8sNodeName, testType).Set(sizeMB)
}

// recordDuration records the duration of a test operation in seconds.
func recordDuration(metrics *Metrics, registryType string, testType string, duration time.Duration) {
	durationSeconds := duration.Seconds()
	metrics.RegistryDuration.WithLabelValues(registryType, k8sNodeName, testType).Observe(durationSeconds)
}

// createFileOfSize creates a file of the target size with a timestamp header for uniqueness.
// It is used to create the pull and push test artifacts.
// Note: Inspired by https://stackoverflow.com/questions/16797380/how-to-create-a-10mb-file-filled-with-000000-data-in-golang
func createFileOfSize(filePath string, artifactType string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	timeStamp := time.Now()
	header := fmt.Sprintf("%s test artifact created at %s\n", artifactType, timeStamp.String())
	_, err = file.WriteString(header)
	if err != nil {
		return err
	}

	err = file.Truncate(targetFileSize)
	if err != nil {
		return err
	}

	// Writing a byte at the end to ensure the file is not sparse as in the thread.
	_, err = file.Seek(targetFileSize-1, 0)
	if err != nil {
		return err
	}
	_, err = file.Write([]byte{0})
	if err != nil {
		return err
	}

	return nil
}

func executeCmdWithRetry(args []string) (output []byte, err error) {
	for attempt := range maxRetries {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)

		cmd := exec.CommandContext(ctx, "oras", args...)
		output, err = cmd.CombinedOutput()
		cancel()

		if err == nil {
			return output, nil
		}

		if attempt+1 < maxRetries {
			backoff_duration := time.Duration(math.Pow(2, float64(attempt))) * time.Second

			log.Printf("Command attempt %d failed: %v, output: %s", attempt+1, err, string(output))
			log.Printf("Retrying in %v...", backoff_duration)
			time.Sleep(backoff_duration)
		} else {
			return output, err
		}
	}
	return output, err
}

func loadDockerConfig() (DockerConfig, error) {
	configPath := filepath.Join(os.Getenv("DOCKER_CONFIG"), "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return DockerConfig{}, fmt.Errorf("failed to read Docker config file %s: %w", configPath, err)
	}

	var dockerConfig DockerConfig
	if err := json.Unmarshal(data, &dockerConfig); err != nil {
		return DockerConfig{}, fmt.Errorf("failed to unmarshal Docker config: %w", err)
	}

	return dockerConfig, nil
}

// extractCredentialsFromAuth decodes the base64 auth string and splits it into username and password
func extractCredentialsForRegistry(auth string) (username, password string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode base64 auth string: %w", err)
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid auth format: expected username:password")
	}

	return parts[0], parts[1], nil
}

func extractCredentials(dockerConfig DockerConfig, registry string) (username, password string, err error) {
	for key, authConfig := range dockerConfig.Auths {
		if strings.Contains(key, registry) {
			username, password, err := extractCredentialsForRegistry(authConfig.Auth)
			if err != nil {
				return "", "", fmt.Errorf("failed to extract credentials for %s: %w", key, err)
			}
			return username, password, nil
		}
	}

	return "", "", fmt.Errorf("credentials not found in Docker config")
}

func PrepareRegistryMap() map[string]RegistryConfig {
	quayUrl := os.Getenv("QUAY_URL")
	if quayUrl == "" {
		log.Panicf("QUAY_URL environment variable is required")
	}

	dockerConfig, err := loadDockerConfig()
	if err != nil {
		log.Panicf("Failed to load Docker config: %v", err)
	}

	username, password, err := extractCredentials(dockerConfig, "quay.io") // hardcoded for quay.io only, could serve as template for more in future
	if err != nil {
		log.Panicf("Failed to extract credentials: %v", err)
	}

	registryMap := map[string]RegistryConfig{
		"quay.io": {
			URL: quayUrl,
			Credentials: Credentials{
				Username: username,
				Password: password,
			},
		},
	}

	return registryMap
}

func CreatePullTag(registryMap map[string]RegistryConfig, registryType string, skipCheckExisting bool) {
	registryName := registryMap[registryType].URL
	registryName += pullTag

	var args []string
	if !skipCheckExisting {
		// Check if the tag already exists in the registry
		args = []string{"pull", registryName, "--output", pullArtifactPath}
		if _, err := executeCmdWithRetry(args); err == nil {
			// Check the size of the existing artifact
			existingSizeMB, err := getFileSizeMB(pullArtifactPath)
			if err == nil {
				targetFileSizeMB := float64(targetFileSize) / float64(1024*1024)
				if math.Abs(existingSizeMB-targetFileSizeMB) < 0.01 {
					log.Printf("Pull tag %s for %s already exists with size %.2f MB, skipping creation.", pullTag, registryType, existingSizeMB)
					return
				}
				log.Printf("Pull tag %s for %s already exists with size %.2f MB (!= %.2f MB), trying to push new version...", pullTag, registryType, existingSizeMB, targetFileSizeMB)
			}
		}
	}

	err := createFileOfSize(pullArtifactPath, "Pull")
	if err != nil {
		log.Panicf("Failed to create artifact: %v", err)
	}

	args = []string{"push", registryName, "--disable-path-validation", pullArtifactPath}
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Pull tag creation failed: %v, output: %s", err, string(output))
		return
	}

	log.Printf("Pull tag %s for %s created successfully.", pullTag, registryType)
}

func PullTest(metrics *Metrics, registryMap map[string]RegistryConfig, registryType string) {
	registryName := registryMap[registryType].URL
	registryName += pullTag

	args := []string{"pull", registryName, "--output", pullArtifactPath}
	startTime := time.Now()
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Pull test failed: %v, output: %s", err, string(output))
		// Edge case that the pullTag does not exist anymore, registry error otherwise
		if !strings.Contains(string(output), "not found") {
			setErrorState(metrics, registryType, "pull", output, err.Error())
			return
		}
		log.Printf("Pull tag %s for %s not found, creating it.", pullTag, registryType)
		CreatePullTag(registryMap, registryType, true)
		// Retry the pull operation after re-creating the tag
		if output, err = executeCmdWithRetry(args); err != nil {
			log.Printf("Pull test failed after re-creating tag: %v, output: %s", err, string(output))
			setErrorState(metrics, registryType, "pull", output, err.Error())
			return
		}
	}
	log.Printf("Pull test for registry type %s successful.", registryType)

	recordDuration(metrics, registryType, "pull", time.Since(startTime))

	if sizeMB, err := getFileSizeMB(pullArtifactPath); err == nil {
		setImageSize(metrics, registryType, "pull", sizeMB)
	}

	setSuccessState(metrics, registryType, "pull")
}

func PushTest(metrics *Metrics, registryMap map[string]RegistryConfig, registryType string) {
	registryName := registryMap[registryType].URL
	registryName += ":push-" + os.Getenv("HOSTNAME")

	artifactPaths := []string{
		"/mnt/storage/push-artifact-1.txt",
		"/mnt/storage/push-artifact-2.txt",
	}

	for i, file := range artifactPaths {
		err := createFileOfSize(file, fmt.Sprintf("Push test artifact %d", i+1))
		if err != nil {
			log.Panicf("Failed to create artifact %d: %v", i+1, err)
		}
	}

	args := []string{"push", registryName, "--annotation", "quay.expires-after=30s", "--disable-path-validation"}
	args = append(args, artifactPaths...)

	startTime := time.Now()
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Push test failed: %v, output: %s", err, string(output))
		setErrorState(metrics, registryType, "push", output, err.Error())
		return
	}
	log.Printf("Push test for registry type %s successful.", registryType)

	sizeMB := 0.0
	for _, file := range artifactPaths {
		if fileSizeMB, err := getFileSizeMB(file); err == nil {
			sizeMB += fileSizeMB
		}
	}
	recordDuration(metrics, registryType, "push", time.Since(startTime))

	setImageSize(metrics, registryType, "push", sizeMB)

	setSuccessState(metrics, registryType, "push")
}

// Note: deleteArtifact is not directly possible with oras, but possible with override of existing tag with one that can have expiration annotation
func deleteArtifact(registryMap map[string]RegistryConfig, registryType string, tag string) {
	registryName := registryMap[registryType].URL
	registryName += tag

	args := []string{"push", registryName, "--annotation", "quay.expires-after=10s", "--disable-path-validation", "/mnt/storage/pull-artifact.txt"}
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Artifact deletion failed: %v, output: %s", err, string(output))
		return
	}
	log.Printf("Artifact %s deleted successfully.", tag)
}

func MetadataTest(metrics *Metrics, registryMap map[string]RegistryConfig, registryType string) {
	registryName := registryMap[registryType].URL
	sourceArtifact := registryName + pullTag

	newTag := metadataTag + "-" + os.Getenv("HOSTNAME")

	args := []string{"tag", sourceArtifact, strings.TrimPrefix(newTag, ":")}

	startTime := time.Now()
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Tag creation test failed: %v, output: %s", err, string(output))
		setErrorState(metrics, registryType, "metadata", output, err.Error())
		return
	}
	log.Printf("Metadata test for registry type %s successful.", registryType)

	recordDuration(metrics, registryType, "metadata", time.Since(startTime))

	setSuccessState(metrics, registryType, "metadata")
}

func AuthenticationTest(metrics *Metrics, registryMap map[string]RegistryConfig, registryType string) {
	credentials := registryMap[registryType].Credentials

	startTime := time.Now()
	args := []string{"login", registryType, "--username", credentials.Username, "--password", credentials.Password, "--registry-config", "/mnt/storage/authtest-config.json"} // new path needed not to overwrite the original config.json
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Authentication test failed: %v, output: %s", err, string(output))
		setErrorState(metrics, registryType, "authentication", output, err.Error())
		return
	}
	log.Printf("Authentication test for registry type %s successful.", registryType)

	recordDuration(metrics, registryType, "authentication", time.Since(startTime))

	setSuccessState(metrics, registryType, "authentication")
}

func main() {
	// Setting up logging prefix so the logs are easier to identify
	var ScrapeID int = 0
	log.SetPrefix(fmt.Sprintf("[ScrapeID:%d] ", ScrapeID))
	log.SetOutput(os.Stderr)

	if k8sNodeName == "" {
		log.Fatalf("FATAL: Missing NODE_NAME environment variable. Metrics must always have node label.")
	}
	registryMap := PrepareRegistryMap()

	reg := prometheus.NewRegistry()
	metrics := InitMetrics(reg, registryMap)

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	go func() {
		log.Println("Prometheus exporter starting on :9101/metrics...")
		if err := http.ListenAndServe(":9101", nil); err != nil {
			log.Fatalf("FATAL: Error starting Prometheus HTTP server: %v", err)
		}
		log.Println("curl http://localhost:9101/metrics")
	}()

	for registryType := range registryMap {
		log.Printf("Preparing pull tag %s for registry type: %s", pullTag, registryType)
		go CreatePullTag(registryMap, registryType, false)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Received interrupt signal, deleting metadata tags and gracefully exiting...")
		// Delete the metadata tag because tag function cannot insert expiration annotation
		for registryType := range registryMap {
			deleteArtifact(registryMap, registryType, metadataTag+"-"+os.Getenv("HOSTNAME"))
		}
		os.Exit(0)
	}()

	// Start a ticker to run tests at regular intervals
	log.Printf("Starting periodic metrics fetch every %v.", scrapeInterval)

	ticker := time.NewTicker(scrapeInterval)
	defer ticker.Stop()

	for range ticker.C {
		ScrapeID++
		log.SetPrefix(fmt.Sprintf("[ScrapeID:%d] ", ScrapeID))

		log.Println("Scheduled scrape, running tests...")
		for registryType := range registryMap {
			log.Printf("Processing test for registry type: %s", registryType)
			go PullTest(metrics, registryMap, registryType)
			go PushTest(metrics, registryMap, registryType)
			go MetadataTest(metrics, registryMap, registryType)
			go AuthenticationTest(metrics, registryMap, registryType)
		}
	}
}
