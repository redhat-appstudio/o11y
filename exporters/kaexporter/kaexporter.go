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

	// To identify Tenant namespaces
	tenantLabelKey   = "konflux-ci.dev/type"
	tenantLabelValue = "tenant"

	// Correlation keys
	labelBuildPipelineRun = "appstudio.openshift.io/build-pipelinerun"
	labelAppStudioApp     = "appstudio.openshift.io/application"
	labelAppStudioComp    = "appstudio.openshift.io/component"
	// Used as an annotation on the build PLR after Snapshot creation; also a label on test PLRs.
	labelOrAnnotationSnapshot = "appstudio.openshift.io/snapshot"

	// Release PipelineRun
	labelAppStudioService          = "appstudio.openshift.io/service"
	valueAppStudioServiceRelease   = "release"
	labelPipelinesType             = "pipelines.appstudio.openshift.io/type"
	valuePipelinesTypeManaged      = "managed"
	labelReleaseApplicationNS      = "release.appstudio.openshift.io/namespace"
	labelTektonPipeline            = "tekton.dev/pipeline"
	managedReleasePLRNamespacesEnv = "MANAGED_RELEASE_PLR_NAMESPACES"

	// kaWindowHoursEnv controls how far back the exporter fetches resources from
	// KubeArchive.
	kaWindowHoursEnv     = "KA_WINDOW_HOURS"
	defaultKAWindowHours = 48

	kaScrapeTimeoutSecsEnv      = "KA_SCRAPE_TIMEOUT_SECONDS"
	defaultScrapeTimeoutSeconds = 120

	// KubeArchive default is 100; maximum allowed is 1000.
	kaPageLimit = 500

	// kaMaxItems is the safety cap on total items fetched per endpoint per scrape.
	kaMaxItems = 1000

	// parallelism for KubeArchive API calls.
	defaultMaxConcurrent = 10
	maxConcurrentEnv     = "KA_MAX_CONCURRENT"
)

// KubeArchive API response structures.
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

// snapshotIndex is an in-memory lookup for Snapshot CRs built page-by-page during streaming.
// Maps hold integer indices into store so they remain valid after slice reallocation.
type snapshotIndex struct {
	store      []Snapshot
	byBuildPLR map[string]int
	byName     map[string]int
}

func newSnapshotIndex() *snapshotIndex {
	return &snapshotIndex{
		byBuildPLR: make(map[string]int),
		byName:     make(map[string]int),
	}
}

// add copies each Snapshot from a page into the index.
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
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace,omitempty"`
		Labels            map[string]string `json:"labels"`
		CreationTimestamp string            `json:"creationTimestamp"`
	} `json:"metadata"`
	Status struct {
		StartTime      string      `json:"startTime"`
		CompletionTime string      `json:"completionTime"`
		Conditions     []Condition `json:"conditions"`
	} `json:"status"`
}

// releaseEntry is a Release CR plus the namespace it was listed
type releaseEntry struct {
	Release
	crNamespace string
}

// releaseIndex is a dual-keyed lookup for build-PLR → Release correlation.
type releaseIndex struct {
	store      []releaseEntry
	byBuildPLR map[string]int 
	bySnapshot map[string]int
}

// newReleaseIndex returns an empty releaseIndex ready to receive releases.
func newReleaseIndex() *releaseIndex {
	return &releaseIndex{
		byBuildPLR: make(map[string]int),
		bySnapshot: make(map[string]int),
	}
}

// addReleases copies releases from a namespace into the index.
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
		// Fallback key: snapshot label — keep the first to avoid overwriting with retries.
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
	applicationNamespace string // tenant NS for release_plr
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

// newSafeOutcomeCounts returns an empty safeOutcomeCounts.
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

	// mu serializes Collect() calls to prevent concurrent scrape races on Reset()/Set()
	mu sync.Mutex

	// windowHours is the look-back window for KubeArchive queries
	windowHours int

	// scrapeTimeout is a hard deadline applied to each Collect() invocation.
	// All in-flight HTTP requests are cancelled when it fires.
	scrapeTimeout time.Duration

	// fixedTenantNamespace, if non-empty, restricts scraping to that namespace only (no K8s list).
	fixedTenantNamespace string
	// k8sClient lists tenant namespaces when fixedTenantNamespace is empty; nil in single-tenant mode.
	k8sClient kubernetes.Interface

	// Duration metrics — Histograms for event durations
	buildDurationHist       *prometheus.HistogramVec // build PLR creation → completion
	integrationDurationHist *prometheus.HistogramVec // integration test PLR creation → completion
	releaseDurationHist     *prometheus.HistogramVec // Release CR creation → completion
	releasePLRTotalHist     *prometheus.HistogramVec // managed release PLR creation → completion
	releasePLRExecHist      *prometheus.HistogramVec // managed release PLR start → completion

	// State metrics — Gauges (point-in-time, no single join key, or counts).
	buildWaitGauge          *prometheus.GaugeVec // build PLR creation → start (Kueue admission wait)
	integrationWaitGauge    *prometheus.GaugeVec // integration test PLR creation → start (Kueue admission wait)
	integrationDelayGauge   *prometheus.GaugeVec // build completion → first test PLR creation (gap)
	releasePLRWaitGauge     *prometheus.GaugeVec // release PLR creation → start (Kueue admission wait)
	archivedCompletionGauge *prometheus.GaugeVec // count of completed resources by outcome

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

		// --- Histograms for event durations ---
		// Buckets are tuned to Konflux build/test/release cadence.
		// Exemplars carry stable join keys (pipelinerun, snapshot, release_cr) for
		// metric → KubeArchive → OTel trace drill-down. Enabled via EnableOpenMetrics=true.

		buildDurationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_build_pipelinerun_duration_seconds",
				Help:    "Distribution of build PipelineRun durations from creation to completion. Exemplars carry pipelinerun and snapshot names as join keys.",
			Buckets: []float64{60, 120, 300, 600, 900, 1200, 1800, 2700, 3600, 5400}, // 1m–90m
			},
			[]string{"cluster", "namespace", "application", "component", "result"},
		),
		integrationDurationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_integration_pipelinerun_duration_seconds",
				Help:    "Distribution of integration test PipelineRun durations from creation to completion. Exemplars carry pipelinerun and snapshot names as join keys.",
			Buckets: []float64{120, 300, 600, 900, 1800, 3600, 5400}, // 2m–90m
			},
			[]string{"cluster", "namespace", "application", "component", "scenario", "result", "optional"},
		),
		releaseDurationHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_release_duration_seconds",
				Help:    "Distribution of Release CR durations from creation to completion, covering the full release pipeline execution. Exemplars carry pipelinerun, snapshot, and release_cr names.",
				Buckets: []float64{300, 600, 1200, 1800, 3600, 5400, 7200, 14400}, // 5m–4h
			},
			[]string{"cluster", "namespace", "application", "component", "release_namespace"},
		),
		releasePLRTotalHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_release_pipelinerun_duration_seconds",
				Help:    "Distribution of managed release PipelineRun total durations from creation to completion. Exemplars carry pipelinerun name.",
				Buckets: []float64{60, 120, 300, 600, 900, 1800, 3600}, // 1m–1h
			},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline", "result"},
		),
		releasePLRExecHist: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_release_pipelinerun_execution_duration_seconds",
				Help:    "Distribution of managed release PipelineRun execution durations from start to completion. Exemplars carry pipelinerun name.",
				Buckets: []float64{60, 120, 300, 600, 900, 1800, 3600}, // 1m–1h
			},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline", "result"},
		),

		// --- Gauges for point-in-time state ---
		// Queue times and gap metrics are not event durations — no single join key for exemplars.

		buildWaitGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_pipelinerun_wait_seconds",
				Help: "Elapsed time in seconds from build PipelineRun creation to execution start.",
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
		integrationWaitGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_integration_pipelinerun_wait_seconds",
				Help: "Elapsed time in seconds from integration test PipelineRun creation to execution start (Kueue admission + Tekton reconciler delay).",
			},
			[]string{"cluster", "namespace", "application", "component", "scenario"},
		),
		integrationDelayGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_build_to_integration_gap_seconds",
				Help: "Elapsed time in seconds from build PipelineRun completion to the first integration test PipelineRun creation.",
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
		releasePLRWaitGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "konflux_release_pipelinerun_wait_seconds",
				Help: "Elapsed time in seconds from managed release PipelineRun creation to execution start.",
			},
			[]string{"cluster", "namespace", "application_namespace", "application", "pipeline"},
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

// Collect implements prometheus.Collector
func (e *KAExporter) Collect(ch chan<- prometheus.Metric) {
	// Serialize concurrent scrapes to prevent data races on gauge Reset()/Set().
	// Second scrape blocks until first completes (correct behavior).
	e.mu.Lock()
	defer e.mu.Unlock()

	start := time.Now()

	// Gauges reset each scrape — stale label sets from completed/deleted resources are removed.
	// Histograms accumulate across scrapes (correct behavior); rate() normalises the counts.
	e.buildWaitGauge.Reset()
	e.integrationWaitGauge.Reset()
	e.integrationDelayGauge.Reset()
	e.releasePLRWaitGauge.Reset()
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

// collectNamespace scrapes PLRs, Snapshots, and linked Releases for one tenant namespace
// and emits duration metrics. Resources are streamed page-by-page to bound memory usage.
func (e *KAExporter) collectNamespace(ctx context.Context, tenantNS string, globalReleases *releaseIndex, since string, outcomeCounts *safeOutcomeCounts) (buildCount, testCount int, err error) {
	// Phase 1: build snapshot index by streaming — pages are freed after indexing.
	snapURL := fmt.Sprintf("%s/apis/appstudio.redhat.com/v1alpha1/namespaces/%s/snapshots", e.kaHost, tenantNS)
	idx := newSnapshotIndex()
	if streamErr := e.streamSnapshots(ctx, snapURL, since, idx.add); streamErr != nil {
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

// pageURL appends pagination parameters and an optional creation-timestamp filter to base.
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


// resolveSnapshotNameForBuild returns the Snapshot name for a build PLR.
// It prefers the index (O(1)) over the PLR annotation, because KubeArchive may archive
// a PLR before the annotation is patched; the Snapshot label is the durable source.
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

// snapshotCRNamespace returns the namespace stored in the Snapshot metadata, or listNS if empty.
func snapshotCRNamespace(s *Snapshot, listNS string) string {
	if s.Metadata.Namespace != "" {
		return s.Metadata.Namespace
	}
	return listNS
}

// snapshotLabel returns the value of key from s.Metadata.Labels, or "" if absent.
func snapshotLabel(s *Snapshot, key string) string {
	if s.Metadata.Labels == nil {
		return ""
	}
	return s.Metadata.Labels[key]
}

// snapshotCompatibleWithPLR reports whether s's application and component labels are
// consistent with the given PLR's application and component (empty labels match anything).
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

// findMatchingRelease returns the Release CR for a build PLR using the pre-built releaseIndex.
// It tries the build-pipelinerun label first (primary), then the snapshot label (fallback).
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

// releaseLabelsCompatible reports whether a Release's app/component labels are consistent
// with the given PLR's app/component (empty Release labels match anything).
func releaseLabelsCompatible(relApp, relComp, plrApp, plrComp string) bool {
	if relApp != "" && relApp != plrApp {
		return false
	}
	if relComp != "" && relComp != plrComp {
		return false
	}
	return true
}

// getLabel returns the value of key from obj's metadata labels, or defaultVal if absent.
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

// getAnnotation returns the value of key from plr.Metadata.Annotations, or "" if absent.
func getAnnotation(plr PipelineRun, key string) string {
	if val, ok := plr.Metadata.Annotations[key]; ok {
		return val
	}
	return ""
}

// getResult returns the Reason of the "Succeeded" condition from a PipelineRun, or "Unknown".
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

// observeWithExemplar records value on obs with exemplar labels, falling back to plain
// Observe if obs does not implement prometheus.ExemplarObserver.
func observeWithExemplar(obs prometheus.Observer, value float64, exemplar prometheus.Labels) {
	if eo, ok := obs.(prometheus.ExemplarObserver); ok {
		eo.ObserveWithExemplar(value, exemplar)
	} else {
		obs.Observe(value)
	}
}

// secondsBetween parses two RFC3339 timestamps and returns the elapsed seconds.
// Returns -1 if either string is empty or unparseable.
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
			EnableOpenMetrics:   true,
			Registry:            reg,
			MaxRequestsInFlight: 1, // Reject concurrent scrapes with HTTP 503
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
