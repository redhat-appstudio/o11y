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

// ── Environment variable names and configuration ──────────────────────────────

const (
	kaHostEnvVar    = "KA_HOST"
	kaTokenEnvVar   = "KA_TOKEN"
	clusterEnvVar   = "CLUSTER_NAME"
	namespaceEnvVar = "TENANT_NAMESPACE"
	portEnvVar      = "EXPORTER_PORT"
	defaultPort     = "9101"

	// 30-day SLO rolling window configuration
	seenPLRRetentionHours = 72 // 1.5× KA_WINDOW_HOURS to prevent boundary condition double-counting (see DEDUPLICATION-BOUNDARY-CONDITION.md)

	// To identify Tenant namespaces
	tenantLabelKey   = "konflux-ci.dev/type"
	tenantLabelValue = "tenant"

	// Correlation keys
	labelBuildPipelineRun = "appstudio.openshift.io/build-pipelinerun"
	labelAppStudioApp     = "appstudio.openshift.io/application"
	labelAppStudioComp    = "appstudio.openshift.io/component"
	labelTestScenario     = "test.appstudio.openshift.io/scenario"
	labelTestOptional     = "test.appstudio.openshift.io/optional" // "true" if test can fail without blocking release
	labelBuildType        = "build.appstudio.redhat.com/pipeline"  // Build pipeline type (e.g., "docker-build", "build-rpm-package")
	labelEventType        = "pipelinesascode.tekton.dev/event-type" // Event type (e.g., "push", "Merge_Request", "retest-comment")
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

	// Retry configuration for KubeArchive API calls
	kaMaxRetriesEnv         = "KA_MAX_RETRIES"
	defaultMaxRetries       = 3
	kaInitialRetryDelayEnv  = "KA_INITIAL_RETRY_DELAY_MS"
	defaultInitialRetryDelay = 100 // milliseconds
	kaMaxRetryDelayEnv      = "KA_MAX_RETRY_DELAY_MS"
	defaultMaxRetryDelay    = 5000 // milliseconds
	retryBackoffMultiplier  = 2.0
)

// ── Retry configuration ───────────────────────────────────────────────────────

// retryConfig holds exponential backoff parameters for KubeArchive API retries
type retryConfig struct {
	maxRetries   int
	initialDelay time.Duration
	maxDelay     time.Duration
	multiplier   float64
}

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

	// maxConcurrent limits parallel KubeArchive API calls during collection
	maxConcurrent int

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

	// retry holds exponential backoff configuration for KubeArchive API calls
	retry retryConfig

	// Exporter self-monitoring metrics.
	scrapeErrorsTotal      *prometheus.CounterVec // labels: phase
	lastScrapeSuccessGauge prometheus.Gauge       // unix timestamp of last successful full scrape
	lastScrapeSuccessAt    atomic.Int64           // atomic unix timestamp for readiness check
	scrapeDurationGauge    prometheus.Gauge       // last scrape wall-clock duration in seconds
	truncationsTotal       *prometheus.CounterVec // labels: resource (pipelineruns|snapshots|releases), namespace
	retryAttemptsTotal     *prometheus.CounterVec // labels: cluster, reason; total retry attempts
	retryExhaustedTotal    *prometheus.CounterVec // labels: cluster, reason; total requests that exceeded max retries

	// 30-day SLO rolling aggregates (in-memory only, no persistence).
	rollingStore   *Store
	buildSLO       *BuildSLO30d
	integrationSLO *IntegrationSLO30d
	releaseSLO     *ReleaseSLO30d
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

	// Parse retry configuration
	maxRetries := defaultMaxRetries
	if mr := strings.TrimSpace(os.Getenv(kaMaxRetriesEnv)); mr != "" {
		if parsed, err := strconv.Atoi(mr); err != nil || parsed < 0 {
			log.Printf("WARNING: %s=%q is not a non-negative integer; using default %d", kaMaxRetriesEnv, mr, defaultMaxRetries)
		} else {
			maxRetries = parsed
		}
	}

	initialRetryDelayMs := defaultInitialRetryDelay
	if ird := strings.TrimSpace(os.Getenv(kaInitialRetryDelayEnv)); ird != "" {
		if parsed, err := strconv.Atoi(ird); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %dms", kaInitialRetryDelayEnv, ird, defaultInitialRetryDelay)
		} else {
			initialRetryDelayMs = parsed
		}
	}

	maxRetryDelayMs := defaultMaxRetryDelay
	if mrd := strings.TrimSpace(os.Getenv(kaMaxRetryDelayEnv)); mrd != "" {
		if parsed, err := strconv.Atoi(mrd); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %dms", kaMaxRetryDelayEnv, mrd, defaultMaxRetryDelay)
		} else {
			maxRetryDelayMs = parsed
		}
	}

	maxConcurrent := defaultMaxConcurrent
	if mc := strings.TrimSpace(os.Getenv(maxConcurrentEnv)); mc != "" {
		if parsed, err := strconv.Atoi(mc); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %d", maxConcurrentEnv, mc, defaultMaxConcurrent)
		} else {
			maxConcurrent = parsed
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

	e := &KAExporter{
		kaHost:               kaHost,
		kaToken:              kaToken,
		cluster:              cluster,
		windowHours:          windowHours,
		maxConcurrent:        maxConcurrent,
		scrapeTimeout:        time.Duration(collectionTimeoutSecs) * time.Second,
		collectInterval:      time.Duration(collectIntervalSecs) * time.Second,
		readyCh:              make(chan struct{}),
		fixedTenantNamespace: fixedNS,
		k8sClient:            k8sClient,
		httpClient:           httpClient,

		// Retry configuration
		retry: retryConfig{
			maxRetries:   maxRetries,
			initialDelay: time.Duration(initialRetryDelayMs) * time.Millisecond,
			maxDelay:     time.Duration(maxRetryDelayMs) * time.Millisecond,
			multiplier:   retryBackoffMultiplier,
		},

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
		retryAttemptsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "konflux_ka_exporter_retry_attempts_total",
				Help: "Total number of retry attempts for KubeArchive API calls, by reason (network_error, rate_limit, server_error).",
			},
			[]string{"cluster", "reason"},
		),
		retryExhaustedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "konflux_ka_exporter_retry_exhausted_total",
				Help: "Total number of KubeArchive API requests that exceeded max retries and failed permanently.",
			},
			[]string{"cluster", "reason"},
		),
	}

	// Initialize 30d SLO metric modules
	e.buildSLO = newBuildSLO30d()
	e.integrationSLO = newIntegrationSLO30d()
	e.releaseSLO = newReleaseSLO30d()

	// Initialize rolling store (in-memory only, no persistence)
	e.rollingStore = NewStore()

	return e, nil
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
