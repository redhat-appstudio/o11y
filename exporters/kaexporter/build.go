package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// BuildSLO30d manages 30-day SLO metrics for builds
type BuildSLO30d struct {
	SLOGaugeSet
}

// newBuildSLO30d initializes build 30d SLO metrics
func newBuildSLO30d() *BuildSLO30d {
	labels := []string{"cluster", "namespace", "application", "component", "build_type", "event_type"}
	return &BuildSLO30d{
		SLOGaugeSet: newSLOGaugeSet("konflux_build", "build", labels),
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

	duration := secondsBetween(plr.Status.StartTime, plr.Status.CompletionTime)
	if duration < 0 {
		return
	}
	waitTime := secondsBetween(plr.Metadata.CreationTimestamp, plr.Status.StartTime)

	// Extract build-specific labels
	eventType := getLabel(plr, labelEventType, "unknown")
	pipelineName := getLabel(plr, labelTektonPipeline, "")
	buildType := extractBuildType(pipelineName)

	// Extract success status and failure reason
	succeeded, failureReason := plrStatus(plr)

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
		waitTime,
		succeeded,
		failureReason,
	)
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *BuildSLO30d) updateGauges(store *Store) {
	m.SLOGaugeSet.UpdateFromStore(store, metricBuildDuration, func(ls LabelSet) []string {
		return []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.BuildType, ls.EventType}
	})
}

// Describe implements prometheus.Collector
func (m *BuildSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.SLOGaugeSet.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *BuildSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.SLOGaugeSet.Collect(ch)
}
