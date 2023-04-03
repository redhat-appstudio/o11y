# RHTAP Observability 

This repository contains Prometheus recording rule files and alert rule files for
observability use, as well as unit tests for these rules.

## Recording Rules

- `appstudio_container_network_transmit_bytes_total` collects the network egress of
containers and adds the `label_pipelines_appstudio_openshift_io_type` label to our
metric.

- `appstudio_container_resource_limits` collects all the resource limits
(cpu and memory) for all containers and init_containers, and adds
the `label_pipelines_appstudio_openshift_io_type` label to the new metric

- `appstudio_container_resource_minutes_gauge` uses the resource limit configured on
the container, as captured by `appstudio_container_resource_limits`, and transforms it to a
fixed time window gauge representing the resource-limit per minute multiplied by the
period the container was alive in that time window.

Examples (assuming 1-minute time window)
- A container lived for the entire minute and had a limit of 0.5 CPU cores --> 0.5 CPU
minutes.
- A container lived for 30 seconds within the measured time frame and had a limit of
2 CPU cores --> 1 CPU minutes.
This metric is intended to then be summed over the time frame the metric consumer wants
to measure for.


### Limitations

When a pod contains one or more init_containers that transmit data, only the first
init_container will be taken into account while the other init_containers and containers
are ignored from the metric `appstudio_container_network_transmit_bytes_total`
