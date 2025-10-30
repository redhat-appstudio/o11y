package main

import (
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strings"
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

// Max retries for operations
const maxRetries = 5

// Scrape interval == Time between each test execution
const scrapeInterval = 1 * time.Minute

const pullArtifactPath = "/mnt/storage/pull-artifact.txt"
const pullTag = ":pull"

// InitMetrics initializes and registers Prometheus metrics.
func InitMetrics(reg prometheus.Registerer, registryMap map[string]string) *Metrics {
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

func PrepareRegistryMap() map[string]string {
	quayUrl := os.Getenv("QUAY_URL")

	if quayUrl == "" {
		log.Panicf("QUAY_URL environment variable is required")
	}

	return map[string]string{
		"quay.io": quayUrl,
	}
}

func CreatePullTag(registryMap map[string]string, registryType string, skipCheckExisting bool) {
	registryName := registryMap[registryType]
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

func PullTest(metrics *Metrics, registryMap map[string]string, registryType string) {
	defer metrics.RegistryTotalPullCount.WithLabelValues(registryType).Inc()

	registryName := registryMap[registryType]
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

func PushTest(metrics *Metrics, registryMap map[string]string, registryType string) {
	defer metrics.RegistryTotalPushCount.WithLabelValues(registryType).Inc()

	registryName := registryMap[registryType]
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

func ManifestTests(metrics *Metrics, registryMap map[string]string, registryType string) {
	// TODO: Implement manifest test
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
			// go ManifestTests(metrics, registryMap, registryType)
		}
	}
}
