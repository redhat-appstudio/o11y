package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// BuildSLO30d manages 30-day SLO metrics for builds
type BuildSLO30d struct {
	mean30d        *prometheus.GaugeVec
	successRate30d *prometheus.GaugeVec
	totalCount30d  *prometheus.GaugeVec
}

// newBuildSLO30d initializes build 30d SLO metrics
func newBuildSLO30d() *BuildSLO30d {
	return &BuildSLO30d{
		mean30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_mean_duration_30d_seconds",
				Help: "Mean build duration over the past 30 days for successful builds only (completion-time based).",
			},
			[]string{"cluster", "namespace", "application", "component", "build_type", "event_type"},
		),
		successRate30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_success_rate_30d",
				Help: "Build success rate over the past 30 days (Succeeded / total completed).",
			},
			[]string{"cluster", "namespace", "application", "component", "build_type", "event_type"},
		),
		totalCount30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_total_count_30d",
				Help: "Total count of completed builds over the past 30 days (successful + failed).",
			},
			[]string{"cluster", "namespace", "application", "component", "build_type", "event_type"},
		),
	}
}

// recordObservation records a build observation into the 30-day rolling window store
func (m *BuildSLO30d) recordObservation(
	store *Store,
	cluster, namespace, application, component string,
	plr PipelineRun,
) {
	if plr.Status.CompletionTime == "" {
		return
	}

	completionTime, err := time.Parse(time.RFC3339, plr.Status.CompletionTime)
	if err != nil {
		return
	}
	duration := secondsBetween(plr.Metadata.CreationTimestamp, plr.Status.CompletionTime)
	if duration < 0 {
		return
	}

	// Extract build-specific labels
	eventType := getLabel(plr, labelEventType, "unknown")
	pipelineName := getLabel(plr, labelTektonPipeline, "")
	buildType := extractBuildType(pipelineName)

	ls := LabelSet{
		Cluster:     cluster,
		Namespace:   namespace,
		Application: application,
		Component:   component,
		EventType:   eventType,
		BuildType:   buildType,
	}

	store.RecordObservation(
		metricBuildDuration,
		plrDedupeKey(namespace, plr),
		completionTime,
		ls,
		duration,
		isPLRSucceeded(plr),
	)
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *BuildSLO30d) updateGauges(store *Store) {
	m.mean30d.Reset()
	m.successRate30d.Reset()
	m.totalCount30d.Reset()

	store.ForEachWindow(metricBuildDuration, func(ls LabelSet, window *MetricWindow) {
		totalCount := window.ComputeTotalCount()
		if totalCount == 0 {
			return
		}
		labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.BuildType, ls.EventType}
		m.mean30d.WithLabelValues(labels...).Set(window.ComputeSuccessMean())
		m.successRate30d.WithLabelValues(labels...).Set(window.ComputeSuccessRate())
		m.totalCount30d.WithLabelValues(labels...).Set(float64(totalCount))
	})
}

// Describe implements prometheus.Collector
func (m *BuildSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.mean30d.Describe(ch)
	m.successRate30d.Describe(ch)
	m.totalCount30d.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *BuildSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.mean30d.Collect(ch)
	m.successRate30d.Collect(ch)
	m.totalCount30d.Collect(ch)
}
