package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ── Prometheus Collector interface ────────────────────────────────────────────

// Describe implements prometheus.Collector
func (e *KAExporter) Describe(ch chan<- *prometheus.Desc) {
	e.buildSLO.Describe(ch)
	e.integrationSLO.Describe(ch)
	e.releaseSLO.Describe(ch)

	e.scrapeErrorsTotal.Describe(ch)
	e.lastScrapeSuccessGauge.Describe(ch)
	e.scrapeDurationGauge.Describe(ch)
	e.truncationsTotal.Describe(ch)
	e.retryAttemptsTotal.Describe(ch)
	e.retryExhaustedTotal.Describe(ch)
}

// Collect implements prometheus.Collector. It emits the metric state cached
// by the most recent background collection cycle — no I/O is performed here.
// A read lock is held so that a concurrent runCollection() cannot interleave
// its gauge Reset()+Set() sequence with this read.
func (e *KAExporter) Collect(ch chan<- prometheus.Metric) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	e.buildSLO.Collect(ch)
	e.integrationSLO.Collect(ch)
	e.releaseSLO.Collect(ch)

	e.scrapeErrorsTotal.Collect(ch)
	e.lastScrapeSuccessGauge.Collect(ch)
	e.scrapeDurationGauge.Collect(ch)
	e.truncationsTotal.Collect(ch)
	e.retryAttemptsTotal.Collect(ch)
	e.retryExhaustedTotal.Collect(ch)
}

// ── Background collection lifecycle ──────────────────────────────────────────

// Start runs the background metric collection loop. It performs one initial
// collection synchronously so that metrics are populated before /metrics is
// served, closes readyCh to signal readiness, then ticks every collectInterval
// until ctx is cancelled.
//
// IMPORTANT: runCollection() must ONLY be called from this single goroutine.
// The coldStart flag is read without mutex protection, which is safe only because
// this method guarantees serial execution.
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
// metric objects in memory. Data collection (HTTP fetches to KubeArchive) runs
// without holding the lock, since rollingStore has its own internal mutex.
// A write lock is acquired ONLY for the final gauge Reset()+Set() sequence
// (~1-5ms) so that concurrent Collect() calls always observe a consistent snapshot.
func (e *KAExporter) runCollection() {
	start := time.Now()

	// On cold start use a longer timeout (5 min) to accommodate the 30-day bootstrap.
	// Measured cold-start duration is ~95s; 300s gives 3× safety margin.
	timeout := e.scrapeTimeout
	if e.coldStart {
		timeout = time.Duration(defaultColdStartTimeoutSecs) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Fetch data from KubeArchive WITHOUT holding lock.
	// RollingStore is thread-safe with its own internal mutex.
	_, err := e.collectMetrics(ctx)

	// Acquire write lock ONLY for gauge updates (1-5ms operation).
	// This prevents Prometheus scrapes from blocking during KubeArchive HTTP fetches.
	e.mu.Lock()
	defer e.mu.Unlock()

	if err != nil {
		log.Printf("Error collecting metrics: %v", err)
		e.scrapeErrorsTotal.WithLabelValues(e.cluster, "collect").Inc()
	} else {
		now := time.Now().Unix()
		e.lastScrapeSuccessGauge.Set(float64(now))
		e.lastScrapeSuccessAt.Store(now)

		skipBreach := skipBreachNamespacesFromStates(e.bootstrapStates)

		e.buildSLO.updateGauges(e.rollingStore, skipBreach)
		e.integrationSLO.updateGauges(e.rollingStore, skipBreach)
		e.releaseSLO.updateGauges(e.rollingStore, skipBreach)

		// coldStart flag now managed per-namespace; check if all namespaces are bootstrapped
		if e.coldStart {
			log.Printf("First collection complete in %.1fs", time.Since(start).Seconds())
			e.coldStart = false
		}
	}
	e.scrapeDurationGauge.Set(time.Since(start).Seconds())
}

// ── Collection orchestration ──────────────────────────────────────────────────

// collectMetrics fetches data from KubeArchive for every tenant namespace and populates metrics.
func (e *KAExporter) collectMetrics(ctx context.Context) (*releaseIndex, error) {
	namespaces, err := e.tenantNamespaces(ctx)
	if err != nil {
		return nil, err
	}
	if len(namespaces) == 0 {
		log.Printf("No tenant namespaces to scrape (label %s=%s)", tenantLabelKey, tenantLabelValue)
		return nil, nil
	}

	// Prune bootstrapStates for decommissioned namespaces to avoid wasted gap-fill attempts
	e.mu.Lock()
	nsSet := make(map[string]bool, len(namespaces))
	for _, ns := range namespaces {
		nsSet[ns] = true
	}
	for ns := range e.bootstrapStates {
		if !nsSet[ns] {
			delete(e.bootstrapStates, ns)
		}
	}
	e.mu.Unlock()

	// Determine concurrency: lower during initial cold start to ease KubeArchive load
	concurrency := e.maxConcurrent
	if e.coldStart {
		concurrency = coldStartMaxConcurrent
	}

	// Count namespaces by bootstrap phase:
	// - needsFirstFetch: no state yet, requires full 30-day initial fetch
	// - gapFilling: already truncated, gap-fill handles history, main collection uses steady-state
	e.mu.RLock()
	var needsFirstFetch, gapFilling int
	for _, ns := range namespaces {
		state := e.bootstrapStates[ns]
		if state == nil || !state.Bootstrapped {
			if state != nil && state.OldestSeenCreationTS != "" {
				gapFilling++
			} else {
				needsFirstFetch++
			}
		}
	}
	e.mu.RUnlock()
	needsBootstrap := needsFirstFetch + gapFilling

	if e.coldStart {
		log.Printf("COLD START: bootstrapping 30-day rolling store (%d tenant namespace(s), %d need bootstrap, concurrency=%d, releases=30d)...",
			len(namespaces), needsBootstrap, concurrency)
	} else if needsFirstFetch > 0 {
		log.Printf("Collecting metrics from KubeArchive (%d tenant namespace(s), %d need 30d bootstrap, %d gap-filling, concurrency=%d, releases=30d)...",
			len(namespaces), needsFirstFetch, gapFilling, concurrency)
	} else if gapFilling > 0 {
		log.Printf("Collecting metrics from KubeArchive (%d tenant namespace(s), %d gap-filling, concurrency=%d, releases=%dh)...",
			len(namespaces), gapFilling, concurrency, e.queryWindowHours)
	} else {
		log.Printf("Collecting metrics from KubeArchive (%d tenant namespace(s), query_window=%dh, concurrency=%d, releases=%dh)...",
			len(namespaces), e.queryWindowHours, concurrency, e.queryWindowHours)
	}

	// For release fetching, use 30d window only when namespaces still need their
	// initial 30-day fetch. Namespaces that are already truncated (gap-filling)
	// collect fresh data in steady-state; their historical releases were captured
	// on the first fetch and gap-fill handles the rest.
	releaseWindowHours := e.queryWindowHours
	releaseMaxItems := kaMaxItems
	if e.coldStart || needsFirstFetch > 0 {
		releaseWindowHours = coldStartWindowHours
		releaseMaxItems = coldStartMaxItems
	}
	releaseSince := time.Now().UTC().Add(-time.Duration(releaseWindowHours) * time.Hour).Format(time.RFC3339)

	// Parallel Release fetching - maintains global catalog for metric collection
	releaseIdx := e.gatherAllReleasesParallel(ctx, namespaces, releaseSince, concurrency, releaseMaxItems)
	log.Printf("Loaded %d Release CR(s) across tenant namespaces", len(releaseIdx.store))

	// Parallel namespace collection with streaming PLRs/Snapshots
	type nsResult struct {
		namespace        string
		buildCount       int
		testCount        int
		wasTruncated     bool
		oldestCreationTS string
		err              error
	}

	results := make(chan nsResult, len(namespaces))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, ns := range namespaces {
		wg.Add(1)
		go func(ns string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Per-namespace bootstrap check: use 30-day window only on the
			// very first attempt (no state yet). Once truncated, switch to
			// steady-state window for fresh data and let gap-fill handle
			// the historical backfill independently.
			e.mu.RLock()
			state := e.bootstrapStates[ns]
			bootstrapped := state != nil && state.Bootstrapped
			alreadyTruncated := state != nil && state.OldestSeenCreationTS != ""
			e.mu.RUnlock()

			var windowHours int
			var maxItems int
			var nsCtx context.Context
			var nsCancel context.CancelFunc

			if !bootstrapped && !alreadyTruncated {
				// First attempt: full 30-day bootstrap
				windowHours = coldStartWindowHours // 720h (30 days)
				maxItems = coldStartMaxItems       // 30,000
				// Give each non-bootstrapped namespace its own independent 30-minute timeout.
				// Derive from Background() to get full 1800s independent of the 120s steady-state
				// collection timeout, but use AfterFunc to ensure SIGTERM cancels immediately.
				nsCtx, nsCancel = context.WithTimeout(context.Background(), time.Duration(defaultColdStartTimeoutSecs)*time.Second)
				defer nsCancel()
				stop := context.AfterFunc(ctx, nsCancel) // Cancel nsCtx when ctx is cancelled
				defer stop()
			} else {
				// Bootstrapped or already truncated: use steady-state window.
				// Truncated namespaces get fresh data here; gap-fill handles the
				// historical range [30d ago ... OldestSeenCreationTS] separately.
				windowHours = e.queryWindowHours // 36h (steady state with safety margin)
				maxItems = kaMaxItems
				// Share the parent context (global timeout)
				nsCtx = ctx
			}

			since := time.Now().UTC().Add(-time.Duration(windowHours) * time.Hour).Format(time.RFC3339)
			b, t, wasTrunc, oldestTS, err := e.collectNamespace(nsCtx, ns, since, "", maxItems)

			// Track bootstrap state - only on the first-fetch path.
			// Steady-state cycles (alreadyTruncated=true) just collect fresh
			// data; gap-fill handles the historical backfill and state transitions.
			if err == nil && !bootstrapped && !alreadyTruncated {
				e.mu.Lock()
				if e.bootstrapStates[ns] == nil {
					e.bootstrapStates[ns] = &nsBootstrapState{}
				}
				if completed := updateBootstrapState(e.bootstrapStates[ns], wasTrunc, oldestTS); completed {
					log.Printf("namespace %q: 30-day bootstrap complete (%d builds, %d tests)", ns, b, t)
				} else if e.bootstrapStates[ns].OldestSeenCreationTS != "" {
					log.Printf("namespace %q: 30-day bootstrap TRUNCATED (%d builds, %d tests, oldest=%s)",
						ns, b, t, e.bootstrapStates[ns].OldestSeenCreationTS)
				}
				e.mu.Unlock()
			}

			results <- nsResult{
				namespace:        ns,
				buildCount:       b,
				testCount:        t,
				wasTruncated:     wasTrunc,
				oldestCreationTS: oldestTS,
				err:              err,
			}
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

	// Record all release observations into 30d SLO store
	e.releaseSLO.recordAllFromIndex(e.rollingStore, e.cluster, releaseIdx)

	// Gap-fill pass: fill truncated namespaces in parallel (post-steady-state)
	// Only run gap-fill if main collection had some successes (avoid piling on during KubeArchive outages)
	if !e.coldStart && nsOK > 0 {
		e.mu.RLock()
		var gapFillNeeded []string
		for ns, state := range e.bootstrapStates {
			if !state.Bootstrapped && state.OldestSeenCreationTS != "" && state.GapAttempts < maxGapFillAttempts {
				gapFillNeeded = append(gapFillNeeded, ns)
			}
		}
		e.mu.RUnlock()

		if len(gapFillNeeded) > 0 {
			var gapWg sync.WaitGroup
			gapSem := make(chan struct{}, concurrency)

			for _, ns := range gapFillNeeded {
				gapWg.Add(1)

				go func(namespace string) {
					defer gapWg.Done()
					gapSem <- struct{}{}        // Acquire inside goroutine for parallel launch
					defer func() { <-gapSem }() // Release

					// Use cold-start per-namespace timeout for gap-fill queries (30-day window).
					// Derive from Background() to get full 1800s independent of the 120s steady-state
					// collection timeout, but use AfterFunc to ensure SIGTERM cancels immediately.
					gapCtx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultColdStartTimeoutSecs)*time.Second)
					defer cancel()
					stop := context.AfterFunc(ctx, cancel) // Cancel gapCtx when ctx is cancelled
					defer stop()

					e.fillNamespaceGap(gapCtx, namespace)
				}(ns)
			}

			gapWg.Wait()
		}
	}

	log.Printf("Metrics collected: %d build PLRs, %d test PLRs (%d/%d tenant namespaces scraped successfully); %d releases indexed",
		totalBuild, totalTest, nsOK, len(namespaces), len(releaseIdx.store))

	return releaseIdx, nil
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

	filtered := e.nsFilter.apply(names)
	if len(filtered) < len(names) {
		excluded := len(names) - len(filtered)
		log.Printf("Filtered out %d namespace(s) from scraping (filter: %s)", excluded, e.nsFilter.source)
	}

	return filtered, nil
}

// gatherAllReleasesParallel fetches Release CRs from every tenant namespace in parallel
// and returns a *releaseIndex for metric collection and retry analysis. The full catalog
// must be assembled before namespace processing begins because releases may arrive weeks
// after builds and may live in dedicated release namespaces.
func (e *KAExporter) gatherAllReleasesParallel(ctx context.Context, namespaces []string, since string, maxConcurrent, maxItems int) *releaseIndex {
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

			releasesURL := fmt.Sprintf("%s/apis/appstudio.redhat.com/v1alpha1/namespaces/%s/releases", e.kaHost, url.PathEscape(ns))
			items, err := e.fetchReleases(ctx, releasesURL, since, ns, maxItems)
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

// startRollingStoreMaintenance launches a background goroutine that prunes the
// seen-key deduplication map once per hour, removing entries older than
// dedupeRetentionHours (1.5× queryWindowHours). Without pruning, the map would
// grow unbounded. Retention must exceed queryWindowHours to prevent boundary
// condition double-counting when the same PLR appears in consecutive queries.
func (e *KAExporter) startRollingStoreMaintenance(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.rollingStore.PruneSeenKeys(time.Duration(e.dedupeRetentionHours) * time.Hour)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// collectNamespace scrapes PLRs for one tenant namespace and records observations
// into the 30-day rolling store. Resources are streamed page-by-page to bound memory usage.
// since/until are RFC3339 timestamps for creationTimestampAfter/Before filters (until="" for no upper bound).
// Returns (buildCount, testCount, wasTruncated, oldestCreationTimestamp, error).
func (e *KAExporter) collectNamespace(ctx context.Context, tenantNS, since, until string, maxItems int) (buildCount, testCount int, wasTruncated bool, oldestCreationTimestamp string, err error) {

	plrURL := fmt.Sprintf("%s/apis/tekton.dev/v1/namespaces/%s/pipelineruns", e.kaHost, url.PathEscape(tenantNS))
	wasTrunc, oldestTS, streamErr := e.streamPLRs(ctx, plrURL, since, until, tenantNS, maxItems, func(page []PipelineRun) {
		for i := range page {
			plr := &page[i]
			switch getLabel(*plr, labelPipelinesType, "") {
			case "build":
				if plr.Status.CompletionTime == "" {
					continue
				}
				buildCount++
				app := getLabel(*plr, labelAppStudioApp, "unknown")
				comp := getLabel(*plr, labelAppStudioComp, "unknown")

				e.buildSLO.recordObservation(e.rollingStore, e.cluster, tenantNS, app, comp, *plr)
			case "test":
				if plr.Status.CompletionTime == "" {
					continue
				}
				testCount++
				app := getLabel(*plr, labelAppStudioApp, "unknown")
				comp := getLabel(*plr, labelAppStudioComp, "unknown")

				e.integrationSLO.recordObservation(e.rollingStore, e.cluster, tenantNS, app, comp, *plr)
			}
			// page[i] is not retained; the page slice is GC-eligible after this loop.
		}
	})
	if streamErr != nil {
		return buildCount, testCount, false, "", fmt.Errorf("stream pipelineruns: %w", streamErr)
	}

	return buildCount, testCount, wasTrunc, oldestTS, nil
}

// fillNamespaceGap attempts to fill the bootstrap gap for a truncated namespace.
// Queries the time range between [30d ago ... OldestSeenCreationTS] to fetch items
// that were dropped during the initial cold-start truncation.
func (e *KAExporter) fillNamespaceGap(ctx context.Context, namespace string) {
	e.mu.RLock()
	state := e.bootstrapStates[namespace]
	if state == nil || state.Bootstrapped || state.OldestSeenCreationTS == "" {
		e.mu.RUnlock()
		return
	}
	gapSince := time.Now().UTC().Add(-coldStartWindowHours * time.Hour).Format(time.RFC3339)
	gapUntil := state.OldestSeenCreationTS
	errorCount := state.GapAttempts
	e.mu.RUnlock()

	log.Printf("namespace %q: gap-fill (errors: %d/%d, window: %s to %s)",
		namespace, errorCount, maxGapFillAttempts, gapSince, gapUntil)

	buildCnt, testCnt, wasTrunc, oldestTS, err := e.collectNamespace(
		ctx, namespace, gapSince, gapUntil, coldStartMaxItems,
	)

	e.mu.Lock()
	defer e.mu.Unlock()

	// Re-read state under write lock (may have changed between read and write lock)
	updatedState := e.bootstrapStates[namespace]
	if updatedState == nil {
		return // State was removed (unlikely but defensive)
	}

	if err != nil {
		log.Printf("namespace %q: gap-fill failed: %v", namespace, err)
		updatedState.GapAttempts++ // Count failures toward limit
		return
	}

	switch updateGapFillState(updatedState, wasTrunc, oldestTS) {
	case "completed":
		log.Printf("namespace %q: bootstrap COMPLETE (%d builds, %d tests fetched in gap-fill)",
			namespace, buildCnt, testCnt)
	case "progressing":
		log.Printf("namespace %q: gap-fill progressing (%d builds, %d tests, oldest now %s)",
			namespace, buildCnt, testCnt, updatedState.OldestSeenCreationTS)
	case "stalled":
		log.Printf("namespace %q: gap-fill stalled (%d builds, %d tests, oldest unchanged %s, errors %d/%d)",
			namespace, buildCnt, testCnt, oldestTS, updatedState.GapAttempts, maxGapFillAttempts)
	}
}
