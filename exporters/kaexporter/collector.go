package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ── Prometheus Collector interface ────────────────────────────────────────────

// Describe implements prometheus.Collector
func (e *KAExporter) Describe(ch chan<- *prometheus.Desc) {
	e.buildDurationHist.Describe(ch)
	e.integrationDurationHist.Describe(ch)
	e.releaseDurationHist.Describe(ch)
	e.releasePLRTotalHist.Describe(ch)
	e.releasePLRExecHist.Describe(ch)
	e.buildWaitGauge.Describe(ch)
	e.integrationWaitGauge.Describe(ch)
	e.integrationDelayGauge.Describe(ch)
	e.releasePLRWaitGauge.Describe(ch)
	e.archivedCompletionGauge.Describe(ch)
	e.scrapeErrorsTotal.Describe(ch)
	e.lastScrapeSuccessGauge.Describe(ch)
	e.scrapeDurationGauge.Describe(ch)
	e.truncationsTotal.Describe(ch)
}

// Collect implements prometheus.Collector. It emits the metric state cached
// by the most recent background collection cycle — no I/O is performed here.
// A read lock is held so that a concurrent runCollection() cannot interleave
// its gauge Reset()+Set() sequence with this read.
func (e *KAExporter) Collect(ch chan<- prometheus.Metric) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	e.buildDurationHist.Collect(ch)
	e.integrationDurationHist.Collect(ch)
	e.releaseDurationHist.Collect(ch)
	e.releasePLRTotalHist.Collect(ch)
	e.releasePLRExecHist.Collect(ch)
	e.buildWaitGauge.Collect(ch)
	e.integrationWaitGauge.Collect(ch)
	e.integrationDelayGauge.Collect(ch)
	e.releasePLRWaitGauge.Collect(ch)
	e.archivedCompletionGauge.Collect(ch)
	e.scrapeErrorsTotal.Collect(ch)
	e.lastScrapeSuccessGauge.Collect(ch)
	e.scrapeDurationGauge.Collect(ch)
	e.truncationsTotal.Collect(ch)
}

// ── Background collection lifecycle ──────────────────────────────────────────

// Start runs the background metric collection loop. It performs one initial
// collection synchronously so that metrics are populated before /metrics is
// served, closes readyCh to signal readiness, then ticks every collectInterval
// until ctx is cancelled. Intended to be called in a goroutine from main().
func (e *KAExporter) Start(ctx context.Context) {
	log.Printf("Starting background collection (interval=%s, collection-timeout=%s)",
		e.collectInterval, e.scrapeTimeout)
	e.runCollection()
	close(e.readyCh)

	ticker := time.NewTicker(e.collectInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.runCollection()
		case <-ctx.Done():
			log.Printf("Background collection stopped")
			return
		}
	}
}

// runCollection performs one full KubeArchive fetch cycle and updates all
// metric objects in memory. A write lock is held across the entire
// gauge Reset()+Set() sequence so that concurrent Collect() calls always
// observe a consistent (fully-populated or fully-empty) snapshot.
func (e *KAExporter) runCollection() {
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()

	// Gauges are reset at the start of every collection cycle so that stale
	// label sets (resources that have aged out of the window) are pruned.
	// Histograms accumulate monotonically across cycles; rate() normalises.
	e.buildWaitGauge.Reset()
	e.integrationWaitGauge.Reset()
	e.integrationDelayGauge.Reset()
	e.releasePLRWaitGauge.Reset()
	e.archivedCompletionGauge.Reset()

	// Hard deadline for the entire KubeArchive fetch. Cancels all in-flight
	// HTTP requests if KubeArchive is slow or a namespace hangs.
	ctx, cancel := context.WithTimeout(context.Background(), e.scrapeTimeout)
	defer cancel()

	if err := e.collectMetrics(ctx); err != nil {
		log.Printf("Error collecting metrics: %v", err)
		e.scrapeErrorsTotal.WithLabelValues(e.cluster, "collect").Inc()
	} else {
		now := time.Now().Unix()
		e.lastScrapeSuccessGauge.Set(float64(now))
		e.lastScrapeSuccessAt.Store(now)
	}
	e.scrapeDurationGauge.Set(time.Since(start).Seconds())
}

// ── Collection orchestration ──────────────────────────────────────────────────

// collectMetrics fetches data from KubeArchive for every tenant namespace and populates metrics.
func (e *KAExporter) collectMetrics(ctx context.Context) error {
	namespaces, err := e.tenantNamespaces(ctx)
	if err != nil {
		return err
	}
	if len(namespaces) == 0 {
		log.Printf("No tenant namespaces to scrape (label %s=%s)", tenantLabelKey, tenantLabelValue)
		return nil
	}

	// Get concurrency limit from env var or use default
	maxConcurrent := defaultMaxConcurrent
	if mc := strings.TrimSpace(os.Getenv(maxConcurrentEnv)); mc != "" {
		if parsed, err := strconv.Atoi(mc); err == nil && parsed > 0 {
			maxConcurrent = parsed
		}
	}

	// since limits all KubeArchive list queries to the configured look-back window.
	// KubeArchive has no automatic retention — without this filter every scrape would
	// scan 6+ months of history, causing excessive DB load and stale gauge values.
	since := time.Now().UTC().Add(-time.Duration(e.windowHours) * time.Hour).Format(time.RFC3339)
	log.Printf("Collecting metrics from KubeArchive (%d tenant namespace(s), window=%dh, since=%s, concurrency=%d)...",
		len(namespaces), e.windowHours, since, maxConcurrent)

	// Parallel Release fetching - maintains global catalog for correlation
	releaseIdx := e.gatherAllReleasesParallel(ctx, namespaces, since, maxConcurrent)
	log.Printf("Loaded %d Release CR(s) across tenant namespaces for correlation", len(releaseIdx.store))

	// Thread-safe outcome counts
	outcomeCounts := newSafeOutcomeCounts()
	addReleaseOutcomeCounts(releaseIdx, outcomeCounts)

	// Parallel namespace collection with streaming PLRs/Snapshots
	type nsResult struct {
		namespace  string
		buildCount int
		testCount  int
		err        error
	}

	results := make(chan nsResult, len(namespaces))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, ns := range namespaces {
		wg.Add(1)
		go func(ns string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			b, t, err := e.collectNamespace(ctx, ns, releaseIdx, since, outcomeCounts)
			results <- nsResult{namespace: ns, buildCount: b, testCount: t, err: err}
		}(ns)
	}

	// Close results channel when all goroutines finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var totalBuild, totalTest, nsOK int
	for result := range results {
		if result.err != nil {
			log.Printf("namespace %q: %v", result.namespace, result.err)
			e.scrapeErrorsTotal.WithLabelValues(e.cluster, "namespaces").Inc()
			continue
		}
		nsOK++
		totalBuild += result.buildCount
		totalTest += result.testCount
	}

	e.collectManagedReleasePLRs(ctx, since, outcomeCounts)

	// Get final counts and emit metrics
	finalCounts := outcomeCounts.getAll()
	for k, v := range finalCounts {
		e.archivedCompletionGauge.WithLabelValues(
			e.cluster, k.namespace, k.applicationNamespace, k.phase, k.application, k.component, k.result,
		).Set(v)
	}

	log.Printf("Metrics collected: %d build PLRs, %d test PLRs (%d/%d tenant namespaces scraped successfully); %d releases indexed",
		totalBuild, totalTest, nsOK, len(namespaces), len(releaseIdx.store))

	return nil
}

// tenantNamespaces returns either fixed namespace(s) or all namespaces labeled as
// Konflux tenants. The Kubernetes list is paginated with kaPageLimit to handle clusters
// with more than 500 tenant namespaces.
// fixedTenantNamespace supports comma-separated values for multiple specific namespaces.
func (e *KAExporter) tenantNamespaces(ctx context.Context) ([]string, error) {
	if e.fixedTenantNamespace != "" {
		// Support comma-separated namespaces
		namespaces := strings.Split(e.fixedTenantNamespace, ",")
		var result []string
		for _, ns := range namespaces {
			ns = strings.TrimSpace(ns)
			if ns != "" {
				result = append(result, ns)
			}
		}
		return result, nil
	}

	sel := fmt.Sprintf("%s=%s", tenantLabelKey, tenantLabelValue)
	opts := metav1.ListOptions{
		LabelSelector: sel,
		Limit:         kaPageLimit,
	}

	var names []string
	for {
		list, err := e.k8sClient.CoreV1().Namespaces().List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("list namespaces with %q: %w", sel, err)
		}
		for i := range list.Items {
			names = append(names, list.Items[i].Name)
		}
		if len(names) >= kaMaxItems {
			log.Printf("WARNING: tenantNamespaces: reached kaMaxItems cap (%d); "+
				"some tenant namespaces may not be scraped — check label selector %q", kaMaxItems, sel)
			break
		}
		if list.Continue == "" {
			break
		}
		opts.Continue = list.Continue
	}

	sort.Strings(names)
	return names, nil
}

// gatherAllReleasesParallel fetches Release CRs from every tenant namespace in parallel
// and returns a *releaseIndex for O(1) build-PLR → Release correlation. The full catalog
// must be assembled before namespace processing begins because releases may arrive weeks
// after builds and may live in dedicated release namespaces.
func (e *KAExporter) gatherAllReleasesParallel(ctx context.Context, namespaces []string, since string, maxConcurrent int) *releaseIndex {
	type fetchResult struct {
		namespace string
		releases  []Release
		err       error
	}

	results := make(chan fetchResult, len(namespaces))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, ns := range namespaces {
		wg.Add(1)
		go func(ns string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			releasesURL := fmt.Sprintf("%s/apis/appstudio.redhat.com/v1alpha1/namespaces/%s/releases", e.kaHost, ns)
			items, err := e.fetchReleases(ctx, releasesURL, since, ns)
			results <- fetchResult{namespace: ns, releases: items, err: err}
		}(ns)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Build the index under a mutex — results arrive from concurrent goroutines.
	idx := newReleaseIndex()
	var mu sync.Mutex
	for result := range results {
		if result.err != nil {
			log.Printf("releases fetch namespace %q: %v", result.namespace, result.err)
			e.scrapeErrorsTotal.WithLabelValues(e.cluster, "releases").Inc()
			continue
		}
		mu.Lock()
		idx.addReleases(result.namespace, result.releases)
		mu.Unlock()
	}
	return idx
}

// collectNamespace scrapes PLRs, Snapshots, and linked Releases for one tenant namespace
// and emits duration metrics. Resources are streamed page-by-page to bound memory usage.
func (e *KAExporter) collectNamespace(ctx context.Context, tenantNS string, globalReleases *releaseIndex, since string, outcomeCounts *safeOutcomeCounts) (buildCount, testCount int, err error) {
	// Phase 1: build snapshot index by streaming — pages are freed after indexing.
	snapURL := fmt.Sprintf("%s/apis/appstudio.redhat.com/v1alpha1/namespaces/%s/snapshots", e.kaHost, tenantNS)
	idx := newSnapshotIndex()
	if streamErr := e.streamSnapshots(ctx, snapURL, since, tenantNS, idx.add); streamErr != nil {
		log.Printf("namespace %q: could not index snapshots (annotation fallback only): %v", tenantNS, streamErr)
		idx = nil // nil idx → resolveSnapshotNameForBuild falls back to annotation
	}

	// Phase 2: stream PLRs one page at a time.
	//
	// Build PLRs — two passes over each PLR:
	//   1. observeBuildHistograms: called for EVERY completed build PLR in the window.
	//      Histograms accumulate across scrapes; observing all builds gives the true
	//      duration distribution, not just the "most recent" value.
	//   2. seenBuilds gate: only the FIRST (newest) occurrence per (app, component, result)
	//      sets the buildWaitGauge and populates buildInfoBySnapshot for the gap metric.
	//      Gauges are reset each scrape, so only "latest" is meaningful; the gap metric
	//      must also reference the newest build to stay consistent with integration tests.
	//
	// Test PLRs: copied into testBySnapshot for Phase 3.
	// buildInfoBySnapshot: string triples for the integration gap — negligible size.
	type buildMeta struct{ application, component, completedAt string }
	// seenBuilds prevents setting point-in-time Gauges more than once per label set.
	// KubeArchive returns items newest-first, so the first hit is always the most recent.
	type buildKey struct{ app, component, result string }
	seenBuilds := make(map[buildKey]bool)
	testBySnapshot := make(map[string][]PipelineRun)
	buildInfoBySnapshot := make(map[string]buildMeta)

	plrURL := fmt.Sprintf("%s/apis/tekton.dev/v1/namespaces/%s/pipelineruns", e.kaHost, tenantNS)
	streamErr := e.streamPLRs(ctx, plrURL, since, tenantNS, func(page []PipelineRun) {
		for i := range page {
			plr := &page[i]
			switch getLabel(*plr, labelPipelinesType, "") {
			case "build":
				if plr.Status.CompletionTime == "" {
					continue
				}
				buildCount++
				addArchivedOutcome(tenantNS, "build", *plr, outcomeCounts)
				app := getLabel(*plr, labelAppStudioApp, "unknown")
				comp := getLabel(*plr, labelAppStudioComp, "unknown")
				result := getResult(*plr)

				// Resolve snapshot once — used by both histogram exemplar and release correlation.
				snap := resolveSnapshotNameForBuild(tenantNS, *plr, idx)

				// Always observe histograms — captures every build in the window.
				e.observeBuildHistograms(tenantNS, *plr, app, comp, snap, result, globalReleases)

				bk := buildKey{app, comp, result}
				if !seenBuilds[bk] {
					// Newest occurrence: set point-in-time Gauges and populate correlation index.
					seenBuilds[bk] = true
					e.setNewestBuildGauges(tenantNS, *plr, app, comp)
					if snap != "" {
						buildInfoBySnapshot[snap] = buildMeta{app, comp, plr.Status.CompletionTime}
					}
				}
			case "test":
				if plr.Status.CompletionTime == "" {
					continue
				}
				testCount++
				addArchivedOutcome(tenantNS, "test", *plr, outcomeCounts)
				snap := getLabel(*plr, labelOrAnnotationSnapshot, "")
				if snap != "" {
					// Copy PLR value — page[i] is freed at end of this page callback.
					testBySnapshot[snap] = append(testBySnapshot[snap], *plr)
				}
			}
			// page[i] is not retained; the page slice is GC-eligible after this loop.
		}
	})
	if streamErr != nil {
		return buildCount, testCount, fmt.Errorf("stream pipelineruns: %w", streamErr)
	}

	// Post-stream cleanup: drop test PLRs for snapshots that have no matching build PLR
	// in buildInfoBySnapshot (i.e. the build PLR was a de-duplicated older run). These
	// were collected during streaming but will never be processed. Dropping them now
	// releases memory before Phase 3 rather than relying on GC after function return.
	//
	// Note: testBySnapshot is bounded by the 48h window × build rate × component count.
	// In practice this is small, but validate against real tenant data if high-velocity
	// namespaces are added. The cleanup below makes the effective bound tighter:
	// only one entry per (snapshot × most-recent-build) is retained.
	for snap := range testBySnapshot {
		if buildInfoBySnapshot[snap].application == "" {
			delete(testBySnapshot, snap)
		}
	}

	// Phase 3: integration metrics — all data is now in small, indexed structures.
	for snap, tests := range testBySnapshot {
		info := buildInfoBySnapshot[snap]
		e.processIntegrationTests(tenantNS, tests, info.application, info.component, info.completedAt)
	}

	return buildCount, testCount, nil
}

// addArchivedOutcome increments the completion counter for the given PLR and phase.
func addArchivedOutcome(tenantNS, phase string, plr PipelineRun, outcomeCounts *safeOutcomeCounts) {
	app := getLabel(plr, labelAppStudioApp, "unknown")
	comp := getLabel(plr, labelAppStudioComp, "unknown")
	res := getResult(plr)
	k := archivedOutcomeKey{
		namespace: tenantNS, applicationNamespace: "", phase: phase, application: app, component: comp, result: res,
	}
	outcomeCounts.increment(k)
}

// addReleaseOutcomeCounts increments completion counters for all completed Release CRs in the index.
func addReleaseOutcomeCounts(idx *releaseIndex, outcomeCounts *safeOutcomeCounts) {
	for i := range idx.store {
		re := &idx.store[i]
		if re.Status.CompletionTime == "" {
			continue
		}
		ns := re.crNamespace
		if ns == "" {
			ns = re.Metadata.Namespace
		}
		app := getLabel(re.Release, labelAppStudioApp, "unknown")
		comp := getLabel(re.Release, labelAppStudioComp, "unknown")
		res := getReleaseResult(re.Release)
		k := archivedOutcomeKey{
			namespace: ns, applicationNamespace: "", phase: "release_cr", application: app, component: comp, result: res,
		}
		outcomeCounts.increment(k)
	}
}

// parseManagedReleasePLRNamespaces returns the deduplicated, sorted list of namespaces
// from MANAGED_RELEASE_PLR_NAMESPACES (comma-separated).
func parseManagedReleasePLRNamespaces() []string {
	s := strings.TrimSpace(os.Getenv(managedReleasePLRNamespacesEnv))
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	seen := make(map[string]struct{})
	var out []string
	for _, p := range parts {
		n := strings.TrimSpace(p)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// collectManagedReleasePLRs streams PipelineRuns from managed release namespaces and
// emits queue, execution, and total duration metrics for release-service PLRs.
func (e *KAExporter) collectManagedReleasePLRs(ctx context.Context, since string, outcomeCounts *safeOutcomeCounts) {
	managedNS := parseManagedReleasePLRNamespaces()
	if len(managedNS) == 0 {
		log.Printf("%s not set or empty; skipping managed release PipelineRun metrics", managedReleasePLRNamespacesEnv)
		return
	}
	log.Printf("KubeArchive: scraping %d managed namespace(s) for release PipelineRuns: %v", len(managedNS), managedNS)
	for _, ns := range managedNS {
		plrURL := fmt.Sprintf("%s/apis/tekton.dev/v1/namespaces/%s/pipelineruns", e.kaHost, ns)
		if err := e.streamPLRs(ctx, plrURL, since, ns, func(page []PipelineRun) {
			for i := range page {
				plr := &page[i]
				if isReleaseServicePLR(*plr) {
					e.processReleasePipelineRun(ns, *plr, outcomeCounts)
				}
			}
		}); err != nil {
			log.Printf("managed namespace %q: stream pipelineruns: %v", ns, err)
			e.scrapeErrorsTotal.WithLabelValues(e.cluster, "managed_plrs").Inc()
		}
	}
}

// isReleaseServicePLR reports whether plr is a completed managed release-service PipelineRun.
func isReleaseServicePLR(plr PipelineRun) bool {
	return getLabel(plr, labelAppStudioService, "") == valueAppStudioServiceRelease &&
		getLabel(plr, labelPipelinesType, "") == valuePipelinesTypeManaged &&
		plr.Status.CompletionTime != ""
}
