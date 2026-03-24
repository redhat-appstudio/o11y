package main

import (
	"context"
	"fmt"
	"log"
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
// metric objects in memory. Data collection (HTTP fetches to KubeArchive) runs
// without holding the lock, since rollingStore has its own internal mutex.
// A write lock is acquired ONLY for the final gauge Reset()+Set() sequence
// (~1-5ms) so that concurrent Collect() calls always observe a consistent snapshot.
func (e *KAExporter) runCollection() {
	start := time.Now()

	// Hard deadline for the entire KubeArchive fetch. Cancels all in-flight
	// HTTP requests if KubeArchive is slow or a namespace hangs.
	ctx, cancel := context.WithTimeout(context.Background(), e.scrapeTimeout)
	defer cancel()

	// Fetch data from KubeArchive WITHOUT holding lock (30-60s operation).
	// RollingStore is thread-safe with its own internal mutex.
	err := e.collectMetrics(ctx)

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
		e.buildSLO.updateGauges(e.rollingStore)
		e.integrationSLO.updateGauges(e.rollingStore)
		e.releaseSLO.updateGauges(e.rollingStore)
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

	// since limits all KubeArchive list queries to the configured look-back window.
	// KubeArchive has no automatic retention — without this filter every scrape would
	// scan 6+ months of history, causing excessive DB load and stale gauge values.
	since := time.Now().UTC().Add(-time.Duration(e.windowHours) * time.Hour).Format(time.RFC3339)
	log.Printf("Collecting metrics from KubeArchive (%d tenant namespace(s), window=%dh, since=%s, concurrency=%d)...",
		len(namespaces), e.windowHours, since, e.maxConcurrent)

	// Parallel Release fetching - maintains global catalog for correlation
	releaseIdx := e.gatherAllReleasesParallel(ctx, namespaces, since, e.maxConcurrent)
	log.Printf("Loaded %d Release CR(s) across tenant namespaces for correlation", len(releaseIdx.store))

	// Parallel namespace collection with streaming PLRs/Snapshots
	type nsResult struct {
		namespace  string
		buildCount int
		testCount  int
		err        error
	}

	results := make(chan nsResult, len(namespaces))
	sem := make(chan struct{}, e.maxConcurrent)
	var wg sync.WaitGroup

	for _, ns := range namespaces {
		wg.Add(1)
		go func(ns string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			b, t, err := e.collectNamespace(ctx, ns, since)
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

	// Record all release observations into 30d SLO store
	e.releaseSLO.recordAllFromIndex(e.rollingStore, e.cluster, releaseIdx)

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

// startRollingStoreMaintenance launches a background goroutine that prunes the
// seen-key deduplication map once per hour, removing entries older than
// seenPLRRetentionHours (72 h = 1.5× KA_WINDOW_HOURS). Without pruning, the
// map would grow until all 48 h-old entries age out naturally, wasting ~1.5 MB.
func (e *KAExporter) startRollingStoreMaintenance(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				e.rollingStore.PruneSeenKeys(seenPLRRetentionHours * time.Hour)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// collectNamespace scrapes PLRs for one tenant namespace and records observations
// into the 30-day rolling store. Resources are streamed page-by-page to bound memory usage.
func (e *KAExporter) collectNamespace(ctx context.Context, tenantNS string, since string) (buildCount, testCount int, err error) {

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
		return buildCount, testCount, fmt.Errorf("stream pipelineruns: %w", streamErr)
	}

	return buildCount, testCount, nil
}
