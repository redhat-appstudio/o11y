package main

import (
	"os"
	"testing"
)

// TestQueryWindowCalculation verifies that query window and dedupe retention
// are correctly calculated from KA_WINDOW_HOURS with safety margin.
func TestQueryWindowCalculation(t *testing.T) {
	tests := []struct {
		name                 string
		kaWindowHours        string
		expectedWindow       int
		expectedQuery        int
		expectedDedupe       int
	}{
		{
			name:           "Default 48h window",
			kaWindowHours:  "",
			expectedWindow: 48,
			expectedQuery:  72,  // 48 + 24 (50% safety margin)
			expectedDedupe: 108, // 1.5 × 72
		},
		{
			name:           "96h window (double default)",
			kaWindowHours:  "96",
			expectedWindow: 96,
			expectedQuery:  144, // 96 + 48
			expectedDedupe: 216, // 1.5 × 144
		},
		{
			name:           "24h window (half default)",
			kaWindowHours:  "24",
			expectedWindow: 24,
			expectedQuery:  36,  // 24 + 12
			expectedDedupe: 54,  // 1.5 × 36
		},
		{
			name:           "168h window (1 week)",
			kaWindowHours:  "168",
			expectedWindow: 168,
			expectedQuery:  252, // 168 + 84
			expectedDedupe: 378, // 1.5 × 252
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			os.Setenv(kaHostEnvVar, "https://test-ka-host")
			os.Setenv(kaTokenEnvVar, "test-token")
			os.Setenv(clusterEnvVar, "test-cluster")
			os.Setenv(namespaceEnvVar, "test-namespace") // Single-tenant mode
			defer os.Unsetenv(kaHostEnvVar)
			defer os.Unsetenv(kaTokenEnvVar)
			defer os.Unsetenv(clusterEnvVar)
			defer os.Unsetenv(namespaceEnvVar)

			if tt.kaWindowHours != "" {
				os.Setenv(kaWindowHoursEnv, tt.kaWindowHours)
				defer os.Unsetenv(kaWindowHoursEnv)
			}

			// Create exporter
			exporter, err := NewKAExporter()
			if err != nil {
				t.Fatalf("NewKAExporter() failed: %v", err)
			}

			// Verify window calculations
			if exporter.windowHours != tt.expectedWindow {
				t.Errorf("windowHours = %d, expected %d", exporter.windowHours, tt.expectedWindow)
			}

			if exporter.queryWindowHours != tt.expectedQuery {
				t.Errorf("queryWindowHours = %d, expected %d", exporter.queryWindowHours, tt.expectedQuery)
			}

			if exporter.dedupeRetentionHours != tt.expectedDedupe {
				t.Errorf("dedupeRetentionHours = %d, expected %d", exporter.dedupeRetentionHours, tt.expectedDedupe)
			}

			// Verify invariants
			if exporter.queryWindowHours <= exporter.windowHours {
				t.Errorf("queryWindowHours (%d) must be > windowHours (%d)", exporter.queryWindowHours, exporter.windowHours)
			}

			if exporter.dedupeRetentionHours <= exporter.queryWindowHours {
				t.Errorf("dedupeRetentionHours (%d) must be > queryWindowHours (%d)", exporter.dedupeRetentionHours, exporter.queryWindowHours)
			}

			// Verify safety margin is 50%
			expectedMargin := exporter.windowHours / 2
			actualMargin := exporter.queryWindowHours - exporter.windowHours
			if actualMargin != expectedMargin {
				t.Errorf("safety margin = %d, expected %d (50%% of windowHours)", actualMargin, expectedMargin)
			}

			// Verify dedupe retention is 1.5× query window
			expectedDedupe := int(float64(exporter.queryWindowHours) * 1.5)
			if exporter.dedupeRetentionHours != expectedDedupe {
				t.Errorf("dedupeRetentionHours = %d, expected %d (1.5× queryWindowHours)", exporter.dedupeRetentionHours, expectedDedupe)
			}
		})
	}
}

// TestInvalidKAWindowHours verifies that invalid KA_WINDOW_HOURS values
// fall back to the default.
func TestInvalidKAWindowHours(t *testing.T) {
	tests := []struct {
		name          string
		kaWindowHours string
	}{
		{"Negative value", "-10"},
		{"Zero", "0"},
		{"Non-numeric", "invalid"},
		{"Float", "48.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			os.Setenv(kaHostEnvVar, "https://test-ka-host")
			os.Setenv(kaTokenEnvVar, "test-token")
			os.Setenv(clusterEnvVar, "test-cluster")
			os.Setenv(namespaceEnvVar, "test-namespace")
			os.Setenv(kaWindowHoursEnv, tt.kaWindowHours)
			defer os.Unsetenv(kaHostEnvVar)
			defer os.Unsetenv(kaTokenEnvVar)
			defer os.Unsetenv(clusterEnvVar)
			defer os.Unsetenv(namespaceEnvVar)
			defer os.Unsetenv(kaWindowHoursEnv)

			exporter, err := NewKAExporter()
			if err != nil {
				t.Fatalf("NewKAExporter() failed: %v", err)
			}

			// Should fall back to default
			if exporter.windowHours != defaultKAWindowHours {
				t.Errorf("windowHours = %d, expected default %d", exporter.windowHours, defaultKAWindowHours)
			}
		})
	}
}
