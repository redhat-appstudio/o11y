package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	kaHostEnvVar    = "KA_HOST"
	kaTokenEnvVar   = "KA_TOKEN"
	clusterEnvVar   = "CLUSTER_NAME"
	namespaceEnvVar = "TENANT_NAMESPACE"
	portEnvVar      = "EXPORTER_PORT"
	defaultPort     = "9101"

	// Tenant namespaces carry this label in Konflux (see konflux-release-data / tenant onboarding).
	tenantLabelKey   = "konflux-ci.dev/type"
	tenantLabelValue = "tenant"

	// Correlation keys (integration-service / AppStudio APIs).
	labelBuildPipelineRun = "appstudio.openshift.io/build-pipelinerun"
	labelAppStudioApp     = "appstudio.openshift.io/application"
	labelAppStudioComp    = "appstudio.openshift.io/component"
	// Used as an annotation on the build PLR after Snapshot creation; also a label on test PLRs.
	labelOrAnnotationSnapshot = "appstudio.openshift.io/snapshot"

	// Release PipelineRun (managed namespace): see KONFLUX-12377 / Faizal UX metrics.
	labelAppStudioService          = "appstudio.openshift.io/service"
	valueAppStudioServiceRelease   = "release"
	labelPipelinesType             = "pipelines.appstudio.openshift.io/type"
	valuePipelinesTypeManaged      = "managed"
	labelReleaseApplicationNS      = "release.appstudio.openshift.io/namespace"
	labelTektonPipeline            = "tekton.dev/pipeline"
	managedReleasePLRNamespacesEnv = "MANAGED_RELEASE_PLR_NAMESPACES"

	// kaWindowHoursEnv controls how far back the exporter fetches resources from
	// KubeArchive. KubeArchive has no built-in retention period — all resources
	// since deployment (~6+ months) remain queryable. Without a time filter every
	// scrape scans the full history, causing excessive load and stale gauge values.
	// KubeArchive API: creationTimestampAfter=<RFC3339>
	// Default 48 h covers weekends and off-hours builds without pulling history.
	kaWindowHoursEnv     = "KA_WINDOW_HOURS"
	defaultKAWindowHours = 48

	// kaScrapeTimeoutSecsEnv sets a hard deadline on the entire Collect() call.
	// If KubeArchive is slow or a namespace hangs, this cancels all in-flight HTTP
	// requests and allows Prometheus to record a scrape failure rather than waiting
	// until its own scrape_timeout fires and drops the entire scrape.
	//
	// IMPORTANT: Must be set to slightly less than the Prometheus scrape_timeout
	// configured in the ServiceMonitor. promhttp.HandlerFor does not propagate the
	// HTTP request context into Collect(), so without this internal deadline, goroutines
	// continue running for the full default duration even after Prometheus drops the
	// connection — causing goroutine accumulation under repeated slow scrapes.
	//
	// Current deployment: ServiceMonitor scrapeTimeout=180s → KA_SCRAPE_TIMEOUT_SECONDS=160s
	// Default: 120s (fallback only; the deployment manifest should always set this explicitly).
	kaScrapeTimeoutSecsEnv      = "KA_SCRAPE_TIMEOUT_SECONDS"
	defaultScrapeTimeoutSeconds = 120

	// kaPageLimit is the number of items requested per KubeArchive API page.
	// KubeArchive default is 100; maximum allowed is 1000.
	kaPageLimit = 500

	// kaMaxItems is the safety cap on total items fetched per endpoint per scrape.
	// With a 48 h window and typical build rates this should never be reached.
	// If the warning fires, either reduce the window or investigate component growth.
	kaMaxItems = 1000

	// maxConcurrentFetches controls parallelism for KubeArchive API calls.
	// Set to 10 to balance speed vs API load. Configurable via KA_MAX_CONCURRENT env var.
	defaultMaxConcurrent = 10
	maxConcurrentEnv     = "KA_MAX_CONCURRENT"
)

// KubeArchive API response structures.
// Metadata.Continue is the Kubernetes list pagination token; empty means last page.
type ListResponse struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []PipelineRun `json:"items"`
}

type ReleaseListResponse struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []Release `json:"items"`
}

type SnapshotListResponse struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []Snapshot `json:"items"`
}

// snapshotIndex is an in-memory lookup built page-by-page while streaming Snapshots.
// It replaces the []Snapshot linear scan with O(1) map lookups for the two primary
// resolution paths, retaining a compact all-slice only for the rare component fallback.
//
// Snapshot values are copied into store so the original page slice can be GC'd promptly.
// Maps hold integer indices into store — safe across store reallocation.
type snapshotIndex struct {
	store      []Snapshot        // owns all Snapshot copies; backed slice, index-stable
	byBuildPLR map[string]int    // label[build-pipelinerun] → store index (case 1)
	byName     map[string]int    // snapshot name → store index (case 2 annotation fallback)
}

// newSnapshotIndex returns an empty snapshotIndex ready to accept pages.
func newSnapshotIndex() *snapshotIndex {
	return &snapshotIndex{
		byBuildPLR: make(map[string]int),
		byName:     make(map[string]int),
	}
}

// add copies each Snapshot from a page into the index. It is the fn argument for streamSnapshots.
// The page slice may be freed after add returns.
func (idx *snapshotIndex) add(page []Snapshot) {
	for _, s := range page {
		i := len(idx.store)
		idx.store = append(idx.store, s) // copy — page slot is no longer referenced
		idx.byName[idx.store[i].Metadata.Name] = i
		if bplr := snapshotLabel(&idx.store[i], labelBuildPipelineRun); bplr != "" {
			idx.byBuildPLR[bplr] = i
		}
	}
}

// Snapshot is a subset of appstudio.redhat.com Snapshot CR JSON from KubeArchive.
type Snapshot struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace,omitempty"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Application string `json:"application"`
		Components  []struct {
			Name string `json:"name"`
		} `json:"components"`
	} `json:"spec"`
}

type PipelineRun struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		StartTime      string      `json:"startTime"`
		CompletionTime string      `json:"completionTime"`
		Conditions     []Condition `json:"conditions"`
	} `json:"status"`
}

type Release struct {
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace,omitempty"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		StartTime      string      `json:"startTime"`
		CompletionTime string      `json:"completionTime"`
		Conditions     []Condition `json:"conditions"`
	} `json:"status"`
}

// releaseEntry is a Release CR plus the namespace it was listed from (for cross-tenant joins).
type releaseEntry struct {
	Release
	crNamespace string
}

// releaseIndex is a dual-keyed lookup for O(1) build-PLR → Release correlation.
//
// The backing store owns all releaseEntry values; maps hold integer indices so
// they remain valid across store reallocation (same pattern as snapshotIndex).
//
// Two index keys cover the two cases in findMatchingRelease:
//   - byBuildPLR: label[appstudio.openshift.io/build-pipelinerun] → index (primary)
//   - bySnapshot: label[release.appstudio.openshift.io/snapshot]  → index (fallback)
//
// When multiple releases share a key (e.g. retries), the most recently added wins
// for byBuildPLR (overwrite) and the first wins for bySnapshot (skip duplicates),
// which matches the old linear-scan semantics.
type releaseIndex struct {
	store      []releaseEntry
	byBuildPLR map[string]int // label[build-pipelinerun] → store index
	bySnapshot map[string]int // label[snapshot]          → store index
}

func newReleaseIndex() *releaseIndex {
	return &releaseIndex{
		byBuildPLR: make(map[string]int),
		bySnapshot: make(map[string]int),
	}
}

// addReleases copies releases from a namespace into the index.
// Safe to call from multiple goroutines only if each call is for a distinct batch;
// callers in gatherAllReleasesParallel hold a mutex around this.
func (idx *releaseIndex) addReleases(ns string, releases []Release) {
	for _, r := range releases {
		i := len(idx.store)
		crNS := r.Metadata.Namespace
		if crNS == "" {
			crNS = ns
		}
		idx.store = append(idx.store, releaseEntry{Release: r, crNamespace: crNS})

		// Primary key: build-pipelinerun label — overwrite so the latest release wins.
		if bplr := getLabel(r, labelBuildPipelineRun, ""); bplr != "" {
			idx.byBuildPLR[bplr] = i
		}
		// Fallback key: snapshot label — keep the first (avoid overwriting with retries).
		if snap := getLabel(r, "release.appstudio.openshift.io/snapshot", ""); snap != "" {
			if _, exists := idx.bySnapshot[snap]; !exists {
				idx.bySnapshot[snap] = i
			}
		}
	}
}

type Condition struct {
	Type   string `json:"type"`
	Status string `json:"status,omitempty"`
	Reason string `json:"reason"`
}

// archivedOutcomeKey groups counts for konflux_archived_completion_count (Gauge).
type archivedOutcomeKey struct {
	namespace            string // tenant NS for build/test/release_cr; managed NS for release_plr
	applicationNamespace string // tenant NS for release_plr (from label); empty otherwise
	phase                string // build | test | release_cr | release_plr
	application          string
	component            string
	result               string
}

// safeOutcomeCounts wraps the outcome counts map with a mutex for thread-safe concurrent updates.
type safeOutcomeCounts struct {
	sync.Mutex
	counts map[archivedOutcomeKey]float64
}

func newSafeOutcomeCounts() *safeOutcomeCounts {
	return &safeOutcomeCounts{
		counts: make(map[archivedOutcomeKey]float64),
	}
}

func (s *safeOutcomeCounts) increment(key archivedOutcomeKey) {
	s.Lock()
	s.counts[key]++
	s.Unlock()
}

func (s *safeOutcomeCounts) getAll() map[archivedOutcomeKey]float64 {
	s.Lock()
	defer s.Unlock()
	result := make(map[archivedOutcomeKey]float64, len(s.counts))
	for k, v := range s.counts {
		result[k] = v
	}
	return result
}

// KAExporter collects metrics from KubeArchive
type KAExporter struct {
	kaHost     string
	kaToken    string
	cluster    string
	httpClient *http.Client

	// windowHours is the look-back window for KubeArchive queries (creationTimestampAfter).
	// Computed once from KA_WINDOW_HOURS at startup; applied per scrape.
	windowHours int

	// scrapeTimeout is a hard deadline applied to each Collect() invocation.
	// All in-flight HTTP requests are cancelled when it fires.
	scrapeTimeout time.Duration

	// fixedTenantNamespace, if non-empty, restricts scraping to that namespace only (no K8s list).
	fixedTenantNamespace string
	// k8sClient lists tenant namespaces when fixedTenantNamespace is empty; nil in single-tenant mode.
	k8sClient kubernetes.Interface

	// Metrics — all duration gauges are in seconds.
	buildDurationGauge       *prometheus.GaugeVec
	queueWaitGauge           *prometheus.GaugeVec
	integrationDurationGauge *prometheus.GaugeVec
	integrationDelayGauge    *prometheus.GaugeVec
	releaseDurationGauge     *prometheus.GaugeVec
	releasePLRTotalGauge     *prometheus.GaugeVec
	releasePLRQueueGauge     *prometheus.GaugeVec
	releasePLRExecGauge      *prometheus.GaugeVec
	archivedCompletionGauge  *prometheus.GaugeVec

	// Exporter self-monitoring metrics.
	scrapeErrorsTotal    *prometheus.CounterVec // labels: phase
	lastScrapeSuccessGauge prometheus.Gauge     // unix timestamp of last successful full scrape
	scrapeDurationGauge  prometheus.Gauge       // last scrape wall-clock duration in seconds
	truncationsTotal     *prometheus.CounterVec // labels: resource (pipelineruns|snapshots|releases), namespace
}

// NewKAExporter creates a new KubeArchive exporter
func NewKAExporter() (*KAExporter, error) {
	kaHost := os.Getenv(kaHostEnvVar)
	if kaHost == "" {
		return nil, fmt.Errorf("missing required environment variable: %s", kaHostEnvVar)
	}

	kaToken := os.Getenv(kaTokenEnvVar)
	if kaToken == "" {
		return nil, fmt.Errorf("missing required environment variable: %s", kaTokenEnvVar)
	}

	cluster := os.Getenv(clusterEnvVar)
	if cluster == "" {
		cluster = "unknown"
	}

	windowHours := defaultKAWindowHours
	if wh := strings.TrimSpace(os.Getenv(kaWindowHoursEnv)); wh != "" {
		if parsed, err := strconv.Atoi(wh); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %dh", kaWindowHoursEnv, wh, defaultKAWindowHours)
		} else {
			windowHours = parsed
		}
	}

	scrapeTimeoutSecs := defaultScrapeTimeoutSeconds
	if st := strings.TrimSpace(os.Getenv(kaScrapeTimeoutSecsEnv)); st != "" {
		if parsed, err := strconv.Atoi(st); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %ds", kaScrapeTimeoutSecsEnv, st, defaultScrapeTimeoutSeconds)
		} else {
			scrapeTimeoutSecs = parsed
		}
	}

	fixedNS := strings.TrimSpace(os.Getenv(namespaceEnvVar))

	var k8sClient kubernetes.Interface
	if fixedNS == "" {
		cfg, err := kubeRESTConfig()
		if err != nil {
			return nil, fmt.Errorf("multi-tenant mode (unset %s): kubernetes client config: %w", namespaceEnvVar, err)
		}
		k8sClient, err = kubernetes.NewForConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("multi-tenant mode: kubernetes.NewForConfig: %w", err)
		}
	}

	// HTTP client with TLS verification disabled (for self-signed certs)
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	return &KAExporter{
		kaHost:               kaHost,
		kaToken:              kaToken,
		cluster:              cluster,
		windowHours:          windowHours,
		scrapeTimeout:        time.Duration(scrapeTimeoutSecs) * time.Second,
		fixedTenantNamespace: fixedNS,
		k8sClient:            k8sClient,
		httpClient:           httpClient,

		buildDurationGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_pipelinerun_duration_seconds",
				Help: "Elapsed time in seconds from build PipelineRun creation to completion.",
			},
			[]string{"cluster", "namespace", "application", "component", "result"},
		),
		queueWaitGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_pipelinerun_queue_seconds",
				Help: "Elapsed time in seconds from build PipelineRun creation to execution start.",
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
		integrationDurationGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_integration_pipelinerun_duration_seconds",
				Help: "Elapsed time in seconds from integration test PipelineRun creation to completion.",
			},
			[]string{"cluster", "namespace", "application", "component", "scenario", "result", "optional"},
		),
		integrationDelayGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_to_integration_gap_seconds",
				Help: "Elapsed time in seconds from build PipelineRun completion to the first integration test PipelineRun creation.",
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
		releaseDurationGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_duration_seconds",
				Help: "Elapsed time in seconds from Release CR creation to completion, covering the full release pipeline execution.",
			},
			[]string{"cluster", "namespace", "application", "component", "release_namespace"},
		),
		releasePLRTotalGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_pipelinerun_duration_seconds",
				Help: "Elapsed time in seconds from managed release PipelineRun creation to completion.",
			},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline", "result"},
		),
		releasePLRQueueGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_pipelinerun_queue_seconds",
				Help: "Elapsed time in seconds from managed release PipelineRun creation to execution start.",
			},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline"},
		),
		releasePLRExecGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_pipelinerun_execution_duration_seconds",
				Help: "Elapsed time in seconds from managed release PipelineRun execution start to completion.",
			},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline", "result"},
		),
		archivedCompletionGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_archived_completion_count",
				Help: "Number of completed Konflux pipeline and release resources observed in the current scrape, by phase and outcome.",
			},
			[]string{"cluster", "namespace", "application_namespace", "phase", "application", "component", "result"},
		),

		scrapeErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "konflux_ka_exporter_scrape_errors_total",
				Help: "Total number of errors encountered during scrape, by phase (releases|namespaces|managed_plrs).",
			},
			[]string{"cluster", "phase"},
		),
		lastScrapeSuccessGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "konflux_ka_exporter_last_scrape_success_timestamp_seconds",
			Help: "Unix timestamp of the last fully successful scrape (all namespace errors count as partial failure).",
		}),
		scrapeDurationGauge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "konflux_ka_exporter_scrape_duration_seconds",
			Help: "Wall-clock time in seconds of the last Collect() invocation.",
		}),
		truncationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "konflux_ka_exporter_truncations_total",
				Help: "Total number of times a paginated KubeArchive fetch was capped at kaMaxItems, indicating potentially incomplete data.",
			},
			[]string{"cluster", "resource", "namespace"},
		),
	}, nil
}

// Describe implements prometheus.Collector
func (e *KAExporter) Describe(ch chan<- *prometheus.Desc) {
	e.buildDurationGauge.Describe(ch)
	e.queueWaitGauge.Describe(ch)
	e.integrationDurationGauge.Describe(ch)
	e.integrationDelayGauge.Describe(ch)
	e.releaseDurationGauge.Describe(ch)
	e.releasePLRTotalGauge.Describe(ch)
	e.releasePLRQueueGauge.Describe(ch)
	e.releasePLRExecGauge.Describe(ch)
	e.archivedCompletionGauge.Describe(ch)
	e.scrapeErrorsTotal.Describe(ch)
	e.lastScrapeSuccessGauge.Describe(ch)
	e.scrapeDurationGauge.Describe(ch)
	e.truncationsTotal.Describe(ch)
}

// Collect implements prometheus.Collector
func (e *KAExporter) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()

	e.buildDurationGauge.Reset()
	e.queueWaitGauge.Reset()
	e.integrationDurationGauge.Reset()
	e.integrationDelayGauge.Reset()
	e.releaseDurationGauge.Reset()
	e.releasePLRTotalGauge.Reset()
	e.releasePLRQueueGauge.Reset()
	e.releasePLRExecGauge.Reset()
	e.archivedCompletionGauge.Reset()

	// Hard deadline for the entire scrape. Cancels all in-flight KubeArchive HTTP
	// requests if KubeArchive is slow or a namespace hangs, preventing unbounded
	// goroutine accumulation when Prometheus's own scrape_timeout fires.
	ctx, cancel := context.WithTimeout(context.Background(), e.scrapeTimeout)
	defer cancel()

	if err := e.collectMetrics(ctx); err != nil {
		log.Printf("Error collecting metrics: %v", err)
		e.scrapeErrorsTotal.WithLabelValues(e.cluster, "collect").Inc()
	} else {
		e.lastScrapeSuccessGauge.Set(float64(time.Now().Unix()))
	}
	e.scrapeDurationGauge.Set(time.Since(start).Seconds())

	e.buildDurationGauge.Collect(ch)
	e.queueWaitGauge.Collect(ch)
	e.integrationDurationGauge.Collect(ch)
	e.integrationDelayGauge.Collect(ch)
	e.releaseDurationGauge.Collect(ch)
	e.releasePLRTotalGauge.Collect(ch)
	e.releasePLRQueueGauge.Collect(ch)
	e.releasePLRExecGauge.Collect(ch)
	e.archivedCompletionGauge.Collect(ch)
	e.scrapeErrorsTotal.Collect(ch)
	e.lastScrapeSuccessGauge.Collect(ch)
	e.scrapeDurationGauge.Collect(ch)
	e.truncationsTotal.Collect(ch)
}

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

// tenantNamespaces returns either a single fixed namespace or all namespaces labeled as
// Konflux tenants. The Kubernetes list is paginated with kaPageLimit to handle clusters
// with more than 500 tenant namespaces.
func (e *KAExporter) tenantNamespaces(ctx context.Context) ([]string, error) {
	if e.fixedTenantNamespace != "" {
		return []string{e.fixedTenantNamespace}, nil
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

// gatherAllReleasesParallel fetches Release CRs from every tenant namespace in parallel and
// returns a *releaseIndex for O(1) build-PLR → Release correlation.
//
// The global Release catalog must be complete before namespace processing begins because
// releases can arrive weeks after builds and may live in dedicated release namespaces
// (cross-namespace correlation). Parallel fetching keeps this phase from dominating scrape time.
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
			items, err := e.fetchReleases(ctx, releasesURL, since)
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
			continue
		}
		mu.Lock()
		idx.addReleases(result.namespace, result.releases)
		mu.Unlock()
	}
	return idx
}

// collectNamespace scrapes one tenant namespace with bounded, streaming memory usage.
//
// Memory model:
//   - Snapshots: streamed once → compacted into snapshotIndex. Pages freed after indexing.
//   - PLRs: streamed page-by-page. Each page is processed and discarded.
//     Test PLRs are copied into testBySnapshot only while streaming; after the stream a
//     cleanup pass removes entries with no matching build PLR (de-duplicated older runs).
//   - buildInfoBySnapshot holds only string triples — negligible.
//
// Gauge overwrite fix:
//   KubeArchive returns items newest-first (by creationTimestamp). seenBuilds tracks
//   the first (= most recent) build PLR encountered per (app, component, result) label
//   set. Subsequent PLRs for the same label set are counted in outcomeCounts but do not
//   overwrite the gauge — ensuring the gauge always reflects the latest build.
//
// Peak memory per namespace:
//   (snapshotIndex) + (testBySnapshot after cleanup) + (buildInfoBySnapshot) + (seenBuilds) + (1 page PLRs)
//   testBySnapshot is bounded by window × build rate × component count (one entry per most-recent build).
//   Validate this assumption for high-velocity tenants; the cleanup pass enforces the bound post-stream.
func (e *KAExporter) collectNamespace(ctx context.Context, tenantNS string, globalReleases *releaseIndex, since string, outcomeCounts *safeOutcomeCounts) (buildCount, testCount int, err error) {
	// Phase 1: build snapshot index by streaming — pages are freed after indexing.
	snapURL := fmt.Sprintf("%s/apis/appstudio.redhat.com/v1alpha1/namespaces/%s/snapshots", e.kaHost, tenantNS)
	idx := newSnapshotIndex()
	if streamErr := e.streamSnapshots(ctx, snapURL, since, idx.add); streamErr != nil {
		log.Printf("namespace %q: could not index snapshots (annotation fallback only): %v", tenantNS, streamErr)
		idx = nil // nil idx → resolveSnapshotNameForBuild falls back to annotation
	}

	// Phase 2: stream PLRs one page at a time.
	//   - Build PLRs: emit gauge for the FIRST (newest) occurrence per label set only.
	//   - Test PLRs:  copied into testBySnapshot (small, bounded by window × build rate).
	//   - buildInfoBySnapshot holds only string triples needed for the integration gap.
	type buildMeta struct{ application, component, completedAt string }
	// buildKey identifies the Prometheus label set for a build duration gauge.
	// seenBuilds is keyed on (app, component, result); since KubeArchive returns
	// newest-first, the first hit per key is the most recent completed build.
	type buildKey struct{ app, component, result string }
	seenBuilds := make(map[buildKey]bool)
	testBySnapshot := make(map[string][]PipelineRun)
	buildInfoBySnapshot := make(map[string]buildMeta)

	plrURL := fmt.Sprintf("%s/apis/tekton.dev/v1/namespaces/%s/pipelineruns", e.kaHost, tenantNS)
	streamErr := e.streamPLRs(ctx, plrURL, since, func(page []PipelineRun) {
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
				bk := buildKey{app, comp, result}
				if !seenBuilds[bk] {
					// First (newest) occurrence for this label set — emit gauges.
					seenBuilds[bk] = true
					snap := resolveSnapshotNameForBuild(tenantNS, *plr, idx)
					e.emitBuildPLRMetrics(tenantNS, *plr, app, comp, snap, globalReleases)
					if snap != "" {
						buildInfoBySnapshot[snap] = buildMeta{app, comp, plr.Status.CompletionTime}
					}
				}
				// Older duplicates: counted in outcomeCounts above but not in gauges.
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

// emitBuildPLRMetrics emits all metrics for a single completed build PLR.
// Called immediately during PLR page streaming — no slice retention.
func (e *KAExporter) emitBuildPLRMetrics(tenantNS string, plr PipelineRun, application, component, snapshot string, globalReleases *releaseIndex) {
	createdAt := plr.Metadata.CreationTimestamp
	startedAt := plr.Status.StartTime
	completedAt := plr.Status.CompletionTime
	result := getResult(plr)

	if buildDur := secondsBetween(createdAt, completedAt); buildDur >= 0 {
		e.buildDurationGauge.WithLabelValues(e.cluster, tenantNS, application, component, result).Set(buildDur)
	}
	if startDelay := secondsBetween(createdAt, startedAt); startDelay >= 0 {
		e.queueWaitGauge.WithLabelValues(e.cluster, tenantNS, application, component).Set(startDelay)
	}
	if matched := findMatchingRelease(plr, snapshot, application, component, globalReleases); matched != nil {
		relNS := matched.crNamespace
		if relDur := secondsBetween(matched.Status.StartTime, matched.Status.CompletionTime); relDur >= 0 {
			e.releaseDurationGauge.WithLabelValues(e.cluster, tenantNS, application, component, relNS).Set(relDur)
		}
	}
}

func addArchivedOutcome(tenantNS, phase string, plr PipelineRun, outcomeCounts *safeOutcomeCounts) {
	app := getLabel(plr, labelAppStudioApp, "unknown")
	comp := getLabel(plr, labelAppStudioComp, "unknown")
	res := getResult(plr)
	k := archivedOutcomeKey{
		namespace: tenantNS, applicationNamespace: "", phase: phase, application: app, component: comp, result: res,
	}
	outcomeCounts.increment(k)
}

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

// parseManagedReleasePLRNamespaces parses MANAGED_RELEASE_PLR_NAMESPACES
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

func (e *KAExporter) collectManagedReleasePLRs(ctx context.Context, since string, outcomeCounts *safeOutcomeCounts) {
	managedNS := parseManagedReleasePLRNamespaces()
	if len(managedNS) == 0 {
		log.Printf("%s not set or empty; skipping managed release PipelineRun metrics", managedReleasePLRNamespacesEnv)
		return
	}
	log.Printf("KubeArchive: scraping %d managed namespace(s) for release PipelineRuns: %v", len(managedNS), managedNS)
	for _, ns := range managedNS {
		plrURL := fmt.Sprintf("%s/apis/tekton.dev/v1/namespaces/%s/pipelineruns", e.kaHost, ns)
		if err := e.streamPLRs(ctx, plrURL, since, func(page []PipelineRun) {
			for i := range page {
				plr := &page[i]
				if isReleaseServicePLR(*plr) {
					e.processReleasePipelineRun(ns, *plr, outcomeCounts)
				}
			}
		}); err != nil {
			log.Printf("managed namespace %q: stream pipelineruns: %v", ns, err)
		}
	}
}

// isReleaseServicePLR reports whether plr is a completed managed release-service PipelineRun.
func isReleaseServicePLR(plr PipelineRun) bool {
	return getLabel(plr, labelAppStudioService, "") == valueAppStudioServiceRelease &&
		getLabel(plr, labelPipelinesType, "") == valuePipelinesTypeManaged &&
		plr.Status.CompletionTime != ""
}

func (e *KAExporter) processReleasePipelineRun(managedNS string, plr PipelineRun, outcomeCounts *safeOutcomeCounts) {
	app := getLabel(plr, labelAppStudioApp, "unknown")
	appTenantNS := getLabel(plr, labelReleaseApplicationNS, "unknown")
	pipeline := getLabel(plr, labelTektonPipeline, "unknown")
	result := getResult(plr)

	created := plr.Metadata.CreationTimestamp
	started := plr.Status.StartTime
	completed := plr.Status.CompletionTime

	if total := secondsBetween(created, completed); total >= 0 {
		e.releasePLRTotalGauge.WithLabelValues(e.cluster, managedNS, appTenantNS, app, pipeline, result).Set(total)
	}
	if q := secondsBetween(created, started); q >= 0 {
		e.releasePLRQueueGauge.WithLabelValues(e.cluster, managedNS, appTenantNS, app, pipeline).Set(q)
	}
	if exec := secondsBetween(started, completed); exec >= 0 {
		e.releasePLRExecGauge.WithLabelValues(e.cluster, managedNS, appTenantNS, app, pipeline, result).Set(exec)
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

// kubeRESTConfig prefers in-cluster config, then default kubeconfig (local dev).
func kubeRESTConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	return kubeConfig.ClientConfig()
}


// processIntegrationTests processes integration test PLRs
func (e *KAExporter) processIntegrationTests(tenantNS string, tests []PipelineRun, application, component, buildCompletedAt string) {
	// firstTestCreatedTime tracks the earliest integration test start for the gap metric.
	// Use time.Time comparison rather than raw string comparison: RFC3339 strings are
	// lexicographically ordered only when all timestamps are in UTC ("Z" suffix).
	// Mixing "+HH:MM" offsets (technically valid RFC3339) with "Z" would silently
	// produce wrong results with a string min. time.Time comparison is always correct.
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
			e.integrationDurationGauge.WithLabelValues(
				e.cluster, tenantNS, application, component, scenario, testResult, optional,
			).Set(testDuration)
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

// pageURL appends pagination parameters and an optional creation-timestamp filter to base.
// since is an RFC3339 timestamp; when non-empty it adds creationTimestampAfter=<since>
// which limits results to resources created after that point in time.
// KubeArchive returns items newest-first (by creationTimestamp), so page 1 holds the
// most recent items — the seen-map in the caller relies on this ordering guarantee.
func pageURL(base, continueToken, since string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(kaPageLimit))
	if continueToken != "" {
		q.Set("continue", continueToken)
	}
	if since != "" {
		q.Set("creationTimestampAfter", since)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// fetchPage executes a single GET request and closes the response body before returning.
// ctx is honoured for cancellation — if the scrape deadline fires all in-flight calls
// return immediately with context.DeadlineExceeded rather than blocking until the
// per-request HTTP client timeout (30s) expires.
func (e *KAExporter) fetchPage(ctx context.Context, pageURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+e.kaToken)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// streamPLRs fetches PipelineRuns page-by-page, calling fn once per page.
// since is an RFC3339 timestamp passed as creationTimestampAfter to KubeArchive,
// limiting results to the configured look-back window (KA_WINDOW_HOURS).
// KubeArchive returns items newest-first; callers may rely on this ordering.
// fn receives the page slice and must not retain references beyond its return.
func (e *KAExporter) streamPLRs(ctx context.Context, baseURL, since string, fn func(page []PipelineRun)) error {
	continueToken := ""
	total := 0
	for {
		u, err := pageURL(baseURL, continueToken, since)
		if err != nil {
			return err
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("API returned status %d: %s", status, string(body))
		}
		var page ListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		fn(page.Items)
		total += len(page.Items)
		if total >= kaMaxItems {
			log.Printf("WARNING: streamPLRs %s: reached kaMaxItems cap (%d); "+
				"check KubeArchive retention — results may be incomplete", baseURL, kaMaxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "pipelineruns", baseURL).Inc()
			break
		}
		if page.Metadata.Continue == "" {
			break
		}
		continueToken = page.Metadata.Continue
		log.Printf("streamPLRs %s: processed %d items, continuing (token len=%d)",
			baseURL, total, len(continueToken))
	}
	return nil
}

// streamSnapshots fetches Snapshot CRs page-by-page, calling fn once per page.
// since limits results to the configured look-back window (KA_WINDOW_HOURS).
// Intended to be called with snapshotIndex.add so pages are indexed and then freed.
func (e *KAExporter) streamSnapshots(ctx context.Context, baseURL, since string, fn func(page []Snapshot)) error {
	continueToken := ""
	total := 0
	for {
		u, err := pageURL(baseURL, continueToken, since)
		if err != nil {
			return err
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("API returned status %d: %s", status, string(body))
		}
		var page SnapshotListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		fn(page.Items)
		total += len(page.Items)
		if total >= kaMaxItems {
			log.Printf("WARNING: streamSnapshots %s: reached kaMaxItems cap (%d); "+
				"check KubeArchive retention — results may be incomplete", baseURL, kaMaxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "snapshots", baseURL).Inc()
			break
		}
		if page.Metadata.Continue == "" {
			break
		}
		continueToken = page.Metadata.Continue
	}
	return nil
}

// fetchReleases fetches all Release CRs from a KubeArchive endpoint with pagination.
// since limits results to the configured look-back window (KA_WINDOW_HOURS).
func (e *KAExporter) fetchReleases(ctx context.Context, baseURL, since string) ([]Release, error) {
	var all []Release
	continueToken := ""
	for {
		u, err := pageURL(baseURL, continueToken, since)
		if err != nil {
			return nil, err
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("API returned status %d: %s", status, string(body))
		}
		var page ReleaseListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if len(all) >= kaMaxItems {
			log.Printf("WARNING: fetchReleases %s: reached kaMaxItems cap (%d); "+
				"check KubeArchive retention — results may be incomplete", baseURL, kaMaxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "releases", baseURL).Inc()
			break
		}
		if page.Metadata.Continue == "" {
			break
		}
		continueToken = page.Metadata.Continue
		log.Printf("fetchReleases %s: fetched %d items, continuing (token len=%d)",
			baseURL, len(all), len(continueToken))
	}
	return all, nil
}


// resolveSnapshotNameForBuild derives the Snapshot name for a build PLR.
//
// Integration-service creates the Snapshot first, sets label build-pipelinerun=<PLR name>,
// then patches the build PLR annotation. KubeArchive may archive the PLR before the annotation
// exists; the Snapshot CR is the durable source of truth.
//
// Resolution order (all O(1) except the rare component-fallback scan):
//  1. O(1) index: Snapshot whose label[build-pipelinerun] == plrName.
//  2. O(1) index: PLR annotation snapshot name, validated against the index.
//  3. O(n) fallback: unique Snapshot in the namespace whose spec.components contains plrComp.
func resolveSnapshotNameForBuild(tenantNS string, plr PipelineRun, idx *snapshotIndex) string {
	if idx == nil {
		return getAnnotation(plr, labelOrAnnotationSnapshot)
	}

	plrName := plr.Metadata.Name
	plrApp := getLabel(plr, labelAppStudioApp, "")
	plrComp := getLabel(plr, labelAppStudioComp, "")

	// Case 1: O(1) — build-pipelinerun label on the Snapshot.
	if i, ok := idx.byBuildPLR[plrName]; ok {
		s := &idx.store[i]
		if snapshotCRNamespace(s, tenantNS) == tenantNS && snapshotCompatibleWithPLR(s, plrApp, plrComp) {
			return s.Metadata.Name
		}
	}

	// Case 2: O(1) — annotation on the build PLR, validated against the index.
	if ann := getAnnotation(plr, labelOrAnnotationSnapshot); ann != "" {
		if i, ok := idx.byName[ann]; ok {
			s := &idx.store[i]
			if snapshotCRNamespace(s, tenantNS) == tenantNS {
				bp := snapshotLabel(s, labelBuildPipelineRun)
				if (bp == "" || bp == plrName) && snapshotCompatibleWithPLR(s, plrApp, plrComp) {
					return ann
				}
			}
		}
		// Annotation exists but snapshot not in index or failed validation — still trust the annotation.
		return ann
	}

	// Case 3: O(n) — component-based fallback, rare (group/heterogeneous snapshots).
	if plrComp != "" {
		var names []string
		for i := range idx.store {
			s := &idx.store[i]
			if snapshotCRNamespace(s, tenantNS) != tenantNS {
				continue
			}
			if s.Spec.Application != "" && s.Spec.Application != plrApp {
				continue
			}
			for _, c := range s.Spec.Components {
				if c.Name == plrComp {
					names = append(names, s.Metadata.Name)
					break
				}
			}
		}
		if len(names) == 1 {
			return names[0]
		}
	}

	return ""
}

func snapshotCRNamespace(s *Snapshot, listNS string) string {
	if s.Metadata.Namespace != "" {
		return s.Metadata.Namespace
	}
	return listNS
}

func snapshotLabel(s *Snapshot, key string) string {
	if s.Metadata.Labels == nil {
		return ""
	}
	return s.Metadata.Labels[key]
}

func snapshotCompatibleWithPLR(s *Snapshot, plrApp, plrComp string) bool {
	sa := snapshotLabel(s, labelAppStudioApp)
	sc := snapshotLabel(s, labelAppStudioComp)
	if sa != "" && sa != plrApp {
		return false
	}
	if sc != "" && sc != plrComp {
		return false
	}
	if s.Spec.Application != "" && s.Spec.Application != plrApp {
		return false
	}
	return true
}

// Helper functions


// findMatchingRelease joins a build PLR to a Release CR using the pre-built releaseIndex.
// Both lookups are O(1); the old O(n) linear scan over []releaseEntry is replaced.
//
// Lookup order matches the previous semantics:
//  1. label[build-pipelinerun] == plrName (primary, unambiguous)
//  2. label[snapshot] == resolved snapshot name (fallback for manual/heterogeneous releases)
func findMatchingRelease(plr PipelineRun, snapshot, application, component string, idx *releaseIndex) *releaseEntry {
	if idx == nil {
		return nil
	}
	plrName := plr.Metadata.Name

	// O(1) primary: build-pipelinerun label.
	if i, ok := idx.byBuildPLR[plrName]; ok {
		re := &idx.store[i]
		rel := &re.Release
		if releaseLabelsCompatible(getLabel(*rel, labelAppStudioApp, ""), getLabel(*rel, labelAppStudioComp, ""), application, component) {
			return re
		}
	}

	// O(1) fallback: snapshot label.
	if snapshot != "" {
		if i, ok := idx.bySnapshot[snapshot]; ok {
			re := &idx.store[i]
			rel := &re.Release
			if releaseLabelsCompatible(getLabel(*rel, labelAppStudioApp, ""), getLabel(*rel, labelAppStudioComp, ""), application, component) {
				return re
			}
		}
	}

	return nil
}

func releaseLabelsCompatible(relApp, relComp, plrApp, plrComp string) bool {
	if relApp != "" && relApp != plrApp {
		return false
	}
	if relComp != "" && relComp != plrComp {
		return false
	}
	return true
}

func getLabel(obj interface{}, key, defaultVal string) string {
	switch v := obj.(type) {
	case PipelineRun:
		if val, ok := v.Metadata.Labels[key]; ok {
			return val
		}
	case Release:
		if val, ok := v.Metadata.Labels[key]; ok {
			return val
		}
	}
	return defaultVal
}

func getAnnotation(plr PipelineRun, key string) string {
	if val, ok := plr.Metadata.Annotations[key]; ok {
		return val
	}
	return ""
}

func getResult(plr PipelineRun) string {
	for _, cond := range plr.Status.Conditions {
		if cond.Type == "Succeeded" {
			return cond.Reason
		}
	}
	return "Unknown"
}

// getReleaseResult returns the Released condition reason, or a normalized outcome from status.
func getReleaseResult(r Release) string {
	for _, cond := range r.Status.Conditions {
		if cond.Type != "Released" {
			continue
		}
		switch strings.ToLower(cond.Status) {
		case "true":
			if cond.Reason != "" {
				return cond.Reason
			}
			return "Succeeded"
		case "false":
			if cond.Reason != "" {
				return cond.Reason
			}
			return "Failed"
		}
	}
	return "Unknown"
}

func secondsBetween(start, end string) float64 {
	if start == "" || end == "" {
		return -1
	}

	startTime, err1 := time.Parse(time.RFC3339, start)
	endTime, err2 := time.Parse(time.RFC3339, end)

	if err1 != nil || err2 != nil {
		return -1
	}

	return endTime.Sub(startTime).Seconds()
}

func main() {
	exporter, err := NewKAExporter()
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(exporter)

	http.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
			Registry:          reg,
		},
	))

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	port := os.Getenv(portEnvVar)
	if port == "" {
		port = defaultPort
	}

	log.Printf("KubeArchive Prometheus Exporter started on http://0.0.0.0:%s/metrics", port)
	if exporter.fixedTenantNamespace != "" {
		log.Printf("Cluster: %s, mode: single-tenant, TENANT_NAMESPACE=%s", exporter.cluster, exporter.fixedTenantNamespace)
	} else {
		log.Printf("Cluster: %s, mode: multi-tenant (namespaces with label %s=%s)", exporter.cluster, tenantLabelKey, tenantLabelValue)
	}

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
