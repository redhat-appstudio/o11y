package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ReleaseSLO30d manages 30-day SLO metrics for releases
type ReleaseSLO30d struct {
	mean30d        *prometheus.GaugeVec
	successRate30d *prometheus.GaugeVec
}

// newReleaseSLO30d initializes release 30d SLO metrics
func newReleaseSLO30d() *ReleaseSLO30d {
	return &ReleaseSLO30d{
		mean30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_mean_duration_30d_seconds",
				Help: "Mean Release CR duration over the past 30 days for successful releases only (completion-time based).",
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
		successRate30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_success_rate_30d",
				Help: "Release success rate over the past 30 days (Released=True / total completed).",
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
	}
}

// recordObservation records a single release observation into the rolling store
func (m *ReleaseSLO30d) recordObservation(
	store *Store,
	cluster, namespace, application, component string,
	rel Release,
) {
	if rel.Status.CompletionTime == "" {
		return
	}

	completionTime, err := time.Parse(time.RFC3339, rel.Status.CompletionTime)
	if err != nil {
		return
	}
	start := rel.Status.StartTime
	if start == "" {
		start = rel.Metadata.CreationTimestamp
	}
	duration := secondsBetween(start, rel.Status.CompletionTime)
	if duration < 0 {
		return
	}

	ls := LabelSet{
		Cluster:     cluster,
		Namespace:   namespace,
		Application: application,
		Component:   component,
	}

	ns := rel.Metadata.Namespace
	if ns == "" {
		ns = namespace
	}

	store.RecordObservation(
		metricReleaseDuration,
		releaseDedupeKey(ns, rel.Metadata.Name),
		completionTime,
		ls,
		duration,
		isReleaseSucceeded(rel),
	)
}

// recordAllFromIndex iterates through a releaseIndex and records all releases
func (m *ReleaseSLO30d) recordAllFromIndex(
	store *Store,
	cluster string,
	releaseIdx *releaseIndex,
) {
	for i := range releaseIdx.store {
		entry := &releaseIdx.store[i]
		if entry.Status.CompletionTime == "" {
			continue
		}
		tenantNS := entry.crNamespace
		if tenantNS == "" {
			tenantNS = entry.Metadata.Namespace
		}
		app := getLabel(entry.Release, labelAppStudioApp, "unknown")
		comp := getLabel(entry.Release, labelAppStudioComp, "unknown")
		m.recordObservation(store, cluster, tenantNS, app, comp, entry.Release)
	}
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *ReleaseSLO30d) updateGauges(store *Store) {
	m.mean30d.Reset()
	m.successRate30d.Reset()

	store.ForEachWindow(metricReleaseDuration, func(ls LabelSet, window *MetricWindow) {
		if window.TotalCount() == 0 {
			return // no data in window — don't emit, don't misfire alerts
		}
		labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component}
		m.mean30d.WithLabelValues(labels...).Set(window.ComputeSuccessMean())
		m.successRate30d.WithLabelValues(labels...).Set(window.ComputeSuccessRate())
	})
}

// Describe implements prometheus.Collector
func (m *ReleaseSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.mean30d.Describe(ch)
	m.successRate30d.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *ReleaseSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.mean30d.Collect(ch)
	m.successRate30d.Collect(ch)
}
