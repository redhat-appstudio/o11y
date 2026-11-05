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
// time of the most recent successful signing TaskRun. The 30-day aggregates
// cannot express a signing outage: a signing task that hangs never reaches a
// completion time, so it is never archived and never counted. Absence of
// success is therefore the only observable form such an outage takes.
type SigningSLO30d struct {
	SLOGaugeSet

	lastSuccessGauge *prometheus.GaugeVec

	// mu guards lastSuccessAt, which is written from the parallel
	// per-namespace collection goroutines.
	mu sync.Mutex
	// lastSuccessAt holds the newest successful completion seen per label
	// combination. It is monotonic and deliberately outlives the rolling
	// window, so a long gap keeps reporting the real age of the last success.
	lastSuccessAt map[signingKey]time.Time
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
			Help: "Unix timestamp of the most recent successful signing task completion. Use time() - this to detect a signing outage; a hung signing task never completes and is therefore invisible to success/failure counts.",
		}, labels),
		lastSuccessAt: make(map[signingKey]time.Time),
	}
}

// recordObservation records a single signing TaskRun observation into the
// rolling store and updates last-success tracking.
func (m *SigningSLO30d) recordObservation(
	store *Store,
	cluster, namespace string,
	tr TaskRun,
) {
	if tr.Status.CompletionTime == "" {
		return // Not finished, or hung — see the type comment.
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

	if recorded && succeeded {
		m.recordSuccessAt(signingKey{cluster, namespace, task, pipeline}, completionTime)
	}
}

// recordSuccessAt advances the last-success timestamp for one series.
func (m *SigningSLO30d) recordSuccessAt(key signingKey, completionTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.lastSuccessAt[key]; !ok || completionTime.After(prev) {
		m.lastSuccessAt[key] = completionTime
	}
}

// updateGauges reads from the rolling store and updates the 30d SLO gauges
func (m *SigningSLO30d) updateGauges(store *Store, skipBreachNamespaces map[string]bool) {
	m.SLOGaugeSet.UpdateFromStore(store, metricSigningDuration, func(ls LabelSet) []string {
		return []string{ls.Cluster, ls.Namespace, ls.Task, ls.Pipeline}
	}, skipBreachNamespaces)

	// last-success gauges are rebuilt from tracking state rather than from the
	// rolling store, so that a success older than the window keeps reporting.
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSuccessGauge.Reset()
	for key, ts := range m.lastSuccessAt {
		m.lastSuccessGauge.
			WithLabelValues(key.cluster, key.namespace, key.task, key.pipeline).
			Set(float64(ts.Unix()))
	}
}

// Describe implements prometheus.Collector
func (m *SigningSLO30d) Describe(ch chan<- *prometheus.Desc) {
	m.SLOGaugeSet.Describe(ch)
	m.lastSuccessGauge.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *SigningSLO30d) Collect(ch chan<- prometheus.Metric) {
	m.SLOGaugeSet.Collect(ch)
	m.lastSuccessGauge.Collect(ch)
}
