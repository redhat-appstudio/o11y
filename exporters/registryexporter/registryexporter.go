package main

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	RegistryTestUp         *prometheus.GaugeVec
	RegistryPullCount      *prometheus.CounterVec
	RegistryTotalPullCount *prometheus.CounterVec
	RegistryPushCount      *prometheus.CounterVec
	RegistryTotalPushCount *prometheus.CounterVec
}

// Max retries for the pull test initialisation
const maxRetries = 5

// Scrape interval == Time between each test execution
const scrapeInterval = 1 * time.Minute

// InitMetrics initializes and registers Prometheus metrics.
func InitMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RegistryTestUp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "registry_test_up",
				Help: "A simple gauge to indicate if the registryType registry is accessible (1 for up).",
			},
			[]string{"registryType"},
		),
		RegistryPullCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_successful_pull_count",
				Help: "Total number of successful pulls from the registry.",
			},
			[]string{"registryType"},
		),
		RegistryTotalPullCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_total_pull_count",
				Help: "Total number of pulls from the registry.",
			},
			[]string{"registryType"},
		),
		RegistryPushCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_successful_push_count",
				Help: "Total number of successful pushes to the registry.",
			},
			[]string{"registryType"},
		),
		RegistryTotalPushCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_total_push_count",
				Help: "Total number of pushes to the registry.",
			},
			[]string{"registryType"},
		),
	}
	reg.MustRegister(m.RegistryTestUp)
	reg.MustRegister(m.RegistryPullCount)
	reg.MustRegister(m.RegistryTotalPullCount)
	reg.MustRegister(m.RegistryPushCount)
	reg.MustRegister(m.RegistryTotalPushCount)
	return m
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

func PreparePullTest(registryMap map[string]string, registryType string) {
	registryName := registryMap[registryType]
	registryName += ":pull" // TODO: Add tag management

	timeStamp := time.Now()
	artifactContent := []byte("Pull test artifact created at " + timeStamp.String())
	err := os.WriteFile("/mnt/storage/pull-artifact.txt", artifactContent, 0644)
	if err != nil {
		log.Panicf("Failed to create artifact: %v", err)
		return
	}

	for attempt := range maxRetries {
		cmd := exec.Command("oras", "push", registryName,
			"--disable-path-validation", // Using /mnt/storage/ dir now, so disable absolute path validation
			"/mnt/storage/pull-artifact.txt")

		outputPush, errPush := cmd.CombinedOutput()
		if errPush == nil {
			break
		}

		if attempt+1 < maxRetries {
			log.Printf("Pull preparation attempt %d failed: %v, output: %s. Retrying...", attempt+1, errPush, string(outputPush))
			time.Sleep(time.Second)
		} else {
			log.Panicf("Pull preparation failed after %d attempts: %v, output: %s", maxRetries, errPush, string(outputPush))
		}
	}

	cmdRm := exec.Command("rm", "/mnt/storage/pull-artifact.txt")
	outputRm, errRm := cmdRm.CombinedOutput()
	if errRm != nil {
		log.Panicf("Cleanup failed: %v, output: %s", errRm, string(outputRm))
		return
	}

	log.Printf("Pull preparation for registry type %s successful.", registryType)
}

func PullTest(metrics *Metrics, registryMap map[string]string, registryType string) {
	defer metrics.RegistryTotalPullCount.WithLabelValues(registryType).Inc()

	registryName := registryMap[registryType]
	registryName += ":pull" // TODO: Add tag management

	// Expects to download /mnt/storage/pull-artifact.txt
	cmd := exec.Command("oras", "pull", registryName, "--output", "/mnt/storage")
	outputPull, errPull := cmd.CombinedOutput()
	if errPull != nil {
		log.Printf("Pull test failed: %v, output: %s", errPull, string(outputPull))
		return
	}

	cmdRm := exec.Command("rm", "/mnt/storage/pull-artifact.txt")
	outputRm, errRm := cmdRm.CombinedOutput()
	if errRm != nil {
		log.Panicf("Cleanup failed: %v, output: %s", errRm, string(outputRm))
		return
	}
	log.Printf("Pull test for registry type %s successful.", registryType)

	metrics.RegistryPullCount.WithLabelValues(registryType).Inc()
}

func PushTest(metrics *Metrics, registryMap map[string]string, registryType string) {
	defer metrics.RegistryTotalPushCount.WithLabelValues(registryType).Inc()

	registryName := registryMap[registryType]
	registryName += ":push" // TODO: Add tag management

	// Create a simple unique artifact to push
	timeStamp := time.Now()
	artifactContent := []byte("Push test artifact created at " + timeStamp.String())
	err := os.WriteFile("/mnt/storage/push-artifact.txt", artifactContent, 0644)
	if err != nil {
		log.Panicf("Failed to create artifact: %v", err)
		return
	}

	// Push the artifact to the registry
	cmd := exec.Command("oras", "push", registryName,
		"--annotation", "quay.expires-after=30s",
		"--disable-path-validation", // Using /mnt/storage/ dir now, so disable absolute path validation
		"/mnt/storage/push-artifact.txt")
	outputPush, errPush := cmd.CombinedOutput()
	if errPush != nil {
		log.Printf("Push test failed: %v, output: %s", errPush, string(outputPush))
		return
	}

	cmdRm := exec.Command("rm", "/mnt/storage/push-artifact.txt")
	outputRm, errRm := cmdRm.CombinedOutput()
	if errRm != nil {
		log.Panicf("Cleanup failed: %v, output: %s", errRm, string(outputRm))
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

	reg := prometheus.NewRegistry()
	metrics := InitMetrics(reg)

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	go func() {
		log.Println("Prometheus exporter starting on :9101/metrics...")
		if err := http.ListenAndServe(":9101", nil); err != nil {
			log.Fatalf("FATAL: Error starting Prometheus HTTP server: %v", err)
		}
		log.Println("curl http://localhost:9101/metrics")
	}()

	registryMap := PrepareRegistryMap()

	for registryType := range registryMap {
		metrics.RegistryTestUp.WithLabelValues(registryType).Set(1)
		defer metrics.RegistryTestUp.WithLabelValues(registryType).Set(0)
		go PreparePullTest(registryMap, registryType)
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
