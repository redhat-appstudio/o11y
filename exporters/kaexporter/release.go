package main

import (
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// rerunNamePattern matches Release CR names that indicate a rerun/retry
// e.g. "my-release-rerun-abc12", "my-release-rr-xyz", "my-release-retry-def"
var rerunNamePattern = regexp.MustCompile(`(-rerun-[^-]*|-rr-[^-]*|-retry-[^-]*)$`)

// ReleaseSLO30d manages 30-day SLO metrics for releases
type ReleaseSLO30d struct {
	SLOGaugeSet
}

// newReleaseSLO30d initializes release 30d SLO metrics
func newReleaseSLO30d() *ReleaseSLO30d {
	labels := []string{"cluster", "namespace", "application", "component", "automated", "event_type"}
	return &ReleaseSLO30d{
		SLOGaugeSet: newSLOGaugeSet("konflux_release_cr", "Release CR", labels),
	}
}

// recordObservation records a single release observation into the rolling store
func (m *ReleaseSLO30d) recordObservation(
	store *Store,
	cluster, namespace, application, component string,
	rel Release,
) {
	// Excludes in-progress releases (Status="False" + Reason="Progressing")
	completed, succeeded, failureReason := releaseStatus(rel)

	if !completed {
		return // Skip in-progress releases
	}

	if rel.Status.CompletionTime == "" {
		return // Additional safety check
	}

	completionTime, err := time.Parse(time.RFC3339, rel.Status.CompletionTime)
	if err != nil {
		return
	}
	duration := secondsBetween(rel.Status.StartTime, rel.Status.CompletionTime)
	if duration < 0 {
		return
	}
	waitTime := secondsBetween(rel.Metadata.CreationTimestamp, rel.Status.StartTime)

	// Extract release-specific labels
	automated := getLabel(rel, labelReleaseAutomated, "unknown")
	eventType := getLabel(rel, labelPACEventType, "")
	if eventType == "" {
		if rerunNamePattern.MatchString(rel.Metadata.Name) {
			eventType = "kaexporter-rerun"
		} else {
			eventType = "unknown"
		}
	}

	ls := LabelSet{
		Cluster:     cluster,
		Namespace:   namespace,
		Application: application,
		Component:   component,
		Automated:   automated,
		EventType:   eventType,
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
		waitTime,
		succeeded,
		failureReason,
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
	m.SLOGaugeSet.UpdateFromStore(store, metricReleaseDuration, func(ls LabelSet) []string {
		return []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Automated, ls.EventType}
	})
}

// Describe implements prometheus.Collector
func (m *ReleaseSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.SLOGaugeSet.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *ReleaseSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.SLOGaugeSet.Collect(ch)
}
