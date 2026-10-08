package main

import (
	"testing"
	"time"
)

// ── In-flight release tests ───────────────────────────────────────────────────

func TestReleaseInFlightRecordLive(t *testing.T) {
	m := newReleaseInFlight()
	m.recordLive("c1", map[string][]float64{
		"ns-a": {120, 7200, 300},
		"ns-b": {60},
		"ns-c": {}, // no running releases: must not emit a series
	})

	counts := collectGauge(t, m.inProgressCount)
	assertEqual(t, "ns-a count", counts[seriesKey("c1", "ns-a")], 3.0)
	assertEqual(t, "ns-b count", counts[seriesKey("c1", "ns-b")], 1.0)
	if _, ok := counts[seriesKey("c1", "ns-c")]; ok {
		t.Errorf("series emitted for a namespace with no running releases")
	}

	ages := collectGauge(t, m.oldestAge)
	assertEqual(t, "ns-a oldest", ages[seriesKey("c1", "ns-a")], 7200.0)
	assertEqual(t, "ns-b oldest", ages[seriesKey("c1", "ns-b")], 60.0)
}

// A cycle in which nothing is running must clear the previous values rather
// than leave a stale age behind, which would otherwise alert forever.
func TestReleaseInFlightClearsOnEmptyCycle(t *testing.T) {
	m := newReleaseInFlight()
	m.recordLive("c1", map[string][]float64{"ns-a": {7200}})
	assertEqual(t, "before", collectGauge(t, m.oldestAge)[seriesKey("c1", "ns-a")], 7200.0)

	m.recordLive("c1", map[string][]float64{})
	if len(collectGauge(t, m.oldestAge)) != 0 {
		t.Errorf("stale in-progress age survived an empty cycle")
	}
}

func TestReleaseNeverCompletedCounting(t *testing.T) {
	m := newReleaseInFlight()
	labels := inFlightKey{cluster: "c1", namespace: "ns-a", application: "app", component: "comp"}
	now := time.Now().UTC()

	m.recordNeverCompleted("release:ns-a/r1", labels, now.Add(-time.Hour))
	m.recordNeverCompleted("release:ns-a/r2", labels, now.Add(-2*time.Hour))
	// Same release seen again on the next collection cycle: must not double count.
	m.recordNeverCompleted("release:ns-a/r1", labels, now.Add(-time.Hour))
	// Older than the 30-day window: must not be counted at all.
	m.recordNeverCompleted("release:ns-a/r-old", labels, now.AddDate(0, 0, -31))

	m.updateGauges()

	got, ok := gaugeValue(t, m.neverCompleted, map[string]string{
		"cluster": "c1", "namespace": "ns-a", "application": "app", "component": "comp",
	})
	if !ok {
		t.Fatalf("no never-completed series emitted")
	}
	assertEqual(t, "never completed count", got, 2.0)
}

// ── Live API parsing ──────────────────────────────────────────────────────────

func TestIsUnstructuredReleaseRunning(t *testing.T) {
	cond := func(condType, status, reason string) map[string]interface{} {
		return map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": condType, "status": status, "reason": reason},
				},
			},
		}
	}

	tests := []struct {
		name string
		obj  map[string]interface{}
		want bool
	}{
		{"progressing", cond("Released", "False", "Progressing"), true},
		{"succeeded", cond("Released", "True", "Succeeded"), false},
		{"failed", cond("Released", "False", "Failed"), false},
		{"other condition type only", cond("Validated", "False", "Progressing"), false},
		{"no status", map[string]interface{}{}, false},
		{"no conditions", map[string]interface{}{"status": map[string]interface{}{}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEqual(t, "running", isUnstructuredReleaseRunning(tt.obj), tt.want)
		})
	}
}

func TestUnstructuredString(t *testing.T) {
	obj := map[string]interface{}{
		"status": map[string]interface{}{"startTime": "2026-10-08T10:00:00Z", "count": 3},
	}
	assertEqual(t, "present", unstructuredString(obj, "status", "startTime"), "2026-10-08T10:00:00Z")
	assertEqual(t, "wrong type", unstructuredString(obj, "status", "count"), "")
	assertEqual(t, "missing leaf", unstructuredString(obj, "status", "nope"), "")
	assertEqual(t, "missing branch", unstructuredString(obj, "nope", "startTime"), "")
}

// ── Archive path ──────────────────────────────────────────────────────────────

// An archived Release with no completion time must be counted as never
// completed and must stay out of the release_cr duration aggregates, which
// measure completions and are consumed by a live SLO.
func TestRecordAllFromIndexSeparatesNeverCompleted(t *testing.T) {
	store := NewStore()
	slo := newReleaseSLO30d(nil)
	inFlight := newReleaseInFlight()

	idx := newReleaseIndex()
	idx.addReleases("ns-a", []Release{
		NewRelease().Name("done").
			Times(secondsAgo(7200), secondsAgo(7100), secondsAgo(3600)).
			Succeeded().Build(),
		NewRelease().Name("never-completed").
			CreatedAt(secondsAgo(86400)).StartedAt(secondsAgo(86300)).
			DeletedAt(secondsAgo(3600)).Build(),
	})

	slo.recordAllFromIndex(store, "c1", idx, inFlight)
	inFlight.updateGauges()

	recorded := 0
	store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
		recorded += int(window.ComputeTotalCount(time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")))
	})
	assertEqual(t, "completed releases in duration aggregates", recorded, 1)

	total := 0.0
	for _, v := range collectGauge(t, inFlight.neverCompleted) {
		total += v
	}
	assertEqual(t, "never completed count", total, 1.0)
}

func TestNeverCompletedObservedAt(t *testing.T) {
	deleted := NewRelease().CreatedAt(secondsAgo(86400)).DeletedAt(secondsAgo(60)).Build()
	want, err := time.Parse(time.RFC3339, deleted.Metadata.DeletionTimestamp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertEqual(t, "prefers deletion time", neverCompletedObservedAt(deleted).Unix(), want.UTC().Unix())

	created := NewRelease().CreatedAt(secondsAgo(120)).Build()
	want, err = time.Parse(time.RFC3339, created.Metadata.CreationTimestamp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	assertEqual(t, "falls back to creation", neverCompletedObservedAt(created).Unix(), want.UTC().Unix())
}
