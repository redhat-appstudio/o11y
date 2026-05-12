#!/usr/bin/env python3
"""Validate alert label conventions across PrometheusRule files."""
import glob
import sys
import yaml

ALERT_DIR = "rhobs/alerting/data_plane"
VALID_SEVERITIES = {"critical", "high", "warning", "info"}

def check_rules(path):
    errors = []
    with open(path) as f:
        doc = yaml.safe_load(f)

    for group in doc.get("spec", {}).get("groups", []):
        for rule in group.get("rules", []):
            alert = rule.get("alert")
            if not alert:
                continue
            labels = rule.get("labels", {})
            severity = labels.get("severity")
            slo = labels.get("slo")

            if severity not in VALID_SEVERITIES:
                errors.append(f"{path}: {alert}: invalid severity \"{severity}\" (must be one of {sorted(VALID_SEVERITIES)})")
            if slo == "true" and severity != "critical":
                errors.append(f"{path}: {alert}: slo is \"true\" but severity is not critical")

    return errors

def main():
    files = sorted(glob.glob(f"{ALERT_DIR}/*.yaml"))
    if not files:
        print(f"No alert files found in {ALERT_DIR}/")
        sys.exit(1)

    all_errors = []
    for f in files:
        all_errors.extend(check_rules(f))

    if all_errors:
        print("Alert convention violations found:")
        for e in all_errors:
            print(f"  {e}")
        sys.exit(1)

    print(f"check-alert-conventions: all {len(files)} files OK")

if __name__ == "__main__":
    main()
