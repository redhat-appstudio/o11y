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

// isReleaseSucceeded checks if a Release has Released=True condition with Succeeded reason
func isReleaseSucceeded(rel Release) bool {
	for _, cond := range rel.Status.Conditions {
		if cond.Type == "Released" {
			return cond.Status == "True" && cond.Reason == "Succeeded"
		}
	}
	return false
}

// extractBuildType parses the tekton.dev/pipeline label value and returns a
// low-cardinality build type category for Prometheus metrics.
//
// Categorization logic (order matters - first match wins):
//   - docker-build-multi-platform* → "docker-multi-arch-builds"
//   - docker-build* → "docker-builds"
//   - bundle-build* → "bundle-builds"
//   - standard-pipeline → "standard-builds"
//   - *-operator-bundle → "operator-bundle-builds" (OLM operator bundles)
//   - *-operator → "operator-builds" (OLM operators, even if prefixed with "fbc")
//   - *fbc* → "fbc-builds" (only if not operator-related)
//   - *rpm* → "rpm-builds"
//   - everything else → "custom-builds"
//
// This keeps cardinality low (8-9 unique values) across all clusters.
func extractBuildType(pipelineName string) string {
	if pipelineName == "" {
		return "unknown"
	}

	// Check prefixes first (most specific first)
	// Multi-arch container builds (MUST come before regular docker-build check)
	if len(pipelineName) >= 27 && pipelineName[:27] == "docker-build-multi-platform" {
		return "docker-multi-arch-builds"
	}

	// Regular container builds
	if len(pipelineName) >= 12 && pipelineName[:12] == "docker-build" {
		return "docker-builds"
	}

	// Bundle builds (OCI bundle artifacts)
	if len(pipelineName) >= 12 && pipelineName[:12] == "bundle-build" {
		return "bundle-builds"
	}

	// Standard pipeline (exact match)
	if pipelineName == "standard-pipeline" {
		return "standard-builds"
	}

	// Operator bundle builds (must come before operator builds check)
	// Examples: ose-4-23-local-storage-operator-bundle
	// In Konflux, bundle builds and operator builds are distinct components
	if containsSubstring(pipelineName, "-operator-bundle") {
		return "operator-bundle-builds"
	}

	// Operator builds (but not operator-bundle, which was caught above)
	// Examples: mtc-1-8-openshift-migration-operator, fbc-mtc-1-8-openshift-migration-operator
	// Note: Even if prefixed with "fbc", if it ends with "-operator" it's an operator build
	if containsSubstring(pipelineName, "-operator") {
		return "operator-builds"
	}

	// FBC builds (only if not already caught by operator checks above)
	// Examples: v419-cnv-fbc-on-push (has "fbc" but no "-operator" suffix)
	if containsSubstring(pipelineName, "fbc") {
		return "fbc-builds"
	}

	// RPM builds
	if containsSubstring(pipelineName, "rpm") {
		return "rpm-builds"
	}

	// Default: custom pipeline
	return "custom-builds"
}

// containsSubstring checks if s contains substr (case-sensitive)
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && indexOfSubstring(s, substr) >= 0
}

// indexOfSubstring returns the index of substr in s, or -1 if not found
func indexOfSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
