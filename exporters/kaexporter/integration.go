package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// IntegrationSLO30d manages 30-day SLO metrics for integration tests
type IntegrationSLO30d struct {
	mean30d        *prometheus.GaugeVec
	successRate30d *prometheus.GaugeVec
	totalCount30d  *prometheus.GaugeVec
}

// newIntegrationSLO30d initializes integration 30d SLO metrics
func newIntegrationSLO30d() *IntegrationSLO30d {
	return &IntegrationSLO30d{
		mean30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_integration_mean_duration_30d_seconds",
				Help: "Mean integration test duration over the past 30 days for successful tests only (completion-time based).",
			},
			[]string{"cluster", "namespace", "application", "component", "scenario", "optional", "test_type", "event_type"},
		),
		successRate30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_integration_success_rate_30d",
				Help: "Integration test success rate over the past 30 days (Succeeded / total completed).",
			},
			[]string{"cluster", "namespace", "application", "component", "scenario", "optional", "test_type", "event_type"},
		),
		totalCount30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_integration_total_count_30d",
				Help: "Total count of completed integration tests over the past 30 days (successful + failed). Includes both integration and EC test types.",
			},
			[]string{"cluster", "namespace", "application", "component", "scenario", "optional", "test_type", "event_type"},
		),
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
	duration := secondsBetween(plr.Metadata.CreationTimestamp, plr.Status.CompletionTime)
	if duration < 0 {
		return
	}

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
		isPLRSucceeded(plr),
	)
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *IntegrationSLO30d) updateGauges(store *Store) {
	m.mean30d.Reset()
	m.successRate30d.Reset()
	m.totalCount30d.Reset()

	store.ForEachWindow(metricIntegrationDuration, func(ls LabelSet, window *MetricWindow) {
		totalCount := window.ComputeTotalCount()
		if totalCount == 0 {
			return // no data in window — don't emit, don't misfire alerts
		}
		labels := []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Scenario, ls.Optional, ls.TestType, ls.EventType}
		m.mean30d.WithLabelValues(labels...).Set(window.ComputeSuccessMean())
		m.successRate30d.WithLabelValues(labels...).Set(window.ComputeSuccessRate())
		m.totalCount30d.WithLabelValues(labels...).Set(float64(totalCount))
	})
}

// Describe implements prometheus.Collector
func (m *IntegrationSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.mean30d.Describe(ch)
	m.successRate30d.Describe(ch)
	m.totalCount30d.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *IntegrationSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.mean30d.Collect(ch)
	m.successRate30d.Collect(ch)
	m.totalCount30d.Collect(ch)
}
