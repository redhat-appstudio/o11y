## Overview of the o11y repository

Repository contains three major components of Konflux Prometheus metric observability stack: Prometheus alerting/recording rules, Grafana dashboards, and custom Prometheus Go exporters and their deployment resources for the Konflux CI/CD platform. 

This readme file contains information applicable to all these components. Further information about architecture, building, testing, deploying, validating changes and specific conventions can be found in the component readme files. These files should be read by agent only when needed for the assigned work.

## Build & Test Commands

### Full validation (runs all existing checks)

```
make all
```

## Architecture overview

Prometheus rules are deployed into RHOBS (Red Hat Observability Service) via app-interface repository; exporters run as containers in Konflux clusters and are deployed via infra-deployments repository; Grafana dashboards are deployed via app-interface into app-sre Grafana instances. The App-sre Grafana instances use RHOBS as a datasource.

### Alert rules (`rhobs/staging/`, `rhobs/production/`, `test/` and `scripts/`)

See docs/ALERTS.md for component details.

### Recording rules (`rhobs/recording/`)

See docs/RECORDING.md for component details.

### Exporters (`exporters*/` and `config/`)

See docs/EXPORTERS.md for component details.

### Dashboards (`dashboards/`)

See docs/DASHBOARDS.md for component details.

### CI/CD (`.tekton` and `.github`)

See docs/CICD.md for details.

## Conventions

- Commit messages follow [conventional commits](https://www.conventionalcommits.org/en/v1.0.0/) — include Jira ticket ID (e.g. `STONEO11Y-123`) when applicable
- Review channel: `#forum-konflux-o11y` on Slack, tag `@konflux-o11y-ic`
