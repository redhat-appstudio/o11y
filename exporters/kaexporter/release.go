package main

import (
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ReleaseSLO30d manages 30-day SLO metrics for releases
type ReleaseSLO30d struct {
	SLOGaugeSet
	retryCount30d *prometheus.GaugeVec
}

// newReleaseSLO30d initializes release 30d SLO metrics
func newReleaseSLO30d() *ReleaseSLO30d {
	labels := []string{"cluster", "namespace", "application", "component", "automated"}
	return &ReleaseSLO30d{
		SLOGaugeSet: newSLOGaugeSet("konflux_release_cr", "Release CR", labels),
		retryCount30d: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_cr_retry_count_30d",
				Help: "Number of retries for this release intent (snapshot + releasePlan) over the past 30 days. 0 = original succeeded without retries.",
			},
			[]string{"cluster", "namespace", "snapshot", "release_plan", "final_status"},
		),
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

	// Extract automated label
	automated := getLabel(rel, labelReleaseAutomated, "unknown")

	ls := LabelSet{
		Cluster:     cluster,
		Namespace:   namespace,
		Application: application,
		Component:   component,
		Automated:   automated,
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

// releaseIntentGroup represents all Release CRs for a single intent (snapshot + releasePlan)
type releaseIntentGroup struct {
	Intent      releaseIntentKey
	Releases    []Release
	RetryCount  int
	FinalStatus string
}

// sortReleasesByTimestamp sorts releases by creation timestamp (ascending)
func sortReleasesByTimestamp(releases []Release) {
	sort.Slice(releases, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, releases[i].Metadata.CreationTimestamp)
		tj, _ := time.Parse(time.RFC3339, releases[j].Metadata.CreationTimestamp)
		return ti.Before(tj)
	})
}

// buildReleaseIntentGroups groups releases by (namespace, snapshot, releasePlan) and computes retry counts
func (m *ReleaseSLO30d) buildReleaseIntentGroups(releaseIdx *releaseIndex) map[releaseIntentKey]*releaseIntentGroup {
	groups := make(map[releaseIntentKey]*releaseIntentGroup)

	// Group releases by intent
	for i := range releaseIdx.store {
		entry := &releaseIdx.store[i]
		rel := entry.Release

		// Extract intent key
		snapshot := resolveSnapshot(rel)
		plan := resolveReleasePlan(rel)
		namespace := entry.crNamespace
		if namespace == "" {
			namespace = rel.Metadata.Namespace
		}

		// Skip if missing intent data
		if snapshot == "" || plan == "" {
			continue
		}

		intent := releaseIntentKey{
			Namespace:   namespace,
			Snapshot:    snapshot,
			ReleasePlan: plan,
		}

		// Create or append to group
		if groups[intent] == nil {
			groups[intent] = &releaseIntentGroup{
				Intent:   intent,
				Releases: []Release{},
			}
		}
		groups[intent].Releases = append(groups[intent].Releases, rel)
	}

	// 30-day cutoff for retry metrics (align with rolling window)
	cutoff := time.Now().UTC().AddDate(0, 0, -30)

	// Process each group: sort and calculate retry count
	for intent, group := range groups {
		// Sort by creation timestamp
		sortReleasesByTimestamp(group.Releases)

		// Skip groups where most recent release completed >30 days ago
		if len(group.Releases) > 0 {
			mostRecent := group.Releases[len(group.Releases)-1]
			if mostRecent.Status.CompletionTime != "" {
				completionTime, err := time.Parse(time.RFC3339, mostRecent.Status.CompletionTime)
				if err == nil && completionTime.Before(cutoff) {
					delete(groups, intent)
					continue
				}
			}
		}

		// Calculate retry count
		group.RetryCount = len(group.Releases) - 1

		// Determine final status (most recent attempt)
		if len(group.Releases) > 0 {
			mostRecent := group.Releases[len(group.Releases)-1]
			completed, succeeded, failureReason := releaseStatus(mostRecent)
			if !completed {
				group.FinalStatus = "InProgress"
			} else if succeeded {
				group.FinalStatus = "Succeeded"
			} else if failureReason != "" {
				group.FinalStatus = "Failed"
			} else {
				group.FinalStatus = "Unknown"
			}
		}
	}

	return groups
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *ReleaseSLO30d) updateGauges(store *Store, cluster string, releaseIdx *releaseIndex) {
	m.SLOGaugeSet.UpdateFromStore(store, metricReleaseDuration, func(ls LabelSet) []string {
		return []string{ls.Cluster, ls.Namespace, ls.Application, ls.Component, ls.Automated}
	})

	// Release-specific: retry count metrics
	m.retryCount30d.Reset()
	if releaseIdx != nil {
		intentGroups := m.buildReleaseIntentGroups(releaseIdx)
		for _, group := range intentGroups {
			labels := []string{
				cluster,
				group.Intent.Namespace,
				group.Intent.Snapshot,
				group.Intent.ReleasePlan,
				group.FinalStatus,
			}
			m.retryCount30d.WithLabelValues(labels...).Set(float64(group.RetryCount))
		}
	}
}

// Describe implements prometheus.Collector
func (m *ReleaseSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.SLOGaugeSet.Describe(ch)
	m.retryCount30d.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *ReleaseSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.SLOGaugeSet.Collect(ch)
	m.retryCount30d.Collect(ch)
}
