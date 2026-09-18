# Konflux Observability 

This repository contains the Konflux Prometheus metric observability stack:


* **[Alerting rules](docs/ALERTS.md)** — Prometheus alert rules deployed to RHOBS (`rhobs/staging/alerting/`, `rhobs/production/alerting/`)
* **[Recording rules](docs/RECORDING.md)** — precomputed Prometheus expressions deployed to RHOBS (`rhobs/staging/recording/`, `rhobs/production/recording/`)
* **[Grafana dashboards](docs/DASHBOARDS.md)** — dashboard definitions deployed to AppSRE Grafana (`dashboards/`)
* **[Availability exporters](docs/EXPORTERS.md)** — custom Prometheus Go exporters (`exporters/`, `config/`)
* **[CI/CD](docs/CICD.md)** — Tekton pipelines and GitHub Actions (`.tekton/`, `.github/`)

## Adding Metrics and Labels

Only a subset of the metrics and labels available within the Konflux clusters is
forwarded to RHOBS. If additional metrics or labels are needed, add them by following
the steps described for
[Troubleshooting Missing Metrics and Labels](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md?ref_type=heads).

## Support

- Slack: [#forum-konflux-o11y](https://app.slack.com/client/E030G10V24F/C04FDFTF8EB)
