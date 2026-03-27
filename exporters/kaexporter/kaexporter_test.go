package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
	// releaseDurationHist uses startTime→completionTime on the Release CR itself,
	// which is always positive for a valid completed Release, so it IS observed even for stale matches.
	relDur := secondsBetween(got.Status.StartTime, got.Status.CompletionTime)
	if relDur < 0 {
		t.Errorf("release duration should be positive: %v", relDur)
	}
}

// makeTestExporter creates a minimal KAExporter with fresh metric objects — no KA_HOST/KA_TOKEN
// required. Metric names use a "t_" prefix so they never clash with production registrations.
func makeTestExporter() *KAExporter {
	return &KAExporter{
		cluster: "test-cluster",

		// Production: validated against RH01 (P95=87.6m). See BUCKET-VALIDATION-RH01.md.
		buildDurationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "t_build_dur", Buckets: []float64{60, 120, 300, 600, 900, 1200, 1800, 2700, 3600, 5400}},
			[]string{"cluster", "namespace", "application", "component", "result"},
		),
		queueWaitGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "t_queue_wait"},
			[]string{"cluster", "namespace", "application", "component"},
		),

		// Production: validated against RH01 (P50=2.4m, P95=69.1m; 60s removed — 0% usage). See BUCKET-VALIDATION-RH01.md.
		integrationDurationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "t_int_dur", Buckets: []float64{120, 300, 600, 900, 1800, 3600, 5400}},
			[]string{"cluster", "namespace", "application", "component", "scenario", "result", "optional"},
		),
		integrationDelayGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "t_int_delay"},
			[]string{"cluster", "namespace", "application", "component"},
		),

		// Production: no live RH01 data; range 5m–4h covers expected release durations.
		releaseDurationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "t_rel_dur", Buckets: []float64{300, 600, 1200, 1800, 3600, 5400, 7200, 14400}},
			[]string{"cluster", "namespace", "application", "component", "release_namespace"},
		),

		// Production: no live RH01 data; range 1m–1h covers expected managed release PLR durations.
		releasePLRTotalHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "t_rel_plr_total", Buckets: []float64{60, 120, 300, 600, 900, 1800, 3600}},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline", "result"},
		),
		releasePLRQueueGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "t_rel_plr_queue"},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline"},
		),
		releasePLRExecHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "t_rel_plr_exec", Buckets: []float64{60, 120, 300, 600, 900, 1800, 3600}},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline", "result"},
		),
	}
}

// gatherOne registers a single Collector to a fresh registry and returns the first MetricFamily
// gathered. Returns nil when the collector has no observations (nothing to export yet).
func gatherOne(t *testing.T, c prometheus.Collector) *dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(mfs) == 0 {
		return nil
	}
	return mfs[0]
}

// TestObserveBuildHistograms_noRelease verifies that observeBuildHistograms records the
// build duration into the histogram and the release histogram stays empty when no release
// matches. Queue gauge is NOT set by observeBuildHistograms — that is setNewestBuildGauges.
func TestObserveBuildHistograms_noRelease(t *testing.T) {
	e := makeTestExporter()

	plr := PipelineRun{}
	plr.Metadata.Name = "build-run-abc"
	plr.Metadata.CreationTimestamp = "2024-01-01T10:00:00Z"
	plr.Status.StartTime = "2024-01-01T10:01:00Z"      // 60s queue wait
	plr.Status.CompletionTime = "2024-01-01T10:05:00Z" // 300s total duration
	plr.Status.Conditions = []Condition{{Type: "Succeeded", Reason: "Completed"}}
	plr.Metadata.Labels = map[string]string{
		labelAppStudioApp:  "myapp",
		labelAppStudioComp: "mycomp",
	}

	e.observeBuildHistograms("tenant-ns", plr, "myapp", "mycomp", "snap-abc", "Completed", newReleaseIndex())

	// Build histogram: exactly 1 observation of 300s.
	mf := gatherOne(t, e.buildDurationHist)
	if mf == nil {
		t.Fatal("buildDurationHist: no observations gathered")
	}
	if got := mf.Metric[0].Histogram.GetSampleCount(); got != 1 {
		t.Errorf("buildDurationHist SampleCount = %d, want 1", got)
	}
	if got := mf.Metric[0].Histogram.GetSampleSum(); got != 300.0 {
		t.Errorf("buildDurationHist SampleSum = %v, want 300.0", got)
	}

	// Queue gauge is NOT set by observeBuildHistograms.
	if mf = gatherOne(t, e.queueWaitGauge); mf != nil {
		t.Errorf("queueWaitGauge: expected empty (not set by observeBuildHistograms), got %+v", mf)
	}

	// Empty release index → release histogram should have no observations.
	if mf = gatherOne(t, e.releaseDurationHist); mf != nil {
		t.Errorf("releaseDurationHist: expected no observations, got metric family %+v", mf)
	}
}

// TestSetNewestBuildGauges verifies the queue wait Gauge is set correctly.
func TestSetNewestBuildGauges_queueWait(t *testing.T) {
	e := makeTestExporter()

	plr := PipelineRun{}
	plr.Metadata.CreationTimestamp = "2024-01-01T10:00:00Z"
	plr.Status.StartTime = "2024-01-01T10:01:00Z" // 60s queue wait
	plr.Metadata.Labels = map[string]string{
		labelAppStudioApp:  "myapp",
		labelAppStudioComp: "mycomp",
	}

	e.setNewestBuildGauges("tenant-ns", plr, "myapp", "mycomp")

	mf := gatherOne(t, e.queueWaitGauge)
	if mf == nil {
		t.Fatal("queueWaitGauge: no value gathered")
	}
	if got := mf.Metric[0].Gauge.GetValue(); got != 60.0 {
		t.Errorf("queueWaitGauge = %v, want 60.0", got)
	}
}

func TestEmitBuildPLRMetrics_releaseDurationObserved(t *testing.T) {
	e := makeTestExporter()

	plr := PipelineRun{}
	plr.Metadata.Name = "build-run-xyz"
	plr.Metadata.CreationTimestamp = "2024-01-01T10:00:00Z"
	plr.Status.StartTime = "2024-01-01T10:00:30Z"
	plr.Status.CompletionTime = "2024-01-01T10:05:00Z"
	plr.Status.Conditions = []Condition{{Type: "Succeeded", Reason: "Completed"}}
	plr.Metadata.Labels = map[string]string{
		labelAppStudioApp:  "myapp",
		labelAppStudioComp: "mycomp",
	}

	rel := Release{}
	rel.Metadata.Name = "release-cr-001"
	rel.Metadata.Namespace = "release-ns"
	rel.Metadata.Labels = map[string]string{
		labelBuildPipelineRun: "build-run-xyz",
		labelAppStudioApp:     "myapp",
		labelAppStudioComp:    "mycomp",
	}
	rel.Status.StartTime = "2024-01-01T10:06:00Z"
	rel.Status.CompletionTime = "2024-01-01T10:16:00Z" // 600s release duration

	idx := newReleaseIndex()
	idx.addReleases("release-ns", []Release{rel})

	e.observeBuildHistograms("tenant-ns", plr, "myapp", "mycomp", "snap-xyz", "Completed", idx)

	mf := gatherOne(t, e.releaseDurationHist)
	if mf == nil {
		t.Fatal("releaseDurationHist: no observations gathered")
	}
	if got := mf.Metric[0].Histogram.GetSampleCount(); got != 1 {
		t.Errorf("releaseDurationHist SampleCount = %d, want 1", got)
	}
	if got := mf.Metric[0].Histogram.GetSampleSum(); got != 600.0 {
		t.Errorf("releaseDurationHist SampleSum = %v, want 600.0", got)
	}
}

func TestProcessReleasePipelineRun_histogramObservations(t *testing.T) {
	e := makeTestExporter()

	plr := PipelineRun{}
	plr.Metadata.Name = "release-plr-001"
	plr.Metadata.CreationTimestamp = "2024-01-01T10:00:00Z"
	plr.Status.StartTime = "2024-01-01T10:02:00Z"      // 120s queue
	plr.Status.CompletionTime = "2024-01-01T10:07:00Z" // 420s total, 300s exec
	plr.Status.Conditions = []Condition{{Type: "Succeeded", Reason: "Completed"}}
	plr.Metadata.Labels = map[string]string{
		labelAppStudioApp:       "myapp",
		labelReleaseApplicationNS: "tenant-ns",
		labelTektonPipeline:     "release-pipeline",
	}

	e.processReleasePipelineRun("managed-ns", plr, newSafeOutcomeCounts())

	// Total duration: 420s (creation → completion).
	mf := gatherOne(t, e.releasePLRTotalHist)
	if mf == nil {
		t.Fatal("releasePLRTotalHist: no observations gathered")
	}
	if got := mf.Metric[0].Histogram.GetSampleCount(); got != 1 {
		t.Errorf("releasePLRTotalHist SampleCount = %d, want 1", got)
	}
	if got := mf.Metric[0].Histogram.GetSampleSum(); got != 420.0 {
		t.Errorf("releasePLRTotalHist SampleSum = %v, want 420.0", got)
	}

	// Exec duration: 300s (start → completion).
	mf = gatherOne(t, e.releasePLRExecHist)
	if mf == nil {
		t.Fatal("releasePLRExecHist: no observations gathered")
	}
	if got := mf.Metric[0].Histogram.GetSampleCount(); got != 1 {
		t.Errorf("releasePLRExecHist SampleCount = %d, want 1", got)
	}
	if got := mf.Metric[0].Histogram.GetSampleSum(); got != 300.0 {
		t.Errorf("releasePLRExecHist SampleSum = %v, want 300.0", got)
	}

	// Queue gauge: 120s.
	mf = gatherOne(t, e.releasePLRQueueGauge)
	if mf == nil {
		t.Fatal("releasePLRQueueGauge: no observations gathered")
	}
	if got := mf.Metric[0].Gauge.GetValue(); got != 120.0 {
		t.Errorf("releasePLRQueueGauge = %v, want 120.0", got)
	}
}

func TestProcessIntegrationTests_histogramObservations(t *testing.T) {
	e := makeTestExporter()

	testPLR := PipelineRun{}
	testPLR.Metadata.Name = "int-test-001"
	testPLR.Metadata.CreationTimestamp = "2024-01-01T10:06:00Z"
	testPLR.Status.CompletionTime = "2024-01-01T10:10:00Z" // 240s test duration
	testPLR.Status.Conditions = []Condition{{Type: "Succeeded", Reason: "Completed"}}
	testPLR.Metadata.Labels = map[string]string{
		"test.appstudio.openshift.io/scenario": "scenario-a",
		"test.appstudio.openshift.io/optional": "false",
		labelOrAnnotationSnapshot:               "snap-abc",
	}

	buildCompletedAt := "2024-01-01T10:05:00Z" // 60s gap to first test creation
	e.processIntegrationTests("tenant-ns", []PipelineRun{testPLR}, "myapp", "mycomp", buildCompletedAt)

	// Integration duration histogram: 1 observation of 240s.
	mf := gatherOne(t, e.integrationDurationHist)
	if mf == nil {
		t.Fatal("integrationDurationHist: no observations gathered")
	}
	if got := mf.Metric[0].Histogram.GetSampleCount(); got != 1 {
		t.Errorf("integrationDurationHist SampleCount = %d, want 1", got)
	}
	if got := mf.Metric[0].Histogram.GetSampleSum(); got != 240.0 {
		t.Errorf("integrationDurationHist SampleSum = %v, want 240.0", got)
	}

	// Integration delay gauge: 60s gap (build completion → first test creation).
	mf = gatherOne(t, e.integrationDelayGauge)
	if mf == nil {
		t.Fatal("integrationDelayGauge: no observations gathered")
	}
	if got := mf.Metric[0].Gauge.GetValue(); got != 60.0 {
		t.Errorf("integrationDelayGauge = %v, want 60.0", got)
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
