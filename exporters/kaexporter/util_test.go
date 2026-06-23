package main

import (
	"testing"
)

// ─── isReleaseSucceeded Tests ─────────────────────────────────────────────────

func TestIsReleaseSucceeded(t *testing.T) {
	tests := []struct {
		name       string
		release    Release
		wantResult bool
	}{
		{
			name: "success: Released=True with Reason=Succeeded",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: "Succeeded"},
					},
				},
			},
			wantResult: true,
		},
		{
			name: "failure: Released=True but Reason=Failed (BREAKING CHANGE)",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: "Failed"},
					},
				},
			},
			wantResult: false, // OLD behavior would return true
		},
		{
			name: "failure: Released=True with empty Reason",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: ""},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: Released=True with Reason=ValidationFailed",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Released", Status: "True", Reason: "ValidationFailed"},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: Released=False with Reason=Succeeded",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Released", Status: "False", Reason: "Succeeded"},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: Released=False",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Released", Status: "False", Reason: "Failed"},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: no Released condition at all",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "SomeOtherCondition", Status: "True", Reason: "Succeeded"},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: empty conditions array",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{},
				},
			},
			wantResult: false,
		},
		{
			name: "success: multiple conditions, Released=True with Reason=Succeeded is present",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Ready", Status: "True", Reason: "AllGood"},
						{Type: "Released", Status: "True", Reason: "Succeeded"},
						{Type: "Validated", Status: "True", Reason: "Passed"},
					},
				},
			},
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isReleaseSucceeded(tt.release)
			if got != tt.wantResult {
				t.Errorf("isReleaseSucceeded() = %v, want %v", got, tt.wantResult)
			}
		})
	}
}

// ─── isPLRSucceeded Tests ─────────────────────────────────────────────────────

func TestIsPLRSucceeded(t *testing.T) {
	tests := []struct {
		name       string
		plr        PipelineRun
		wantResult bool
	}{
		{
			name: "success: Succeeded=True",
			plr: PipelineRun{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Succeeded", Status: "True"},
					},
				},
			},
			wantResult: true,
		},
		{
			name: "failure: Succeeded=False",
			plr: PipelineRun{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Succeeded", Status: "False"},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: no Succeeded condition",
			plr: PipelineRun{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Ready", Status: "True"},
					},
				},
			},
			wantResult: false,
		},
		{
			name: "failure: empty conditions",
			plr: PipelineRun{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{},
				},
			},
			wantResult: false,
		},
		{
			name: "success: multiple conditions, Succeeded=True is present",
			plr: PipelineRun{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Ready", Status: "True"},
						{Type: "Succeeded", Status: "True"},
					},
				},
			},
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPLRSucceeded(tt.plr)
			if got != tt.wantResult {
				t.Errorf("isPLRSucceeded() = %v, want %v", got, tt.wantResult)
			}
		})
	}
}

// ─── getLabel Tests ───────────────────────────────────────────────────────────

func TestGetLabel(t *testing.T) {
	t.Run("PipelineRun with label", func(t *testing.T) {
		plr := PipelineRun{
			Metadata: struct {
				UID               string            `json:"uid"`
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: map[string]string{
					"test-key": "test-value",
					"foo":      "bar",
				},
			},
		}

		got := getLabel(plr, "test-key", "default")
		if got != "test-value" {
			t.Errorf("getLabel() = %q, want %q", got, "test-value")
		}
	})

	t.Run("PipelineRun with missing label returns default", func(t *testing.T) {
		plr := PipelineRun{
			Metadata: struct {
				UID               string            `json:"uid"`
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: map[string]string{
					"other-key": "other-value",
				},
			},
		}

		got := getLabel(plr, "missing-key", "default-value")
		if got != "default-value" {
			t.Errorf("getLabel() = %q, want %q", got, "default-value")
		}
	})

	t.Run("PipelineRun with nil labels returns default", func(t *testing.T) {
		plr := PipelineRun{
			Metadata: struct {
				UID               string            `json:"uid"`
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: nil,
			},
		}

		got := getLabel(plr, "any-key", "default")
		if got != "default" {
			t.Errorf("getLabel() = %q, want %q", got, "default")
		}
	})

	t.Run("Release with label", func(t *testing.T) {
		rel := Release{
			Metadata: struct {
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace,omitempty"`
				Labels            map[string]string `json:"labels"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: map[string]string{
					"release-key": "release-value",
				},
			},
		}

		got := getLabel(rel, "release-key", "default")
		if got != "release-value" {
			t.Errorf("getLabel() = %q, want %q", got, "release-value")
		}
	})

	t.Run("Release with missing label returns default", func(t *testing.T) {
		rel := Release{
			Metadata: struct {
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace,omitempty"`
				Labels            map[string]string `json:"labels"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: map[string]string{},
			},
		}

		got := getLabel(rel, "missing", "my-default")
		if got != "my-default" {
			t.Errorf("getLabel() = %q, want %q", got, "my-default")
		}
	})
}

// ─── secondsBetween Tests ─────────────────────────────────────────────────────

func TestSecondsBetween(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
		want  float64
	}{
		{
			name:  "5 minute duration",
			start: "2026-06-01T10:00:00Z",
			end:   "2026-06-01T10:05:00Z",
			want:  300.0,
		},
		{
			name:  "1 hour duration",
			start: "2026-06-01T10:00:00Z",
			end:   "2026-06-01T11:00:00Z",
			want:  3600.0,
		},
		{
			name:  "zero duration (same timestamp)",
			start: "2026-06-01T10:00:00Z",
			end:   "2026-06-01T10:00:00Z",
			want:  0.0,
		},
		{
			name:  "subsecond precision",
			start: "2026-06-01T10:00:00.500Z",
			end:   "2026-06-01T10:00:01.250Z",
			want:  0.75,
		},
		{
			name:  "empty start string",
			start: "",
			end:   "2026-06-01T10:05:00Z",
			want:  -1.0,
		},
		{
			name:  "empty end string",
			start: "2026-06-01T10:00:00Z",
			end:   "",
			want:  -1.0,
		},
		{
			name:  "both empty",
			start: "",
			end:   "",
			want:  -1.0,
		},
		{
			name:  "invalid start timestamp",
			start: "not-a-timestamp",
			end:   "2026-06-01T10:05:00Z",
			want:  -1.0,
		},
		{
			name:  "invalid end timestamp",
			start: "2026-06-01T10:00:00Z",
			end:   "invalid",
			want:  -1.0,
		},
		{
			name:  "negative duration (end before start - clock skew)",
			start: "2026-06-01T10:05:00Z",
			end:   "2026-06-01T10:00:00Z",
			want:  -300.0, // Function returns negative, caller should validate
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := secondsBetween(tt.start, tt.end)
			if got != tt.want {
				t.Errorf("secondsBetween(%q, %q) = %v, want %v", tt.start, tt.end, got, tt.want)
			}
		})
	}
}

// ─── extractBuildType Tests ───────────────────────────────────────────────────

func TestExtractBuildType(t *testing.T) {
	tests := []struct {
		pipelineName string
		want         string
	}{
		// Docker builds
		{"docker-build-oci-ta", "docker-builds"},
		{"docker-build", "docker-builds"},
		{"docker-build-custom", "docker-builds"},

		// Multi-platform docker builds (must come before regular docker check)
		{"docker-build-multi-platform-oci-ta", "docker-multi-arch-builds"},
		{"docker-build-multi-platform", "docker-multi-arch-builds"},

		// Bundle builds
		{"bundle-build-oci-ta", "bundle-builds"},
		{"bundle-build", "bundle-builds"},

		// Standard pipeline
		{"standard-pipeline", "standard-builds"},

		// Operator bundle builds (must match before operator builds)
		{"ose-4-23-local-storage-operator-bundle", "operator-bundle-builds"},
		{"my-operator-bundle", "operator-bundle-builds"},
		{"custom-operator-bundle-build", "operator-bundle-builds"},

		// Operator builds (even if prefixed with fbc)
		{"mtc-1-8-openshift-migration-operator", "operator-builds"},
		{"fbc-mtc-1-8-openshift-migration-operator", "operator-builds"},
		{"custom-operator", "operator-builds"},

		// FBC builds (only if not operator-related)
		{"v419-cnv-fbc-on-push", "fbc-builds"},
		{"my-fbc-pipeline", "fbc-builds"},
		{"fbc-build-custom", "fbc-builds"},

		// RPM builds
		{"rpm-build-oci-ta", "rpm-builds"},
		{"custom-rpm-builder", "rpm-builds"},

		// Custom builds (everything else)
		{"my-custom-pipeline", "custom-builds"},
		{"special-build-v2", "custom-builds"},

		// Edge cases
		{"", "unknown"},
		{"docker", "custom-builds"},                     // doesn't match "docker-build" prefix
		{"operator-bundle-operator", "operator-builds"}, // operator suffix takes precedence
	}

	for _, tt := range tests {
		t.Run(tt.pipelineName, func(t *testing.T) {
			got := extractBuildType(tt.pipelineName)
			if got != tt.want {
				t.Errorf("extractBuildType(%q) = %q, want %q", tt.pipelineName, got, tt.want)
			}
		})
	}
}

// ─── Dedupe Key Tests ─────────────────────────────────────────────────────────

func TestPLRDedupeKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		plr       PipelineRun
		want      string
	}{
		{
			name:      "with UID",
			namespace: "test-ns",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:  "abc-123-def",
					Name: "my-pipeline",
				},
			},
			want: "plr:abc-123-def",
		},
		{
			name:      "without UID (fallback to namespace/name)",
			namespace: "default",
			plr: PipelineRun{
				Metadata: struct {
					UID               string            `json:"uid"`
					Name              string            `json:"name"`
					Namespace         string            `json:"namespace"`
					Labels            map[string]string `json:"labels"`
					Annotations       map[string]string `json:"annotations"`
					CreationTimestamp string            `json:"creationTimestamp"`
				}{
					UID:  "",
					Name: "build-run-1",
				},
			},
			want: "plr:default/build-run-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plrDedupeKey(tt.namespace, tt.plr)
			if got != tt.want {
				t.Errorf("plrDedupeKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReleaseDedupeKey(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		relName   string
		want      string
	}{
		{
			name:      "standard release",
			namespace: "tenant-ns",
			relName:   "release-123",
			want:      "release:tenant-ns/release-123",
		},
		{
			name:      "empty namespace",
			namespace: "",
			relName:   "my-release",
			want:      "release:/my-release",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := releaseDedupeKey(tt.namespace, tt.relName)
			if got != tt.want {
				t.Errorf("releaseDedupeKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
