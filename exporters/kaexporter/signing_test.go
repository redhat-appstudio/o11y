package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// ── Signing Metrics Tests ─────────────────────────────────────────────────────

func TestSigningRecordObservation(t *testing.T) {
	tests := []struct {
		name         string
		tr           TaskRun
		wantRecorded bool
		wantTask     string
		wantPipeline string
	}{
		{
			name: "successful container signing",
			tr: NewPLR().UID("sign-1").
				Times(secondsAgo(3600), secondsAgo(3590), secondsAgo(3400)).
				PipelineTask("rh-direct-sign-image").Pipeline("rh-advisories").Succeeded().Build(),
			wantRecorded: true, wantTask: "rh-direct-sign-image", wantPipeline: "rh-advisories",
		},
		{
			name: "failed cosign signing",
			tr: NewPLR().UID("sign-2").
				Times(secondsAgo(3600), secondsAgo(3580), secondsAgo(3500)).
				PipelineTask("rh-sign-image-cosign").Pipeline("rh-push-to-registry-redhat-io").Failed("Failed").Build(),
			wantRecorded: true, wantTask: "rh-sign-image-cosign", wantPipeline: "rh-push-to-registry-redhat-io",
		},
		{
			name: "labels absent",
			tr: NewPLR().UID("sign-3").
				Times(secondsAgo(3600), secondsAgo(3590), secondsAgo(3400)).
				Succeeded().Build(),
			wantRecorded: true, wantTask: "unknown", wantPipeline: "unknown",
		},
		{
			// A hung signing task is the August 17-21 failure mode: it never
			// completes, so KubeArchive never holds it and it must not count.
			name: "hung task has no completion time",
			tr: NewPLR().UID("sign-4").
				CreatedAt(secondsAgo(7200)).StartedAt(secondsAgo(7190)).
				PipelineTask("rh-direct-sign-image").Pipeline("rh-advisories").Build(),
			wantRecorded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore()
			slo := newSigningSLO30d(nil)
			slo.recordObservation(store, "test-cluster", "rhtap-releng-tenant", tt.tr)

			recorded := false
			store.ForEachWindow(metricSigningDuration, func(ls LabelSet, window *MetricWindow) {
				recorded = true
				assertEqual(t, "Task", ls.Task, tt.wantTask)
				assertEqual(t, "Pipeline", ls.Pipeline, tt.wantPipeline)
				assertEqual(t, "Namespace", ls.Namespace, "rhtap-releng-tenant")
			})
			assertEqual(t, "recorded", recorded, tt.wantRecorded)
		})
	}
}

// TestSigningLastSuccessTracking covers the metric that makes a signing outage
// visible. Only successes advance it, it keeps the newest timestamp regardless
// of arrival order, and a later failure must not move it backwards or forwards.
func TestSigningLastSuccessTracking(t *testing.T) {
	store := NewStore()
	slo := newSigningSLO30d(nil)

	older := secondsAgo(7200)
	newer := secondsAgo(1800)

	// Record newest first, then an older one: KubeArchive returns items
	// newest-first, so out-of-order arrival is the normal case.
	slo.recordObservation(store, "c1", "ns1", NewPLR().UID("s-new").
		Times(secondsAgo(2000), secondsAgo(1990), newer).
		PipelineTask("rh-direct-sign-image").Pipeline("rh-advisories").Succeeded().Build())
	slo.recordObservation(store, "c1", "ns1", NewPLR().UID("s-old").
		Times(secondsAgo(7400), secondsAgo(7390), older).
		PipelineTask("rh-direct-sign-image").Pipeline("rh-advisories").Succeeded().Build())
	// A failure after both must not touch last-success.
	slo.recordObservation(store, "c1", "ns1", NewPLR().UID("s-fail").
		Times(secondsAgo(600), secondsAgo(590), secondsAgo(300)).
		PipelineTask("rh-direct-sign-image").Pipeline("rh-advisories").Failed("Failed").Build())

	slo.updateGauges(store, nil)

	wantTS, err := time.Parse(time.RFC3339, newer)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}

	got := gaugeValueFor(t, slo.lastSuccessGauge, "c1", "ns1", "rh-direct-sign-image", "rh-advisories")
	assertEqual(t, "last success timestamp", int64(got), wantTS.Unix())

	// A series that only ever failed must not report a last success at all.
	slo.recordObservation(store, "c1", "ns1", NewPLR().UID("s-only-fail").
		Times(secondsAgo(900), secondsAgo(890), secondsAgo(600)).
		PipelineTask("sign-base64-blob").Pipeline("release-to-github").Failed("Failed").Build())
	slo.updateGauges(store, nil)

	if _, ok := collectGauge(t, slo.lastSuccessGauge)[seriesKey("c1", "ns1", "sign-base64-blob", "release-to-github")]; ok {
		t.Errorf("last success reported for a series that never succeeded")
	}
}

// TestSigningLastSuccessOutlivesWindow checks that a success stays reported
// after the rolling store no longer holds it. Without this the gauge would
// disappear exactly when an outage makes it most useful.
func TestSigningLastSuccessOutlivesWindow(t *testing.T) {
	store := NewStore()
	slo := newSigningSLO30d(nil)

	completion := secondsAgo(3600)
	slo.recordObservation(store, "c1", "ns1", NewPLR().UID("s-1").
		Times(secondsAgo(4000), secondsAgo(3990), completion).
		PipelineTask("rh-direct-sign-image").Pipeline("rh-advisories").Succeeded().Build())

	// Emptying the store models the observation ageing out of the 30-day window.
	emptyStore := NewStore()
	slo.updateGauges(emptyStore, nil)

	wantTS, err := time.Parse(time.RFC3339, completion)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}
	got := gaugeValueFor(t, slo.lastSuccessGauge, "c1", "ns1", "rh-direct-sign-image", "rh-advisories")
	assertEqual(t, "last success timestamp", int64(got), wantTS.Unix())
}

// ── helpers ───────────────────────────────────────────────────────────────────

func seriesKey(values ...string) string {
	key := ""
	for _, v := range values {
		key += v + "\x00"
	}
	return key
}

// collectGauge reads a GaugeVec into a map keyed by its label values in order.
func collectGauge(t *testing.T, g *prometheus.GaugeVec) map[string]float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	g.Collect(ch)
	close(ch)

	out := make(map[string]float64)
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		values := make([]string, 0, len(pb.Label))
		for _, l := range pb.Label {
			values = append(values, l.GetValue())
		}
		out[seriesKey(values...)] = pb.GetGauge().GetValue()
	}
	return out
}

// gaugeValue looks up one series by label name and value, independent of the
// order in which the client library emits label values.
func gaugeValue(t *testing.T, g *prometheus.GaugeVec, want map[string]string) (float64, bool) {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	g.Collect(ch)
	close(ch)

	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		got := make(map[string]string, len(pb.Label))
		for _, l := range pb.Label {
			got[l.GetName()] = l.GetValue()
		}
		match := len(got) == len(want)
		for k, v := range want {
			if got[k] != v {
				match = false
				break
			}
		}
		if match {
			return pb.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func gaugeValueFor(t *testing.T, g *prometheus.GaugeVec, cluster, namespace, task, pipeline string) float64 {
	t.Helper()
	// Label values are emitted in alphabetical label-name order:
	// cluster, namespace, pipeline, task.
	key := seriesKey(cluster, namespace, pipeline, task)
	values := collectGauge(t, g)
	v, ok := values[key]
	if !ok {
		t.Fatalf("no series for %s/%s/%s/%s; have %v", cluster, namespace, task, pipeline, values)
	}
	return v
}
