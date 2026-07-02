package main

import (
	"fmt"
	"testing"
	"time"
)

// ── Build Metrics Tests ───────────────────────────────────────────────────────

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
			name: "successful docker build",
			plr: NewPLR().UID("build-123").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3300)).
				Pipeline("docker-build-oci-ta").EventType("push").Succeeded().Build(),
			wantRecorded: true, wantBuildType: "docker-builds", wantEventType: "push", wantSucceeded: true,
		},
		{
			name: "failed build with missing event type",
			plr: NewPLR().UID("build-fail").
				Times(secondsAgo(3600), secondsAgo(3590), secondsAgo(3420)).
				Pipeline("bundle-build-oci-ta").Failed("").Build(),
			wantRecorded: true, wantBuildType: "bundle-builds", wantEventType: "unknown", wantSucceeded: false,
		},
		{
			name: "incomplete build (no completion time)",
			plr: NewPLR().UID("build-running").
				CreatedAt(secondsAgo(3600)).
				Pipeline("docker-build").EventType("pull_request").Build(),
			wantRecorded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			slo := newBuildSLO30d()
			slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", tt.plr)

			recorded := false
			store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				if tt.wantRecorded {
					assertEqual(t, "BuildType", ls.BuildType, tt.wantBuildType)
					assertEqual(t, "EventType", ls.EventType, tt.wantEventType)
					assertEqual(t, "TotalCount", window.ComputeTotalCount(), int64(1))
					if tt.wantSucceeded {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(1))
					} else {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(0))
					}
				}
			})
			assertEqual(t, "recorded", recorded, tt.wantRecorded)
		})
	}
}

// ── Integration Metrics Tests ─────────────────────────────────────────────────

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
			name: "required integration test (optional defaults to false)",
			plr: NewPLR().UID("test-123").
				Times(secondsAgo(3600), secondsAgo(3585), secondsAgo(3000)).
				Pipeline("custom-integration").TestScenario("scenario-1").PACEventType("push").Succeeded().Build(),
			wantRecorded: true, wantTestType: "integration", wantOptional: "false", wantEventType: "push",
		},
		{
			name: "optional test",
			plr: NewPLR().UID("test-optional").
				Times(secondsAgo(3600), secondsAgo(3580), secondsAgo(3300)).
				Pipeline("tmt-integration").TestScenario("scenario-2").Optional(true).PACEventType("pull_request").Failed("").Build(),
			wantRecorded: true, wantTestType: "integration", wantOptional: "true", wantEventType: "pull_request",
		},
		{
			name: "EC test",
			plr: NewPLR().UID("test-ec").
				Times(secondsAgo(3600), secondsAgo(3595), secondsAgo(3480)).
				Pipeline("enterprise-contract").TestScenario("ec-scan").PACEventType("push").Succeeded().Build(),
			wantRecorded: true, wantTestType: "ec", wantOptional: "false", wantEventType: "push",
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
					assertEqual(t, "TestType", ls.TestType, tt.wantTestType)
					assertEqual(t, "Optional", ls.Optional, tt.wantOptional)
					assertEqual(t, "EventType", ls.EventType, tt.wantEventType)
				}
			})
			assertEqual(t, "recorded", recorded, tt.wantRecorded)
		})
	}
}

// ── Release Metrics Tests ─────────────────────────────────────────────────────

func TestReleaseRecordObservation(t *testing.T) {
	tests := []struct {
		name          string
		release       Release
		wantRecorded  bool
		wantAutomated string
		wantEventType string
		wantSucceeded bool
	}{
		{
			name: "automated push release",
			release: NewRelease().Name("release-1").
				Times(secondsAgo(3600), secondsAgo(3600), secondsAgo(2700)).
				App("my-app").Component("my-comp").PACEventType("push").Automated(true).Succeeded().Build(),
			wantRecorded: true, wantAutomated: "true", wantEventType: "push", wantSucceeded: true,
		},
		{
			name: "manual release (missing automated label)",
			release: NewRelease().Name("manual-release").
				Times(secondsAgo(3600), secondsAgo(3540), secondsAgo(2400)).
				App("my-app").Component("my-comp").PACEventType("incoming").Succeeded().Build(),
			wantRecorded: true, wantAutomated: "unknown", wantEventType: "incoming", wantSucceeded: true,
		},
		{
			name: "failed release",
			release: NewRelease().Name("failed-release").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3300)).
				App("my-app").Component("my-comp").PACEventType("push").Automated(false).Failed("Failed").Build(),
			wantRecorded: true, wantAutomated: "false", wantEventType: "push", wantSucceeded: false,
		},
		{
			name: "release without event type defaults to unknown",
			release: NewRelease().Name("my-release-abc12").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3000)).
				App("my-app").Component("my-comp").Automated(true).Succeeded().Build(),
			wantRecorded: true, wantAutomated: "true", wantEventType: "unknown", wantSucceeded: true,
		},
		{
			name: "rerun release name (-rerun-) gets kaexporter-rerun",
			release: NewRelease().Name("my-release-rerun-abc12").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3000)).
				App("my-app").Component("my-comp").Automated(true).Succeeded().Build(),
			wantRecorded: true, wantAutomated: "true", wantEventType: "kaexporter-rerun", wantSucceeded: true,
		},
		{
			name: "retry release name (-retry-) gets kaexporter-rerun",
			release: NewRelease().Name("my-release-retry-xyz99").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3000)).
				App("my-app").Component("my-comp").Automated(true).Succeeded().Build(),
			wantRecorded: true, wantAutomated: "true", wantEventType: "kaexporter-rerun", wantSucceeded: true,
		},
		{
			name: "rr release name (-rr-) gets kaexporter-rerun",
			release: NewRelease().Name("my-release-rr-def45").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3000)).
				App("my-app").Component("my-comp").Automated(true).Succeeded().Build(),
			wantRecorded: true, wantAutomated: "true", wantEventType: "kaexporter-rerun", wantSucceeded: true,
		},
		{
			name: "rerun name with PAC label uses PAC label value",
			release: NewRelease().Name("my-release-rerun-abc12").
				Times(secondsAgo(3600), secondsAgo(3570), secondsAgo(3000)).
				App("my-app").Component("my-comp").PACEventType("push").Automated(true).Succeeded().Build(),
			wantRecorded: true, wantAutomated: "true", wantEventType: "push", wantSucceeded: true,
		},
		{
			name: "incomplete release (no completion time)",
			release: NewRelease().Name("running-release").
				CreatedAt(secondsAgo(3600)).
				App("my-app").Component("my-comp").PACEventType("push").Automated(true).Build(),
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
					assertEqual(t, "Automated", ls.Automated, tt.wantAutomated)
					assertEqual(t, "EventType", ls.EventType, tt.wantEventType)
					if tt.wantSucceeded {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(1))
					} else {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(0))
					}
				}
			})
			assertEqual(t, "recorded", recorded, tt.wantRecorded)
		})
	}
}

// ── Edge Cases ────────────────────────────────────────────────────────────────

func TestEdgeCases(t *testing.T) {
	t.Run("negative duration is not recorded", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()
		plr := NewPLR().UID("bad-time").
			CreatedAt(secondsAgo(3600)).
			CompletedAt(secondsAgo(3900)). // Before creation
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "app", "comp", plr)

		recorded := false
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			recorded = true
		})
		if recorded {
			t.Error("negative duration PLR was recorded (should be skipped)")
		}
	})

	t.Run("deduplication", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()
		plr := NewPLR().UID("same-build").
			Times(secondsAgo(3600), secondsAgo(3590), secondsAgo(3300)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "cluster", "ns", "app", "comp", plr)
		slo.recordObservation(store, "cluster", "ns", "app", "comp", plr) // duplicate

		var totalCount int64
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			totalCount = window.ComputeTotalCount()
		})
		assertEqual(t, "TotalCount", totalCount, int64(1))
	})
}

// ── Gauge Update Tests ────────────────────────────────────────────────────────

func TestUpdateGauges(t *testing.T) {
	t.Run("empty store does not panic", func(t *testing.T) {
		store := NewStore()
		newBuildSLO30d().updateGauges(store)
		newIntegrationSLO30d().updateGauges(store)
		newReleaseSLO30d().updateGauges(store)
	})

	t.Run("with data", func(t *testing.T) {
		store := NewStore()
		buildSLO := newBuildSLO30d()
		now := time.Now().UTC()

		for i := 0; i < 10; i++ {
			completionTime := now.Add(-time.Duration(i*24) * time.Hour)
			succeeded := i%3 != 0 // 66% success rate
			failureReason := ""
			if !succeeded {
				failureReason = "Failed"
			}
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("build-%d", i), completionTime,
				LabelSet{Cluster: "test-cluster", Namespace: "test-ns", Application: "test-app",
					Component: "test-comp", BuildType: "docker-builds", EventType: "push"},
				float64(300+i*10), float64(10+i*2), succeeded, failureReason,
			)
		}

		buildSLO.updateGauges(store)

		metricCount := 0
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			metricCount++
			totalCount := window.ComputeTotalCount()
			if totalCount == 0 {
				t.Error("TotalCount should not be 0")
			}
			successCount := window.ComputeSuccessCount()
			if successCount < 0 || successCount > totalCount {
				t.Errorf("SuccessCount %d out of valid range [0, %d]", successCount, totalCount)
			}
		})
		if metricCount == 0 {
			t.Error("Expected at least one metric")
		}
	})

	t.Run("all failures - no duration metrics emitted", func(t *testing.T) {
		store := NewStore()
		buildSLO := newBuildSLO30d()
		now := time.Now().UTC()

		for i := 0; i < 5; i++ {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("failed-%d", i), now.Add(-time.Duration(i*24)*time.Hour),
				LabelSet{Cluster: "test-cluster", Namespace: "test-ns", Application: "test-app",
					Component: "failing-comp", BuildType: "docker-builds", EventType: "push"},
				0, 10.0, false, "Failed",
			)
		}

		buildSLO.updateGauges(store)

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertEqual(t, "TotalCount", window.ComputeTotalCount(), int64(5))
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(0))
		})
	})
}

// ── Queue Time Tests ──────────────────────────────────────────────────────────

func TestQueueTimeMetrics(t *testing.T) {
	t.Run("build with valid wait time", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()
		plr := NewPLR().UID("wait-test-1").
			Times(secondsAgo(3600), secondsAgo(3450), secondsAgo(3150)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", plr)

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(), 150.0)
		})
	})

	t.Run("build with missing startTime is rejected", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()
		plr := NewPLR().UID("wait-test-2").
			CreatedAt(secondsAgo(3600)).
			CompletedAt(secondsAgo(3300)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", plr)

		recorded := false
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			recorded = true
		})
		if recorded {
			t.Error("PLR with missing startTime should be rejected")
		}
	})

	t.Run("build with zero wait time", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d()
		plr := NewPLR().UID("wait-test-3").
			Times(secondsAgo(3600), secondsAgo(3600), secondsAgo(3300)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", plr)

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(), 0.0)
		})
	})

	t.Run("mean calculation with multiple observations", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		waitTimes := []float64{10, 20, 30, 40, 50}

		for i, waitTime := range waitTimes {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("wait-mean-%d", i), now.Add(-time.Duration(i)*time.Hour),
				LabelSet{Cluster: "test-cluster", Namespace: "test-ns", Application: "test-app",
					Component: "test-comp", BuildType: "docker-builds", EventType: "push"},
				300.0, waitTime, true, "",
			)
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(), 30.0)
			assertEqual(t, "TotalCount", window.ComputeTotalCount(), int64(5))
		})
	})

	t.Run("wait time excludes failed builds", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		// 3 successful builds with wait times [10, 20, 30]
		for i, wt := range []float64{10, 20, 30} {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("success-%d", i), now.Add(-time.Duration(i)*time.Hour),
				LabelSet{Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
					BuildType: "docker-builds", EventType: "push"},
				300.0, wt, true, "",
			)
		}
		// 2 failed builds with HIGH wait times [100, 200] - should NOT be included
		for i, wt := range []float64{100, 200} {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("failed-%d", i), now.Add(-time.Duration(i+10)*time.Hour),
				LabelSet{Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
					BuildType: "docker-builds", EventType: "push"},
				0, wt, false, "Failed",
			)
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(), 20.0) // (10+20+30)/3
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(3))
			assertEqual(t, "TotalCount", window.ComputeTotalCount(), int64(5))
		})
	})

	t.Run("release queue time", func(t *testing.T) {
		store := NewStore()
		slo := newReleaseSLO30d()
		release := NewRelease().Name("release-wait-test").
			Times(secondsAgo(3600), secondsAgo(3300), secondsAgo(2700)).
			App("app").Component("comp").PACEventType("push").Automated(true).Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "app", "comp", release)

		store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(), 300.0)
		})
	})

	t.Run("release with missing startTime is rejected", func(t *testing.T) {
		store := NewStore()
		slo := newReleaseSLO30d()
		release := NewRelease().Name("release-no-start").
			CreatedAt(secondsAgo(3600)).
			CompletedAt(secondsAgo(2700)).
			App("app").Component("comp").PACEventType("push").Automated(true).Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "app", "comp", release)

		recorded := false
		store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
			recorded = true
		})
		if recorded {
			t.Error("Release with missing startTime should be rejected")
		}
	})
}

// ── Store Cleanup Tests ───────────────────────────────────────────────────────

func TestStoreCleanup(t *testing.T) {
	t.Run("prune seen keys", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		store.SeenKeys["old-key-1"] = now.Add(-48 * time.Hour)
		store.SeenKeys["old-key-2"] = now.Add(-25 * time.Hour)
		store.SeenKeys["recent-key"] = now.Add(-1 * time.Hour)
		store.SeenKeys["fresh-key"] = now

		store.PruneSeenKeys(24 * time.Hour)

		if _, exists := store.SeenKeys["old-key-1"]; exists {
			t.Error("old-key-1 should be pruned")
		}
		if _, exists := store.SeenKeys["old-key-2"]; exists {
			t.Error("old-key-2 should be pruned")
		}
		if _, exists := store.SeenKeys["recent-key"]; !exists {
			t.Error("recent-key should be kept")
		}
		if _, exists := store.SeenKeys["fresh-key"]; !exists {
			t.Error("fresh-key should be kept")
		}
	})

	t.Run("prune empty store does not panic", func(t *testing.T) {
		store := NewStore()
		store.PruneSeenKeys(24 * time.Hour)
		assertEqual(t, "SeenKeys length", len(store.SeenKeys), 0)
	})

	t.Run("stale bucket eviction", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		labels := LabelSet{Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
			BuildType: "docker-builds", EventType: "push"}

		// Old observations (should be excluded from 30-day calculations)
		store.RecordObservation(metricBuildDuration, "old-build", now.AddDate(0, 0, -35), labels, 300.0, 10.0, true, "")
		store.RecordObservation(metricBuildDuration, "almost-old", now.AddDate(0, 0, -31), labels, 400.0, 15.0, true, "")

		// Recent observations (should be included)
		store.RecordObservation(metricBuildDuration, "recent", now.AddDate(0, 0, -15), labels, 200.0, 5.0, true, "")
		store.RecordObservation(metricBuildDuration, "fresh", now.AddDate(0, 0, -1), labels, 100.0, 3.0, true, "")

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "SuccessMean", window.ComputeSuccessMean(), 150.0)
			assertFloat(t, "WaitMean", window.ComputeWaitMean(), 4.0)
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(), int64(2))
			assertEqual(t, "TotalCount", window.ComputeTotalCount(), int64(2))
		})
	})

	t.Run("stale bucket eviction for failure reasons", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		labels := LabelSet{Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
			BuildType: "docker-builds", EventType: "push"}

		// Old failure (should be excluded)
		store.RecordObservation(metricBuildDuration, "old-failure", now.AddDate(0, 0, -35), labels, 0, 0, false, "OldError")

		// Recent failures (should be included)
		store.RecordObservation(metricBuildDuration, "recent-1", now.AddDate(0, 0, -15), labels, 0, 0, false, "RecentError")
		store.RecordObservation(metricBuildDuration, "recent-2", now.AddDate(0, 0, -15), labels, 0, 0, false, "RecentError")
		store.RecordObservation(metricBuildDuration, "recent-3", now.AddDate(0, 0, -15), labels, 0, 0, false, "AnotherError")

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			reasons := window.ComputeFailureReasons()
			if _, exists := reasons["OldError"]; exists {
				t.Error("OldError should be excluded")
			}
			assertEqual(t, "RecentError count", reasons["RecentError"], int64(2))
			assertEqual(t, "AnotherError count", reasons["AnotherError"], int64(1))
		})
	})
}

// ── Test Helpers ──────────────────────────────────────────────────────────────

func assertEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func assertFloat(t *testing.T, name string, got, want float64) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %f, want %f", name, got, want)
	}
}
