## Exporters overview

Custom Prometheus exporters, written in Go, expose metrics for service and dependencies which cannot be directly instrumented.

Exporters run as containers in Konflux clusters. For detailed metrics, configuration, and development instructions for each exporter, see the individual exporter READMEs in the `exporters/<exporter-name>` folder. Kubernetes resources required for deployment are located in `config/` folder and are deployed via infra-deployments repository.

## Exporter inventory

| **Exporter** | **Purpose** | **Metrics** |
|----------|---------|---------------|
| dsexporter | Grafana datasource availability. Serves mostly as a demo on how to create custom exporters. | `grafana_ds_up` |
| registryexporter | Monitors health of container registries by performing various operations such as authentication or image push/pull| `registry_exporter_success`, `_error_count`, `_duration_seconds`, `_image_size_mbytes` |
| kanaryexporter | Exposes end-to-end test results from external database owned by Perf&Test team as metrics. This is used as a key metric for determining user experience with Konflux workflows. | `kanary_up`, `kanary_error` |

## Architecture

### Common deployment patterns

All exporters follow a consistent Kubernetes deployment pattern:

- **Dedicated namespace** per exporter. This is simply for clear separation of resources.
- **kube-rbac-proxy sidecar** allows access only to authorized users and serviceaccounts.
- **ServiceMonitor or PodMonitor** with TLS configuration for Prometheus scraping.
- **ServiceAccount + RBAC** for API access and authentication proxy permissions.

## Build

All exporters are compiled into a single multi-binary container image via a multi-stage Dockerfile. The build auto-discovers exporters: any subdirectory under `exporters/` with a Go `main` package is compiled into a binary named after the directory (e.g. `exporters/dsexporter/` → `/bin/dsexporter`).

The entrypoint script selects which exporter to run based on the first container argument:
```
# Run <exporter-name>
docker run <image> <exporter-name> 
```

### Testing

```
go test ./...                             # all exporter unit tests
go test ./exporters/<exporter-name>       # single exporter
make kustomize-build                      # validate Kubernetes manifests
```

## Deployment model

Exporters are deployed via infra-deployments to Konflux clusters

The container image is built by the Konflux CI push pipeline (see [CICD.md](CICD.md)) and referenced in [infra-deployments](https://github.com/redhat-appstudio/infra-deployments). Kustomize overlays in `config/exporters/monitoring/` define the Kubernetes resources (Deployment/DaemonSet, Service, ServiceMonitor, RBAC), but the actual deployment to clusters is managed by infra-deployments, not this repository.

Image reference: `quay.io/redhat-appstudio/o11y`

## Reference documentation

- [Availability exporters SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/availability_exporters.md)
- [Monitoring architecture SOP](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/monitoring-architecture.md)
- [Troubleshooting missing metrics](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md)
