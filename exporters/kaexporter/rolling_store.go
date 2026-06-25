package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// LabelSet identifies a metric label combination (see field comments for optionality).
type LabelSet struct {
	Cluster     string `json:"cluster"`
	Namespace   string `json:"namespace"`
	Application string `json:"application"`
	Component   string `json:"component"`
	Scenario    string `json:"scenario,omitempty"`   // integration tests
	EventType   string `json:"event_type,omitempty"` // builds, integration tests, and releases
	BuildType   string `json:"build_type,omitempty"` // builds
	Optional    string `json:"optional,omitempty"`   // integration tests
	TestType    string `json:"test_type,omitempty"`  // integration tests
	Automated   string `json:"automated,omitempty"`  // "true" if release is automated, "false" if manual
}

func (l LabelSet) String() string {
	parts := []string{
		fmt.Sprintf("cluster=%s", l.Cluster),
		fmt.Sprintf("namespace=%s", l.Namespace),
		fmt.Sprintf("application=%s", l.Application),
		fmt.Sprintf("component=%s", l.Component),
	}
	if l.Scenario != "" {
		parts = append(parts, fmt.Sprintf("scenario=%s", l.Scenario))
	}
	if l.EventType != "" {
		parts = append(parts, fmt.Sprintf("event_type=%s", l.EventType))
	}
	if l.BuildType != "" {
		parts = append(parts, fmt.Sprintf("build_type=%s", l.BuildType))
	}
	if l.Optional != "" {
		parts = append(parts, fmt.Sprintf("optional=%s", l.Optional))
	}
	if l.TestType != "" {
		parts = append(parts, fmt.Sprintf("test_type=%s", l.TestType))
	}
	if l.Automated != "" {
		parts = append(parts, fmt.Sprintf("automated=%s", l.Automated))
	}
	return strings.Join(parts, ",")
}

// DailyBucket holds per-day aggregates for one label set.
type DailyBucket struct {
	Day               string           `json:"day"`
	Count             int64            `json:"count"`               // All completed PLRs
	SuccessCount      int64            `json:"success_count"`       // Count of successful PLRs
	SuccessSumSeconds float64          `json:"success_sum_seconds"` // Duration sum of successful PLRs
	WaitSumSeconds    float64          `json:"wait_sum_seconds"`    // Wait time sum ofsuccesful PLRs
	FailureReasons    map[string]int64 `json:"failure_reasons"`     // Failure count by reason
}

// MetricWindow is a fixed 30-day circular buffer indexed by day offset.
type MetricWindow struct {
	Buckets [30]DailyBucket `json:"buckets"`
}

// Store holds 30-day rolling aggregates and a seen-set for deduplication.
type Store struct {
	mu       sync.RWMutex
	Data     map[string]map[LabelSet]*MetricWindow
	SeenKeys map[string]time.Time
}

// NewStore returns an empty rolling store.
func NewStore() *Store {
	return &Store{
		Data:     make(map[string]map[LabelSet]*MetricWindow),
		SeenKeys: make(map[string]time.Time),
	}
}

// RecordObservation adds one observation to its day bucket.
func (s *Store) RecordObservation(
	metricName, dedupeKey string,
	completionTime time.Time,
	labelSet LabelSet,
	durationSeconds float64,
	waitSeconds float64,
	succeeded bool,
	failureReason string,
) bool {
	if durationSeconds < 0 {
		return false
	}

	now := time.Now().UTC().Truncate(24 * time.Hour)
	bucketDay := completionTime.UTC().Truncate(24 * time.Hour)
	dayOffset := int(now.Sub(bucketDay).Hours() / 24)
	if dayOffset < 0 || dayOffset >= 30 {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, seen := s.SeenKeys[dedupeKey]; seen {
		return false
	}
	s.SeenKeys[dedupeKey] = completionTime

	window := s.getOrCreateLocked(metricName, labelSet)
	bucket := &window.Buckets[29-dayOffset]

	completionDay := bucketDay.Format("2006-01-02")
	if bucket.Day != "" && bucket.Day != completionDay {
		*bucket = DailyBucket{} // Clear stale 30-day-old data
	}
	if bucket.Day == "" {
		bucket.Day = completionDay
	}
	if bucket.FailureReasons == nil {
		bucket.FailureReasons = make(map[string]int64)
	}

	bucket.Count++
	if succeeded {
		bucket.SuccessCount++
		bucket.SuccessSumSeconds += durationSeconds
		// Only accumulate wait time for successful PLRs to match SuccessCount denominator
		if waitSeconds >= 0 {
			bucket.WaitSumSeconds += waitSeconds
		}
	} else {
		// Track failure reason
		if failureReason == "" {
			failureReason = "Unknown"
		}
		bucket.FailureReasons[failureReason]++
	}
	return true
}

// ComputeSuccessMean returns the mean duration for successful PLRs only.
func (w *MetricWindow) ComputeSuccessMean() float64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	var sum float64
	var count int64
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		sum += w.Buckets[i].SuccessSumSeconds
		count += w.Buckets[i].SuccessCount
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// ComputeSuccessRate returns SuccessCount / Count across all buckets.
// Skips stale buckets (older than 30 days from today).
func (w *MetricWindow) ComputeSuccessRate() float64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	var success, count int64
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		success += w.Buckets[i].SuccessCount
		count += w.Buckets[i].Count
	}
	if count == 0 {
		return 0
	}
	return float64(success) / float64(count)
}

// ComputeWaitMean returns the mean wait time across successful observations only.
func (w *MetricWindow) ComputeWaitMean() float64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	var sum float64
	var count int64
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		sum += w.Buckets[i].WaitSumSeconds
		count += w.Buckets[i].SuccessCount
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// ComputeTotalCount returns the total Count across all fresh buckets (within 30 days).
// Used for both empty window detection and total_count_30d gauge.
func (w *MetricWindow) ComputeTotalCount() int64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	var count int64
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		count += w.Buckets[i].Count
	}
	return count
}

// ComputeSuccessCount returns the total number of successful observations (within 30 days).
// Used to determine if mean_duration_30d metrics should be emitted (only when successes exist).
func (w *MetricWindow) ComputeSuccessCount() int64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	var count int64
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		count += w.Buckets[i].SuccessCount
	}
	return count
}

// ComputeFailureRate returns the failure rate (FailureCount / TotalCount).
// Returns 0 if no data.
func (w *MetricWindow) ComputeFailureRate() float64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	var success, count int64
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		success += w.Buckets[i].SuccessCount
		count += w.Buckets[i].Count
	}
	if count == 0 {
		return 0
	}
	failureCount := count - success
	return float64(failureCount) / float64(count)
}

// ComputeFailureReasons returns aggregated failure counts by reason across all buckets.
// Skips stale buckets (older than 30 days from today).
func (w *MetricWindow) ComputeFailureReasons() map[string]int64 {
	cutoff := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	aggregated := make(map[string]int64)
	for i := range w.Buckets {
		if w.Buckets[i].Day == "" || w.Buckets[i].Day <= cutoff {
			continue
		}
		for reason, count := range w.Buckets[i].FailureReasons {
			aggregated[reason] += count
		}
	}
	return aggregated
}

// PruneSeenKeys removes entries older than retention.
func (s *Store) PruneSeenKeys(retention time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().Add(-retention)
	for key, t := range s.SeenKeys {
		if t.Before(cutoff) {
			delete(s.SeenKeys, key)
		}
	}
}

// ForEachWindow calls fn for every label set in metricName while holding a read lock.
func (s *Store) ForEachWindow(metricName string, fn func(LabelSet, *MetricWindow)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	metricMap, ok := s.Data[metricName]
	if !ok {
		return
	}
	for ls, window := range metricMap {
		fn(ls, window)
	}
}

func (s *Store) getOrCreateLocked(metricName string, labelSet LabelSet) *MetricWindow {
	metricMap, ok := s.Data[metricName]
	if !ok {
		metricMap = make(map[LabelSet]*MetricWindow)
		s.Data[metricName] = metricMap
	}
	window, ok := metricMap[labelSet]
	if !ok {
		window = &MetricWindow{}
		metricMap[labelSet] = window
	}
	return window
}

// ── SLOGaugeSet: shared 30-day SLO gauge abstraction ──────────────────────────

// SLOGaugeSet holds the common set of 30-day SLO gauges shared across
// build, integration, and release metrics. Domain-specific modules embed
// this struct and add their own record logic + any extra gauges.
type SLOGaugeSet struct {
	mean30d          *prometheus.GaugeVec
	meanWait30d      *prometheus.GaugeVec
	successRate30d   *prometheus.GaugeVec
	totalCount30d    *prometheus.GaugeVec
	successCount30d  *prometheus.GaugeVec
	failureRate30d   *prometheus.GaugeVec
	failureCount30d  *prometheus.GaugeVec
}

// newSLOGaugeSet creates the common gauge set. The failureCount gauge
// automatically appends a "reason" label to the provided base labels.
func newSLOGaugeSet(prefix, helpContext string, labels []string) SLOGaugeSet {
	failureLabels := make([]string, len(labels)+1)
	copy(failureLabels, labels)
	failureLabels[len(labels)] = "reason"

	return SLOGaugeSet{
		mean30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_mean_duration_30d_seconds",
			Help: fmt.Sprintf("Mean %s duration over the past 30 days for successful completions only (completion-time based).", helpContext),
		}, labels),
		meanWait30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_mean_wait_30d_seconds",
			Help: fmt.Sprintf("Mean %s wait time over the past 30 days for successful completions.", helpContext),
		}, labels),
		successRate30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_success_rate_30d",
			Help: fmt.Sprintf("%s success rate over the past 30 days (Succeeded / total completed).", helpContext),
		}, labels),
		totalCount30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_total_count_30d",
			Help: fmt.Sprintf("Total count of completed %s over the past 30 days (successful + failed).", helpContext),
		}, labels),
		successCount30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_success_count_30d",
			Help: fmt.Sprintf("Count of successful %s over the past 30 days. Use for volume-weighted aggregation: sum(success_count) / sum(total_count).", helpContext),
		}, labels),
		failureRate30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_failure_rate_30d",
			Help: fmt.Sprintf("%s failure rate over the past 30 days (FailureCount / TotalCount).", helpContext),
		}, labels),
		failureCount30d: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_failure_count_30d",
			Help: fmt.Sprintf("%s failure count over the past 30 days, broken down by failure reason.", helpContext),
		}, failureLabels),
	}
}

// UpdateFromStore resets all gauges and repopulates from the rolling store.
// labelExtractor converts a LabelSet into the ordered label values for this gauge set.
func (s *SLOGaugeSet) UpdateFromStore(store *Store, metricName string, labelExtractor func(LabelSet) []string) {
	s.mean30d.Reset()
	s.meanWait30d.Reset()
	s.successRate30d.Reset()
	s.totalCount30d.Reset()
	s.successCount30d.Reset()
	s.failureRate30d.Reset()
	s.failureCount30d.Reset()

	store.ForEachWindow(metricName, func(ls LabelSet, window *MetricWindow) {
		totalCount := window.ComputeTotalCount()
		if totalCount == 0 {
			return
		}

		labels := labelExtractor(ls)

		successCount := window.ComputeSuccessCount()

		s.totalCount30d.WithLabelValues(labels...).Set(float64(totalCount))
		s.successCount30d.WithLabelValues(labels...).Set(float64(successCount))
		s.successRate30d.WithLabelValues(labels...).Set(window.ComputeSuccessRate())
		s.failureRate30d.WithLabelValues(labels...).Set(window.ComputeFailureRate())

		if successCount > 0 {
			s.mean30d.WithLabelValues(labels...).Set(window.ComputeSuccessMean())
			s.meanWait30d.WithLabelValues(labels...).Set(window.ComputeWaitMean())
		}

		for reason, count := range window.ComputeFailureReasons() {
			reasonLabels := make([]string, len(labels)+1)
			copy(reasonLabels, labels)
			reasonLabels[len(labels)] = reason
			s.failureCount30d.WithLabelValues(reasonLabels...).Set(float64(count))
		}
	})
}

// Describe emits descriptors for all gauges in the set.
func (s *SLOGaugeSet) Describe(ch chan<- *prometheus.Desc) {
	s.mean30d.Describe(ch)
	s.meanWait30d.Describe(ch)
	s.successRate30d.Describe(ch)
	s.totalCount30d.Describe(ch)
	s.successCount30d.Describe(ch)
	s.failureRate30d.Describe(ch)
	s.failureCount30d.Describe(ch)
}

// Collect emits current metric values for all gauges in the set.
func (s *SLOGaugeSet) Collect(ch chan<- prometheus.Metric) {
	s.mean30d.Collect(ch)
	s.meanWait30d.Collect(ch)
	s.successRate30d.Collect(ch)
	s.totalCount30d.Collect(ch)
	s.successCount30d.Collect(ch)
	s.failureRate30d.Collect(ch)
	s.failureCount30d.Collect(ch)
}

// Metric names used by the exporter.
const (
	metricBuildDuration       = "build_duration"
	metricIntegrationDuration = "integration_duration"
	metricReleaseDuration     = "release_duration"
)
