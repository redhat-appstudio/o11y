#!/bin/bash
# Test runner for check-env-drift.py scenarios.
# Usage: ./test/drift-detection/run-tests.sh
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${SCRIPT_DIR}/../.."
CHECKER="${REPO_ROOT}/scripts/check-env-drift.py"
TESTS_DIR="${SCRIPT_DIR}"

PASS=0
FAIL=0
ERRORS=""

run_test() {
  local scenario="$1"
  local description="$2"
  local expected_exit="$3"
  local expected_output="$4"
  local extra_flags="${5:-}"

  local dir="${TESTS_DIR}/${scenario}"
  local staging="${dir}/staging"
  local production="${dir}/production"

  local root_dir
  root_dir=$(mktemp -d)
  mkdir -p "${root_dir}/rhobs"
  mkdir -p "$staging" "$production"
  ln -s "$(cd "$staging" && pwd)" "${root_dir}/rhobs/staging"
  ln -s "$(cd "$production" && pwd)" "${root_dir}/rhobs/production"

  local output
  output=$(python3 "$CHECKER" --root "$root_dir" $extra_flags 2>&1)
  local actual_exit=$?

  rm -rf "$root_dir"

  local status="PASS"
  local reason=""

  if [ "$actual_exit" -ne "$expected_exit" ]; then
    status="FAIL"
    reason="expected exit $expected_exit, got $actual_exit"
  fi

  if [ -n "$expected_output" ] && ! echo "$output" | grep -qF -- "$expected_output"; then
    status="FAIL"
    reason="${reason:+$reason; }expected output to contain: '$expected_output'"
  fi

  if [ "$status" = "PASS" ]; then
    PASS=$((PASS + 1))
    printf "  %-4s %-14s %s\n" "OK" "$scenario" "$description"
  else
    FAIL=$((FAIL + 1))
    printf "  %-4s %-14s %s\n" "FAIL" "$scenario" "$description"
    printf "       reason: %s\n" "$reason"
    ERRORS="${ERRORS}\n--- ${scenario}: ${description} ---\n${output}\n"
  fi
}

echo "=== check-env-drift test suite ==="
echo ""
echo "A. File-level differences"
run_test "scenario_01" "File only in staging, no bypass → warn (exit 0)" \
  0 "[WARN]"
run_test "scenario_02" "File only in production, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_03" "File only in staging, drift:ignore-file → acknowledged" \
  0 "[bypass]"

echo ""
echo "B. Rule-level (file in both envs)"
run_test "scenario_04" "Rule only in staging, drift:ignore above rule → acknowledged" \
  0 "[bypass]"
run_test "scenario_05" "Rule only in staging, no bypass → warn (exit 0)" \
  0 "[WARN]"
run_test "scenario_06" "Rule only in production, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_07" "Rule only in production, drift:ignore above rule → acknowledged" \
  0 "[bypass]"

echo ""
echo "C. Field-level (rule in both envs)"
run_test "scenario_08" "expr differs, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_09" "severity differs, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_10" "slo differs, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_11" "expr differs, drift:ignore SAME LINE → pass" \
  0 "[bypass]"
run_test "scenario_12" "severity differs, drift:ignore LINE ABOVE → FAIL (not recognized)" \
  1 "[DRIFT]"
run_test "scenario_13" "for differs, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_14" "annotation differs, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_15" "extra label differs, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_16" "extra label differs, drift:ignore same line → pass" \
  0 "[bypass]"

echo ""
echo "D. Edge cases"
run_test "scenario_17" "drift:ignore-file, wildly different content → skipped" \
  0 "[bypass]"
run_test "scenario_18" "empty rules only in staging → info" \
  0 "no rules"
run_test "scenario_19" "identical files → nothing reported" \
  0 "No drift violations"
run_test "scenario_20" "multiple rules, one drifts → only that one fails" \
  1 "pod_alerts/PodNotReady"
run_test "scenario_21" "same alert name, different groups → separate (group_b drifts)" \
  1 "group_b/HighErrorRate"

run_test "scenario_27" "severity bypassed, slo still drifts → FAIL" \
  1 "[DRIFT]"
run_test "scenario_28" "for + label differences, no bypass → FAIL" \
  1 "[DRIFT]"
run_test "scenario_29" "subtle expr difference (extra exclusion) → FAIL" \
  1 "[DRIFT]"

echo ""
echo "E. Flag tests"
run_test "scenario_02" "--strict: warnings+bypasses become failures (reuse s02)" \
  1 "[DRIFT]" "--strict"
run_test "scenario_01" "--strict: warnings become failures (reuse s01)" \
  1 "warnings/bypasses treated as violations" "--strict"
run_test "scenario_08" "--allow-fail: violations logged but exit 0" \
  0 "--allow-fail: exiting 0" "--allow-fail"

run_test "scenario_24" "drift:ignore on SAME LINE as - alert: → acknowledged" \
  0 "[bypass]"
run_test "scenario_25" "drift:ignore on labels: → all label diffs bypassed" \
  0 "[bypass]"
run_test "scenario_26" "drift:ignore on annotations: → all annotation diffs bypassed" \
  0 "[bypass]"

echo ""
echo "F. Recording rules"
run_test "scenario_23" "record: rule expr differs → FAIL" \
  1 "[DRIFT]"

echo ""
echo "G. Report mode"
run_test "scenario_08" "report mode outputs markdown" \
  0 "# Environment Drift Report" "--report"
run_test "scenario_19" "report mode — no drift → sync message" \
  0 "No drift detected" "--report"

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "=== Failed test output ==="
  printf "$ERRORS"
  exit 1
fi
