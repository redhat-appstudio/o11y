#!/usr/bin/env python3
"""
Detect configuration drift between staging/ and production/ alert and recording rules.

Compares PrometheusRule files across the environment split and reports differences.

Bypass mechanisms:

  1. Rule-level: add `# drift:ignore <reason>` on the line ABOVE `- alert:` or `- record:`.
     The entire rule is skipped from comparison. The reason is logged.

  2. Field-level: add `# drift:ignore <reason>` on the SAME LINE as the field.
     Only that specific field is skipped. Must be on the same line — a comment on the
     line above a field does NOT apply to it.

  3. File-level: add `# drift:ignore-file <reason>` anywhere in the file.
     The entire file is skipped from comparison. The reason is logged.

All bypasses are reported in the output so they remain visible (not silently hidden).

Direction-aware severity:
  - staging-only file/rule:    warning (staged rollout is normal)
  - production-only file/rule: violation (production must not drift ahead of staging)
  - field differs:             violation (unless bypassed with drift:ignore)

Modes:

  Default (PR check):
    python3 scripts/check-env-drift.py [--only FILE ...] [--diff-base REF]
    Reports drift in text format. With --diff-base, only reports NEW drift
    introduced since the given git ref (items not present at the base).

  Report (scheduled overview):
    python3 scripts/check-env-drift.py --report
    Full scan with markdown output including drift age from git history.
    Always exits 0.

Exit codes:
  0 — no violations (or --report / --allow-fail)
  1 — violations found (or --strict mode triggered)
"""
import argparse
import glob
import os
import re
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path

import yaml


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

DRIFT_IGNORE_RE = re.compile(r"#\s*drift:ignore\s+(.+)", re.IGNORECASE)
DRIFT_IGNORE_FILE_RE = re.compile(r"#\s*drift:ignore-file\s+(.+)", re.IGNORECASE)

STAGING_REL = os.path.join("rhobs", "staging")
PRODUCTION_REL = os.path.join("rhobs", "production")

REPO_URL = "https://github.com/redhat-appstudio/o11y"


# ---------------------------------------------------------------------------
# Repo root detection
# ---------------------------------------------------------------------------

def _detect_repo_root():
    try:
        r = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],
            capture_output=True, text=True, timeout=5,
        )
        if r.returncode == 0:
            return r.stdout.strip()
    except (subprocess.SubprocessError, FileNotFoundError):
        pass
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


# ---------------------------------------------------------------------------
# Structured drift item
# ---------------------------------------------------------------------------

def _drift_item(file_path, rule=None, field=None,
                staging_val=None, production_val=None, reason=None):
    return {
        "file": file_path,
        "rule": rule,
        "field": field,
        "staging_value": staging_val,
        "production_value": production_val,
        "reason": reason,
        "category": None,
        "drift_since": None,
        "drift_commit": None,
        "drift_commit_subject": None,
        "drift_age_days": None,
    }


def _item_key(item):
    return (item.get("file"), item.get("rule"), item.get("field"))


# ---------------------------------------------------------------------------
# Report
# ---------------------------------------------------------------------------

class DriftReport:

    def __init__(self):
        self.violations = []
        self.warnings = []
        self.acknowledged = []
        self.info = []
        self.items = []

    def add_violation(self, msg, item=None):
        self.violations.append(msg)
        if item is not None:
            item["category"] = "violation"
            self.items.append(item)

    def add_warning(self, msg, item=None):
        self.warnings.append(msg)
        if item is not None:
            item["category"] = "warning"
            self.items.append(item)

    def add_acknowledged(self, msg, item=None):
        self.acknowledged.append(msg)
        if item is not None:
            item["category"] = "acknowledged"
            self.items.append(item)

    def add_info(self, msg, item=None):
        self.info.append(msg)
        if item is not None:
            item["category"] = "info"
            self.items.append(item)

    @property
    def ok(self):
        return len(self.violations) == 0

    def print_report(self):
        if self.info:
            print("\n--- Info ---")
            for m in self.info:
                print(f"  {m}")

        if self.warnings:
            print(f"\n--- Warnings ({len(self.warnings)}) ---")
            for m in self.warnings:
                print(f"  [WARN] {m}")

        if self.acknowledged:
            print(f"\n--- Acknowledged bypasses ({len(self.acknowledged)}) ---")
            for m in self.acknowledged:
                print(f"  [bypass] {m}")

        if self.violations:
            print(f"\n--- Drift violations ({len(self.violations)}) ---")
            for m in self.violations:
                print(f"  [DRIFT] {m}")
        else:
            print("\n--- No drift violations found ---")

    @staticmethod
    def _commit_link(short_hash):
        if not short_hash:
            return "-"
        return f"[`{short_hash}`]({REPO_URL}/commit/{short_hash})"

    def to_markdown(self):
        lines = []
        now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
        lines.append("# Environment Drift Report")
        lines.append(f"Generated: {now}")
        lines.append("")

        lines.append("## Summary")
        lines.append("| Metric | Count |")
        lines.append("|--------|-------|")
        lines.append(f"| Violations | {len(self.violations)} |")
        lines.append(f"| Warnings | {len(self.warnings)} |")
        lines.append(f"| Acknowledged bypasses | {len(self.acknowledged)} |")
        lines.append("")

        drift_items = [i for i in self.items if i["category"] in ("violation", "warning")]
        if drift_items:
            drift_items.sort(key=lambda x: (
                x.get("drift_age_days") is None,
                -(x.get("drift_age_days") or 0),
            ))

            lines.append("## Drift Items")
            lines.append("")
            lines.append("| # | File | Rule | Field | Type | Since | Age |")
            lines.append("|---|------|------|-------|------|-------|-----|")
            for idx, item in enumerate(drift_items, 1):
                since = (item.get("drift_since") or "unknown")[:10]
                age = f"{item['drift_age_days']}d" if item.get("drift_age_days") is not None else "?"
                field = item.get("field") or "-"
                rule = item.get("rule") or "-"
                lines.append(
                    f"| {idx} | {item.get('file', '')} | {rule} | {field} "
                    f"| {item['category']} | {since} | {age} |"
                )
            lines.append("")

            lines.append("## Details")
            lines.append("")
            for idx, item in enumerate(drift_items, 1):
                lines.append(f"### {idx}. {item.get('file', '')} :: {item.get('rule', '-')}")
                if item.get("field"):
                    lines.append(f"**Field:** `{item['field']}`")
                if item.get("drift_since"):
                    commit_info = ""
                    if item.get("drift_commit"):
                        commit_info = f" ({self._commit_link(item['drift_commit'])}"
                        if item.get("drift_commit_subject"):
                            commit_info += f": {item['drift_commit_subject']}"
                        commit_info += ")"
                    lines.append(f"**Drift since:** {item['drift_since'][:10]}{commit_info}")
                if item.get("staging_value") is not None:
                    lines.append(f"**Staging:** `{item['staging_value']}`")
                if item.get("production_value") is not None:
                    lines.append(f"**Production:** `{item['production_value']}`")
                lines.append("")

        ack_items = [i for i in self.items if i["category"] == "acknowledged"]
        if ack_items:
            lines.append("## Acknowledged Bypasses")
            lines.append("")
            lines.append("| File | Rule | Reason | Since | Commit | Age |")
            lines.append("|------|------|--------|-------|--------|-----|")
            for item in ack_items:
                since = (item.get("drift_since") or "unknown")[:10]
                age = f"{item['drift_age_days']}d" if item.get("drift_age_days") is not None else "?"
                commit = self._commit_link(item.get("drift_commit"))
                lines.append(
                    f"| {item.get('file', '')} | {item.get('rule', '-')} "
                    f"| {item.get('reason', '')} | {since} | {commit} | {age} |"
                )
            lines.append("")

        if not drift_items and not ack_items:
            lines.append("## No drift detected")
            lines.append("")
            lines.append("All staging and production configurations are in sync.")
            lines.append("")

        return "\n".join(lines)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def relative(path, base):
    try:
        return str(Path(path).relative_to(base))
    except ValueError:
        return path


def load_yaml_and_raw(filepath):
    with open(filepath) as f:
        raw_lines = f.readlines()
    with open(filepath) as f:
        doc = yaml.safe_load(f)
    return doc, raw_lines


def find_inline_ignores(raw_lines):
    ignores = {}
    for i, line in enumerate(raw_lines, start=1):
        m = DRIFT_IGNORE_RE.search(line)
        if m:
            ignores[i] = m.group(1).strip()
    return ignores


def has_file_level_ignore(raw_lines):
    for line in raw_lines:
        m = DRIFT_IGNORE_FILE_RE.search(line)
        if m:
            return m.group(1).strip()
    return None


def find_rule_line_range(raw_lines, rule_name, occurrence=1):
    """Find the Nth occurrence of a rule in raw lines (1-based)."""
    start = None
    found = 0
    for i, line in enumerate(raw_lines, start=1):
        stripped = line.split("#")[0].strip() if "#" in line else line.strip()
        if stripped in (f"- alert: {rule_name}", f"alert: {rule_name}",
                        f"- record: {rule_name}", f"record: {rule_name}"):
            found += 1
            if found == occurrence:
                start = i
        elif start is not None and stripped.startswith(("- alert:", "- record:", "- name:")):
            return (start, i - 1)
    if start is not None:
        return (start, len(raw_lines))
    return None


def find_key_in_range(raw_lines, key, start, end):
    for i in range(start, end + 1):
        stripped = raw_lines[i - 1].strip()
        if stripped.startswith(f"{key}:"):
            return i
    return None


def check_same_line_ignore(line_num, ignores):
    return ignores.get(line_num)


def check_line_or_above_for_ignore(raw_lines, line_num, ignores):
    if line_num in ignores:
        return ignores[line_num]
    if (line_num - 1) in ignores:
        return ignores[line_num - 1]
    return None


def _format_value(val):
    s = str(val).rstrip()
    lines = s.split("\n")
    if len(lines) == 1:
        return s
    indent = " " * 20
    return ("\n" + indent).join(line.rstrip() for line in lines)


# ---------------------------------------------------------------------------
# Rule extraction
# ---------------------------------------------------------------------------

def extract_rules(doc):
    rules = {}
    if not doc or "spec" not in doc:
        return rules
    for group in doc["spec"].get("groups", []):
        group_name = group.get("name", "<unnamed>")
        seen = {}
        for rule in group.get("rules", []):
            name = rule.get("alert") or rule.get("record")
            if not name:
                continue
            seen[name] = seen.get(name, 0) + 1
            if seen[name] > 1:
                key = f"{group_name}/{name}#{seen[name]}"
            else:
                key = f"{group_name}/{name}"
            rule["_occurrence"] = seen[name]
            rules[key] = rule
    return rules


# ---------------------------------------------------------------------------
# Comparison engine
# ---------------------------------------------------------------------------

def _is_rule_fully_ignored(rule, raw_lines):
    rule_name = rule.get("alert") or rule.get("record")
    occurrence = rule.get("_occurrence", 1)
    rng = find_rule_line_range(raw_lines, rule_name, occurrence=occurrence)
    if rng:
        ignores = find_inline_ignores(raw_lines)
        return check_line_or_above_for_ignore(raw_lines, rng[0], ignores)
    return None


def compare_rules(rule_key, rel_path, rule_name, staging_rule, production_rule,
                  staging_lines, production_lines, report):
    s_rule_ignore = _is_rule_fully_ignored(staging_rule, staging_lines)
    p_rule_ignore = _is_rule_fully_ignored(production_rule, production_lines)
    if s_rule_ignore or p_rule_ignore:
        reason = s_rule_ignore or p_rule_ignore
        report.add_acknowledged(
            f"{rule_key}: entire rule bypassed — {reason}",
            item=_drift_item(rel_path, rule=rule_name, reason=reason),
        )
        return

    staging_labels = staging_rule.get("labels", {})
    production_labels = production_rule.get("labels", {})

    staging_ignores = find_inline_ignores(staging_lines)
    production_ignores = find_inline_ignores(production_lines)

    alert_or_record = staging_rule.get("alert") or staging_rule.get("record")
    s_occurrence = staging_rule.get("_occurrence", 1)
    p_occurrence = production_rule.get("_occurrence", 1)
    s_range = find_rule_line_range(staging_lines, alert_or_record, occurrence=s_occurrence)
    p_range = find_rule_line_range(production_lines, alert_or_record, occurrence=p_occurrence)

    def field_ignored_in_either(field):
        for lines, ignores, rng in [
            (staging_lines, staging_ignores, s_range),
            (production_lines, production_ignores, p_range),
        ]:
            if not rng:
                continue
            ln = find_key_in_range(lines, field, rng[0], rng[1])
            if ln:
                reason = check_same_line_ignore(ln, ignores)
                if reason:
                    return reason
        return None

    def section_ignored_in_either(section_key):
        for lines, ignores, rng in [
            (staging_lines, staging_ignores, s_range),
            (production_lines, production_ignores, p_range),
        ]:
            if not rng:
                continue
            ln = find_key_in_range(lines, section_key, rng[0], rng[1])
            if ln:
                reason = check_same_line_ignore(ln, ignores)
                if reason:
                    return reason
        return None

    all_fields = set()
    all_fields.update(staging_rule.keys(), production_rule.keys())

    for field in sorted(all_fields):
        if field in ("labels", "annotations", "_occurrence"):
            continue

        s_val = staging_rule.get(field)
        p_val = production_rule.get(field)

        if s_val == p_val:
            continue

        ignore_reason = field_ignored_in_either(field)
        if ignore_reason:
            report.add_acknowledged(
                f"{rule_key}: field '{field}' differs — bypass: {ignore_reason}",
                item=_drift_item(rel_path, rule=rule_name, field=field,
                                 staging_val=s_val, production_val=p_val,
                                 reason=ignore_reason),
            )
            continue

        report.add_violation(
            f"{rule_key}: field '{field}' differs between staging and production\n"
            f"        staging:    {_format_value(s_val)}\n"
            f"        production: {_format_value(p_val)}",
            item=_drift_item(rel_path, rule=rule_name, field=field,
                             staging_val=s_val, production_val=p_val),
        )

    labels_ignore = section_ignored_in_either("labels")
    all_label_keys = sorted(set(list(staging_labels.keys()) + list(production_labels.keys())))
    for lk in all_label_keys:
        s_lv = staging_labels.get(lk)
        p_lv = production_labels.get(lk)
        if s_lv == p_lv:
            continue

        if labels_ignore:
            report.add_acknowledged(
                f"{rule_key}: label '{lk}' differs — bypass (labels section): {labels_ignore}",
                item=_drift_item(rel_path, rule=rule_name, field=f"label:{lk}",
                                 staging_val=s_lv, production_val=p_lv,
                                 reason=labels_ignore),
            )
            continue

        ignore_reason = field_ignored_in_either(lk)
        if ignore_reason:
            report.add_acknowledged(
                f"{rule_key}: label '{lk}' differs — bypass: {ignore_reason}",
                item=_drift_item(rel_path, rule=rule_name, field=f"label:{lk}",
                                 staging_val=s_lv, production_val=p_lv,
                                 reason=ignore_reason),
            )
        else:
            report.add_violation(
                f"{rule_key}: label '{lk}' differs between staging and production\n"
                f"        staging:    {_format_value(s_lv)}\n"
                f"        production: {_format_value(p_lv)}",
                item=_drift_item(rel_path, rule=rule_name, field=f"label:{lk}",
                                 staging_val=s_lv, production_val=p_lv),
            )

    annotations_ignore = section_ignored_in_either("annotations")
    s_ann = staging_rule.get("annotations", {})
    p_ann = production_rule.get("annotations", {})
    for ann_key in sorted(set(list(s_ann.keys()) + list(p_ann.keys()))):
        s_v = s_ann.get(ann_key)
        p_v = p_ann.get(ann_key)
        if s_v == p_v:
            continue

        if annotations_ignore:
            report.add_acknowledged(
                f"{rule_key}: annotation '{ann_key}' differs — bypass (annotations section): {annotations_ignore}",
                item=_drift_item(rel_path, rule=rule_name, field=f"annotation:{ann_key}",
                                 staging_val=s_v, production_val=p_v,
                                 reason=annotations_ignore),
            )
            continue

        ignore_reason = field_ignored_in_either(ann_key)
        if ignore_reason:
            report.add_acknowledged(
                f"{rule_key}: annotation '{ann_key}' differs — bypass: {ignore_reason}",
                item=_drift_item(rel_path, rule=rule_name, field=f"annotation:{ann_key}",
                                 staging_val=s_v, production_val=p_v,
                                 reason=ignore_reason),
            )
        else:
            report.add_violation(
                f"{rule_key}: annotation '{ann_key}' differs between staging and production\n"
                f"        staging:    {_format_value(s_v)}\n"
                f"        production: {_format_value(p_v)}",
                item=_drift_item(rel_path, rule=rule_name, field=f"annotation:{ann_key}",
                                 staging_val=s_v, production_val=p_v),
            )


def _check_rule_bypass(rule, raw_lines):
    return _is_rule_fully_ignored(rule, raw_lines)


# ---------------------------------------------------------------------------
# File-pair matching
# ---------------------------------------------------------------------------

def build_file_map(env_dir):
    files = {}
    for ext in ("**/*.yaml", "**/*.yml"):
        for filepath in sorted(glob.glob(os.path.join(env_dir, ext), recursive=True)):
            rel = relative(filepath, env_dir)
            files[rel] = filepath
    return files


def check_env_drift(staging_dir, production_dir, report, only_files=None):
    staging_files = build_file_map(staging_dir)
    production_files = build_file_map(production_dir)

    all_rel_paths = sorted(set(list(staging_files.keys()) + list(production_files.keys())))

    if only_files:
        all_rel_paths = [p for p in all_rel_paths if p in only_files]

    for rel_path in all_rel_paths:
        s_path = staging_files.get(rel_path)
        p_path = production_files.get(rel_path)

        if not s_path or not p_path:
            only_env = "staging" if s_path else "production"
            only_path = s_path or p_path
            doc, raw_lines = load_yaml_and_raw(only_path)

            file_ignore = has_file_level_ignore(raw_lines)
            if file_ignore:
                report.add_acknowledged(
                    f"{rel_path}: exists only in {only_env} — file bypass: {file_ignore}",
                    item=_drift_item(rel_path, reason=file_ignore),
                )
                continue

            rules = extract_rules(doc)
            if not rules:
                report.add_info(
                    f"{rel_path}: exists only in {only_env} — file has no rules (empty or disabled)",
                    item=_drift_item(rel_path, reason=f"exists only in {only_env} — file has no rules (empty or disabled)"),
                )
                continue

            if only_env == "staging":
                report.add_warning(
                    f"{rel_path}: exists only in staging",
                    item=_drift_item(rel_path, reason="exists only in staging"),
                )
            else:
                report.add_violation(
                    f"{rel_path}: exists only in production",
                    item=_drift_item(rel_path, reason="exists only in production"),
                )
            continue

        s_doc, s_lines = load_yaml_and_raw(s_path)
        p_doc, p_lines = load_yaml_and_raw(p_path)

        s_file_ignore = has_file_level_ignore(s_lines)
        p_file_ignore = has_file_level_ignore(p_lines)
        if s_file_ignore or p_file_ignore:
            reason = s_file_ignore or p_file_ignore
            report.add_acknowledged(
                f"{rel_path}: file-level bypass in {'staging' if s_file_ignore else 'production'}: {reason}",
                item=_drift_item(rel_path, reason=reason),
            )
            continue

        s_rules = extract_rules(s_doc)
        p_rules = extract_rules(p_doc)

        all_rule_keys = sorted(set(list(s_rules.keys()) + list(p_rules.keys())))

        for rk in all_rule_keys:
            s_rule = s_rules.get(rk)
            p_rule = p_rules.get(rk)

            if s_rule and not p_rule:
                bypass = _check_rule_bypass(s_rule, s_lines)
                if bypass:
                    report.add_acknowledged(
                        f"{rel_path}: rule '{rk}' exists only in staging — {bypass}",
                        item=_drift_item(rel_path, rule=rk, reason=bypass),
                    )
                else:
                    report.add_warning(
                        f"{rel_path}: rule '{rk}' exists only in staging",
                        item=_drift_item(rel_path, rule=rk, reason="exists only in staging"),
                    )
                continue

            if p_rule and not s_rule:
                bypass = _check_rule_bypass(p_rule, p_lines)
                if bypass:
                    report.add_acknowledged(
                        f"{rel_path}: rule '{rk}' exists only in production — {bypass}",
                        item=_drift_item(rel_path, rule=rk, reason=bypass),
                    )
                else:
                    report.add_violation(
                        f"{rel_path}: rule '{rk}' exists only in production",
                        item=_drift_item(rel_path, rule=rk, reason="exists only in production"),
                    )
                continue

            compare_rules(
                rule_key=f"{rel_path} :: {rk}",
                rel_path=rel_path,
                rule_name=rk,
                staging_rule=s_rule,
                production_rule=p_rule,
                staging_lines=s_lines,
                production_lines=p_lines,
                report=report,
            )


# ---------------------------------------------------------------------------
# Diff-aware check (--diff-base)
# ---------------------------------------------------------------------------

def _extract_base_files(base_ref, staging_dir, production_dir, only_files, tmp_dir):
    """Extract file versions from a git ref into temp staging/production dirs."""
    tmp_staging = os.path.join(tmp_dir, "staging")
    tmp_production = os.path.join(tmp_dir, "production")

    staging_files = build_file_map(staging_dir)
    production_files = build_file_map(production_dir)

    all_rel = set(list(staging_files.keys()) + list(production_files.keys()))
    if only_files:
        all_rel = {p for p in all_rel if p in only_files}

    for rel_path in all_rel:
        for env_label, env_rel_dir in [("staging", STAGING_REL), ("production", PRODUCTION_REL)]:
            git_path = os.path.join(env_rel_dir, rel_path)
            tmp_env = tmp_staging if env_label == "staging" else tmp_production
            dest = os.path.join(tmp_env, rel_path)

            try:
                r = subprocess.run(
                    ["git", "show", f"{base_ref}:{git_path}"],
                    capture_output=True, text=True, timeout=10,
                )
                if r.returncode == 0:
                    os.makedirs(os.path.dirname(dest), exist_ok=True)
                    with open(dest, "w") as f:
                        f.write(r.stdout)
            except (subprocess.SubprocessError, FileNotFoundError):
                pass

    return tmp_staging, tmp_production


def run_diff_aware_check(staging_dir, production_dir, base_ref, only_files=None):
    """Run drift check on both base and current, return only new items."""
    tmp_dir = tempfile.mkdtemp(prefix="drift-base-")
    try:
        base_staging, base_production = _extract_base_files(
            base_ref, staging_dir, production_dir, only_files, tmp_dir,
        )

        base_report = DriftReport()
        os.makedirs(base_staging, exist_ok=True)
        os.makedirs(base_production, exist_ok=True)
        check_env_drift(base_staging, base_production, base_report, only_files=only_files)

        current_report = DriftReport()
        check_env_drift(staging_dir, production_dir, current_report, only_files=only_files)

        base_keys = {_item_key(item) for item in base_report.items}

        diff_report = DriftReport()
        for item in current_report.items:
            if _item_key(item) not in base_keys:
                msg = _rebuild_text_message(item)
                if item["category"] == "violation":
                    diff_report.add_violation(msg, item=item)
                elif item["category"] == "warning":
                    diff_report.add_warning(msg, item=item)
                elif item["category"] == "acknowledged":
                    diff_report.add_acknowledged(msg, item=item)
                elif item["category"] == "info":
                    diff_report.add_info(msg, item=item)

        return diff_report
    finally:
        shutil.rmtree(tmp_dir, ignore_errors=True)


def _rebuild_text_message(item):
    """Reconstruct a human-readable message from a structured item."""
    parts = []
    file_path = item.get("file", "")
    rule = item.get("rule", "")
    field = item.get("field", "")
    reason = item.get("reason", "")

    if rule and field:
        key = f"{file_path} :: {rule}"
        s_val = item.get("staging_value")
        p_val = item.get("production_value")
        if reason:
            parts.append(f"{key}: field '{field}' differs — bypass: {reason}")
        elif s_val is not None or p_val is not None:
            parts.append(
                f"{key}: field '{field}' differs between staging and production\n"
                f"        staging:    {_format_value(s_val)}\n"
                f"        production: {_format_value(p_val)}"
            )
        else:
            parts.append(f"{key}: field '{field}' differs")
    elif rule:
        if reason:
            parts.append(f"{file_path}: rule '{rule}' — {reason}")
        else:
            parts.append(f"{file_path}: rule '{rule}'")
    elif reason:
        parts.append(f"{file_path}: {reason}")
    else:
        parts.append(file_path)

    return parts[0] if parts else file_path


# ---------------------------------------------------------------------------
# Git drift age analysis (--report)
# ---------------------------------------------------------------------------

def _git_file_divergence(staging_path, production_path, max_commits=100):
    """Find when staging and production versions of a file first diverged.

    Walks git history from newest to oldest. Returns (iso_date, short_hash, subject)
    of the commit that introduced the divergence, or None if files have always matched.
    """
    try:
        r = subprocess.run(
            ["git", "log", "--format=%H %aI %s", f"--max-count={max_commits}",
             "--", staging_path, production_path],
            capture_output=True, text=True, timeout=30,
        )
        if r.returncode != 0 or not r.stdout.strip():
            return None
    except (subprocess.SubprocessError, FileNotFoundError):
        return None

    commits = []
    for line in r.stdout.strip().split("\n"):
        if not line.strip():
            continue
        parts = line.split(" ", 2)
        if len(parts) >= 2:
            commits.append((parts[0], parts[1], parts[2] if len(parts) > 2 else ""))

    last_diverging = None
    for commit_hash, commit_date, commit_subject in commits:
        try:
            s = subprocess.run(
                ["git", "show", f"{commit_hash}:{staging_path}"],
                capture_output=True, text=True, timeout=10,
            )
            p = subprocess.run(
                ["git", "show", f"{commit_hash}:{production_path}"],
                capture_output=True, text=True, timeout=10,
            )
        except subprocess.TimeoutExpired:
            continue

        s_exists = s.returncode == 0
        p_exists = p.returncode == 0

        if not s_exists and not p_exists:
            break

        if s_exists and p_exists and s.stdout == p.stdout:
            if last_diverging:
                return last_diverging
            return None

        last_diverging = (commit_date, commit_hash[:10], commit_subject)

    if last_diverging:
        return last_diverging
    return None


def compute_drift_ages(items, staging_dir, production_dir):
    cache = {}

    for item in items:
        rel_file = item.get("file")
        if not rel_file:
            continue

        if rel_file not in cache:
            staging_path = os.path.join(STAGING_REL, rel_file)
            production_path = os.path.join(PRODUCTION_REL, rel_file)
            cache[rel_file] = _git_file_divergence(staging_path, production_path)

        divergence = cache[rel_file]
        if divergence:
            iso_date, short_hash, subject = divergence
            item["drift_since"] = iso_date
            item["drift_commit"] = short_hash
            item["drift_commit_subject"] = subject
            try:
                dt = datetime.fromisoformat(iso_date)
                item["drift_age_days"] = (datetime.now(timezone.utc) - dt).days
            except (ValueError, TypeError):
                pass


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Detect configuration drift between staging and production rule directories.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "--report", action="store_true",
        help="Full scan with markdown output including drift age. Always exits 0.",
    )
    parser.add_argument(
        "--diff-base", metavar="REF",
        help="Only report drift introduced since this git ref (e.g. origin/main). "
             "Compares current state against the base ref and filters out pre-existing drift.",
    )
    parser.add_argument(
        "--strict", action="store_true",
        help="Treat warnings and acknowledged bypasses as violations.",
    )
    parser.add_argument(
        "--allow-fail", action="store_true",
        help="Log everything but always exit 0 (dry-run mode).",
    )
    parser.add_argument(
        "--only", nargs="*", metavar="FILE",
        help="Only compare these relative paths (e.g. alerting/data-plane/alerts/foo.yaml). "
             "Paths are relative to the staging/production dir. "
             "Ignored when --report is used.",
    )
    parser.add_argument(
        "--root", metavar="DIR",
        help=argparse.SUPPRESS,  # test-only: override repo root
    )
    args = parser.parse_args()

    if args.strict and args.allow_fail:
        print("Error: --strict and --allow-fail are mutually exclusive")
        sys.exit(2)

    if args.report and args.diff_base:
        print("Error: --report and --diff-base are mutually exclusive")
        sys.exit(2)

    repo_root = args.root if args.root else _detect_repo_root()
    staging_dir = os.path.join(repo_root, STAGING_REL)
    production_dir = os.path.join(repo_root, PRODUCTION_REL)

    if not os.path.isdir(staging_dir):
        print(f"Error: staging directory not found: {staging_dir}")
        sys.exit(2)
    if not os.path.isdir(production_dir):
        print(f"Error: production directory not found: {production_dir}")
        sys.exit(2)

    only_files = set(args.only) if args.only else None

    # --report mode: full scan, markdown output, drift age, always exit 0
    if args.report:
        report = DriftReport()
        check_env_drift(staging_dir, production_dir, report)
        compute_drift_ages(report.items, staging_dir, production_dir)
        print(report.to_markdown())
        sys.exit(0)

    # --diff-base mode: only report new drift vs. base ref
    if args.diff_base:
        report = run_diff_aware_check(
            staging_dir, production_dir, args.diff_base, only_files=only_files,
        )
    else:
        report = DriftReport()
        check_env_drift(staging_dir, production_dir, report, only_files=only_files)

    report.print_report()

    if args.allow_fail:
        print("\n--allow-fail: exiting 0 regardless of findings")
        sys.exit(0)

    if args.strict and (report.warnings or report.acknowledged):
        count = len(report.warnings) + len(report.acknowledged)
        print(f"\n--strict: {count} warnings/bypasses treated as violations")
        sys.exit(1)

    if not report.ok:
        sys.exit(1)

    print("\ncheck-env-drift: OK")
    sys.exit(0)


if __name__ == "__main__":
    main()
