package main

import (
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
	RegistryPullCount      *prometheus.CounterVec
	RegistryTotalPullCount *prometheus.CounterVec
	RegistryPushCount      *prometheus.CounterVec
	RegistryTotalPushCount *prometheus.CounterVec
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

// Max retries for operations
const maxRetries = 5

// Scrape interval == Time between each test execution
const scrapeInterval = 1 * time.Minute

const pullArtifactPath = "/mnt/storage/pull-artifact.txt"
const pullTag = ":pull"
const metadataTag = ":metadata"

// InitMetrics initializes and registers Prometheus metrics.
func InitMetrics(reg prometheus.Registerer, registryMap map[string]RegistryConfig) *Metrics {
	m := &Metrics{
		RegistryPullCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_exporter_successful_pull_count",
				Help: "Total number of successful pulls from the registry.",
			},
			[]string{"tested_registry"},
		),
		RegistryTotalPullCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_exporter_total_pull_count",
				Help: "Total number of pulls from the registry.",
			},
			[]string{"tested_registry"},
		),
		RegistryPushCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_exporter_successful_push_count",
				Help: "Total number of successful pushes to the registry.",
			},
			[]string{"tested_registry"},
		),
		RegistryTotalPushCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_exporter_total_push_count",
				Help: "Total number of pushes to the registry.",
			},
			[]string{"tested_registry"},
		),
	}
	reg.MustRegister(m.RegistryPullCount)
	reg.MustRegister(m.RegistryTotalPullCount)
	reg.MustRegister(m.RegistryPushCount)
	reg.MustRegister(m.RegistryTotalPushCount)

	for registryType := range registryMap {
		m.RegistryPullCount.WithLabelValues(registryType).Add(0)
		m.RegistryTotalPullCount.WithLabelValues(registryType).Add(0)
		m.RegistryPushCount.WithLabelValues(registryType).Add(0)
		m.RegistryTotalPushCount.WithLabelValues(registryType).Add(0)
	}

	return m
}

func executeCmdWithRetry(args []string) (output []byte, err error) {
	for attempt := range maxRetries {
		cmd := exec.Command("oras", args...)
		output, err = cmd.CombinedOutput()
		if err == nil {
			return output, nil
		}

		if attempt+1 < maxRetries {
			backoff_duration := time.Duration(math.Pow(2, float64(attempt))) * time.Second

			log.Printf("Command attempt %d failed: %v, output: %s. Retrying in %v...", attempt+1, err, string(output), backoff_duration)
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
			log.Printf("Pull tag %s for %s already exists, skipping creation.", pullTag, registryType)
			return
		}
	}

	timeStamp := time.Now()
	artifactContent := []byte("Pull test artifact created at " + timeStamp.String())
	err := os.WriteFile(pullArtifactPath, artifactContent, 0644)
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
	defer metrics.RegistryTotalPullCount.WithLabelValues(registryType).Inc()

	registryName := registryMap[registryType].URL
	registryName += pullTag

	args := []string{"pull", registryName, "--output", pullArtifactPath}
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Pull test failed: %v, output: %s", err, string(output))
		// Edge case that the pullTag does not exist anymore, registry error otherwise
		if !strings.Contains(string(output), "not found") {
			return
		}
		log.Printf("Pull tag %s for %s not found, creating it.", pullTag, registryType)
		CreatePullTag(registryMap, registryType, true)
		// Retry the pull operation after re-creating the tag
		if output, err = executeCmdWithRetry(args); err != nil {
			log.Printf("Pull test failed after re-creating tag: %v, output: %s", err, string(output))
			return
		}
	}
	log.Printf("Pull test for registry type %s successful.", registryType)

	metrics.RegistryPullCount.WithLabelValues(registryType).Inc()
}

func PushTest(metrics *Metrics, registryMap map[string]RegistryConfig, registryType string) {
	defer metrics.RegistryTotalPushCount.WithLabelValues(registryType).Inc()

	registryName := registryMap[registryType].URL
	registryName += ":push-" + os.Getenv("HOSTNAME")

	timeStamp := time.Now()

	artifactPaths := []string{
		"/mnt/storage/push-artifact-1.txt",
		"/mnt/storage/push-artifact-2.txt",
		"/mnt/storage/push-artifact-3.txt",
	}

	contents := []string{
		"Push test artifact 1 created at " + timeStamp.String(),
		"Push test artifact 2 created at " + timeStamp.String(),
		"Push test artifact 3 created at " + timeStamp.String(),
	}

	for i, file := range artifactPaths {
		err := os.WriteFile(file, []byte(contents[i]), 0644)
		if err != nil {
			log.Panicf("Failed to create artifact %d: %v", i+1, err)
		}
	}

	args := []string{"push", registryName, "--annotation", "quay.expires-after=30s", "--disable-path-validation"}
	args = append(args, artifactPaths...)

	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Push test failed: %v, output: %s", err, string(output))
		return
	}
	log.Printf("Push test for registry type %s successful.", registryType)

	metrics.RegistryPushCount.WithLabelValues(registryType).Inc()
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
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Tag creation test failed: %v, output: %s", err, string(output))
		return
	}

	log.Printf("Metadata test for registry type %s successful.", registryType)
}

func AuthenticationTest(metrics *Metrics, registryMap map[string]RegistryConfig, registryType string) {
	credentials := registryMap[registryType].Credentials

	args := []string{"login", registryType, "--username", credentials.Username, "--password", credentials.Password, "--registry-config", "/mnt/storage/authtest-config.json"} // new path needed not to overwrite the original config.json
	if output, err := executeCmdWithRetry(args); err != nil {
		log.Printf("Authentication test failed: %v, output: %s", err, string(output))
		return
	}
	log.Printf("Authentication test for registry type %s successful.", registryType)
}

func main() {
	log.SetOutput(os.Stderr)

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
