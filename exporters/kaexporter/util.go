package main

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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

// getAnnotation returns the value of key from plr.Metadata.Annotations, or "" if absent.
func getAnnotation(plr PipelineRun, key string) string {
	if val, ok := plr.Metadata.Annotations[key]; ok {
		return val
	}
	return ""
}

// getResult returns the Reason of the "Succeeded" condition from a PipelineRun, or "Unknown".
func getResult(plr PipelineRun) string {
	for _, cond := range plr.Status.Conditions {
		if cond.Type == "Succeeded" {
			return cond.Reason
		}
	}
	return "Unknown"
}

// getReleaseResult returns the Released condition reason, or a normalized outcome from status.
func getReleaseResult(r Release) string {
	for _, cond := range r.Status.Conditions {
		if cond.Type != "Released" {
			continue
		}
		switch strings.ToLower(cond.Status) {
		case "true":
			if cond.Reason != "" {
				return cond.Reason
			}
			return "Succeeded"
		case "false":
			if cond.Reason != "" {
				return cond.Reason
			}
			return "Failed"
		}
	}
	return "Unknown"
}

// observeWithExemplar records value on obs with exemplar labels, falling back to plain
// Observe if obs does not implement prometheus.ExemplarObserver.
//
// Prometheus exemplar labels are capped at 128 UTF-8 characters total (including keys,
// equals signs, and commas in the serialized form). When the total length exceeds this
// limit, this function applies a smart truncation strategy:
//   1. Calculate the overhead (keys + separators)
//   2. If all values fit within the remaining budget, use them as-is
//   3. Otherwise, drop the least critical label (release_cr) and retry
//   4. If still too long, truncate from the END of each value to preserve unique prefixes
//
// This approach prioritizes data integrity: full names when possible, meaningful suffixes
// when truncation is needed (Tekton resource names often have unique suffixes like timestamps).
func observeWithExemplar(obs prometheus.Observer, value float64, exemplar prometheus.Labels) {
	if eo, ok := obs.(prometheus.ExemplarObserver); ok {
		const maxLen = 128

		// Helper to estimate serialized length: "k1=v1,k2=v2,..."
		calcLen := func(labels prometheus.Labels) int {
			total := 0
			first := true
			for k, v := range labels {
				if !first {
					total++ // comma
				}
				total += len(k) + 1 + len(v) // key + "=" + value
				first = false
			}
			return total
		}

		current := exemplar
		currentLen := calcLen(current)

		// Fast path: no truncation needed
		if currentLen <= maxLen {
			eo.ObserveWithExemplar(value, current)
			return
		}

		// Strategy 1: Drop the least critical label (release_cr) if present
		if _, hasRelease := current["release_cr"]; hasRelease && len(current) > 1 {
			fallback := make(prometheus.Labels, len(current)-1)
			for k, v := range current {
				if k != "release_cr" {
					fallback[k] = v
				}
			}
			if calcLen(fallback) <= maxLen {
				eo.ObserveWithExemplar(value, fallback)
				return
			}
			current = fallback
			currentLen = calcLen(current)
		}

		// Strategy 2: Truncate from the END to preserve unique prefixes
		// Resource names like "build-plr-abc123-r2d2c-20260528t120000z" have unique suffixes.
		budget := maxLen
		for k := range current {
			budget -= len(k) + 1 // key + "="
		}
		budget -= len(current) - 1 // commas

		if budget <= 0 {
			// Pathological case: too many labels. Drop all but pipelinerun.
			eo.ObserveWithExemplar(value, prometheus.Labels{"pipelinerun": current["pipelinerun"][:20]})
			return
		}

		perValue := budget / len(current)
		truncated := make(prometheus.Labels, len(current))
		for k, v := range current {
			if len(v) <= perValue {
				truncated[k] = v
			} else {
				// Take the LAST perValue characters to preserve unique suffixes
				truncated[k] = v[len(v)-perValue:]
			}
		}

		eo.ObserveWithExemplar(value, truncated)
	} else {
		obs.Observe(value)
	}
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

// getConditionTime returns the lastTransitionTime for a specific condition type.
// Returns empty string if the condition is not found or status is not "True".
func getConditionTime(conditions []Condition, conditionType string) string {
	for _, cond := range conditions {
		if cond.Type == conditionType && cond.Status == "True" {
			return cond.LastTransitionTime
		}
	}
	return ""
}
