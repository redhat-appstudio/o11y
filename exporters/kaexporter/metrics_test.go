package main

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
			slo := newBuildSLO30d(nil)
			slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", tt.plr)

			recorded := false
			store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				if tt.wantRecorded {
					assertEqual(t, "BuildType", ls.BuildType, tt.wantBuildType)
					assertEqual(t, "EventType", ls.EventType, tt.wantEventType)
					assertEqual(t, "TotalCount", window.ComputeTotalCount(testCutoff()), int64(1))
					if tt.wantSucceeded {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(1))
					} else {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(0))
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
			slo := newIntegrationSLO30d(nil)
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
			slo := newReleaseSLO30d(nil)
			slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", tt.release)

			recorded := false
			store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				if tt.wantRecorded {
					assertEqual(t, "Automated", ls.Automated, tt.wantAutomated)
					assertEqual(t, "EventType", ls.EventType, tt.wantEventType)
					if tt.wantSucceeded {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(1))
					} else {
						assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(0))
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
		slo := newBuildSLO30d(nil)
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
		slo := newBuildSLO30d(nil)
		plr := NewPLR().UID("same-build").
			Times(secondsAgo(3600), secondsAgo(3590), secondsAgo(3300)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "cluster", "ns", "app", "comp", plr)
		slo.recordObservation(store, "cluster", "ns", "app", "comp", plr) // duplicate

		var totalCount int64
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			totalCount = window.ComputeTotalCount(testCutoff())
		})
		assertEqual(t, "TotalCount", totalCount, int64(1))
	})
}

// ── Gauge Update Tests ────────────────────────────────────────────────────────

func TestUpdateGauges(t *testing.T) {
	t.Run("empty store does not panic", func(t *testing.T) {
		store := NewStore()
		newBuildSLO30d(nil).updateGauges(store, nil)
		newIntegrationSLO30d(nil).updateGauges(store, nil)
		newReleaseSLO30d(nil).updateGauges(store, nil)
	})

	t.Run("with data", func(t *testing.T) {
		store := NewStore()
		buildSLO := newBuildSLO30d(nil)
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

		buildSLO.updateGauges(store, nil)

		metricCount := 0
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			metricCount++
			totalCount := window.ComputeTotalCount(testCutoff())
			if totalCount == 0 {
				t.Error("TotalCount should not be 0")
			}
			successCount := window.ComputeSuccessCount(testCutoff())
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
		buildSLO := newBuildSLO30d(nil)
		now := time.Now().UTC()

		for i := 0; i < 5; i++ {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("failed-%d", i), now.Add(-time.Duration(i*24)*time.Hour),
				LabelSet{Cluster: "test-cluster", Namespace: "test-ns", Application: "test-app",
					Component: "failing-comp", BuildType: "docker-builds", EventType: "push"},
				0, 10.0, false, "Failed",
			)
		}

		buildSLO.updateGauges(store, nil)

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertEqual(t, "TotalCount", window.ComputeTotalCount(testCutoff()), int64(5))
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(0))
		})
	})
}

// ── Queue Time Tests ──────────────────────────────────────────────────────────

func TestQueueTimeMetrics(t *testing.T) {
	t.Run("build with valid wait time", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d(nil)
		plr := NewPLR().UID("wait-test-1").
			Times(secondsAgo(3600), secondsAgo(3450), secondsAgo(3150)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", plr)

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(testCutoff()), 150.0)
		})
	})

	t.Run("build with missing startTime is rejected", func(t *testing.T) {
		store := NewStore()
		slo := newBuildSLO30d(nil)
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
		slo := newBuildSLO30d(nil)
		plr := NewPLR().UID("wait-test-3").
			Times(secondsAgo(3600), secondsAgo(3600), secondsAgo(3300)).
			Pipeline("docker-build").Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "test-app", "test-comp", plr)

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(testCutoff()), 0.0)
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
			assertFloat(t, "WaitMean", window.ComputeWaitMean(testCutoff()), 30.0)
			assertEqual(t, "TotalCount", window.ComputeTotalCount(testCutoff()), int64(5))
		})
	})

	t.Run("wait time excludes failed builds", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		// 3 successful builds with wait times [10, 20, 30]
		for i, wt := range []float64{10, 20, 30} {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("success-%d", i), now.Add(-time.Duration(i)*time.Hour),
				buildLabelShort,
				300.0, wt, true, "",
			)
		}
		// 2 failed builds with HIGH wait times [100, 200] - should NOT be included
		for i, wt := range []float64{100, 200} {
			store.RecordObservation(
				metricBuildDuration, fmt.Sprintf("failed-%d", i), now.Add(-time.Duration(i+10)*time.Hour),
				buildLabelShort,
				0, wt, false, "Failed",
			)
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(testCutoff()), 20.0) // (10+20+30)/3
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(3))
			assertEqual(t, "TotalCount", window.ComputeTotalCount(testCutoff()), int64(5))
		})
	})

	t.Run("release queue time", func(t *testing.T) {
		store := NewStore()
		slo := newReleaseSLO30d(nil)
		release := NewRelease().Name("release-wait-test").
			Times(secondsAgo(3600), secondsAgo(3300), secondsAgo(2700)).
			App("app").Component("comp").PACEventType("push").Automated(true).Succeeded().Build()

		slo.recordObservation(store, "test-cluster", "test-ns", "app", "comp", release)

		store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "WaitMean", window.ComputeWaitMean(testCutoff()), 300.0)
		})
	})

	t.Run("release with missing startTime is rejected", func(t *testing.T) {
		store := NewStore()
		slo := newReleaseSLO30d(nil)
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

		labels := buildLabelShort

		// Old observations (should be excluded from 30-day calculations)
		store.RecordObservation(metricBuildDuration, "old-build", now.AddDate(0, 0, -35), labels, 300.0, 10.0, true, "")
		store.RecordObservation(metricBuildDuration, "almost-old", now.AddDate(0, 0, -31), labels, 400.0, 15.0, true, "")

		// Recent observations (should be included)
		store.RecordObservation(metricBuildDuration, "recent", now.AddDate(0, 0, -15), labels, 200.0, 5.0, true, "")
		store.RecordObservation(metricBuildDuration, "fresh", now.AddDate(0, 0, -1), labels, 100.0, 3.0, true, "")

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "SuccessMean", window.ComputeSuccessMean(testCutoff()), 150.0)
			assertFloat(t, "WaitMean", window.ComputeWaitMean(testCutoff()), 4.0)
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(2))
			assertEqual(t, "TotalCount", window.ComputeTotalCount(testCutoff()), int64(2))
		})
	})

	t.Run("stale bucket eviction for failure reasons", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		labels := buildLabelShort

		// Old failure (should be excluded)
		store.RecordObservation(metricBuildDuration, "old-failure", now.AddDate(0, 0, -35), labels, 0, 0, false, "OldError")

		// Recent failures (should be included)
		store.RecordObservation(metricBuildDuration, "recent-1", now.AddDate(0, 0, -15), labels, 0, 0, false, "RecentError")
		store.RecordObservation(metricBuildDuration, "recent-2", now.AddDate(0, 0, -15), labels, 0, 0, false, "RecentError")
		store.RecordObservation(metricBuildDuration, "recent-3", now.AddDate(0, 0, -15), labels, 0, 0, false, "AnotherError")

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			reasons := window.ComputeFailureReasons(testCutoff())
			if _, exists := reasons["OldError"]; exists {
				t.Error("OldError should be excluded")
			}
			assertEqual(t, "RecentError count", reasons["RecentError"], int64(2))
			assertEqual(t, "AnotherError count", reasons["AnotherError"], int64(1))
		})
	})
}

// ── Bootstrap State Transition Tests ─────────────────────────────────────────

func TestUpdateBootstrapState(t *testing.T) {
	t.Run("successful non-truncated fetch marks bootstrapped", func(t *testing.T) {
		state := &nsBootstrapState{}
		completed := updateBootstrapState(state, false, "")
		if !completed {
			t.Error("expected completed=true")
		}
		if !state.Bootstrapped {
			t.Error("expected Bootstrapped=true")
		}
		if state.OldestSeenCreationTS != "" {
			t.Errorf("expected empty OldestSeenCreationTS, got %q", state.OldestSeenCreationTS)
		}
	})

	t.Run("first truncation records oldest timestamp", func(t *testing.T) {
		state := &nsBootstrapState{}
		completed := updateBootstrapState(state, true, "2026-07-29T00:44:46Z")
		if completed {
			t.Error("expected completed=false")
		}
		if state.Bootstrapped {
			t.Error("expected Bootstrapped=false")
		}
		if state.OldestSeenCreationTS != "2026-07-29T00:44:46Z" {
			t.Errorf("expected OldestSeenCreationTS=2026-07-29T00:44:46Z, got %q", state.OldestSeenCreationTS)
		}
	})

	t.Run("subsequent truncation does not overwrite oldest timestamp", func(t *testing.T) {
		state := &nsBootstrapState{OldestSeenCreationTS: "2026-07-25T10:00:00Z"}
		completed := updateBootstrapState(state, true, "2026-07-29T00:50:25Z")
		if completed {
			t.Error("expected completed=false")
		}
		if state.OldestSeenCreationTS != "2026-07-25T10:00:00Z" {
			t.Errorf("expected original OldestSeenCreationTS preserved, got %q", state.OldestSeenCreationTS)
		}
	})

	t.Run("already bootstrapped is a no-op", func(t *testing.T) {
		state := &nsBootstrapState{Bootstrapped: true}
		completed := updateBootstrapState(state, false, "")
		if completed {
			t.Error("expected completed=false for already-bootstrapped")
		}
		if !state.Bootstrapped {
			t.Error("Bootstrapped should remain true")
		}
	})

	t.Run("already bootstrapped ignores truncation", func(t *testing.T) {
		state := &nsBootstrapState{Bootstrapped: true}
		completed := updateBootstrapState(state, true, "2026-07-29T00:00:00Z")
		if completed {
			t.Error("expected completed=false")
		}
		if state.OldestSeenCreationTS != "" {
			t.Errorf("should not set OldestSeenCreationTS, got %q", state.OldestSeenCreationTS)
		}
	})

	t.Run("successful fetch after truncation marks bootstrapped and clears oldest", func(t *testing.T) {
		state := &nsBootstrapState{
			OldestSeenCreationTS: "2026-07-25T10:00:00Z",
			GapAttempts:          3,
		}
		completed := updateBootstrapState(state, false, "")
		if !completed {
			t.Error("expected completed=true")
		}
		if !state.Bootstrapped {
			t.Error("expected Bootstrapped=true")
		}
		if state.OldestSeenCreationTS != "" {
			t.Errorf("expected OldestSeenCreationTS cleared, got %q", state.OldestSeenCreationTS)
		}
		if state.GapAttempts != 0 {
			t.Errorf("expected GapAttempts reset to 0, got %d", state.GapAttempts)
		}
	})
}

// -- SLO Breach Tests -----------------------------------------------------

func TestComputeSuccessStdDev(t *testing.T) {
	t.Run("uniform values have zero stddev", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		for i := 0; i < 10; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("u-%d", i),
				now.Add(-time.Duration(i)*time.Hour), labels, 300.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "StdDev", window.ComputeSuccessStdDev(testCutoff()), 0.0)
		})
	})

	t.Run("known values", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// Durations: 100, 200, 300, 400, 500 -> mean=300, variance=20000, stddev~141.42
		durations := []float64{100, 200, 300, 400, 500}
		for i, d := range durations {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("k-%d", i),
				now.Add(-time.Duration(i)*time.Hour), labels, d, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			got := window.ComputeSuccessStdDev(testCutoff())
			want := math.Sqrt(20000.0) // ~ 141.42
			if math.Abs(got-want) > 0.01 {
				t.Errorf("StdDev = %f, want %f", got, want)
			}
		})
	})

	t.Run("single observation returns zero", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		store.RecordObservation(metricBuildDuration, "single", now, labels, 300.0, 10.0, true, "")

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "StdDev", window.ComputeSuccessStdDev(testCutoff()), 0.0)
		})
	})

	t.Run("only failures returns zero", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		for i := 0; i < 5; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("f-%d", i),
				now.Add(-time.Duration(i)*time.Hour), labels, 0, 0, false, "Failed")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "StdDev", window.ComputeSuccessStdDev(testCutoff()), 0.0)
		})
	})
}

func TestCountBreachingDays(t *testing.T) {
	t.Run("no breaching days", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// All durations are 300s, threshold is 500s -> no breaches
		for i := 0; i < 10; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("nb-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			breaching, total := window.CountBreachingDays(testCutoff(),500.0)
			assertEqual(t, "breachingDays", breaching, 0)
			assertEqual(t, "totalDays", total, 10)
		})
	})

	t.Run("some breaching days", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// Days 0-4: duration 300s (below threshold)
		for i := 0; i < 5; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("low-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}
		// Days 5-6: duration 600s (above threshold of 400)
		for i := 5; i < 7; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("high-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 600.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			breaching, total := window.CountBreachingDays(testCutoff(),400.0)
			assertEqual(t, "breachingDays", breaching, 2)
			assertEqual(t, "totalDays", total, 7)
		})
	})

	t.Run("daily mean exactly equal to threshold is not breaching", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// All durations are 400s, threshold is 400 -> dailyMean == threshold, strict > means no breach
		for i := 0; i < 10; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("eq-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 400.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			breaching, total := window.CountBreachingDays(testCutoff(), 400.0)
			assertEqual(t, "breachingDays", breaching, 0)
			assertEqual(t, "totalDays", total, 10)
		})
	})

	t.Run("days with only failures are skipped", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// Day 0: successful observation
		store.RecordObservation(metricBuildDuration, "s-0", now, labels, 300.0, 10.0, true, "")
		// Day 1: only failures
		store.RecordObservation(metricBuildDuration, "f-1",
			now.Add(-24*time.Hour), labels, 0, 0, false, "Failed")

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			breaching, total := window.CountBreachingDays(testCutoff(),500.0)
			assertEqual(t, "breachingDays", breaching, 0)
			assertEqual(t, "totalDays", total, 1) // Only 1 day has successful observations
		})
	})
}

func TestBuildDurationSLOBreach(t *testing.T) {
	t.Run("breach triggered when daily means exceed threshold", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// 20 days of normal builds at 300s
		for i := 3; i < 23; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("normal-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}
		// 3 recent days with dramatically higher duration (900s)
		for i := 0; i < 3; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("slow-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 900.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			successCount := window.ComputeSuccessCount(testCutoff())
			if successCount < minSuccessCountForSLO {
				t.Fatalf("expected >= %d successes, got %d", minSuccessCountForSLO, successCount)
			}

			mean30d := window.ComputeSuccessMean(testCutoff())
			stddev30d := window.ComputeSuccessStdDev(testCutoff())
			threshold := mean30d + sloThresholdK*stddev30d

			breachingDays, totalDays := window.CountBreachingDays(testCutoff(),threshold)
			if totalDays < minDaysWithDataForSLO {
				t.Fatalf("expected >= %d days with data, got %d", minDaysWithDataForSLO, totalDays)
			}

			breachFraction := float64(breachingDays) / float64(totalDays)
			if breachFraction < sloBreachPercentage {
				t.Errorf("expected breach: %d/%d days (%.1f%%) >= %.1f%%, threshold=%.1f (mean=%.1f + stddev=%.1f)",
					breachingDays, totalDays, breachFraction*100, sloBreachPercentage*100, threshold, mean30d, stddev30d)
			}
		})

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		m := &dto.Metric{}
		buildSLO.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 1 {
			t.Errorf("expected breach gauge=1, got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("no breach when all durations are consistent", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		for i := 0; i < 20; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("consistent-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			mean30d := window.ComputeSuccessMean(testCutoff())
			stddev30d := window.ComputeSuccessStdDev(testCutoff())
			threshold := mean30d + sloThresholdK*stddev30d

			assertFloat(t, "stddev", stddev30d, 0.0)
			assertFloat(t, "threshold", threshold, mean30d)

			breachingDays, totalDays := window.CountBreachingDays(testCutoff(),threshold)
			assertEqual(t, "breachingDays", breachingDays, 0)
			if totalDays < minDaysWithDataForSLO {
				t.Fatalf("expected >= %d days with data, got %d", minDaysWithDataForSLO, totalDays)
			}
		})

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		m := &dto.Metric{}
		buildSLO.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach gauge=0, got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("no breach for moderate variance that exceeds mean+1*stddev but not mean+2*stddev", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// 15 days at 300s, 5 days at 450s.
		// mean = (15*300 + 5*450) / 20 = 337.5
		// stddev ~= 64.95
		// mean + 1*stddev ~= 402.5  -> 5 days (450s) exceed this -> 5/20 = 25% -> breach at k=1
		// mean + 2*stddev ~= 467.4  -> 0 days exceed this -> 0/20 = 0% -> no breach at k=2
		for i := 0; i < 15; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("normal-%d", i),
				now.Add(-time.Duration((i+5)*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}
		for i := 0; i < 5; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("elevated-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 450.0, 10.0, true, "")
		}

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		m := &dto.Metric{}
		buildSLO.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach=0 with k=2 (daily means between mean+1*stddev and mean+2*stddev should not breach), got %v",
				m.GetGauge().GetValue())
		}
	})

	t.Run("single day with data skips SLO evaluation", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// 15 observations all on the same day - enough total count (>10)
		// but only 1 day with data (< minDaysWithDataForSLO=3)
		for i := 0; i < 15; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("single-day-%d", i),
				now.Add(-time.Duration(i)*time.Minute), labels, 900.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			successCount := window.ComputeSuccessCount(testCutoff())
			if successCount < minSuccessCountForSLO {
				t.Fatalf("expected >= %d successes, got %d", minSuccessCountForSLO, successCount)
			}
			mean30d := window.ComputeSuccessMean(testCutoff())
			stddev30d := window.ComputeSuccessStdDev(testCutoff())
			threshold := mean30d + sloThresholdK*stddev30d
			_, totalDays := window.CountBreachingDays(testCutoff(),threshold)
			if totalDays >= minDaysWithDataForSLO {
				t.Errorf("expected < %d days with data, got %d", minDaysWithDataForSLO, totalDays)
			}
		})

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		ch := make(chan prometheus.Metric, 100)
		buildSLO.durationSLOBreach.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count != 0 {
			t.Error("breach metric should not be emitted when days with data < minDaysWithDataForSLO")
		}
	})

	t.Run("insufficient data skips SLO evaluation", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// Only 5 observations - below minSuccessCountForSLO (10)
		for i := 0; i < 5; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("sparse-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 900.0, 10.0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			successCount := window.ComputeSuccessCount(testCutoff())
			if successCount >= minSuccessCountForSLO {
				t.Errorf("expected < %d successes, got %d", minSuccessCountForSLO, successCount)
			}
		})

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		ch := make(chan prometheus.Metric, 100)
		buildSLO.durationSLOBreach.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count != 0 {
			t.Error("breach metric should not be emitted when successCount < minSuccessCountForSLO")
		}
	})

	t.Run("boundary: exactly at breach percentage triggers breach", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// 19 days at 300s, 1 day at 600s with fixed threshold=400
		// -> 1/20 = 5% == sloBreachPercentage (0.05), >= fires -> breach=1
		for i := 0; i < 19; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("norm-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}
		store.RecordObservation(metricBuildDuration, "slow-0",
			now.Add(-19*24*time.Hour), labels, 600.0, 10.0, true, "")

		cfg := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: thresholdValueSimple(400),
			},
		}
		buildSLO := newBuildSLO30d(cfg)
		buildSLO.updateGauges(store, nil)

		m := &dto.Metric{}
		buildSLO.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 1 {
			t.Errorf("expected breach=1 at exact boundary (1/20=5%%), got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("boundary: just below breach percentage does not trigger", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		// 20 days all at 300s with fixed threshold=400
		// -> 0/20 = 0% < 5% -> breach=0
		for i := 0; i < 20; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("norm-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}

		cfg := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: thresholdValueSimple(400),
			},
		}
		buildSLO := newBuildSLO30d(cfg)
		buildSLO.updateGauges(store, nil)

		m := &dto.Metric{}
		buildSLO.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach=0 below boundary (0/20=0%%), got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("existing metrics unchanged after adding breach gauge", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		durations := []float64{100, 200, 300, 400, 500}
		for i, d := range durations {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("existing-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, d, float64(10+i*2), true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "SuccessMean", window.ComputeSuccessMean(testCutoff()), 300.0)
			assertEqual(t, "TotalCount", window.ComputeTotalCount(testCutoff()), int64(5))
			assertEqual(t, "SuccessCount", window.ComputeSuccessCount(testCutoff()), int64(5))
		})

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		// Verify store values are unchanged after updateGauges
		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "SuccessMean after update", window.ComputeSuccessMean(testCutoff()), 300.0)
			assertEqual(t, "TotalCount after update", window.ComputeTotalCount(testCutoff()), int64(5))
			assertEqual(t, "SuccessCount after update", window.ComputeSuccessCount(testCutoff()), int64(5))
		})
	})

	t.Run("empty store does not panic", func(t *testing.T) {
		store := NewStore()
		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)
	})

	t.Run("zero-duration workloads suppress breach metric", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		labels := buildLabelShort

		for i := 0; i < 20; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("zero-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 0, 0, true, "")
		}

		store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
			assertFloat(t, "SuccessMean", window.ComputeSuccessMean(testCutoff()), 0.0)
			assertFloat(t, "SuccessStdDev", window.ComputeSuccessStdDev(testCutoff()), 0.0)
		})

		buildSLO := newBuildSLO30d(nil)
		buildSLO.updateGauges(store, nil)

		ch := make(chan prometheus.Metric, 100)
		buildSLO.durationSLOBreach.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count != 0 {
			t.Error("breach metric should not be emitted when threshold=0 (zero-duration workloads)")
		}
	})
}

// ── Gap-Fill State Transition Tests ──────────────────────────────────────────

func TestUpdateGapFillState(t *testing.T) {
	t.Run("non-truncated completes bootstrap", func(t *testing.T) {
		state := &nsBootstrapState{
			OldestSeenCreationTS: "2026-07-20T00:00:00Z",
			GapAttempts:          2,
		}
		result := updateGapFillState(state, false, "")
		if result != "completed" {
			t.Errorf("expected completed, got %s", result)
		}
		if !state.Bootstrapped {
			t.Error("expected Bootstrapped=true")
		}
		if state.OldestSeenCreationTS != "" {
			t.Errorf("expected OldestSeenCreationTS cleared, got %q", state.OldestSeenCreationTS)
		}
		if state.GapAttempts != 0 {
			t.Errorf("expected GapAttempts=0, got %d", state.GapAttempts)
		}
	})

	t.Run("truncated with progress resets error counter", func(t *testing.T) {
		state := &nsBootstrapState{
			OldestSeenCreationTS: "2026-07-20T00:00:00Z",
			GapAttempts:          3,
		}
		result := updateGapFillState(state, true, "2026-07-15T00:00:00Z")
		if result != "progressing" {
			t.Errorf("expected progressing, got %s", result)
		}
		if state.OldestSeenCreationTS != "2026-07-15T00:00:00Z" {
			t.Errorf("expected oldest moved to Jul 15, got %q", state.OldestSeenCreationTS)
		}
		if state.GapAttempts != 0 {
			t.Errorf("expected GapAttempts reset to 0, got %d", state.GapAttempts)
		}
	})

	t.Run("truncated without progress increments error counter", func(t *testing.T) {
		state := &nsBootstrapState{
			OldestSeenCreationTS: "2026-07-20T00:00:00Z",
			GapAttempts:          2,
		}
		result := updateGapFillState(state, true, "2026-07-20T00:00:00Z")
		if result != "stalled" {
			t.Errorf("expected stalled, got %s", result)
		}
		if state.OldestSeenCreationTS != "2026-07-20T00:00:00Z" {
			t.Errorf("expected oldest unchanged, got %q", state.OldestSeenCreationTS)
		}
		if state.GapAttempts != 3 {
			t.Errorf("expected GapAttempts=3, got %d", state.GapAttempts)
		}
	})

	t.Run("newer oldest timestamp counts as stall", func(t *testing.T) {
		state := &nsBootstrapState{
			OldestSeenCreationTS: "2026-07-15T00:00:00Z",
			GapAttempts:          0,
		}
		result := updateGapFillState(state, true, "2026-07-20T00:00:00Z")
		if result != "stalled" {
			t.Errorf("expected stalled, got %s", result)
		}
		if state.GapAttempts != 1 {
			t.Errorf("expected GapAttempts=1, got %d", state.GapAttempts)
		}
	})

	t.Run("progress after errors resets counter", func(t *testing.T) {
		state := &nsBootstrapState{
			OldestSeenCreationTS: "2026-07-20T00:00:00Z",
			GapAttempts:          4,
		}
		result := updateGapFillState(state, true, "2026-07-10T00:00:00Z")
		if result != "progressing" {
			t.Errorf("expected progressing, got %s", result)
		}
		if state.GapAttempts != 0 {
			t.Errorf("expected GapAttempts reset to 0 after progress, got %d", state.GapAttempts)
		}
	})
}

func TestSumSquaredSecondsAccumulation(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	labels := buildLabelShort

	durations := []float64{100, 200, 300}
	for i, d := range durations {
		store.RecordObservation(metricBuildDuration, fmt.Sprintf("sq-%d", i),
			now.Add(-time.Duration(i)*time.Hour), labels, d, 10.0, true, "")
	}

	store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
		var totalSumSq float64
		for _, b := range window.Buckets {
			totalSumSq += b.SuccessSumSquaredSeconds
		}
		// 100^2 + 200^2 + 300^2 = 10000 + 40000 + 90000 = 140000
		assertFloat(t, "SumSquaredSeconds", totalSumSq, 140000.0)
	})
}

// ── Integration Duration SLO Breach Tests ────────────────────────────────────

func TestIntegrationDurationSLOBreach(t *testing.T) {
	integrationLabels := integrationLabelShort

	t.Run("breach triggered when daily means exceed threshold", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		for i := 3; i < 23; i++ {
			store.RecordObservation(metricIntegrationDuration, fmt.Sprintf("normal-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), integrationLabels, 300.0, 10.0, true, "")
		}
		for i := 0; i < 3; i++ {
			store.RecordObservation(metricIntegrationDuration, fmt.Sprintf("slow-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), integrationLabels, 900.0, 10.0, true, "")
		}

		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			mean30d := window.ComputeSuccessMean(testCutoff())
			stddev30d := window.ComputeSuccessStdDev(testCutoff())
			threshold := mean30d + sloThresholdK*stddev30d
			breachingDays, totalDays := window.CountBreachingDays(testCutoff(),threshold)
			breachFraction := float64(breachingDays) / float64(totalDays)
			if breachFraction < sloBreachPercentage {
				t.Errorf("expected breach: %d/%d = %.1f%% >= %.1f%%",
					breachingDays, totalDays, breachFraction*100, sloBreachPercentage*100)
			}
		})

		slo := newIntegrationSLO30d(nil)
		slo.updateGauges(store, nil)

		m := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "my-scenario", "false", "integration", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 1 {
			t.Errorf("expected breach gauge=1, got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("no breach when durations are consistent", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		for i := 0; i < 20; i++ {
			store.RecordObservation(metricIntegrationDuration, fmt.Sprintf("consistent-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), integrationLabels, 300.0, 10.0, true, "")
		}

		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			stddev30d := window.ComputeSuccessStdDev(testCutoff())
			assertFloat(t, "stddev", stddev30d, 0.0)
		})

		slo := newIntegrationSLO30d(nil)
		slo.updateGauges(store, nil)

		m := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "my-scenario", "false", "integration", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach gauge=0, got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("insufficient data skips evaluation", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		for i := 0; i < 5; i++ {
			store.RecordObservation(metricIntegrationDuration, fmt.Sprintf("sparse-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), integrationLabels, 900.0, 10.0, true, "")
		}

		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			if window.ComputeSuccessCount(testCutoff()) >= minSuccessCountForSLO {
				t.Errorf("expected < %d successes", minSuccessCountForSLO)
			}
		})

		slo := newIntegrationSLO30d(nil)
		slo.updateGauges(store, nil)
	})

	t.Run("empty store does not panic", func(t *testing.T) {
		store := NewStore()
		slo := newIntegrationSLO30d(nil)
		slo.updateGauges(store, nil)
	})
}

// ── Release Duration SLO Breach Tests ────────────────────────────────────────

func TestReleaseDurationSLOBreach(t *testing.T) {
	releaseLabels := releaseLabelShort

	t.Run("breach triggered when daily means exceed threshold", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		for i := 3; i < 23; i++ {
			store.RecordObservation(metricReleaseDuration, fmt.Sprintf("normal-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), releaseLabels, 600.0, 10.0, true, "")
		}
		for i := 0; i < 3; i++ {
			store.RecordObservation(metricReleaseDuration, fmt.Sprintf("slow-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), releaseLabels, 1800.0, 10.0, true, "")
		}

		store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
			mean30d := window.ComputeSuccessMean(testCutoff())
			stddev30d := window.ComputeSuccessStdDev(testCutoff())
			threshold := mean30d + sloThresholdK*stddev30d
			breachingDays, totalDays := window.CountBreachingDays(testCutoff(),threshold)
			breachFraction := float64(breachingDays) / float64(totalDays)
			if breachFraction < sloBreachPercentage {
				t.Errorf("expected breach: %d/%d = %.1f%% >= %.1f%%",
					breachingDays, totalDays, breachFraction*100, sloBreachPercentage*100)
			}
		})

		slo := newReleaseSLO30d(nil)
		slo.updateGauges(store, nil)

		m := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "true", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 1 {
			t.Errorf("expected breach gauge=1, got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("no breach when durations are consistent", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		for i := 0; i < 20; i++ {
			store.RecordObservation(metricReleaseDuration, fmt.Sprintf("consistent-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), releaseLabels, 600.0, 10.0, true, "")
		}

		slo := newReleaseSLO30d(nil)
		slo.updateGauges(store, nil)

		m := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "true", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach gauge=0, got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("insufficient data skips evaluation", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		for i := 0; i < 5; i++ {
			store.RecordObservation(metricReleaseDuration, fmt.Sprintf("sparse-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), releaseLabels, 1800.0, 10.0, true, "")
		}

		slo := newReleaseSLO30d(nil)
		slo.updateGauges(store, nil)
	})

	t.Run("empty store does not panic", func(t *testing.T) {
		store := NewStore()
		slo := newReleaseSLO30d(nil)
		slo.updateGauges(store, nil)
	})
}

// ── SLO Config Override Tests ────────────────────────────────────────────────

func TestSLOBreachWithConfigOverrides(t *testing.T) {
	buildLabels := LabelSet{Cluster: "c", Namespace: "my-tenant", Application: "my-app", Component: "my-comp",
		BuildType: "docker-builds", EventType: "push"}

	populateConsistentBuilds := func(store *Store, labels LabelSet, duration float64, count int) {
		now := time.Now().UTC()
		for i := 0; i < count; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("b-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, duration, 10.0, true, "")
		}
	}

	// Table-driven: consistent 300s builds, varying config hierarchy level and threshold
	hierarchyTests := []struct {
		name        string
		cfg         *SLOConfig
		wantBreach  float64
		addOtherNS  bool        // also populate other-tenant (no override)
		wantOther   float64     // expected breach for other-tenant when addOtherNS=true
	}{
		{
			name: "global threshold exceeded",
			cfg: &SLOConfig{
				SLOThresholds: SLOThresholds{
					BuildDurationThresholdSeconds: thresholdValueSimple(200),
				},
			},
			wantBreach: 1,
		},
		{
			name: "global threshold not exceeded",
			cfg: &SLOConfig{
				SLOThresholds: SLOThresholds{
					BuildDurationThresholdSeconds: thresholdValueSimple(500),
				},
			},
			wantBreach: 0,
		},
		{
			name: "tenant-scoped override only affects matching namespace",
			cfg: &SLOConfig{
				Tenants: map[string]*TenantSLOConfig{
					"my-tenant": {
						SLOThresholds: SLOThresholds{
							BuildDurationThresholdSeconds: thresholdValueSimple(200),
						},
					},
				},
			},
			wantBreach: 1,
			addOtherNS: true,
			wantOther:  0,
		},
		{
			name: "component overrides tenant",
			cfg: newSLOCfg().
				tenant("my-tenant").buildThreshold(500).
				app("my-app").
				comp("my-comp", SLOThresholds{BuildDurationThresholdSeconds: thresholdValueSimple(200)}).
				build(),
			wantBreach: 1,
		},
	}
	for _, tt := range hierarchyTests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			populateConsistentBuilds(store, buildLabels, 300.0, 20)

			if tt.addOtherNS {
				otherLabels := buildLabels
				otherLabels.Namespace = "other-tenant"
				populateConsistentBuilds(store, otherLabels, 300.0, 20)
			}

			slo := newBuildSLO30d(tt.cfg)
			slo.updateGauges(store, nil)

			m := &dto.Metric{}
			slo.durationSLOBreach.WithLabelValues("c", "my-tenant", "my-app", "my-comp", "docker-builds", "push").Write(m) //nolint:errcheck
			if m.GetGauge().GetValue() != tt.wantBreach {
				t.Errorf("my-tenant: expected breach=%v, got %v", tt.wantBreach, m.GetGauge().GetValue())
			}

			if tt.addOtherNS {
				m = &dto.Metric{}
				slo.durationSLOBreach.WithLabelValues("c", "other-tenant", "my-app", "my-comp", "docker-builds", "push").Write(m) //nolint:errcheck
				if m.GetGauge().GetValue() != tt.wantOther {
					t.Errorf("other-tenant: expected breach=%v, got %v", tt.wantOther, m.GetGauge().GetValue())
				}
			}
		})
	}

	t.Run("breach percentage override relaxes threshold", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()

		// 17 days at 300s, 3 days at 600s -> with 2*stddev, 3 days breach
		// 3/20 = 15% -> default 5% would trigger, but 20% override should not
		for i := 0; i < 17; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("norm-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), buildLabels, 300.0, 10.0, true, "")
		}
		for i := 17; i < 20; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("slow-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), buildLabels, 600.0, 10.0, true, "")
		}

		cfg := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationBreachPercentage: thresholdValueSimple(0.20),
			},
		}
		slo := newBuildSLO30d(cfg)
		slo.updateGauges(store, nil)

		m := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "my-tenant", "my-app", "my-comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach=0 with 20%% override (15%% < 20%%), got %v", m.GetGauge().GetValue())
		}
	})

	t.Run("integration domain override does not affect build", func(t *testing.T) {
		store := NewStore()
		populateConsistentBuilds(store, buildLabels, 300.0, 20)

		cfg := &SLOConfig{
			SLOThresholds: SLOThresholds{
				IntegrationDurationThresholdSeconds: thresholdValueSimple(100),
			},
		}

		resolved := cfg.Resolve(buildLabels, metricBuildDuration)
		if resolved.DurationThreshold != nil {
			t.Errorf("integration threshold should not apply to build, got %v", *resolved.DurationThreshold)
		}

		slo := newBuildSLO30d(cfg)
		slo.updateGauges(store, nil)

		m := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "my-tenant", "my-app", "my-comp", "docker-builds", "push").Write(m) //nolint:errcheck
		if m.GetGauge().GetValue() != 0 {
			t.Errorf("expected breach=0 (integration override should not affect build domain), got %v", m.GetGauge().GetValue())
		}
	})
}

// ── Match-Based Config End-to-End Tests ──────────────────────────────────────

func TestSLOBreachWithMatchBasedConfig(t *testing.T) {
	now := time.Now().UTC()

	populateIntegrationData := func(store *Store, scenario, eventType string, duration float64, count int) {
		ls := LabelSet{
			Cluster: "c", Namespace: "my-tenant", Application: "my-app", Component: "my-comp",
			Scenario: scenario, EventType: eventType, TestType: "integration", Optional: "true",
		}
		for i := 0; i < count; i++ {
			store.RecordObservation(metricIntegrationDuration,
				fmt.Sprintf("%s-%s-%d", scenario, eventType, i),
				now.Add(-time.Duration(i*24)*time.Hour), ls, duration, 5.0, true, "")
		}
	}

	t.Run("different scenarios get different breach verdicts", func(t *testing.T) {
		store := NewStore()

		// ec-scan runs at ~50s, long-test push at ~800s, long-test PR at ~60s
		populateIntegrationData(store, "ec-scan", "push", 50.0, 20)
		populateIntegrationData(store, "long-test", "push", 800.0, 20)
		populateIntegrationData(store, "long-test", "pull_request", 60.0, 20)

		cfg := newSLOCfg().
			tenant("my-tenant").
			app("my-app").
			comp("my-comp", SLOThresholds{
				IntegrationDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(120),
					Matches: []ThresholdMatch{
						{Scenario: "ec-scan", Value: 300},
						{Scenario: "long-test", EventType: "push", Value: 900},
					},
				},
			}).
			build()

		slo := newIntegrationSLO30d(cfg)
		slo.updateGauges(store, nil)

		type breachResult struct {
			scenario  string
			eventType string
			breach    float64
		}
		var results []breachResult

		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			if ls.Component != "my-comp" {
				return
			}
			labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Scenario, ls.Optional, ls.TestType, ls.EventType}

			m := &dto.Metric{}
			slo.durationSLOBreach.WithLabelValues(labels...).Write(m) //nolint:errcheck
			results = append(results, breachResult{
				scenario:  ls.Scenario,
				eventType: ls.EventType,
				breach:    m.GetGauge().GetValue(),
			})
		})

		for _, r := range results {
			switch {
			case r.scenario == "ec-scan" && r.eventType == "push":
				// 50s mean vs 300 threshold -> no breach
				if r.breach != 0 {
					t.Errorf("ec-scan/push: expected breach=0 (50s < 300s threshold), got %v", r.breach)
				}
			case r.scenario == "long-test" && r.eventType == "push":
				// 800s mean vs 900 threshold -> no breach
				if r.breach != 0 {
					t.Errorf("long-test/push: expected breach=0 (800s < 900s match threshold), got %v", r.breach)
				}
			case r.scenario == "long-test" && r.eventType == "pull_request":
				// 60s mean vs 120 default -> no breach
				if r.breach != 0 {
					t.Errorf("long-test/pull_request: expected breach=0 (60s < 120s default), got %v", r.breach)
				}
			}
		}
		if len(results) != 3 {
			t.Errorf("expected 3 series with breach metrics, got %d", len(results))
		}
	})

	t.Run("uncovered scenario falls to default and breaches", func(t *testing.T) {
		store := NewStore()

		// uncovered-scenario runs at 800s, only match is for ec-scan
		populateIntegrationData(store, "uncovered-scenario", "push", 800.0, 20)

		cfg := newSLOCfg().
			tenant("my-tenant").
			app("my-app").
			comp("my-comp", SLOThresholds{
				IntegrationDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(120),
					Matches: []ThresholdMatch{
						{Scenario: "ec-scan", Value: 300},
					},
				},
			}).
			build()

		slo := newIntegrationSLO30d(cfg)
		slo.updateGauges(store, nil)

		var found bool
		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			if ls.Scenario != "uncovered-scenario" {
				return
			}
			found = true
			labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Scenario, ls.Optional, ls.TestType, ls.EventType}
			m := &dto.Metric{}
			slo.durationSLOBreach.WithLabelValues(labels...).Write(m) //nolint:errcheck
			if m.GetGauge().GetValue() != 1 {
				t.Errorf("uncovered-scenario: expected breach=1 (800s > 120s default), got %v", m.GetGauge().GetValue())
			}
		})
		if !found {
			t.Error("uncovered-scenario series not found in store")
		}
	})

	t.Run("matches-only config with no default preserves parent threshold", func(t *testing.T) {
		store := NewStore()

		// other-scenario runs at 800s; component has matches-only (no default),
		// tenant has threshold=5000 which should be preserved
		populateIntegrationData(store, "other-scenario", "push", 800.0, 20)

		cfg := newSLOCfg().
			tenant("my-tenant").integrationThreshold(5000).
			app("my-app").
			comp("my-comp", SLOThresholds{
				IntegrationDurationThresholdSeconds: &ThresholdValue{
					Matches: []ThresholdMatch{
						{Scenario: "ec-scan", Value: 300},
					},
				},
			}).
			build()

		slo := newIntegrationSLO30d(cfg)
		slo.updateGauges(store, nil)

		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			if ls.Scenario != "other-scenario" {
				return
			}
			labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Scenario, ls.Optional, ls.TestType, ls.EventType}
			m := &dto.Metric{}
			slo.durationSLOBreach.WithLabelValues(labels...).Write(m) //nolint:errcheck
			// 800s vs tenant threshold 5000 (preserved because component match didn't fire) -> no breach
			if m.GetGauge().GetValue() != 0 {
				t.Errorf("other-scenario: expected breach=0 (800s < 5000s parent threshold), got %v", m.GetGauge().GetValue())
			}
		})
	})

	t.Run("unmatched scenario with no parent threshold falls to mean+2*stddev", func(t *testing.T) {
		store := NewStore()

		// ec-scan: 50s mean. Match sets fixed threshold=60, and 50 < 60 -> no breach.
		// If it fell to mean+2*stddev instead, threshold would be ~50 (stddev~=0),
		// and daily means = 50, not > 50, also no breach -- so we need ec-scan
		// to prove the match fires by using a threshold that differs from mean+2*stddev.
		populateIntegrationData(store, "ec-scan", "push", 50.0, 20)

		// other-scenario: consistent 300s with a few spikes.
		// mean+2*stddev with consistent data -> stddev~=0, threshold~=300,
		// daily means = 300, not > 300 -> no breach under statistical baseline.
		// BUT if a fixed threshold like ec-scan's 60 were applied, 300 >> 60 -> breach.
		// So breach=0 here proves it's using mean+2*stddev, not the ec-scan match.
		populateIntegrationData(store, "other-scenario", "push", 300.0, 20)

		cfg := newSLOCfg().
			tenant("my-tenant").
			app("my-app").
			comp("my-comp", SLOThresholds{
				IntegrationDurationThresholdSeconds: &ThresholdValue{
					Matches: []ThresholdMatch{
						{Scenario: "ec-scan", Value: 60},
					},
				},
			}).
			build()

		slo := newIntegrationSLO30d(cfg)
		slo.updateGauges(store, nil)

		store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
			labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Scenario, ls.Optional, ls.TestType, ls.EventType}
			m := &dto.Metric{}
			slo.durationSLOBreach.WithLabelValues(labels...).Write(m) //nolint:errcheck

			switch ls.Scenario {
			case "ec-scan":
				// 50s mean vs 60 fixed threshold -> no breach
				if m.GetGauge().GetValue() != 0 {
					t.Errorf("ec-scan: expected breach=0 (50s < 60s match threshold), got %v", m.GetGauge().GetValue())
				}
			case "other-scenario":
				// 300s consistent -> mean+2*stddev threshold ~300, daily means not > 300 -> no breach
				// If the ec-scan match (60) leaked here, 300 >> 60 would breach=1
				if m.GetGauge().GetValue() != 0 {
					t.Errorf("other-scenario: expected breach=0 (using mean+2*stddev, not ec-scan's 60s), got %v", m.GetGauge().GetValue())
				}
			}
		})
	})
}

// ── Skip Breach Map Construction Tests ───────────────────────────────────────

func TestSkipBreachNamespacesFromStates(t *testing.T) {
	tests := []struct {
		name        string
		states      map[string]*nsBootstrapState
		wantNil     bool
		wantLen     int
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name:    "nil map returns nil",
			states:  nil,
			wantNil: true,
		},
		{
			name: "all bootstrapped returns nil",
			states: map[string]*nsBootstrapState{
				"ns-a": {Bootstrapped: true},
				"ns-b": {Bootstrapped: true},
			},
			wantNil: true,
		},
		{
			name: "mixed states returns only un-bootstrapped",
			states: map[string]*nsBootstrapState{
				"bootstrapped-ns": {Bootstrapped: true},
				"pending-ns":      {Bootstrapped: false},
				"another-pending": {Bootstrapped: false, GapAttempts: 2},
			},
			wantLen:     2,
			wantPresent: []string{"pending-ns", "another-pending"},
			wantAbsent:  []string{"bootstrapped-ns"},
		},
		{
			name: "all un-bootstrapped returns all",
			states: map[string]*nsBootstrapState{
				"ns-a": {Bootstrapped: false},
				"ns-b": {Bootstrapped: false},
			},
			wantLen:     2,
			wantPresent: []string{"ns-a", "ns-b"},
		},
		{
			name: "exhausted retries not skipped",
			states: map[string]*nsBootstrapState{
				"still-trying": {Bootstrapped: false, GapAttempts: 3},
				"gave-up":      {Bootstrapped: false, GapAttempts: 5},
			},
			wantLen:     1,
			wantPresent: []string{"still-trying"},
			wantAbsent:  []string{"gave-up"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skip := skipBreachNamespacesFromStates(tt.states)
			if tt.wantNil {
				if skip != nil {
					t.Errorf("expected nil, got %v", skip)
				}
				return
			}
			if len(skip) != tt.wantLen {
				t.Fatalf("expected %d entries, got %d: %v", tt.wantLen, len(skip), skip)
			}
			for _, ns := range tt.wantPresent {
				if !skip[ns] {
					t.Errorf("%s should be in skip set", ns)
				}
			}
			for _, ns := range tt.wantAbsent {
				if skip[ns] {
					t.Errorf("%s should not be in skip set", ns)
				}
			}
		})
	}
}

// ── Gap-Fill Breach Skip Tests ───────────────────────────────────────────────

func TestBreachSkippedDuringGapFill(t *testing.T) {
	labels := LabelSet{Cluster: "c", Namespace: "gapfill-tenant", Application: "app", Component: "comp",
		BuildType: "docker-builds", EventType: "push"}

	populateBreachingData := func(store *Store) {
		now := time.Now().UTC()
		for i := 3; i < 23; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("normal-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 300.0, 10.0, true, "")
		}
		for i := 0; i < 3; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("slow-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), labels, 900.0, 10.0, true, "")
		}
	}

	t.Run("breach emitted when namespace is not in skip set", func(t *testing.T) {
		store := NewStore()
		populateBreachingData(store)

		slo := newBuildSLO30d(nil)
		slo.updateGauges(store, nil)

		ch := make(chan prometheus.Metric, 100)
		slo.durationSLOBreach.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count == 0 {
			t.Error("breach metric should be emitted when namespace is not skipped")
		}
	})

	t.Run("breach not emitted when namespace is in skip set", func(t *testing.T) {
		store := NewStore()
		populateBreachingData(store)

		skip := map[string]bool{"gapfill-tenant": true}
		slo := newBuildSLO30d(nil)
		slo.updateGauges(store, skip)

		ch := make(chan prometheus.Metric, 100)
		slo.durationSLOBreach.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count != 0 {
			t.Error("breach metric should NOT be emitted when namespace is being gap-filled")
		}
	})

	t.Run("base metrics still emitted when namespace is in skip set", func(t *testing.T) {
		store := NewStore()
		populateBreachingData(store)

		skip := map[string]bool{"gapfill-tenant": true}
		slo := newBuildSLO30d(nil)
		slo.updateGauges(store, skip)

		ch := make(chan prometheus.Metric, 100)
		slo.totalCount30d.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count == 0 {
			t.Error("base metrics (total_count) should still be emitted during gap-fill")
		}
	})

	t.Run("other namespaces unaffected by skip set", func(t *testing.T) {
		store := NewStore()
		populateBreachingData(store)

		otherLabels := labels
		otherLabels.Namespace = "healthy-tenant"
		now := time.Now().UTC()
		for i := 3; i < 23; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("other-normal-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), otherLabels, 300.0, 10.0, true, "")
		}
		for i := 0; i < 3; i++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("other-slow-%d", i),
				now.Add(-time.Duration(i*24)*time.Hour), otherLabels, 900.0, 10.0, true, "")
		}

		skip := map[string]bool{"gapfill-tenant": true}
		slo := newBuildSLO30d(nil)
		slo.updateGauges(store, skip)

		ch := make(chan prometheus.Metric, 100)
		slo.durationSLOBreach.Collect(ch)
		close(ch)
		count := 0
		for range ch {
			count++
		}
		if count != 1 {
			t.Errorf("expected 1 breach metric (healthy-tenant only), got %d", count)
		}
	})
}

// ── Test Helpers ──────────────────────────────────────────────────────────────

func testCutoff() string {
	return time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
}

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

