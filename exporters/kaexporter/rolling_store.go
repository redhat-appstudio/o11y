package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
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
	Day               string  `json:"day"`
	Count             int64   `json:"count"`               // All completed PLRs
	SuccessCount      int64   `json:"success_count"`       // Count of successful PLRs
	SuccessSumSeconds float64 `json:"success_sum_seconds"` // Duration sum of successful PLRs
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
	succeeded bool,
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

	bucket.Count++
	if succeeded {
		bucket.SuccessCount++
		bucket.SuccessSumSeconds += durationSeconds
	}
	return true
}

// ComputeSuccessMean returns the mean duration for successful PLRs only.
// Skips stale buckets (older than 30 days from today).
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

// Metric names used by the exporter.
const (
	metricBuildDuration       = "build_duration"
	metricIntegrationDuration = "integration_duration"
	metricReleaseDuration     = "release_duration"
)
