# Konflux Alerting: Architecture, Routing, and Ownership

## Why RHOBS and AppSRE?

Konflux launched under an **AppSRE support agreement** (~early 2023) that required centralized metrics visibility. This ruled out per-cluster Prometheus and mandated **RHOBS** — Red Hat's internal Thanos-based observability federation. AppSRE's operational model required all alerts, dashboards, and routing to live in **app-interface**, their lifecycle management monorepo hosted on internal GitLab.

Key documents:
- [Support agreement](https://gitlab.cee.redhat.com/service/app-interface/-/blob/3e7c3dbf964a603de81e3568f6cc5a4f708b312c/docs/stonesoup/support-agreement.md)
- [RHOBS federation doc](https://docs.google.com/document/d/1Laphmtpb_n-YV20CPKe2MSikgHxuS14p8Iud2_CLso0)
- [Original grooming doc (Dec 2022)](https://docs.google.com/document/d/1CnDiXMvetAblMZesbhAaIlqNXZxLE51Eu1C4FiEqAN0)

## How It Evolved

| When | What | Evidence |
|------|------|----------|
| Dec 2022 | Original plan: per-cluster Prometheus + Catchpoint + PagerDuty. SLOs out of scope. | Grooming doc; RHTAPWATCH-2370 (was -330) |
| Mar 2023 | Pivot to RHOBS for centralized federation, driven by AppSRE support agreement | RHTAPWATCH-1408; commit `3a3bf6b` |
| Mid 2023 | Alert routing conventions established (`alert_routing_key`, `alert_team_handle`) | RHTAPWATCH-2025 (was -734); `docs/ALERTS.md` |
| Late 2023 | `konflux_up` replaces Catchpoint as availability signal | RHTAPWATCH-2005 (was -823); RHTAPWATCH-1919 (was -890) |
| Mid 2024 | SPRE takes over operations — holds PagerDuty pager, owns incident response | SPRE-576, SPRE-1366 |
| 2025 | Tiered alerting initiative; PagerDuty Event Orchestration | SPRE-4705; KONFLUX-12491 |

Note: RHTAPWATCH tickets were renumbered during a Jira migration. The table shows current IDs with original IDs in parentheses.

## Current Ownership Split

**SPRE controls** (operational surface):
- PagerDuty on-call schedule and incident response (SPRE-576)
- Catchpoint → PagerDuty integration (SPRE-1434)

**O11Y controls**
- Alert rule definitions in this repo (`rhobs/alerting/`)
- Alertmanager routing configuration
- Grafana dashboard definitions

**AppSRE still controls** (platform infrastructure):
- RHOBS itself (Prometheus/Thanos federation)
- Grafana instances (hosting, not content)
- app-interface repo — SPRE and O11Y submits MRs but AppSRE owns the repo (SPRE-4916/17/18 document onboarding SPRE members)

The original forcing function — the AppSRE support agreement requiring centralized visibility — may no longer be as binding now that SPRE holds the pager rather than AppSRE. The dependency on app-interface for alert deployment and routing config remains a practical constraint.

## Jira Hierarchy

HATSTRAT-255 ("Konflux") → KONFLUX-373 ("Production Builds for SAAS Teams") → KONFLUX-1219 ("Implement Monitoring and Alerting Framework") → RHTAPWATCH-1408 ("Central Monitoring") → individual implementation tickets.

## Key References

- [Alertmanager routes](https://gitlab.cee.redhat.com/service/app-interface/-/blame/master/resources/rhobs/production/alertmanager-routes-mst.secret.yaml)
- [SAAS rules (prod deployment)](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml)
- [Monitoring architecture SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/monitoring-architecture.md)
- [Miro board (visual architecture)](https://miro.com/app/board/uXjVMeIRKU8=/)
- [Recording rules doc](https://docs.google.com/document/d/1Y72T10JGuJaeyeNexmS_qTHfDB8uxxq0zERRRSOZegg)
