# Environment Drift Detection

## Overview

After the environment split (`rhobs/staging/` and `rhobs/production/`), alert and recording rules are duplicated across both directories. The drift detection tool (`scripts/check-env-drift.py`) compares these directories and flags differences that are not explicitly acknowledged.

## How it runs

### On every PR

When a PR touches files under `rhobs/`, the `check-drift` GitHub Actions workflow runs automatically. It:

1. Identifies which files the PR changed
2. Compares only those files between `staging/` and `production/`
3. Posts a comment on the PR with the result
4. Blocks merge if violations are found

The PR comment has three states:

| Comment | Meaning | CI |
|---|---|---|
| `✅ Passed` | No violations, no warnings | passes |
| `⚠️ Warning` | No violations, but warnings exist (e.g. staging-only rule or PR touches both envs) | passes |
| `❌ Failed` | Violations found — unacknowledged drift | blocks merge |

### Weekly report (scheduled)

A weekly workflow runs every Monday at 08:00 UTC. It performs a full scan of all files, when the drift was introduced, which commit, how many days ago, and uploads a markdown report as a GitHub Actions artifact.

### Running locally

```bash
# Full scan (same as weekly but without age info)
python3 scripts/check-env-drift.py

# Markdown report with drift age
python3 scripts/check-env-drift.py --report

# Dry-run with exit code 0
python3 scripts/check-env-drift.py --allow-fail
```

## Bypass mechanisms

| Situation | Result | Rationale |
|---|---|---|
| File/rule only in staging | warning | Staged rollout is expected workflow |
| File/rule only in production | **violation** | Production should not drift ahead of staging |
| Field differs between envs | **violation** | Must be explicitly acknowledged |

This supports the staged rollout model: land changes in staging first, validate, then promote to production. Going the other direction (production has something staging doesn't) is flagged as a problem.

In case it is needed, bypasses can be added as YAML comments. Similar to how linters would work.

### File-level: `drift:ignore-file`

Place `# drift:ignore-file <reason>` anywhere in the file. The entire file is skipped from comparison.

```yaml
# drift:ignore-file generated per-environment by generate-cluster-monitoring-alerts.sh
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
...
```

### Rule-level: `drift:ignore` above `- alert:`

Place `# drift:ignore <reason>` on the line above or on the same line as `- alert:` or `- record:`. The entire rule is skipped — all fields, labels, and annotations.

Use for staged rollouts (rule in staging but not yet in production) or intentionally env-specific rules.

```yaml
    # drift:ignore staging-first rollout, will promote to production after validation
    - alert: ContainerOOMKilled
      expr: ...
```

Or on the same line:

```yaml
    - alert: ContainerOOMKilled  # drift:ignore prod-only, RelEng runners only exist in production
      expr: ...
```

### Section-level: `drift:ignore` on `labels:` or `annotations:`

Place `# drift:ignore <reason>` on the `labels:` or `annotations:` line. All children under that section are bypassed.

```yaml
      labels:  # drift:ignore labels managed separately per environment
        severity: critical   <- bypassed
        component: runners   <- bypassed
      annotations:
        summary: ...         <- NOT bypassed (different section)
```

### Field-level: `drift:ignore` on the same line

Place `# drift:ignore <reason>` on the same line as the field. Only that specific field is skipped. Adjacent fields are not affected.

```yaml
      labels:
        severity: critical  # drift:ignore SPRE recommended critical severity for production
        slo: "true"         <- NOT bypassed, still compared
```

**Important:** a comment on the line **above** a field does NOT bypass it. This is by design — it prevents accidentally ignoring a field you didn't intend to.

```yaml
      labels:
        # drift:ignore this does NOT work for the line below
        severity: critical  <- still compared, will flag as drift
```

## Workflows

### Adding a new alert (staged rollout)

1. Add the alert to `rhobs/staging/` only
2. The drift check will show `⚠️ Warning` (staging-only rule) — CI passes
3. Validate the alert in the staging RHOBS datasource
4. In a follow-up PR, copy the alert to `rhobs/production/`
5. The drift check shows `✅ Passed` (or `⚠️ Warning` for touching both envs)

If you want the warning to become a clean pass in staging, add e.g. `# drift:ignore staging-first rollout` above the rule. Remove the comment when you add it to production.

### Modifying an existing alert in both environments

1. Make the same change in both `rhobs/staging/` and `rhobs/production/`
2. The drift check shows `⚠️ Warning` — "PR modifies both environments"
3. CI passes, but the reviewer is reminded that both envs are being changed simultaneously

## CLI reference

```
python3 scripts/check-env-drift.py [OPTIONS]

Options:
  --only FILE [FILE ...]   Only compare these files (relative to staging/production dir)
  --diff-base REF          Only report NEW drift vs a git ref (e.g. origin/main)
  --report                 Full markdown report with drift age. Always exits 0.
  --strict                 Treat warnings and acknowledged bypasses as violations
  --allow-fail             Log everything but always exit 0
```
