# Prometheus Exporters

Prometheus exporters expose metrics from a system or an application in a format that can 
be scraped and monitored by Prometheus. The exporter may be an integral part of the 
component that it's monitoring (self-instrumenting), or it can generate metrics for 
another system/component.

## Availability Exporter

In the context of the Konflux ecosystem, the `dsexporter` serves as an 
[Availability Exporter](https://gitlab.cee.redhat.com/konflux/docs/documentation/-/blob/main/o11y/monitoring/availability_exporters.md),
contributing to the evaluation of the availability of each component within the system.
By exposing this metric, it allows for monitoring and analyzing the 
workload and performance of the associated service.

## dsexporter

This `datasource exporter file (dsexporter.go)` exposes a single metric named `"konflux_up"`
that checks if the grafana datasource `"prometheus-appstudio-ds"` is available or not. 
This metric has two labels `"service"` and `"check"` which are constant for a given time 
series.The exporter implements a custom Prometheus collector named `CustomCollector`,
which implements the `prometheus.Collector` interface. The `CustomCollector` struct
has a konfluxUp field to check the availability of grafana datasource. It implements
the `Describe` method to provide descriptions of the metrics to Prometheus and `Collect`
method to send the current values of the metrics to Prometheus.

The `test case file (dsexporter_test.go)` tests the functionality of the different functions 
impelemented in dsexporter.go.

This code provides a simple example of how to create a custom Prometheus exporter and test its 
functionality in Go. It is useful for monitoring the performance and behavior of the overall 
availability of the Konflux ecosystem.

The o11y team provides an example availability exporter that can be used as reference below:

* [Exporter code](https://github.com/redhat-appstudio/o11y/tree/main/exporters/dsexporter)
* [Exporter and Service Monitor Kubernetes Resources](https://github.com/redhat-appstudio/o11y/tree/main/config/exporters/monitoring/grafana/base)
