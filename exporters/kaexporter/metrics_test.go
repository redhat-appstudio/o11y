package main

import (
	"testing"
	"time"
)

// ─── Build Metrics Tests ──────────────────────────────────────────────────────

func TestBuildRecordObservation(t *testing.T) {
	tests := []struct {
		name          string
		plr           PipelineRun
		wantRecorded  bool
		wantBuildType string
		wantEventType string
		wantSucceeded bool
	}{
		{
			name: "successful docker build with push event",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:               "build-123",
					Name:              "my-build",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T10:00:00Z",
					Labels: map[string]string{
						labelTektonPipeline: "docker-build-oci-ta",
						labelEventType:      "push",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T10:05:00Z",
					Conditions: []Condition{
						{Type: "Succeeded", Status: "True"},
					},
				},
			},
			wantRecorded:  true,
			wantBuildType: "docker-builds",
			wantEventType: "push",
			wantSucceeded: true,
		},
		{
			name: "failed build with missing event type (defaults to unknown)",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:               "build-fail",
					Name:              "failed-build",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T11:00:00Z",
					Labels: map[string]string{
						labelTektonPipeline: "bundle-build-oci-ta",
						// No event_type label - should default to "unknown"
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T11:03:00Z",
					Conditions: []Condition{
						{Type: "Succeeded", Status: "False"},
					},
				},
			},
			wantRecorded:  true,
			wantBuildType: "bundle-builds",
			wantEventType: "unknown",
			wantSucceeded: false,
		},
		{
			name: "incomplete build (no completion time) - should not record",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:               "build-running",
					Name:              "running-build",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T12:00:00Z",
					Labels: map[string]string{
						labelTektonPipeline: "docker-build",
						labelEventType:      "pull_request",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "", // Still running
				},
			},
			wantRecorded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			slo := newBuildSLO30d()

			// Record observation
			slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", tt.plr)

			// Check if recorded
			recorded := false
			store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				if tt.wantRecorded {
					// Verify label extraction
					if ls.BuildType != tt.wantBuildType {
						t.Errorf("BuildType = %q, want %q", ls.BuildType, tt.wantBuildType)
					}
					if ls.EventType != tt.wantEventType {
						t.Errorf("EventType = %q, want %q", ls.EventType, tt.wantEventType)
					}

					// Verify success/failure tracking
					totalCount := window.ComputeTotalCount()
					if totalCount != 1 {
						t.Errorf("TotalCount = %d, want 1", totalCount)
					}
					if tt.wantSucceeded {
						if window.ComputeSuccessRate() != 1.0 {
							t.Errorf("SuccessRate = %f, want 1.0", window.ComputeSuccessRate())
						}
					} else {
						if window.ComputeSuccessRate() != 0.0 {
							t.Errorf("SuccessRate = %f, want 0.0 (failed build)", window.ComputeSuccessRate())
						}
					}
				}
			})

			if recorded != tt.wantRecorded {
				t.Errorf("recorded = %v, want %v", recorded, tt.wantRecorded)
			}
		})
	}
}

// ─── Integration Metrics Tests ────────────────────────────────────────────────

func TestIntegrationRecordObservation(t *testing.T) {
	tests := []struct {
		name          string
		plr           PipelineRun
		wantRecorded  bool
		wantTestType  string
		wantOptional  string
		wantEventType string
	}{
		{
			name: "required integration test (optional label missing - defaults to false)",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:               "test-123",
					Name:              "integration-test",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T10:00:00Z",
					Labels: map[string]string{
						labelTektonPipeline: "custom-integration",
						labelTestScenario:   "scenario-1",
						labelPACEventType:   "push",
						// NO optional label - should default to "false"
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T10:10:00Z",
					Conditions: []Condition{
						{Type: "Succeeded", Status: "True"},
					},
				},
			},
			wantRecorded:  true,
			wantTestType:  "integration",
			wantOptional:  "false", // CRITICAL: must default to "false" when label absent
			wantEventType: "push",
		},
		{
			name: "optional test (can fail without blocking release)",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:               "test-optional",
					Name:              "optional-test",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T11:00:00Z",
					Labels: map[string]string{
						labelTektonPipeline: "tmt-integration",
						labelTestScenario:   "scenario-2",
						labelTestOptional:   "true",
						labelPACEventType:   "pull_request",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T11:05:00Z",
					Conditions: []Condition{
						{Type: "Succeeded", Status: "False"},
					},
				},
			},
			wantRecorded:  true,
			wantTestType:  "integration",
			wantOptional:  "true",
			wantEventType: "pull_request",
		},
		{
			name: "EC test (enterprise-contract pipeline)",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:               "test-ec",
					Name:              "ec-test",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T12:00:00Z",
					Labels: map[string]string{
						labelTektonPipeline: "enterprise-contract",
						labelTestScenario:   "ec-scan",
						labelPACEventType:   "push",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T12:02:00Z",
					Conditions: []Condition{
						{Type: "Succeeded", Status: "True"},
					},
				},
			},
			wantRecorded:  true,
			wantTestType:  "ec",
			wantOptional:  "false", // EC tests default to required
			wantEventType: "push",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			slo := newIntegrationSLO30d()

			slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", tt.plr)

			recorded := false
			store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				if tt.wantRecorded {
					if ls.TestType != tt.wantTestType {
						t.Errorf("TestType = %q, want %q", ls.TestType, tt.wantTestType)
					}
					if ls.Optional != tt.wantOptional {
						t.Errorf("Optional = %q, want %q (CRITICAL: must default to 'false')", ls.Optional, tt.wantOptional)
					}
					if ls.EventType != tt.wantEventType {
						t.Errorf("EventType = %q, want %q", ls.EventType, tt.wantEventType)
					}
				}
			})

			if recorded != tt.wantRecorded {
				t.Errorf("recorded = %v, want %v", recorded, tt.wantRecorded)
			}
		})
	}
}

// ─── Release Metrics Tests ────────────────────────────────────────────────────

func TestReleaseRecordObservation(t *testing.T) {
	tests := []struct {
		name          string
		release       Release
		wantRecorded  bool
		wantEventType string
		wantAutomated string
		wantSucceeded bool
	}{
		{
			name: "automated push release (successful)",
			release: Release{
				Metadata: struct {
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace,omitempty"`
					Labels            map[string]string `json:"labels"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					Name:              "release-1",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T10:00:00Z",
					Labels: map[string]string{
						labelAppStudioApp:     "my-app",
						labelAppStudioComp:    "my-comp",
						labelPACEventType:     "push",
						labelReleaseAutomated: "true",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					StartTime:      "2026-06-01T10:00:00Z",
					CompletionTime: "2026-06-01T10:15:00Z",
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: "Succeeded"},
					},
				},
			},
			wantRecorded:  true,
			wantEventType: "push",
			wantAutomated: "true",
			wantSucceeded: true,
		},
		{
			name: "manual release (missing automated label - defaults to unknown)",
			release: Release{
				Metadata: struct {
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace,omitempty"`
					Labels            map[string]string `json:"labels"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					Name:              "manual-release",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T11:00:00Z",
					Labels: map[string]string{
						labelAppStudioApp:  "my-app",
						labelAppStudioComp: "my-comp",
						labelPACEventType:  "incoming",
						// NO automated label - should default to "unknown"
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T11:20:00Z",
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: "Succeeded"},
					},
				},
			},
			wantRecorded:  true,
			wantEventType: "incoming",
			wantAutomated: "unknown", // CRITICAL: default when label missing
			wantSucceeded: true,
		},
		{
			name: "failed release (Released=True but Reason!=Succeeded)",
			release: Release{
				Metadata: struct {
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace,omitempty"`
					Labels            map[string]string `json:"labels"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					Name:              "failed-release",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T12:00:00Z",
					Labels: map[string]string{
						labelAppStudioApp:     "my-app",
						labelAppStudioComp:    "my-comp",
						labelPACEventType:     "push",
						labelReleaseAutomated: "false",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "2026-06-01T12:05:00Z",
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: "Failed"}, // Status=True but Reason=Failed
					},
				},
			},
			wantRecorded:  true,
			wantEventType: "push",
			wantAutomated: "false",
			wantSucceeded: false, // CRITICAL: must check both Status AND Reason
		},
		{
			name: "incomplete release (no completion time) - should not record",
			release: Release{
				Metadata: struct {
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace,omitempty"`
					Labels            map[string]string `json:"labels"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					Name:              "running-release",
					Namespace:         "test-ns",
					CreationTimestamp: "2026-06-01T13:00:00Z",
					Labels: map[string]string{
						labelAppStudioApp:     "my-app",
						labelAppStudioComp:    "my-comp",
						labelPACEventType:     "push",
						labelReleaseAutomated: "true",
					},
				},
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					CompletionTime: "", // Still running
				},
			},
			wantRecorded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			slo := newReleaseSLO30d()

			slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", tt.release)

			recorded := false
			store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				if tt.wantRecorded {
					if ls.EventType != tt.wantEventType {
						t.Errorf("EventType = %q, want %q", ls.EventType, tt.wantEventType)
					}
					if ls.Automated != tt.wantAutomated {
						t.Errorf("Automated = %q, want %q", ls.Automated, tt.wantAutomated)
					}

					// Verify success tracking
					if tt.wantSucceeded {
						if window.ComputeSuccessRate() != 1.0 {
							t.Errorf("SuccessRate = %f, want 1.0", window.ComputeSuccessRate())
						}
					} else {
						if window.ComputeSuccessRate() != 0.0 {
							t.Errorf("SuccessRate = %f, want 0.0 (failed release)", window.ComputeSuccessRate())
						}
					}
				}
			})

			if recorded != tt.wantRecorded {
				t.Errorf("recorded = %v, want %v", recorded, tt.wantRecorded)
			}
		})
	}
}

// ─── Edge Cases & Integration Tests ───────────────────────────────────────────

func TestEdgeCases(t *testing.T) {
	t.Run("negative duration handling", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()

		// CompletionTime before CreationTime (clock skew or bad data)
		plr := PipelineRun{
			Metadata: struct {
				UID               string            `json:"uid"`
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				UID:               "bad-time",
				CreationTimestamp: "2026-06-01T10:00:00Z",
				Labels:            map[string]string{labelTektonPipeline: "docker-build"},
			},
			Status: struct {
				StartTime      string      `json:"startTime"`
				CompletionTime string      `json:"completionTime"`
				Conditions     []Condition `json:"conditions"`
			}{
				CompletionTime: "2026-06-01T09:55:00Z", // 5 minutes BEFORE creation
				Conditions:     []Condition{{Type: "Succeeded", Status: "True"}},
			},
		}

		slo.recordObservation(store, "test-cluster", "test-ns", "app", "comp", plr)

		// Should NOT be recorded (negative duration)
		recorded := false
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			recorded = true
		})
		if recorded {
			t.Error("negative duration PLR was recorded (should be skipped)")
		}
	})

	t.Run("deduplication across record calls", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()

		plr := PipelineRun{
			Metadata: struct {
				UID               string            `json:"uid"`
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				UID:               "same-build",
				CreationTimestamp: "2026-06-01T10:00:00Z",
				Labels:            map[string]string{labelTektonPipeline: "docker-build"},
			},
			Status: struct {
				StartTime      string      `json:"startTime"`
				CompletionTime string      `json:"completionTime"`
				Conditions     []Condition `json:"conditions"`
			}{
				CompletionTime: "2026-06-01T10:05:00Z",
				Conditions:     []Condition{{Type: "Succeeded", Status: "True"}},
			},
		}

		// Record twice
		slo.recordObservation(store, "cluster", "ns", "app", "comp", plr)
		slo.recordObservation(store, "cluster", "ns", "app", "comp", plr) // duplicate

		// Verify only counted once
		var totalCount int64
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			totalCount = window.ComputeTotalCount()
		})
		if totalCount != 1 {
			t.Errorf("TotalCount = %d, want 1 (deduplication failed)", totalCount)
		}
	})
}

// ─── Gauge Update Tests ───────────────────────────────────────────────────────

func TestUpdateGaugesEmptyStore(t *testing.T) {
	store := NewStore()

	t.Run("build gauges with empty store", func(t *testing.T) {
		slo := newBuildSLO30d()
		// Should not panic
		slo.updateGauges(store)
	})

	t.Run("integration gauges with empty store", func(t *testing.T) {
		slo := newIntegrationSLO30d()
		slo.updateGauges(store)
	})

	t.Run("release gauges with empty store", func(t *testing.T) {
		slo := newReleaseSLO30d()
		slo.updateGauges(store)
	})
}

func TestUpdateGaugesWithData(t *testing.T) {
	store := NewStore()
	buildSLO := newBuildSLO30d()

	// Add some build observations
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		completionTime := now.Add(-time.Duration(i*24) * time.Hour) // Spread over 10 days
		succeeded := i%3 != 0                                       // 66% success rate

		store.RecordObservation(
			metricBuildDuration,
			"build-"+string(rune(i)),
			completionTime,
			LabelSet{
				Cluster:     "test-cluster",
				Namespace:   "test-ns",
				Application: "test-app",
				Component:   "test-comp",
				BuildType:   "docker-builds",
				EventType:   "push",
			},
			float64(300+i*10), // durations: 300, 310, 320, ...
			succeeded,
		)
	}

	// Update gauges
	buildSLO.updateGauges(store)

	// Verify metrics were populated (actual values depend on success rate calculation)
	// This test verifies the update process completes without error
	metricCount := 0
	store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
		metricCount++
		totalCount := window.ComputeTotalCount()
		if totalCount == 0 {
			t.Error("TotalCount should not be 0 after recording observations")
		}
		successRate := window.ComputeSuccessRate()
		if successRate < 0 || successRate > 1 {
			t.Errorf("SuccessRate %f out of range [0, 1]", successRate)
		}
	})
	if metricCount == 0 {
		t.Error("Expected at least one metric after updateGauges")
	}
}
