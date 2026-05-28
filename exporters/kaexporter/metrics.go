package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ── Build metrics ─────────────────────────────────────────────────────────────

// observeBuildHistograms records duration observations for a completed build PLR and,
// when a matching Release CR is found, also for the release. Exemplars carry resource
// names as stable join keys for cross-signal correlation.
func (e *KAExporter) observeBuildHistograms(tenantNS string, plr PipelineRun, application, component, snapshot, result string, globalReleases *releaseIndex) {
	createdAt := plr.Metadata.CreationTimestamp
	completedAt := plr.Status.CompletionTime

	buildExemplar := prometheus.Labels{
		"pipelinerun": plr.Metadata.Name,
		"snapshot":    snapshot,
	}
	if buildDur := secondsBetween(createdAt, completedAt); buildDur >= 0 {
		observeWithExemplar(
			e.buildDurationHist.WithLabelValues(e.cluster, tenantNS, application, component, result),
			buildDur, buildExemplar,
		)
	}

	if matched := findMatchingRelease(plr, snapshot, application, component, globalReleases); matched != nil {
		relNS := matched.crNamespace
		relExemplar := prometheus.Labels{
			"pipelinerun": plr.Metadata.Name,
			"snapshot":    snapshot,
			"release_cr":  matched.Metadata.Name,
		}
		// Release CR: use status.startTime → status.completionTime.
		// status.startTime is set by the Release controller when it begins orchestration;
		relStart := matched.Status.StartTime
		if relStart == "" {
			relStart = matched.Metadata.CreationTimestamp
		}
		if relDur := secondsBetween(relStart, matched.Status.CompletionTime); relDur >= 0 {
			observeWithExemplar(
				e.releaseDurationHist.WithLabelValues(e.cluster, tenantNS, application, component, relNS),
				relDur, relExemplar,
			)
		}
	}
}

// setNewestBuildGauges sets point-in-time Gauge metrics for the newest build PLR per
// (application, component) label set.
func (e *KAExporter) setNewestBuildGauges(tenantNS string, plr PipelineRun, application, component string) {
	createdAt := plr.Metadata.CreationTimestamp
	startedAt := plr.Status.StartTime
	if startDelay := secondsBetween(createdAt, startedAt); startDelay >= 0 {
		e.buildWaitGauge.WithLabelValues(e.cluster, tenantNS, application, component).Set(startDelay)
	}
}

// ── Integration test metrics ──────────────────────────────────────────────────

// processIntegrationTests emits duration, queue, and build-to-integration gap metrics
// for a set of integration test PLRs belonging to a single snapshot.
func (e *KAExporter) processIntegrationTests(tenantNS string, tests []PipelineRun, application, component, buildCompletedAt string) {
	// firstTestCreatedTime tracks the earliest integration test start for the gap metric.
	var firstTestCreatedTime time.Time
	hasFirst := false

	for _, test := range tests {
		scenario := getLabel(test, "test.appstudio.openshift.io/scenario", "unknown")
		optional := getLabel(test, "test.appstudio.openshift.io/optional", "false")
		testResult := getResult(test)

		testCreated := test.Metadata.CreationTimestamp
		testCompleted := test.Status.CompletionTime

		// Track the earliest test PLR creation time for the gap calculation.
		if t, err := time.Parse(time.RFC3339, testCreated); err == nil {
			if !hasFirst || t.Before(firstTestCreatedTime) {
				firstTestCreatedTime = t
				hasFirst = true
			}
		}

		if testDuration := secondsBetween(testCreated, testCompleted); testDuration >= 0 {
			testExemplar := prometheus.Labels{
				"pipelinerun": test.Metadata.Name,
				"snapshot":    getLabel(test, labelOrAnnotationSnapshot, ""),
			}
			observeWithExemplar(
				e.integrationDurationHist.WithLabelValues(
					e.cluster, tenantNS, application, component, scenario, testResult, optional,
				),
				testDuration, testExemplar,
			)
		}

		// Queue wait: creation → startTime. Gauge (last-write-wins per scenario within
		// this snapshot; test PLRs retained here are already scoped to the newest build).
		if testQueue := secondsBetween(testCreated, test.Status.StartTime); testQueue >= 0 {
			e.integrationWaitGauge.WithLabelValues(e.cluster, tenantNS, application, component, scenario).Set(testQueue)
		}
	}

	if hasFirst {
		if buildDone, err := time.Parse(time.RFC3339, buildCompletedAt); err == nil {
			if gap := firstTestCreatedTime.Sub(buildDone).Seconds(); gap >= 0 {
				e.integrationDelayGauge.WithLabelValues(e.cluster, tenantNS, application, component).Set(gap)
			}
		}
	}
}

// ── Managed release PipelineRun metrics ──────────────────────────────────────

// processReleasePipelineRun emits queue, execution, and total duration metrics for one
// managed release-service PipelineRun and increments its outcome counter.
func (e *KAExporter) processReleasePipelineRun(managedNS string, plr PipelineRun, outcomeCounts *safeOutcomeCounts) {
	app := getLabel(plr, labelAppStudioApp, "unknown")
	appTenantNS := getLabel(plr, labelReleaseApplicationNS, "unknown")
	pipeline := getLabel(plr, labelTektonPipeline, "unknown")
	result := getResult(plr)

	created := plr.Metadata.CreationTimestamp
	started := plr.Status.StartTime
	completed := plr.Status.CompletionTime

	plrExemplar := prometheus.Labels{
		"pipelinerun": plr.Metadata.Name,
	}
	if total := secondsBetween(created, completed); total >= 0 {
		observeWithExemplar(
			e.releasePLRTotalHist.WithLabelValues(e.cluster, managedNS, appTenantNS, app, pipeline, result),
			total, plrExemplar,
		)
	}
	if q := secondsBetween(created, started); q >= 0 {
		e.releasePLRWaitGauge.WithLabelValues(e.cluster, managedNS, appTenantNS, app, pipeline).Set(q)
	}
	if exec := secondsBetween(started, completed); exec >= 0 {
		observeWithExemplar(
			e.releasePLRExecHist.WithLabelValues(e.cluster, managedNS, appTenantNS, app, pipeline, result),
			exec, plrExemplar,
		)
	}

	comp := getLabel(plr, labelAppStudioComp, "unknown")
	k := archivedOutcomeKey{
		namespace:            managedNS,
		applicationNamespace: appTenantNS,
		phase:                "release_plr",
		application:          app,
		component:            comp,
		result:               result,
	}
	outcomeCounts.increment(k)
}
