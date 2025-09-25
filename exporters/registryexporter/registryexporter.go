// Package main implements a registry exporter for Prometheus metrics.
package main

import (
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

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

// InitMetrics initializes and registers Prometheus metrics.
func InitMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RegistryTestUp: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "registry_test_up",
				Help: "A simple gauge to indicate if the registryType is accessible (1 for up).",
			},
			[]string{"registryType"},
		),
		RegistryPullCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_successful_pull_count",
				Help: "Total number of successful image pulls.",
			},
			[]string{"registryType"},
		),
		RegistryTotalPullCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_total_pull_count",
				Help: "Total number of image pulls.",
			},
			[]string{"registryType"},
		),
		RegistryPushCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_successful_push_count",
				Help: "Total number of successful image pushes.",
			},
			[]string{"registryType"},
		),
		RegistryTotalPushCount: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "registry_total_push_count",
				Help: "Total number of image pushes.",
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

var registryTypes = map[string]string{
	"quay.io":                "quay.io/redhat-user-workloads/rh-ee-tbehal-tenant/test-component",
	"images.paas.redhat.com": "images.paas.redhat.com/o11y/todo",
}

func ImagePullTest(metrics *Metrics, registryType string) {
	defer metrics.RegistryTotalPullCount.WithLabelValues(registryType).Inc()

	imageName, ok := registryTypes[registryType]
	if !ok {
		log.Printf("Unknown registry type: %s", registryType)
		return
	}
	imageName += ":pull" // TODO: Add tag management
	log.Print("Starting Image Pull Test...")
	cmd := exec.Command("podman", "pull", imageName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Image pull failed: %v, output: %s", err, string(output))
		return
	}
	log.Print("Image pull successful.")
	metrics.RegistryPullCount.WithLabelValues(registryType).Inc()
}

// TODO: This works only locally, probably needs to be adapted for cluster use
func CreateDockerfile() {
	log.Printf("Creating Dockerfile...")
	dockerfileContent := `FROM busybox:glibc

# Add a build-time timestamp to force uniqueness and avoid layer caching
ARG BUILD_TIMESTAMP
ENV BUILD_TIMESTAMP=${BUILD_TIMESTAMP}

LABEL quay.expires-after="1m"

# Example: touch a file with the timestamp
# RUN echo "${BUILD_TIMESTAMP}" > /timestamp.txt
`

	// PVC mountpoint expected e.g.: /mnt/data/
	dockerfilePath := filepath.Join(os.Getenv("DOCKERFILE_PATH"), "Dockerfile")
	if dockerfilePath == "" {
		log.Panicf("DOCKERFILE_PATH environment variable is not set")
		return
	}

	if err := os.WriteFile(dockerfilePath, []byte(dockerfileContent), 0644); err != nil {
		log.Panicf("Failed to create Dockerfile: %v", err)
	}
	log.Printf("Dockerfile created at %s", dockerfilePath)
}

func ImagePushTest(metrics *Metrics, registryType string) {
	defer metrics.RegistryTotalPushCount.WithLabelValues(registryType).Inc()

	imageName, ok := registryTypes[registryType]
	if !ok {
		log.Printf("Unknown registry type: %s", registryType)
		return
	}
	imageName += ":push" // TODO: Add tag management
	log.Print("Starting Image Push Test...")

	// Build image with podman
	buildTimestamp := os.Getenv("BUILD_TIMESTAMP")
	if buildTimestamp == "" {
		buildTimestamp = "now"
	}
	buildCmd := exec.Command("podman", "build", "-t", imageName, "--build-arg", "BUILD_TIMESTAMP="+buildTimestamp, "-f", os.Getenv("DOCKERFILE_PATH")+"Dockerfile", ".", "--no-cache")
	buildOutput, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		log.Printf("Image build failed: %v, output: %s", buildErr, string(buildOutput))
		return
	}
	log.Print("Image build successful.")

	authfilePath := os.Getenv("DOCKERCFG_PATH")
	if authfilePath == "" {
		log.Panicf("DOCKERCFG_PATH environment variable is not set")
		return
	}
	// For push secret is mounted and used on the fly
	pushCmd := exec.Command("podman", "push", "--authfile", authfilePath, imageName)
	pushOutput, pushErr := pushCmd.CombinedOutput()
	if pushErr != nil {
		log.Printf("Image push failed: %v, output: %s", pushErr, string(pushOutput))
		return
	}
	log.Print("Image push successful.")
	metrics.RegistryPushCount.WithLabelValues(registryType).Inc()
}

func ImageManifestTest(metrics *Metrics) {
	// TODO: Implement manifest test
}

func main() {
	log.SetOutput(os.Stderr)

	registryType := "quay.io"

	reg := prometheus.NewRegistry()
	metrics := InitMetrics(reg)

	CreateDockerfile()

	metrics.RegistryTestUp.WithLabelValues(registryType).Set(1)

	handler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Metrics endpoint hit, running tests...")
		go ImagePullTest(metrics, registryType)
		go ImagePushTest(metrics, registryType)
		handler.ServeHTTP(w, r)

		// TODO: Figure out where to increment these
		// metrics.RegistryTotalPullCount.WithLabelValues(registryType).Inc()
		// metrics.RegistryTotalPushCount.WithLabelValues(registryType).Inc()
	})

	log.Println("http://localhost:9101/metrics")
	log.Fatal(http.ListenAndServe(":9101", nil))
}
