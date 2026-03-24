package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSecondsBetween(t *testing.T) {
	tests := []struct {
		name     string
		start    string
		end      string
		expected float64
	}{
		{
			name:     "Valid timestamps",
			start:    "2024-01-01T10:00:00Z",
			end:      "2024-01-01T10:05:00Z",
			expected: 300.0, // 5 minutes
		},
		{
			name:     "Empty start",
			start:    "",
			end:      "2024-01-01T10:05:00Z",
			expected: -1,
		},
		{
			name:     "Empty end",
			start:    "2024-01-01T10:00:00Z",
			end:      "",
			expected: -1,
		},
		{
			name:     "Invalid timestamp",
			start:    "invalid",
			end:      "2024-01-01T10:05:00Z",
			expected: -1,
		},
		{
			name:     "End before start returns negative",
			start:    "2024-01-01T10:05:00Z",
			end:      "2024-01-01T10:00:00Z",
			expected: -300.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := secondsBetween(tt.start, tt.end)
			if result != tt.expected {
				t.Errorf("secondsBetween() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetLabel(t *testing.T) {
	plr := PipelineRun{
		Metadata: struct {
			Name              string            `json:"name"`
			Namespace         string            `json:"namespace"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp string            `json:"creationTimestamp"`
		}{
			Labels: map[string]string{
				"app":  "test-app",
				"type": "build",
			},
		},
	}

	tests := []struct {
		name       string
		key        string
		defaultVal string
		expected   string
	}{
		{
			name:       "Existing label",
			key:        "app",
			defaultVal: "default",
			expected:   "test-app",
		},
		{
			name:       "Non-existing label",
			key:        "missing",
			defaultVal: "default",
			expected:   "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLabel(plr, tt.key, tt.defaultVal)
			if result != tt.expected {
				t.Errorf("getLabel() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetResult(t *testing.T) {
	tests := []struct {
		name       string
		conditions []Condition
		expected   string
	}{
		{
			name: "Succeeded condition",
			conditions: []Condition{
				{Type: "Succeeded", Reason: "Completed"},
			},
			expected: "Completed",
		},
		{
			name: "Failed condition",
			conditions: []Condition{
				{Type: "Succeeded", Reason: "Failed"},
			},
			expected: "Failed",
		},
		{
			name:       "No conditions",
			conditions: []Condition{},
			expected:   "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plr := PipelineRun{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: tt.conditions,
				},
			}
			result := getResult(plr)
			if result != tt.expected {
				t.Errorf("getResult() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetReleaseResult(t *testing.T) {
	tests := []struct {
		name     string
		release  Release
		expected string
	}{
		{
			name: "Released True with reason",
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
			expected: "Succeeded",
		},
		{
			name: "Released True empty reason",
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
			expected: "Succeeded",
		},
		{
			name: "Released False",
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
			expected: "Failed",
		},
		{
			name: "No Released condition",
			release: Release{
				Status: struct {
					StartTime      string      `json:"startTime"`
					CompletionTime string      `json:"completionTime"`
					Conditions     []Condition `json:"conditions"`
				}{
					Conditions: []Condition{
						{Type: "Validated", Status: "True", Reason: "Succeeded"},
					},
				},
			},
			expected: "Unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getReleaseResult(tt.release)
			if got != tt.expected {
				t.Errorf("getReleaseResult() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestParseManagedReleasePLRNamespaces(t *testing.T) {
	t.Setenv(managedReleasePLRNamespacesEnv, " rhtap-releng-tenant , rhtap-releng-tenant , foo ")
	got := parseManagedReleasePLRNamespaces()
	want := []string{"foo", "rhtap-releng-tenant"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFilterReleaseServicePLRs(t *testing.T) {
	plrs := []PipelineRun{
		{
			Metadata: struct {
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: map[string]string{
					"appstudio.openshift.io/service":        "release",
					"pipelines.appstudio.openshift.io/type": "managed",
				},
			},
			Status: struct {
				StartTime      string      `json:"startTime"`
				CompletionTime string      `json:"completionTime"`
				Conditions     []Condition `json:"conditions"`
			}{
				CompletionTime: "2024-01-01T10:05:00Z",
			},
		},
		{
			Metadata: struct {
				Name              string            `json:"name"`
				Namespace         string            `json:"namespace"`
				Labels            map[string]string `json:"labels"`
				Annotations       map[string]string `json:"annotations"`
				CreationTimestamp string            `json:"creationTimestamp"`
			}{
				Labels: map[string]string{"pipelines.appstudio.openshift.io/type": "build"},
			},
			Status: struct {
				StartTime      string      `json:"startTime"`
				CompletionTime string      `json:"completionTime"`
				Conditions     []Condition `json:"conditions"`
			}{
				CompletionTime: "2024-01-01T10:05:00Z",
			},
		},
	}
	var got []PipelineRun
	for _, plr := range plrs {
		if isReleaseServicePLR(plr) {
			got = append(got, plr)
		}
	}
	if len(got) != 1 {
		t.Fatalf("isReleaseServicePLR filter len=%d, want 1", len(got))
	}
}

// makeIndex is a test helper that builds a *snapshotIndex from a slice of Snapshots.
func makeIndex(snaps []Snapshot) *snapshotIndex {
	idx := newSnapshotIndex()
	idx.add(snaps)
	return idx
}

// makeReleaseIndex is a test helper that builds a *releaseIndex from a slice of releaseEntry.
func makeReleaseIndex(entries []releaseEntry) *releaseIndex {
	idx := newReleaseIndex()
	// Group by namespace: each entry carries its own crNamespace.
	for _, e := range entries {
		idx.addReleases(e.crNamespace, []Release{e.Release})
	}
	return idx
}

func TestResolveSnapshotNameForBuild_fromSnapshotCRLabel(t *testing.T) {
	plr := PipelineRun{}
	plr.Metadata.Name = "build-xyz"
	plr.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application": "app1",
		"appstudio.openshift.io/component":   "comp1",
	}

	snap := Snapshot{}
	snap.Metadata.Name = "snap-from-cr"
	snap.Metadata.Namespace = "tenant-a"
	snap.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/build-pipelinerun": "build-xyz",
		"appstudio.openshift.io/application":       "app1",
	}

	got := resolveSnapshotNameForBuild("tenant-a", plr, makeIndex([]Snapshot{snap}))
	if got != "snap-from-cr" {
		t.Fatalf("resolveSnapshotNameForBuild = %q, want snap-from-cr", got)
	}
}

func TestResolveSnapshotNameForBuild_annotationWhenNoSnapshotList(t *testing.T) {
	plr := PipelineRun{}
	plr.Metadata.Name = "build-xyz"
	plr.Metadata.Annotations = map[string]string{
		"appstudio.openshift.io/snapshot": "snap-ann",
	}
	if got := resolveSnapshotNameForBuild("tenant-a", plr, nil); got != "snap-ann" {
		t.Fatalf("got %q, want snap-ann", got)
	}
}

func TestResolveSnapshotNameForBuild_specComponentUnique(t *testing.T) {
	plr := PipelineRun{}
	plr.Metadata.Name = "build-other"
	plr.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application": "gapp",
		"appstudio.openshift.io/component":   "comp-b",
	}

	raw := `{
		"metadata": {"name": "hetero-snap", "namespace": "tenant-a"},
		"spec": {"application": "gapp", "components": [{"name": "comp-b"}]}
	}`
	var s Snapshot
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}

	got := resolveSnapshotNameForBuild("tenant-a", plr, makeIndex([]Snapshot{s}))
	if got != "hetero-snap" {
		t.Fatalf("got %q, want hetero-snap", got)
	}
}

func TestFindMatchingRelease_byBuildPipelineRun(t *testing.T) {
	plr := PipelineRun{}
	plr.Metadata.Name = "my-build-abc"
	plr.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application": "myapp",
		"appstudio.openshift.io/component":   "mycomp",
	}

	rel := Release{}
	rel.Metadata.Namespace = "dedicated-release-tenant"
	rel.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/build-pipelinerun": "my-build-abc",
		"appstudio.openshift.io/application":       "myapp",
		"appstudio.openshift.io/component":         "mycomp",
		"release.appstudio.openshift.io/snapshot":  "snap-shared",
	}

	cands := makeReleaseIndex([]releaseEntry{{Release: rel, crNamespace: "dedicated-release-tenant"}})
	got := findMatchingRelease(plr, "snap-shared", "myapp", "mycomp", cands)
	if got == nil || got.crNamespace != "dedicated-release-tenant" {
		t.Fatalf("got %+v, want release in dedicated-release-tenant", got)
	}
}

func TestFindMatchingRelease_heterogeneousSnapshot(t *testing.T) {
	plrA := PipelineRun{}
	plrA.Metadata.Name = "build-a"
	plrA.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application": "groupapp",
		"appstudio.openshift.io/component":   "comp-a",
	}

	relA := Release{}
	relA.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/build-pipelinerun": "build-a",
		"appstudio.openshift.io/application":       "groupapp",
		"appstudio.openshift.io/component":         "comp-a",
		"release.appstudio.openshift.io/snapshot":  "hetero-snap",
	}
	relB := Release{}
	relB.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/build-pipelinerun": "build-b",
		"appstudio.openshift.io/application":       "groupapp",
		"appstudio.openshift.io/component":         "comp-b",
		"release.appstudio.openshift.io/snapshot":  "hetero-snap",
	}

	cands := makeReleaseIndex([]releaseEntry{
		{Release: relB, crNamespace: "rel-ns"},
		{Release: relA, crNamespace: "rel-ns"},
	})
	got := findMatchingRelease(plrA, "hetero-snap", "groupapp", "comp-a", cands)
	if got == nil || getLabel(got.Release, "appstudio.openshift.io/build-pipelinerun", "") != "build-a" {
		t.Fatalf("expected build-a release, got %+v", got)
	}
}

func TestFindMatchingRelease_snapshotFallbackRequiresAppMatch(t *testing.T) {
	plr := PipelineRun{}
	plr.Metadata.Name = "orphan-build"
	plr.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application": "app1",
		"appstudio.openshift.io/component":   "c1",
	}

	rel := Release{}
	rel.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application":      "other-app",
		"release.appstudio.openshift.io/snapshot": "s1",
	}

	cands := makeReleaseIndex([]releaseEntry{{Release: rel, crNamespace: "ns"}})
	if findMatchingRelease(plr, "s1", "app1", "c1", cands) != nil {
		t.Fatal("expected no match when application label on Release disagrees")
	}
}

// TestFindMatchingRelease_staleReleaseNotRejected documents that findMatchingRelease
// does NOT inspect timestamps — it matches by label only. When a Release CR completed
// before the build PLR was created (stale match via build-pipelinerun label), the
// function still returns it. The caller is responsible for guards such as the >= 0
// check on secondsBetween(buildCreated, release.CompletionTime). Without that guard,
// a stale match would emit incorrect duration values on konflux_release_duration_seconds.
func TestFindMatchingRelease_staleReleaseNotRejected(t *testing.T) {
	plr := PipelineRun{}
	plr.Metadata.Name = "new-build"
	plr.Metadata.CreationTimestamp = "2024-01-01T10:00:00Z"
	plr.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/application": "app1",
		"appstudio.openshift.io/component":   "c1",
	}

	staleRel := Release{}
	staleRel.Metadata.Labels = map[string]string{
		"appstudio.openshift.io/build-pipelinerun": "new-build",
		"appstudio.openshift.io/application":       "app1",
	}
	// completionTime is before the build PLR was created — a stale match.
	staleRel.Status.StartTime = "2024-01-01T09:00:00Z"
	staleRel.Status.CompletionTime = "2024-01-01T09:05:00Z"

	cands := makeReleaseIndex([]releaseEntry{{Release: staleRel, crNamespace: "ns"}})
	got := findMatchingRelease(plr, "", "app1", "c1", cands)
	if got == nil {
		t.Fatal("findMatchingRelease should return the match — stale detection is the caller's responsibility")
	}
	// Confirm the caller guard works: secondsBetween(buildCreated, staleCompletionTime) < 0,
	// so the >= 0 check at the call site prevents setting the gauge.
	mttb := secondsBetween(plr.Metadata.CreationTimestamp, got.Status.CompletionTime)
	if mttb >= 0 {
		t.Errorf("expected negative value for stale match, got %v — caller >= 0 guard would not protect here", mttb)
	}
	// releaseDurationGauge uses startTime→completionTime on the Release CR itself,
	// which is always positive for a valid completed Release, so it IS set even for stale matches.
	relDur := secondsBetween(got.Status.StartTime, got.Status.CompletionTime)
	if relDur < 0 {
		t.Errorf("release duration should be positive: %v", relDur)
	}
}

// Benchmark tests
func BenchmarkSecondsBetween(b *testing.B) {
	start := "2024-01-01T10:00:00Z"
	end := "2024-01-01T10:05:00Z"

	for i := 0; i < b.N; i++ {
		secondsBetween(start, end)
	}
}

func BenchmarkParseTimestamp(b *testing.B) {
	timestamp := "2024-01-01T10:00:00Z"

	for i := 0; i < b.N; i++ {
		time.Parse(time.RFC3339, timestamp)
	}
}
