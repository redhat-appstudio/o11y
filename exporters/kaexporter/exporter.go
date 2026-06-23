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
	// NOTE: seenPLRRetentionHours is now calculated dynamically in NewKAExporter()
	// based on the actual query window (KA_WINDOW_HOURS + safety margin)

	// To identify Tenant namespaces
	tenantLabelKey   = "konflux-ci.dev/type"
	tenantLabelValue = "tenant"

	// Build and Integration PipelineRun labels
	labelAppStudioApp     = "appstudio.openshift.io/application"
	labelAppStudioComp    = "appstudio.openshift.io/component"
	labelTestScenario     = "test.appstudio.openshift.io/scenario"
	labelTestOptional     = "test.appstudio.openshift.io/optional"       // "true" if test can fail without blocking release
	labelEventType        = "pipelinesascode.tekton.dev/event-type"      // Event type for builds
	labelPACEventType     = "pac.test.appstudio.openshift.io/event-type" // Shared PAC event type label, used by both integration tests and releases
	labelPipelinesType    = "pipelines.appstudio.openshift.io/type"
	labelTektonPipeline   = "tekton.dev/pipeline"

	// Release CR labels
	labelReleaseAutomated = "release.appstudio.openshift.io/automated" // "true" for automated releases, "false" for manual
	labelReleasePlan      = "release.appstudio.openshift.io/release-plan"
	labelReleaseSnapshot  = "release.appstudio.openshift.io/snapshot"

	// kaWindowHoursEnv controls how far back the exporter fetches resources from KubeArchive.
	kaWindowHoursEnv     = "KA_WINDOW_HOURS"
	defaultKAWindowHours = 24

	// kaCollectionTimeoutSecsEnv is the background collection cycle deadline.
	kaCollectionTimeoutSecsEnv   = "KA_COLLECTION_TIMEOUT_SECONDS"
	defaultCollectionTimeoutSecs = 120

	// KubeArchive default is 100; maximum allowed is 1000.
	kaPageLimit = 500

	// kaMaxItems is the steady-state safety cap on total items fetched per endpoint per scrape.
	kaMaxItems = 1000

	// Cold-start configuration: on first boot the exporter queries 30 days of history
	// to bootstrap full rolling-window accuracy, then switches to the steady-state window.
	coldStartWindowHours        = 720   // 30 days
	coldStartMaxItems           = 10000 // higher cap during bootstrap (busy namespaces exceed 1000 over 30d)
	defaultColdStartTimeoutSecs = 600   // 10 min - allows busy namespaces to complete 30-day bootstrap

	// parallelism for KubeArchive API calls.
	defaultMaxConcurrent   = 10
	coldStartMaxConcurrent = 5 // Reduced concurrency during cold start to ease KubeArchive load
	maxConcurrentEnv       = "KA_MAX_CONCURRENT"

	// HTTP client timeout for KubeArchive API calls
	defaultHTTPTimeoutSecs = 60
	httpTimeoutEnv         = "KA_HTTP_TIMEOUT_SECONDS"

	// kaCollectIntervalSecsEnv controls how often the background collection
	// goroutine fetches data from KubeArchive and refreshes the metric cache.
	// Should be set to match the Prometheus scrape interval.
	kaCollectIntervalSecsEnv      = "KA_COLLECT_INTERVAL_SECONDS"
	defaultCollectIntervalSeconds = 300

	// Retry configuration for KubeArchive API calls
	kaMaxRetriesEnv          = "KA_MAX_RETRIES"
	defaultMaxRetries        = 3
	kaInitialRetryDelayEnv   = "KA_INITIAL_RETRY_DELAY_MS"
	defaultInitialRetryDelay = 100 // milliseconds
	kaMaxRetryDelayEnv       = "KA_MAX_RETRY_DELAY_MS"
	defaultMaxRetryDelay     = 5000 // milliseconds
	retryBackoffMultiplier   = 2.0

	// maxGapFillAttempts limits gap-fill retries for truncated namespaces.
	// With coldStartMaxItems=10,000, this allows up to 50,000 PLRs to be covered.
	maxGapFillAttempts = 5
)

// ── Bootstrap state tracking ─────────────────────────────────────────────────

// nsBootstrapState tracks 30-day bootstrap progress for one namespace.
type nsBootstrapState struct {
	Bootstrapped         bool   // true when namespace has complete 30-day data
	OldestSeenCreationTS string // RFC3339 timestamp of oldest item seen (empty = no gap)
	GapAttempts          int    // number of gap-fill attempts (prevent infinite retry)
}

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
	kaHost      string
	kaToken     string // Static token (local dev only; empty when using file-based token)
	kaTokenFile string // Path to projected SA token file (re-read on each request for rotation)
	cluster     string
	httpClient  *http.Client

	// mu guards the Reset()+Set() sequence in runCollection() from concurrent Collect() reads.
	// runCollection() holds the write lock for the entire gauge reset+populate cycle.
	// Collect() holds the read lock while emitting cached metric state — no I/O.
	mu sync.RWMutex

	// queryWindowHours is the actual query window used for KubeArchive API calls.
	// Computed from KA_WINDOW_HOURS env var with 50% safety margin.
	// Example: KA_WINDOW_HOURS=24 → queryWindowHours=36 (24 + 12)
	queryWindowHours int

	// dedupeRetentionHours is the retention for SeenKeys deduplication map.
	// Must be > queryWindowHours to prevent double-counting.
	// Calculated as: 1.5 × queryWindowHours
	// Example: queryWindowHours=72 → dedupeRetentionHours=108
	dedupeRetentionHours int

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

	// coldStart is true until the first successful collection completes.
	// During cold start, the query window expands to 720h (30 days) and the
	// per-namespace item cap is raised to bootstrap full rolling-store accuracy.
	coldStart bool

	// bootstrapStates tracks which namespaces have completed 30-day bootstrap.
	// Namespaces that were truncated during cold start will be gap-filled progressively.
	bootstrapStates map[string]*nsBootstrapState

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

	kaTokenFile, kaToken, err := resolveTokenSource()
	if err != nil {
		return nil, err
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

	// Calculate query window with safety margin to catch long-running pipelines.
	// Safety margin = 50% of base window (e.g., 48h → 72h query window).
	// This ensures pipelines taking up to queryWindowHours to complete are captured.
	safetyMarginHours := windowHours / 2
	queryWindowHours := windowHours + safetyMarginHours

	// Dedupe retention must exceed query window to prevent boundary condition double-counting.
	// Use 1.5× query window (not base window) for safety.
	// Example: queryWindowHours=72 → dedupeRetentionHours=108
	dedupeRetentionHours := int(float64(queryWindowHours) * 1.5)

	log.Printf("Query window configuration: base_window=%dh, safety_margin=%dh, query_window=%dh, dedupe_retention=%dh",
		windowHours, safetyMarginHours, queryWindowHours, dedupeRetentionHours)

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

	httpTimeoutSecs := defaultHTTPTimeoutSecs
	if ht := strings.TrimSpace(os.Getenv(httpTimeoutEnv)); ht != "" {
		if parsed, err := strconv.Atoi(ht); err != nil || parsed <= 0 {
			log.Printf("WARNING: %s=%q is not a positive integer; using default %ds", httpTimeoutEnv, ht, defaultHTTPTimeoutSecs)
		} else {
			httpTimeoutSecs = parsed
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

	// HTTP client with TLS verification disabled for KubeArchive API.
	// KubeArchive commonly uses self-signed certificates and provides an explicit
	// --kubearchive-insecure-skip-tls-verify flag in its official kubectl plugin.
	// SECURITY JUSTIFICATION (nosec G402):
	httpClient := &http.Client{
		Timeout: time.Duration(httpTimeoutSecs) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nosec G402
		},
	}

	e := &KAExporter{
		kaHost:               kaHost,
		kaToken:              kaToken,
		kaTokenFile:          kaTokenFile,
		cluster:              cluster,
		queryWindowHours:     queryWindowHours,
		dedupeRetentionHours: dedupeRetentionHours,
		maxConcurrent:        maxConcurrent,
		scrapeTimeout:        time.Duration(collectionTimeoutSecs) * time.Second,
		collectInterval:      time.Duration(collectIntervalSecs) * time.Second,
		readyCh:              make(chan struct{}),
		coldStart:            true,
		bootstrapStates:      make(map[string]*nsBootstrapState),
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

// resolveTokenSource determines the KubeArchive API token source at startup.
// Returns the file path to read tokens from, or empty string if using static env var.
// Priority:
//  1. KA_TOKEN_FILE env var (projected SA token with custom path/audience)
//  2. Standard SA token at /var/run/secrets/kubernetes.io/serviceaccount/token
//  3. KA_TOKEN env var (static token for local development)
func resolveTokenSource() (tokenFile string, staticToken string, err error) {
	const defaultSATokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

	if f := strings.TrimSpace(os.Getenv("KA_TOKEN_FILE")); f != "" {
		if _, statErr := os.Stat(f); statErr == nil {
			log.Printf("Using projected token file %s for KubeArchive API authentication", f)
			return f, "", nil
		}
		return "", "", fmt.Errorf("KA_TOKEN_FILE=%q specified but file does not exist", f)
	}

	if _, statErr := os.Stat(defaultSATokenPath); statErr == nil {
		log.Printf("Using ServiceAccount token from %s for KubeArchive API authentication", defaultSATokenPath)
		return defaultSATokenPath, "", nil
	}

	if token := os.Getenv(kaTokenEnvVar); token != "" {
		log.Printf("Using KA_TOKEN environment variable for KubeArchive API authentication (local dev mode)")
		return "", token, nil
	}

	return "", "", fmt.Errorf("no KubeArchive API token found: set KA_TOKEN_FILE, mount SA token, or set %s", kaTokenEnvVar)
}

// readTokenFromFile reads and trims a bearer token from the given path.
// Called on each API request to pick up kubelet-rotated projected tokens.
func readTokenFromFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}
