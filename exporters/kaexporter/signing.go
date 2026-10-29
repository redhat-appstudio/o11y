package main

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SigningSLO30d manages 30-day SLO metrics for release signing tasks.
//
// Unlike the build, integration and release domains, signing is measured at the
// Tekton TaskRun level: a release PipelineRun contains one signing task among
// many, and only that task's outcome describes the signing pipeline.
//
// In addition to the shared 30-day gauge set, this domain tracks the completion
// time of the most recent successful and the most recent failed signing
// TaskRun. A failure newer than every success means signing is broken and has
// not recovered since. A 30-day failure rate cannot say that: during an outage
// it rises slowly from a large base of past successes.
//
// Absence of success alone is not a usable signal either. Conforma blocks
// releases on Fridays, weekends and freeze dates, so long periods with no
// signing at all are normal. Requiring a failure keeps those periods quiet.
type SigningSLO30d struct {
	SLOGaugeSet

	lastSuccessGauge *prometheus.GaugeVec
	lastFailureGauge *prometheus.GaugeVec

	// mu guards lastSuccessAt and lastFailureAt, which are written from the
	// parallel per-namespace collection goroutines.
	mu sync.Mutex
	// lastSuccessAt and lastFailureAt hold the newest completion seen per
	// label combination. They are monotonic and deliberately outlive the
	// rolling window, so a long gap keeps reporting the real age.
	lastSuccessAt map[signingKey]time.Time
	lastFailureAt map[signingKey]time.Time
}

// signingKey identifies one signing series for last-success tracking.
type signingKey struct {
	cluster   string
	namespace string
	task      string
	pipeline  string
}

// newSigningSLO30d initializes signing 30d SLO metrics
func newSigningSLO30d(sloConfig *SLOConfig) *SigningSLO30d {
	return newSigningSLO30dWithTiers(sloConfig, nil, nil)
}

func newSigningSLO30dWithTiers(sloConfig *SLOConfig, tierConfig *TierConfig, tierPins *TierPins) *SigningSLO30d {
	labels := []string{"cluster", "namespace", "task", "pipeline"}
	return &SigningSLO30d{
		SLOGaugeSet: newSLOGaugeSet("konflux_signing", "signing task", labels, sloConfig, tierConfig, tierPins, metricSigningDuration),
		lastSuccessGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "konflux_signing_last_success_timestamp_seconds",
			Help: "Unix timestamp of the most recent successful signing task completion.",
		}, labels),
		lastFailureGauge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "konflux_signing_last_failure_timestamp_seconds",
			Help: "Unix timestamp of the most recent failed signing task completion. Signing is broken and has not recovered when this is newer than konflux_signing_last_success_timestamp_seconds.",
		}, labels),
		lastSuccessAt: make(map[signingKey]time.Time),
		lastFailureAt: make(map[signingKey]time.Time),
	}
}

// recordObservation records a single signing TaskRun observation into the
// rolling store and updates last-success and last-failure tracking.
func (m *SigningSLO30d) recordObservation(
	store *Store,
	cluster, namespace string,
	tr TaskRun,
) {
	if tr.Status.CompletionTime == "" {
		return // Not finished yet.
	}

	completionTime, err := time.Parse(time.RFC3339, tr.Status.CompletionTime)
	if err != nil {
		return
	}

	duration := secondsBetween(tr.Status.StartTime, tr.Status.CompletionTime)
	if duration < 0 {
		return
	}
	waitTime := secondsBetween(tr.Metadata.CreationTimestamp, tr.Status.StartTime)

	task := getLabel(tr, labelTektonPipelineTask, "unknown")
	pipeline := getLabel(tr, labelTektonPipeline, "unknown")

	succeeded, failureReason := plrStatus(tr)

	ls := LabelSet{
		Cluster:   cluster,
		Namespace: namespace,
		Task:      task,
		Pipeline:  pipeline,
	}

	recorded := store.RecordObservation(
		metricSigningDuration,
		taskRunDedupeKey(namespace, tr),
		completionTime,
		ls,
		duration,
		waitTime,
		succeeded,
		failureReason,
	)

	if !recorded {
		return
	}
	key := signingKey{cluster, namespace, task, pipeline}
	if succeeded {
		m.recordCompletionAt(m.lastSuccessAt, key, completionTime)
	} else {
		m.recordCompletionAt(m.lastFailureAt, key, completionTime)
	}
}

// recordCompletionAt advances one series in a last-success or last-failure map.
func (m *SigningSLO30d) recordCompletionAt(last map[signingKey]time.Time, key signingKey, completionTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := last[key]; !ok || completionTime.After(prev) {
		last[key] = completionTime
	}
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *SigningSLO30d) updateGauges(store *Store, skipBreachNamespaces map[string]bool) {
	m.SLOGaugeSet.UpdateFromStore(store, metricSigningDuration, func(ls LabelSet) []string {
		return []string{ls.Cluster, ls.Namespace, ls.Task, ls.Pipeline}
	}, skipBreachNamespaces)

	// last-success and last-failure gauges are rebuilt from tracking state
	// rather than from the rolling store, so that a completion older than the
	// window keeps reporting.
	m.mu.Lock()
	defer m.mu.Unlock()
	setTimestampGauge(m.lastSuccessGauge, m.lastSuccessAt)
	setTimestampGauge(m.lastFailureGauge, m.lastFailureAt)
}

// setTimestampGauge replaces the contents of g with one series per tracked key.
func setTimestampGauge(g *prometheus.GaugeVec, last map[signingKey]time.Time) {
	g.Reset()
	for key, ts := range last {
		g.WithLabelValues(key.cluster, key.namespace, key.task, key.pipeline).
			Set(float64(ts.Unix()))
	}
}

// Describe implements prometheus.Collector
func (m *SigningSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.SLOGaugeSet.Describe(ch)
	m.lastSuccessGauge.Describe(ch)
	m.lastFailureGauge.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *SigningSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.SLOGaugeSet.Collect(ch)
	m.lastSuccessGauge.Collect(ch)
	m.lastFailureGauge.Collect(ch)
}
