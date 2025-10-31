package main

import (
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrepareRegistryMap(t *testing.T) {
	tests := []struct {
		name      string
		quayURL   string
		wantPanic bool
		expected  map[string]string
	}{
		{
			name:      "Valid QUAY_URL",
			quayURL:   "quay.io/myorg/myrepo",
			wantPanic: false,
			expected: map[string]string{
				"quay.io": "quay.io/myorg/myrepo",
			},
		},
		{
			name:      "Empty QUAY_URL should panic",
			quayURL:   "",
			wantPanic: true,
			expected:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.quayURL != "" {
				os.Setenv("QUAY_URL", tt.quayURL)
				defer os.Unsetenv("QUAY_URL")
			} else {
				os.Unsetenv("QUAY_URL")
			}

			if tt.wantPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("PrepareRegistryMap() should have panicked")
					}
				}()
			}

			result := PrepareRegistryMap()

			if !tt.wantPanic {
				if len(result) != len(tt.expected) {
					t.Errorf("PrepareRegistryMap() returned map with wrong length, got %d, want %d", len(result), len(tt.expected))
				}
				for key, expectedValue := range tt.expected {
					if result[key] != expectedValue {
						t.Errorf("PrepareRegistryMap()[%s] = %s, want %s", key, result[key], expectedValue)
					}
				}
			}
		})
	}
}

func TestInitMetrics(t *testing.T) {
	tests := []struct {
		name        string
		registryMap map[string]string
	}{
		{
			name: "Single registry",
			registryMap: map[string]string{
				"quay.io": "quay.io/test/repo",
			},
		},
		{
			name: "Multiple registries",
			registryMap: map[string]string{
				"quay.io":  "quay.io/test/repo",
				"docker.io": "docker.io/test/repo",
			},
		},
		{
			name:        "Empty registry map",
			registryMap: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			metrics := InitMetrics(reg, tt.registryMap)

			// Check that metrics are not nil
			if metrics == nil {
				t.Fatal("InitMetrics() returned nil")
			}

			if metrics.RegistryPullCount == nil {
				t.Error("RegistryPullCount is nil")
			}
			if metrics.RegistryTotalPullCount == nil {
				t.Error("RegistryTotalPullCount is nil")
			}
			if metrics.RegistryPushCount == nil {
				t.Error("RegistryPushCount is nil")
			}
			if metrics.RegistryTotalPushCount == nil {
				t.Error("RegistryTotalPushCount is nil")
			}

			// Verify metrics are registered by checking metric count
			metricFamilies, err := reg.Gather()
			if err != nil {
				t.Fatalf("Failed to gather metrics: %v", err)
			}

			expectedMetricCount := 4 // We register 4 metrics
			if len(metricFamilies) != expectedMetricCount {
				t.Errorf("Expected %d metrics to be registered, got %d", expectedMetricCount, len(metricFamilies))
			}

			// Verify that all registry types have been initialized with 0
			for registryType := range tt.registryMap {
				pullCount := testutil.ToFloat64(metrics.RegistryPullCount.WithLabelValues(registryType))
				if pullCount != 0 {
					t.Errorf("Initial RegistryPullCount for %s should be 0, got %f", registryType, pullCount)
				}

				totalPullCount := testutil.ToFloat64(metrics.RegistryTotalPullCount.WithLabelValues(registryType))
				if totalPullCount != 0 {
					t.Errorf("Initial RegistryTotalPullCount for %s should be 0, got %f", registryType, totalPullCount)
				}

				pushCount := testutil.ToFloat64(metrics.RegistryPushCount.WithLabelValues(registryType))
				if pushCount != 0 {
					t.Errorf("Initial RegistryPushCount for %s should be 0, got %f", registryType, pushCount)
				}

				totalPushCount := testutil.ToFloat64(metrics.RegistryTotalPushCount.WithLabelValues(registryType))
				if totalPushCount != 0 {
					t.Errorf("Initial RegistryTotalPushCount for %s should be 0, got %f", registryType, totalPushCount)
				}
			}
		})
	}
}

func TestInitMetrics_MetricNames(t *testing.T) {
	reg := prometheus.NewRegistry()
	registryMap := map[string]string{
		"quay.io": "quay.io/test/repo",
	}

	InitMetrics(reg, registryMap)

	expectedMetrics := []string{
		"registry_exporter_successful_pull_count",
		"registry_exporter_total_pull_count",
		"registry_exporter_successful_push_count",
		"registry_exporter_total_push_count",
	}

	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}

	metricNames := make(map[string]bool)
	for _, mf := range metricFamilies {
		metricNames[*mf.Name] = true
	}

	for _, expectedName := range expectedMetrics {
		if !metricNames[expectedName] {
			t.Errorf("Expected metric %s not found in registry", expectedName)
		}
	}
}

func TestMetrics_Increment(t *testing.T) {
	reg := prometheus.NewRegistry()
	registryMap := map[string]string{
		"quay.io": "quay.io/test/repo",
	}

	metrics := InitMetrics(reg, registryMap)

	// Test incrementing pull count
	metrics.RegistryPullCount.WithLabelValues("quay.io").Inc()
	pullCount := testutil.ToFloat64(metrics.RegistryPullCount.WithLabelValues("quay.io"))
	if pullCount != 1 {
		t.Errorf("RegistryPullCount after Inc() should be 1, got %f", pullCount)
	}

	// Test incrementing total pull count
	metrics.RegistryTotalPullCount.WithLabelValues("quay.io").Inc()
	totalPullCount := testutil.ToFloat64(metrics.RegistryTotalPullCount.WithLabelValues("quay.io"))
	if totalPullCount != 1 {
		t.Errorf("RegistryTotalPullCount after Inc() should be 1, got %f", totalPullCount)
	}

	// Test incrementing push count
	metrics.RegistryPushCount.WithLabelValues("quay.io").Inc()
	pushCount := testutil.ToFloat64(metrics.RegistryPushCount.WithLabelValues("quay.io"))
	if pushCount != 1 {
		t.Errorf("RegistryPushCount after Inc() should be 1, got %f", pushCount)
	}

	// Test incrementing total push count
	metrics.RegistryTotalPushCount.WithLabelValues("quay.io").Inc()
	totalPushCount := testutil.ToFloat64(metrics.RegistryTotalPushCount.WithLabelValues("quay.io"))
	if totalPushCount != 1 {
		t.Errorf("RegistryTotalPushCount after Inc() should be 1, got %f", totalPushCount)
	}
}

func TestConstants(t *testing.T) {
	// Test that constants have expected values
	if maxRetries != 5 {
		t.Errorf("maxRetries should be 5, got %d", maxRetries)
	}

	if pullArtifactPath != "/mnt/storage/pull-artifact.txt" {
		t.Errorf("pullArtifactPath should be '/mnt/storage/pull-artifact.txt', got %s", pullArtifactPath)
	}

	if pullTag != ":pull" {
		t.Errorf("pullTag should be ':pull', got %s", pullTag)
	}
}

