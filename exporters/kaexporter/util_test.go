package main

import (
	"testing"
)

// ── Simple Helpers ────────────────────────────────────────────────────────────

func makePLR(condType, status, reason string) PipelineRun {
	return PipelineRun{
		Status: struct {
			StartTime      string      `json:"startTime"`
			CompletionTime string      `json:"completionTime"`
			Conditions     []Condition `json:"conditions"`
		}{
			Conditions: []Condition{{Type: condType, Status: status, Reason: reason}},
		},
	}
}

func makeRelease(condType, status, reason string) Release {
	return Release{
		Status: struct {
			StartTime      string      `json:"startTime"`
			CompletionTime string      `json:"completionTime"`
			Conditions     []Condition `json:"conditions"`
		}{
			Conditions: []Condition{{Type: condType, Status: status, Reason: reason}},
		},
	}
}

// ── plrStatus Tests ───────────────────────────────────────────────────────────

func TestPLRStatus(t *testing.T) {
	tests := []struct {
		name          string
		plr           PipelineRun
		wantSucceeded bool
		wantReason    string
	}{
		{"succeeded", makePLR("Succeeded", "True", ""), true, ""},
		{"failed - timeout", makePLR("Succeeded", "False", "PipelineRunTimeout"), false, "PipelineRunTimeout"},
		{"failed - couldn't get task", makePLR("Succeeded", "False", "CouldntGetTask"), false, "CouldntGetTask"},
		{"failed - couldn't get pipeline", makePLR("Succeeded", "False", "CouldntGetPipeline"), false, "CouldntGetPipeline"},
		{"failed - create run failed", makePLR("Succeeded", "False", "CreateRunFailed"), false, "CreateRunFailed"},
		{"failed - generic", makePLR("Succeeded", "False", "Failed"), false, "Failed"},
		{"failed - empty reason defaults to Unknown", makePLR("Succeeded", "False", ""), false, "Unknown"},
		{"no succeeded condition", PipelineRun{}, false, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSucceeded, gotReason := plrStatus(tt.plr)
			if gotSucceeded != tt.wantSucceeded {
				t.Errorf("plrStatus() succeeded = %v, want %v", gotSucceeded, tt.wantSucceeded)
			}
			if gotReason != tt.wantReason {
				t.Errorf("plrStatus() reason = %v, want %v", gotReason, tt.wantReason)
			}
		})
	}
}

// ── releaseStatus Tests ───────────────────────────────────────────────────────

func TestReleaseStatus(t *testing.T) {
	tests := []struct {
		name          string
		release       Release
		wantCompleted bool
		wantSucceeded bool
		wantReason    string
	}{
		{"succeeded", makeRelease("Released", "True", "Succeeded"), true, true, ""},
		{"failed", makeRelease("Released", "False", "Failed"), true, false, "Failed"},
		{"skipped", makeRelease("Released", "False", "Skipped"), true, false, "Skipped"},
		{"progressing (not completed)", makeRelease("Released", "False", "Progressing"), false, false, ""},
		{"failed with empty reason", makeRelease("Released", "False", ""), true, false, "Unknown"},
		{"no released condition", Release{}, false, false, ""},
		// Status=True with Reason != "Succeeded" is treated as incomplete (not counted).
		// The Release controller sets Status=True ONLY with Reason=Succeeded on genuine
		// completion. Any other Reason with Status=True is a transient or malformed state
		// that should not be recorded as either success or failure.
		{"status true but reason not succeeded", makeRelease("Released", "True", "Failed"), false, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCompleted, gotSucceeded, gotReason := releaseStatus(tt.release)
			if gotCompleted != tt.wantCompleted {
				t.Errorf("releaseStatus() completed = %v, want %v", gotCompleted, tt.wantCompleted)
			}
			if gotSucceeded != tt.wantSucceeded {
				t.Errorf("releaseStatus() succeeded = %v, want %v", gotSucceeded, tt.wantSucceeded)
			}
			if gotReason != tt.wantReason {
				t.Errorf("releaseStatus() reason = %v, want %v", gotReason, tt.wantReason)
			}
		})
	}
}

// ── Status Wrapper Tests (spot-check that wrappers delegate correctly) ────────

func TestStatusWrappers(t *testing.T) {
	t.Run("isPLRSucceeded", func(t *testing.T) {
		if !isPLRSucceeded(makePLR("Succeeded", "True", "")) {
			t.Error("expected true for succeeded PLR")
		}
		if isPLRSucceeded(makePLR("Succeeded", "False", "Failed")) {
			t.Error("expected false for failed PLR")
		}
	})

	t.Run("isReleaseSucceeded", func(t *testing.T) {
		if !isReleaseSucceeded(makeRelease("Released", "True", "Succeeded")) {
			t.Error("expected true for succeeded release")
		}
		if isReleaseSucceeded(makeRelease("Released", "False", "Failed")) {
			t.Error("expected false for failed release")
		}
	})

	t.Run("isReleaseCompleted", func(t *testing.T) {
		if !isReleaseCompleted(makeRelease("Released", "True", "Succeeded")) {
			t.Error("expected true for completed release")
		}
		if isReleaseCompleted(makeRelease("Released", "False", "Progressing")) {
			t.Error("expected false for in-progress release")
		}
	})
}

// ── getLabel Tests ────────────────────────────────────────────────────────────

func TestGetLabel(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		key        string
		defaultVal string
		want       string
	}{
		{"found", map[string]string{"foo": "bar"}, "foo", "default", "bar"},
		{"missing", map[string]string{"other": "value"}, "foo", "default", "default"},
		{"nil labels", nil, "foo", "default", "default"},
		{"empty labels", map[string]string{}, "foo", "default", "default"},
	}

	for _, tt := range tests {
		t.Run("PLR_"+tt.name, func(t *testing.T) {
			plr := NewPLR().Labels(tt.labels).Build()
			if got := getLabel(plr, tt.key, tt.defaultVal); got != tt.want {
				t.Errorf("getLabel(PLR) = %q, want %q", got, tt.want)
			}
		})

		t.Run("Release_"+tt.name, func(t *testing.T) {
			rel := NewRelease().Labels(tt.labels).Build()
			if got := getLabel(rel, tt.key, tt.defaultVal); got != tt.want {
				t.Errorf("getLabel(Release) = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── secondsBetween Tests ──────────────────────────────────────────────────────

func TestSecondsBetween(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
		want  float64
	}{
		{"5 minute duration", "2026-06-01T10:00:00Z", "2026-06-01T10:05:00Z", 300.0},
		{"1 hour duration", "2026-06-01T10:00:00Z", "2026-06-01T11:00:00Z", 3600.0},
		{"zero duration", "2026-06-01T10:00:00Z", "2026-06-01T10:00:00Z", 0.0},
		{"subsecond precision", "2026-06-01T10:00:00.500Z", "2026-06-01T10:00:01.250Z", 0.75},
		{"empty start", "", "2026-06-01T10:05:00Z", -1.0},
		{"empty end", "2026-06-01T10:00:00Z", "", -1.0},
		{"both empty", "", "", -1.0},
		{"invalid start", "not-a-timestamp", "2026-06-01T10:05:00Z", -1.0},
		{"invalid end", "2026-06-01T10:00:00Z", "invalid", -1.0},
		{"negative (clock skew)", "2026-06-01T10:05:00Z", "2026-06-01T10:00:00Z", -300.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := secondsBetween(tt.start, tt.end); got != tt.want {
				t.Errorf("secondsBetween(%q, %q) = %v, want %v", tt.start, tt.end, got, tt.want)
			}
		})
	}
}

// ── extractBuildType Tests ────────────────────────────────────────────────────

func TestExtractBuildType(t *testing.T) {
	tests := []struct {
		pipelineName string
		want         string
	}{
		// Docker builds
		{"docker-build-oci-ta", "docker-builds"},
		{"docker-build", "docker-builds"},
		{"docker-build-custom", "docker-builds"},

		// Multi-platform docker (must match before regular docker)
		{"docker-build-multi-platform-oci-ta", "docker-multi-arch-builds"},
		{"docker-build-multi-platform", "docker-multi-arch-builds"},

		// Bundle builds
		{"bundle-build-oci-ta", "bundle-builds"},
		{"bundle-build", "bundle-builds"},

		// Standard pipeline
		{"standard-pipeline", "standard-builds"},

		// Operator bundle (must match before operator)
		{"ose-4-23-local-storage-operator-bundle", "operator-bundle-builds"},
		{"my-operator-bundle", "operator-bundle-builds"},

		// Operator builds
		{"mtc-1-8-openshift-migration-operator", "operator-builds"},
		{"fbc-mtc-1-8-openshift-migration-operator", "operator-builds"},
		{"custom-operator", "operator-builds"},

		// FBC builds (only if not operator-related)
		{"v419-cnv-fbc-on-push", "fbc-builds"},
		{"my-fbc-pipeline", "fbc-builds"},

		// RPM builds
		{"rpm-build-oci-ta", "rpm-builds"},
		{"custom-rpm-builder", "rpm-builds"},

		// Custom builds (fallback)
		{"my-custom-pipeline", "custom-builds"},
		{"special-build-v2", "custom-builds"},

		// Edge cases
		{"", "unknown"},
		{"docker", "custom-builds"},
		{"operator-bundle-operator", "operator-builds"},
	}

	for _, tt := range tests {
		t.Run(tt.pipelineName, func(t *testing.T) {
			if got := extractBuildType(tt.pipelineName); got != tt.want {
				t.Errorf("extractBuildType(%q) = %q, want %q", tt.pipelineName, got, tt.want)
			}
		})
	}
}

// ── Dedupe Key Tests ──────────────────────────────────────────────────────────

func TestDedupeKeys(t *testing.T) {
	t.Run("PLR with UID", func(t *testing.T) {
		plr := NewPLR().UID("abc-123").Name("my-pipeline").Build()
		if got := plrDedupeKey("test-ns", plr); got != "plr:abc-123" {
			t.Errorf("plrDedupeKey() = %q, want %q", got, "plr:abc-123")
		}
	})

	t.Run("PLR without UID (fallback)", func(t *testing.T) {
		plr := NewPLR().Name("build-run-1").Build()
		if got := plrDedupeKey("default", plr); got != "plr:default/build-run-1" {
			t.Errorf("plrDedupeKey() = %q, want %q", got, "plr:default/build-run-1")
		}
	})

	t.Run("Release", func(t *testing.T) {
		if got := releaseDedupeKey("tenant-ns", "release-123"); got != "release:tenant-ns/release-123" {
			t.Errorf("releaseDedupeKey() = %q, want %q", got, "release:tenant-ns/release-123")
		}
	})

	t.Run("Release empty namespace", func(t *testing.T) {
		if got := releaseDedupeKey("", "my-release"); got != "release:/my-release" {
			t.Errorf("releaseDedupeKey() = %q, want %q", got, "release:/my-release")
		}
	})
}
