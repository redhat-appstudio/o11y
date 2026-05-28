package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ── Environment variable names ────────────────────────────────────────────────

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
	// Used as an annotation on the build PLR after Snapshot creation; also a label on test and releae PLRs.
	labelOrAnnotationSnapshot = "appstudio.openshift.io/snapshot"

	// Release PipelineRun
	labelAppStudioService          = "appstudio.openshift.io/service"
	valueAppStudioServiceRelease   = "release"
	labelPipelinesType             = "pipelines.appstudio.openshift.io/type"
	valuePipelinesTypeManaged      = "managed"
	labelReleaseApplicationNS      = "release.appstudio.openshift.io/namespace"
	labelTektonPipeline            = "tekton.dev/pipeline"
	managedReleasePLRNamespacesEnv = "MANAGED_RELEASE_PLR_NAMESPACES"

	// kaWindowHoursEnv controls how far back the exporter fetches resources from KubeArchive.
	kaWindowHoursEnv     = "KA_WINDOW_HOURS"
	defaultKAWindowHours = 48

	// kaCollectionTimeoutSecsEnv is the background collection cycle deadline.
	kaCollectionTimeoutSecsEnv   = "KA_COLLECTION_TIMEOUT_SECONDS"
	defaultCollectionTimeoutSecs = 120

	// KubeArchive default is 100; maximum allowed is 1000.
	kaPageLimit = 500

	// kaMaxItems is the safety cap on total items fetched per endpoint per scrape.
	kaMaxItems = 1000

	// parallelism for KubeArchive API calls.
	defaultMaxConcurrent = 10
	maxConcurrentEnv     = "KA_MAX_CONCURRENT"

	// kaCollectIntervalSecsEnv controls how often the background collection
	// goroutine fetches data from KubeArchive and refreshes the metric cache.
	// Should be set to match the Prometheus scrape interval.
	kaCollectIntervalSecsEnv      = "KA_COLLECT_INTERVAL_SECONDS"
	defaultCollectIntervalSeconds = 300
)

// ── Exporter struct ───────────────────────────────────────────────────────────

// KAExporter collects metrics from KubeArchive
type KAExporter struct {
	kaHost     string
	kaToken    string
	cluster    string
	httpClient *http.Client

	// mu guards the Reset()+Set() sequence in runCollection() from concurrent Collect() reads.
	// runCollection() holds the write lock for the entire gauge reset+populate cycle.
	// Collect() holds the read lock while emitting cached metric state — no I/O.
	mu sync.RWMutex

	// windowHours is the look-back window for KubeArchive queries
	windowHours int

	// scrapeTimeout is a hard deadline applied to each background collection cycle.
	// All in-flight HTTP requests are cancelled when it fires.
	scrapeTimeout time.Duration

	// collectInterval controls how often the background goroutine refreshes metric state.
	collectInterval time.Duration

	// readyCh is closed by Start() after the first successful background collection,
	// signalling main() that /metrics is ready to serve non-empty data.
	readyCh chan struct{}

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

	// Release stage timing metrics
	releaseValidationDuration        *prometheus.HistogramVec // Release CR creation → Validated condition
	releasePipelineExecutionDuration *prometheus.HistogramVec // Validated condition → Released condition

	// State metrics — Gauges (point-in-time, no single join key, or counts).
	buildWaitGauge          *prometheus.GaugeVec // build PLR creation → start (Kueue admission wait)
	integrationWaitGauge    *prometheus.GaugeVec // integration test PLR creation → start (Kueue admission wait)
	integrationDelayGauge   *prometheus.GaugeVec // build completion → first test PLR creation (gap)
	releasePLRWaitGauge     *prometheus.GaugeVec // release PLR creation → start (Kueue admission wait)
	archivedCompletionGauge *prometheus.GaugeVec // count of completed resources by outcome

	// Exporter self-monitoring metrics.
	scrapeErrorsTotal         *prometheus.CounterVec // labels: phase
	lastScrapeSuccessGauge    prometheus.Gauge       // unix timestamp of last successful full scrape
	lastScrapeSuccessAt       atomic.Int64           // atomic unix timestamp for readiness check
	scrapeDurationGauge       prometheus.Gauge       // last scrape wall-clock duration in seconds
	truncationsTotal          *prometheus.CounterVec // labels: resource (pipelineruns|snapshots|releases), namespace
	lookbackOrphanedReleases  prometheus.Counter     // total orphaned releases correlated via lookback
	lookbackBuildsNotFound    prometheus.Counter     // total builds not found during lookback (pre-retention)
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

	collectionTimeoutSecs := defaultCollectionTimeoutSecs
	if v := strings.TrimSpace(os.Getenv(kaCollectionTimeoutSecsEnv)); v != "" {
		if parsed, err := strconv.Atoi(v); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %ds",
				kaCollectionTimeoutSecsEnv, v, defaultCollectionTimeoutSecs)
		} else {
			collectionTimeoutSecs = parsed
		}
	}

	collectIntervalSecs := defaultCollectIntervalSeconds
	if ci := strings.TrimSpace(os.Getenv(kaCollectIntervalSecsEnv)); ci != "" {
		if parsed, err := strconv.Atoi(ci); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %ds", kaCollectIntervalSecsEnv, ci, defaultCollectIntervalSeconds)
		} else {
			collectIntervalSecs = parsed
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
		scrapeTimeout:        time.Duration(collectionTimeoutSecs) * time.Second,
		collectInterval:      time.Duration(collectIntervalSecs) * time.Second,
		readyCh:              make(chan struct{}),
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

		// --- Release stage timing metrics ---
		releaseValidationDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_release_validation_duration_seconds",
				Help:    "Time from Release creation to Validated condition (policy gates, EC checks). Exemplars carry pipelinerun, snapshot, and release_cr names.",
				Buckets: []float64{30, 60, 120, 300, 600, 900, 1800}, // 30s–30m
			},
			[]string{"cluster", "namespace", "application", "component"},
		),
		releasePipelineExecutionDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "konflux_release_pipeline_execution_duration_seconds",
				Help:    "Time from Validated to Released condition (managed release pipeline execution). Exemplars carry pipelinerun, snapshot, and release_cr names.",
				Buckets: []float64{60, 120, 300, 600, 900, 1800, 3600}, // 1m–1h
			},
			[]string{"cluster", "namespace", "application", "component"},
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
			Help: "Wall-clock time in seconds of the last background KubeArchive collection cycle.",
		}),
		truncationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "konflux_ka_exporter_truncations_total",
				Help: "Total number of times a paginated KubeArchive fetch was capped at kaMaxItems, indicating potentially incomplete data.",
			},
			[]string{"cluster", "resource", "namespace"},
		),
		lookbackOrphanedReleases: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "konflux_ka_exporter_lookback_orphaned_releases_total",
			Help: "Total number of orphaned releases successfully correlated via lookback mechanism (build outside 48h window).",
		}),
		lookbackBuildsNotFound: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "konflux_ka_exporter_lookback_builds_not_found_total",
			Help: "Total number of builds not found during lookback (pre-KubeArchive retention or missing from archive).",
		}),
	}, nil
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
