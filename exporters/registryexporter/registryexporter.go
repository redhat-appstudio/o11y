// Package main implements a registry exporter for Prometheus metrics.
package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert/yaml"
)

type Metrics struct {
	RegistryTestUp         *prometheus.GaugeVec
	RegistryPullCount      *prometheus.CounterVec
	RegistryTotalPullCount *prometheus.CounterVec
	RegistryPushCount      *prometheus.CounterVec
	RegistryTotalPushCount *prometheus.CounterVec
}

// authBase64 is used to parse the "auth" field from docker config JSON.
type authBase64 struct {
	Auth string `json:"auth"`
}

// ConfigJSON represents the structure of docker config JSON.
type ConfigJSON struct {
	Auths map[string]authBase64 `json:"auths"`
}

// Secret represents a Kubernetes secret containing docker config.
type Secret struct {
	Data map[string]string `yaml:"data"`
}

func InitPodmanLogin(registryType string) {
	log.Print("Try logging into registry...")

	filePath := os.Getenv("DOCKERCFG_PATH")
	if filePath == "" {
		log.Panicf("DOCKERCFG_PATH environment variable is not set")
		return
	}

	var secret Secret
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("failed to read docker config file at %s: %v", filePath, err)
		return
	}

	if err := yaml.Unmarshal(fileBytes, &secret); err != nil {
		log.Printf("failed to unmarshal docker config yaml: %v", err)
		return
	}

	dockerConfigB64, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		log.Printf(".dockerconfigjson not found in secret data")
		return
	}

	dockerConfigJSONBytes, err := base64.StdEncoding.DecodeString(dockerConfigB64)
	if err != nil {
		log.Printf("failed to decode .dockerconfigjson: %v", err)
		return
	}
	// Note: At this point, this is how would the usual podman authfile look like, maybe it can be used directly?
	// ...

	var configJSON ConfigJSON
	if err := json.Unmarshal(dockerConfigJSONBytes, &configJSON); err != nil {
		log.Printf("failed to unmarshal docker config JSON: %v", err)
		return
	}

	// Extract the first auth token found in the file
	// Is there a better way for that? :thinking:
	var registryAuthToken string
	for _, auth := range configJSON.Auths {
		registryAuthToken = auth.Auth
		break
	}
	if registryAuthToken == "" {
		log.Printf("auth token not found in the docker config file")
		return
	}

	decodedAuth, err := base64.StdEncoding.DecodeString(registryAuthToken)
	if err != nil {
		log.Printf("failed to decode registry auth token: %v", err)
		return
	}

	decodedAuthParts := strings.SplitN(string(decodedAuth), ":", 2)
	if len(decodedAuthParts) != 2 {
		log.Printf("invalid registry auth token format")
		return
	}

	loginCmd := exec.Command("podman", "login", "--username", decodedAuthParts[0], "--password", decodedAuthParts[1], registryType)
	loginOutput, loginErr := loginCmd.CombinedOutput()
	if loginErr != nil {
		log.Panicf("Registry login failed: %v, output: %s", loginErr, string(loginOutput))
	}
	log.Print("Registry login successful.")
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
	dockerfileContent := `FROM busybox:glibc

# Add a build-time timestamp to force uniqueness and avoid layer caching
ARG BUILD_TIMESTAMP
ENV BUILD_TIMESTAMP=${BUILD_TIMESTAMP}

LABEL quay.expires-after="1m"

# Example: touch a file with the timestamp
RUN echo "${BUILD_TIMESTAMP}" > /timestamp.txt
`

	// PVC mountpoint expected e.g.: /mnt/data/
	filePath := os.Getenv("DOCKERFILE_PATH")
	if filePath == "" {
		log.Panicf("DOCKERFILE_PATH environment variable is not set")
		return
	}

	if err := os.WriteFile(filePath+"Dockerfile", []byte(dockerfileContent), 0644); err != nil {
		log.Panicf("Failed to create Dockerfile: %v", err)
	}
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
	buildCmd := exec.Command("podman", "build", "-t", imageName, "--build-arg", "BUILD_TIMESTAMP="+buildTimestamp, "-f", os.Getenv("DOCKERFILE_PATH")+"Dockerfile", ".")
	buildOutput, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		log.Printf("Image build failed: %v, output: %s", buildErr, string(buildOutput))
		return
	}
	log.Print("Image build successful.")

	// For push, assume podman login is handled externally or via entrypoint script
	pushCmd := exec.Command("podman", "push", imageName)
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

	InitPodmanLogin(registryType)
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
