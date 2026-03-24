package main

import (
	"fmt"
	"time"
)

// getLabel returns the value of key from obj's metadata labels, or defaultVal if absent.
func getLabel(obj interface{}, key, defaultVal string) string {
	switch v := obj.(type) {
	case PipelineRun:
		if val, ok := v.Metadata.Labels[key]; ok {
			return val
		}
	case Release:
		if val, ok := v.Metadata.Labels[key]; ok {
			return val
		}
	}
	return defaultVal
}

// secondsBetween parses two RFC3339 timestamps and returns the elapsed seconds.
// Returns -1 if either string is empty or unparseable.
func secondsBetween(start, end string) float64 {
	if start == "" || end == "" {
		return -1
	}

	startTime, err1 := time.Parse(time.RFC3339, start)
	endTime, err2 := time.Parse(time.RFC3339, end)

	if err1 != nil || err2 != nil {
		return -1
	}

	return endTime.Sub(startTime).Seconds()
}

// ── 30-day SLO helper functions ───────────────────────────────────────────────

// plrDedupeKey generates a unique key for deduplicating PipelineRun observations
func plrDedupeKey(namespace string, plr PipelineRun) string {
	if plr.Metadata.UID != "" {
		return "plr:" + plr.Metadata.UID
	}
	return fmt.Sprintf("plr:%s/%s", namespace, plr.Metadata.Name)
}

// releaseDedupeKey generates a unique key for deduplicating Release observations
func releaseDedupeKey(namespace, name string) string {
	return fmt.Sprintf("release:%s/%s", namespace, name)
}

// isPLRSucceeded checks if a PipelineRun has Succeeded=True condition
func isPLRSucceeded(plr PipelineRun) bool {
	for _, cond := range plr.Status.Conditions {
		if cond.Type == "Succeeded" {
			return cond.Status == "True"
		}
	}
	return false
}

// isReleaseSucceeded checks if a Release has Released=True condition
func isReleaseSucceeded(rel Release) bool {
	for _, cond := range rel.Status.Conditions {
		if cond.Type == "Released" {
			return cond.Status == "True"
		}
	}
	return false
}
