package main

import (
	"fmt"
	"strings"
	"time"
)

// Supported types: PipelineRun, Release.
func getLabel[T PipelineRun | Release](obj T, key, defaultVal string) string {
	// Use type assertion to access Metadata.Labels
	switch v := any(obj).(type) {
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

// secondsBetween parses two timestamps and returns the elapsed seconds.
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

// plrStatus returns (succeeded, failureReason) for a PipelineRun.
// For failed PLRs, failureReason comes from Condition.Reason.
// Common failure reasons: CouldntGetPipeline, CouldntGetTask, CreateRunFailed,
// PipelineRunTimeout, Failed, or "Unknown" if reason is empty.
func plrStatus(plr PipelineRun) (succeeded bool, failureReason string) {
	for _, cond := range plr.Status.Conditions {
		if cond.Type == "Succeeded" {
			if cond.Status == "True" {
				return true, ""
			}
			// Failed: extract reason
			reason := cond.Reason
			if reason == "" {
				reason = "Unknown"
			}
			return false, reason
		}
	}
	// No Succeeded condition found
	return false, "Unknown"
}

// isPLRSucceeded checks if a PipelineRun has Succeeded=True condition.
// Kept for backward compatibility - wraps plrStatus().
func isPLRSucceeded(plr PipelineRun) bool {
	succeeded, _ := plrStatus(plr)
	return succeeded
}

// releaseStatus returns (completed, succeeded, failureReason) for a Release.
// completed=false means release is still in progress (should be skipped from metrics).
// succeeded=true means release completed successfully.
// failureReason is set only when completed=true AND succeeded=false.
//
// CRITICAL: Releases with Status="False" AND Reason="Progressing" are NOT completed
// (still running), and should be excluded from all metrics.
func releaseStatus(rel Release) (completed bool, succeeded bool, failureReason string) {
	for _, cond := range rel.Status.Conditions {
		if cond.Type == "Released" {
			if cond.Status == "True" && cond.Reason == "Succeeded" {
				return true, true, "" // Completed successfully
			}

			if cond.Status == "False" {
				// CRITICAL: "Progressing" means still running, not failed
				if cond.Reason == "Progressing" {
					return false, false, "" // Not completed yet - SKIP
				}

				// Status="False" with other reasons = terminal failure
				// Common reasons: Failed, Skipped
				reason := cond.Reason
				if reason == "" {
					reason = "Unknown"
				}
				return true, false, reason // Completed with failure
			}
		}
	}
	// No "Released" condition found - treat as incomplete
	return false, false, ""
}

// isReleaseSucceeded checks if a Release has Released=True condition with Succeeded reason.
// Kept for backward compatibility - wraps releaseStatus().
func isReleaseSucceeded(rel Release) bool {
	completed, succeeded, _ := releaseStatus(rel)
	return completed && succeeded
}

// isReleaseCompleted checks if a release has finished (success or failure).
// Returns false if still in progress (Status="False" + Reason="Progressing").
func isReleaseCompleted(rel Release) bool {
	completed, _, _ := releaseStatus(rel)
	return completed
}

// extractBuildType parses the tekton.dev/pipeline label value and returns a
// low-cardinality build type category for Prometheus metrics.
func extractBuildType(pipelineName string) string {
	if pipelineName == "" {
		return "unknown"
	}

	// Multi-arch container builds
	if strings.HasPrefix(pipelineName, "docker-build-multi-platform") {
		return "docker-multi-arch-builds"
	}

	// Regular container builds
	if strings.HasPrefix(pipelineName, "docker-build") {
		return "docker-builds"
	}

	// Bundle builds (OCI bundle artifacts)
	if strings.HasPrefix(pipelineName, "bundle-build") {
		return "bundle-builds"
	}

	// Standard pipeline
	if pipelineName == "standard-pipeline" {
		return "standard-builds"
	}

	// Operator bundle builds
	if strings.Contains(pipelineName, "-operator-bundle") {
		return "operator-bundle-builds"
	}

	// Operator builds
	if strings.Contains(pipelineName, "-operator") {
		return "operator-builds"
	}

	// FBC builds
	if strings.Contains(pipelineName, "fbc") {
		return "fbc-builds"
	}

	// RPM builds
	if strings.Contains(pipelineName, "rpm") {
		return "rpm-builds"
	}

	// Default: custom pipeline
	return "custom-builds"
}
