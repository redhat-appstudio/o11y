package main

import (
	"testing"
	"time"
)

func TestRecordObservationDeduplication(t *testing.T) {
	s := NewStore()
	ls := LabelSet{Cluster: "c1", Namespace: "ns1", Application: "app", Component: "comp"}
	completion := time.Now().UTC().Add(-2 * time.Hour)

	if !s.RecordObservation(metricBuildDuration, "plr:abc", completion, ls, 120, true) {
		t.Fatal("expected first observation to be recorded")
	}
	if s.RecordObservation(metricBuildDuration, "plr:abc", completion, ls, 120, true) {
		t.Fatal("expected duplicate observation to be skipped")
	}

	window := s.Data[metricBuildDuration][ls]
	if window.ComputeSuccessMean() != 120 {
		t.Fatalf("expected mean 120, got %v", window.ComputeSuccessMean())
	}
	if window.ComputeSuccessRate() != 1 {
		t.Fatalf("expected success rate 1, got %v", window.ComputeSuccessRate())
	}
}

func TestRecordObservationOutsideWindow(t *testing.T) {
	s := NewStore()
	ls := LabelSet{Cluster: "c1", Namespace: "ns1", Application: "app", Component: "comp"}
	completion := time.Now().UTC().Add(-31 * 24 * time.Hour)

	if s.RecordObservation(metricBuildDuration, "plr:old", completion, ls, 120, true) {
		t.Fatal("expected observation outside 30d window to be rejected")
	}
	if len(s.SeenKeys) != 0 {
		t.Fatal("expected no seen-set entry for out-of-window observation")
	}
}

func TestTotalCountAppliesCutoff(t *testing.T) {
	// Regression test for bug: TotalCount() must skip stale buckets
	// Scenario: component active 30 days ago, dormant since
	// Expected: TotalCount() returns 0 (not old count values)

	window := &MetricWindow{}

	// Simulate component that was active 35 days ago
	// All bucket days are stale (older than 30-day cutoff)
	staleDate := time.Now().UTC().AddDate(0, 0, -35).Format("2006-01-02")

	for i := 0; i < 30; i++ {
		window.Buckets[i] = DailyBucket{
			Day:          staleDate,
			SumSeconds:   100.0,
			Count:        5,
			SuccessCount: 4,
		}
	}

	// Before fix: TotalCount() would return 150 (5 × 30 buckets)
	// After fix: TotalCount() returns 0 (all buckets stale)
	totalCount := window.TotalCount()
	if totalCount != 0 {
		t.Errorf("TotalCount() should return 0 for stale buckets, got %d", totalCount)
	}

	// Verify ComputeSuccessMean and ComputeSuccessRate also return 0
	if mean := window.ComputeSuccessMean(); mean != 0 {
		t.Errorf("ComputeSuccessMean() should return 0 for stale buckets, got %f", mean)
	}
	if rate := window.ComputeSuccessRate(); rate != 0 {
		t.Errorf("ComputeSuccessRate() should return 0 for stale buckets, got %f", rate)
	}
}

func TestTotalCountFreshBuckets(t *testing.T) {
	// Verify TotalCount() correctly counts fresh buckets

	window := &MetricWindow{}

	// Add fresh data (2 days ago)
	freshDate := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	window.Buckets[27] = DailyBucket{ // 29-2 = 27
		Day:          freshDate,
		SumSeconds:   120.0,
		Count:        3,
		SuccessCount: 3,
	}

	// Add fresh data (yesterday)
	yesterdayDate := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	window.Buckets[28] = DailyBucket{ // 29-1 = 28
		Day:          yesterdayDate,
		SumSeconds:   100.0,
		Count:        2,
		SuccessCount: 2,
	}

	// TotalCount should return 5 (3 + 2)
	totalCount := window.TotalCount()
	if totalCount != 5 {
		t.Errorf("TotalCount() should return 5 for fresh buckets, got %d", totalCount)
	}
}
