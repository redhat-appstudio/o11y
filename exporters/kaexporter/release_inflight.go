package main

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ReleaseInFlight tracks Release CRs that are still running, and Release CRs
// that reached the archive without ever completing.
//
// Neither is expressible through the 30-day duration aggregates. A Release that
// hangs has no completion time, so KubeArchive does not hold it (the archive
// config requires `has(status.completionTime)`) and the release_cr counters
// never move. During the Aug 17-21 2026 outage releases were blocked for four
// days with no change in any of them.
//
// Two separate signals, with different latency:
//
//   - in-progress age, read from the live API each cycle. Alertable: a release
//     stuck for hours shows up immediately.
//   - never-completed count, read from the archive. A hung Release reaches the
//     archive only when it is finally deleted, which the grace period delays by
//     days, so this is a historical measure, not an alerting one.
type ReleaseInFlight struct {
	inProgressCount *prometheus.GaugeVec
	oldestAge       *prometheus.GaugeVec
	neverCompleted  *prometheus.GaugeVec

	// mu guards neverCompletedSeen, written from the collection goroutine.
	mu sync.Mutex
	// neverCompletedSeen deduplicates archived releases across cycles and holds
	// the observation day, so the count can be limited to a rolling window.
	neverCompletedSeen map[string]neverCompletedEntry
}

type neverCompletedEntry struct {
	labels   inFlightKey
	observed time.Time
}

type inFlightKey struct {
	cluster     string
	namespace   string
	application string
	component   string
}

func newReleaseInFlight() *ReleaseInFlight {
	liveLabels := []string{"cluster", "namespace"}
	archiveLabels := []string{"cluster", "namespace", "application", "component"}

	return &ReleaseInFlight{
		inProgressCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "konflux_release_in_progress_count",
			Help: "Number of Release CRs currently running, read from the live API. Releases that hang are invisible to the 30-day archive aggregates, which only count completions.",
		}, liveLabels),
		oldestAge: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "konflux_release_oldest_in_progress_age_seconds",
			Help: "Age in seconds of the oldest Release CR still running. Use this to detect releases that are stuck rather than failing; a hung release never completes and so never appears in any success or failure count.",
		}, liveLabels),
		neverCompleted: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "konflux_release_never_completed_count_30d",
			Help: "Count of Release CRs seen in the archive with no completion time over the past 30 days, meaning they were deleted while still running. Historical only: the grace period delays archival by days.",
		}, archiveLabels),
		neverCompletedSeen: make(map[string]neverCompletedEntry),
	}
}

// recordLive replaces the in-progress gauges from one observation of the live API.
// ages holds the age in seconds of every running Release, keyed by namespace.
func (m *ReleaseInFlight) recordLive(cluster string, ages map[string][]float64) {
	m.inProgressCount.Reset()
	m.oldestAge.Reset()

	for namespace, list := range ages {
		if len(list) == 0 {
			continue
		}
		oldest := list[0]
		for _, age := range list[1:] {
			if age > oldest {
				oldest = age
			}
		}
		m.inProgressCount.WithLabelValues(cluster, namespace).Set(float64(len(list)))
		m.oldestAge.WithLabelValues(cluster, namespace).Set(oldest)
	}
}

// recordNeverCompleted registers one archived Release that has no completion
// time. key deduplicates across collection cycles.
func (m *ReleaseInFlight) recordNeverCompleted(key string, labels inFlightKey, observed time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, seen := m.neverCompletedSeen[key]; seen {
		return
	}
	m.neverCompletedSeen[key] = neverCompletedEntry{labels: labels, observed: observed}
}

// updateGauges rebuilds the never-completed gauge and drops observations that
// have aged out of the 30-day window.
func (m *ReleaseInFlight) updateGauges() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	counts := make(map[inFlightKey]int)
	for key, entry := range m.neverCompletedSeen {
		if entry.observed.Before(cutoff) {
			delete(m.neverCompletedSeen, key)
			continue
		}
		counts[entry.labels]++
	}

	m.neverCompleted.Reset()
	for labels, count := range counts {
		m.neverCompleted.
			WithLabelValues(labels.cluster, labels.namespace, labels.application, labels.component).
			Set(float64(count))
	}
}

// Describe implements prometheus.Collector
func (m *ReleaseInFlight) Describe(ch chan<- *prometheus.Desc) {
	m.inProgressCount.Describe(ch)
	m.oldestAge.Describe(ch)
	m.neverCompleted.Describe(ch)
}

// Collect implements prometheus.Collector
func (m *ReleaseInFlight) Collect(ch chan<- prometheus.Metric) {
	m.inProgressCount.Collect(ch)
	m.oldestAge.Collect(ch)
	m.neverCompleted.Collect(ch)
}
