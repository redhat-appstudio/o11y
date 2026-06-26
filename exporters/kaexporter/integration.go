package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// IntegrationSLO30d manages 30-day SLO metrics for integration tests
type IntegrationSLO30d struct {
	SLOGaugeSet
}

// newIntegrationSLO30d initializes integration 30d SLO metrics
func newIntegrationSLO30d() *IntegrationSLO30d {
	labels := []string{"cluster", "namespace", "application", "component", "scenario", "optional", "test_type", "event_type"}
	return &IntegrationSLO30d{
		SLOGaugeSet: newSLOGaugeSet("konflux_integration", "integration test", labels),
	}
}

// recordObservation records an integration test observation into the rolling store
func (m *IntegrationSLO30d) recordObservation(
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

	// Extract scenario and optional flag from test PLR labels
	scenario := getLabel(plr, labelTestScenario, "unknown")
	optional := getLabel(plr, labelTestOptional, "false")

	// Detect Enterprise Contract (EC) vs regular integration tests.
	pipelineName := getLabel(plr, labelTektonPipeline, "")
	testType := "integration"
	if pipelineName == "enterprise-contract" {
		testType = "ec"
	}

	// Extract event type from test PipelineRun
	eventType := getLabel(plr, labelPACEventType, "unknown")

	// Extract success status and failure reason
	succeeded, failureReason := plrStatus(plr)

	ls := LabelSet{
		Cluster:     cluster,
		Namespace:   namespace,
		Application: application,
		Component:   component,
		Scenario:    scenario,
		Optional:    optional,
		TestType:    testType,
		EventType:   eventType,
	}

	store.RecordObservation(
		metricIntegrationDuration,
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
func (m *IntegrationSLO30d) updateGauges(store *Store) {
	m.SLOGaugeSet.UpdateFromStore(store, metricIntegrationDuration, func(ls LabelSet) []string {
		return []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Scenario, ls.Optional, ls.TestType, ls.EventType}
	})
}

// Describe implements prometheus.Collector
func (m *IntegrationSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.SLOGaugeSet.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *IntegrationSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.SLOGaugeSet.Collect(ch)
}
