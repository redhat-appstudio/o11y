package main

import (
	"testing"
	"time"
)

func TestStoreRetainsDailyBucketsAcrossUTCDateChange(t *testing.T) {
	store := NewStore()
	labels := LabelSet{
		Cluster:     "test-cluster",
		Namespace:   "test-tenant",
		Application: "test-app",
		Component:   "test-component",
	}
	dayOne := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	dayTwo := dayOne.Add(24 * time.Hour)

	observations := []struct {
		now        time.Time
		key        string
		completion time.Time
		duration   float64
	}{
		{now: dayOne, key: "previous-day", completion: dayOne.Add(-24 * time.Hour), duration: 60},
		{now: dayOne, key: "day-one", completion: dayOne, duration: 120},
		{now: dayTwo, key: "day-two", completion: dayTwo, duration: 600},
	}

	for _, observation := range observations {
		if recorded := store.recordObservationAt(
			observation.now,
			metricBuildDuration,
			observation.key,
			observation.completion,
			labels,
			observation.duration,
			0,
			true,
			"",
		); !recorded {
			t.Fatalf("observation %q was not recorded", observation.key)
		}
	}

	window := store.Data[metricBuildDuration][labels]
	cutoff := dayTwo.AddDate(0, 0, -30).Format("2006-01-02")
	if got, want := window.ComputeTotalCount(cutoff), int64(len(observations)); got != want {
		t.Fatalf("total count after UTC date change = %d, want %d; a daily bucket was overwritten", got, want)
	}
	if _, got := window.CountBreachingDays(cutoff, 0); got != len(observations) {
		t.Fatalf("success day count after UTC date change = %d, want %d", got, len(observations))
	}
	if got, want := window.ComputeSuccessMean(cutoff), 260.0; got != want {
		t.Fatalf("success mean after UTC date change = %f, want %f", got, want)
	}
}
