# Konflux Observability 

This repository contains the following definitions for Konflux:
  * Prometheus alerting rules (deployed to RHOBS)
  * Grafana dashboards (deployed to AppSRE's Grafana)
  * Availability exporters

## Alerting Rules

The repository contains Prometheus alert rules [files](rhobs/alerting) for monitoring
Konflux data plane clusters along with their [tests](test/promql).


The different alerting rules in this repository are:

## Data Plane Alerts

* [**Alert Rule Unschedulable**](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alert-rule-unschedualablePods.md)

* [**Alert Rule CrashLoopBackOff**](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alert-rule-crashLoopBackOff.md?ref_type=heads)

* [**Alert Rule PodNotReady**](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alert-rule-PodNotReady.md?ref_type=heads)

* [**Alert Rule PersistentVolumeIssues**](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alert-rule-pesistentVolumeIssues.md?ref_type=heads)

* [**Alert Rule QuotaExceeded**](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/alert-rule-QuotaExceeded.md)

### Availability Metric Alerts

These Alert rules are defined to monitor and alert if the `konflux_up` metric is missing
for all expected permutations of the `service` and `check` labels across different environments.

### SLO Alerts

SLO (Service Level Objective) alert rules are rules defined to monitor and alert 
when a service or system is not meeting its specified service level objectives.

#### Usage Guidelines:

Apply the `slo` label to alerts directly associated with Service Level Objectives.
These alerts typically indicate issues affecting the performance or reliability of the service.

#### Benefits of using the `slo` Label:

Using the `slo` label facilitates quicker incident response by
promptly identifying and addressing issues that impact service level objectives.
  
#### How to apply the `slo` Label:

Apply `slo: "true"` under labels section of any alerting rule.
  ```
  labels:
      severity: critical
      slo: "true"
  ```
##### Note
SLO alerts should be labeled with `severity: critical`

### Alerts Tagging

Teams receive updates on alerts relevant to them through Slack notifications, 
where the team's handle is tagged in the alert message.

#### Usage Guidelines:

Apply the `alert_team_handle` and `team` annotations to SLO alerts in order to get notified about them.
  
#### How to apply the `alert_team_handle` Annotation:

Apply the `alert_team_handle` key to the annotations section of any alerting rule,
with the relevant team's Slack group handle.
The format of the Slack handle is: `<!subteam^-slack_group_id->` (e.g: `<!subteam^S041261DDEW>`);
To obtain the Slack group ID, click on the team's group handle, then click the three dots, and select "Copy group ID."

make sure to also add the `team` annotation with the name of the relevant team for readability.
  ```
  annotations:
      summary: "PipelineRunFinish to SnapshotInProgress time exceeded"
      alert_team_handle: <!subteam^S04S21ECL8K>
      team: o11y
  ```

### Updating Alert and Recording Rules

Alert rules for data plane clusters and recording rules are being deployed by
app-interface to RHOBS, to where the metrics are also being forwarded. For deploying the
alert rules and recording rules, app-interface references the location of the rules together
with a git reference - branch name or commit hash.

It holds separate references to both staging and production RHOBS instances (monitoring
Konflux staging and production deployments). For both environments, we maintain the
reference to the rules as a commit hash (rather than a branch). This means that any
changes to the rules will not take effect until the references are updated.

Steps for updating the rules:

1. Merge the necessary changes to this repository - alerts, recording rules and tests.
2. The
[data plane staging environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L35),
the
[recording rules staging environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L63)
in app-interface are referencing to the `main` branch in `o11y` repository  and will be automatically updated with the new changes.
3. Once merged and ready to be promoted to production, update the
[data plane production environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L39) 
and/or the
[recording rules production environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L67)
reference in app-interface to the commit hash of the changes you made.

## Grafana Dashboards

Refer to the app-interface [instructions](
https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana)
to learn how to develop AppSRE dashboards for Konflux. This repository serves as
versioned storage for the [dashboard definitions](dashboards/) and nothing more.

Dashboards are automatically deployed to [stage](https://grafana.stage.devshift.net) AppSRE Grafana when merged into the `main` branch.
Deploying to [production](https://grafana.app-sre.devshift.net/) requires an update of a commit
[reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/b03e4336a3223ec7b90dc9bc69707c9ee0ff9af6/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml#L37)
in app-interface.

## Adding Metrics and Labels

Only a subset of the metrics and labels available within the Konflux clusters is forwarded
to RHOBS. If additional metrics or labels are needed, add them by following the steps
described in the
[monitoring stack documentation](https://github.com/redhat-appstudio/infra-deployments/blob/main/components/monitoring/prometheus/README.md#federation-and-remote-write)

## Availability exporters

In order to be able to evaluate the overall availability of the Konflux ecosystem, we
need to be able to establish the availability of each of its components.

By leveraging the existing [Konflux monitoring stack](https://gitlab.cee.redhat.com/konflux/docs/documentation/-/blob/main/o11y/monitoring/monitoring.md)
based on Prometheus, we create Prometheus exporters that generate metrics that are
scraped by the User Workload Monitoring Prometheus instance and remote-written to RHOBS.

### Availability Exporter Example
The o11y team provides an example availability exporter that can be used as reference,
especially in the case in which the exporter is external to the code it's monitoring.

- [Exporter code](https://github.com/redhat-appstudio/o11y/tree/main/exporters/dsexporter)
- [Exporter and service Monitor Kubernetes Resources](https://github.com/redhat-appstudio/o11y/tree/main/config/exporters/monitoring/grafana/base)

For more detailed documentation on [Availability exporters](https://gitlab.cee.redhat.com/konflux/docs/documentation/-/blob/main/o11y/monitoring/availability_exporters.md?ref_type=heads)

## Recording Rules

Recording rules allow us to precompute frequently needed or computationally expensive expressions
and save their result as a new set of time series. Recording rules are the go-to approach for
speeding up the performance of queries that take too long to return. When other teams want to go
with their own metrics format for exporters they need to adapt to desired metric form by
translating it using a recording rule. 

These recording rules should be put in the [rhobs/recording folder](rhobs/recording/). 

The standard format is single availability metric `konflux_up` with labels `service` and `check`.
Each time series will have the service and check labels for the name of the originating service
and availability check it performed, respectively. The metric konflux_up should return either 0
or 1 based on the availability of the component/service. If the service is up then the
metric should return 1 else 0.

[Recording rule example](rhobs/recording/exporter_recording_rules.yml) provided here has
below format
  ```
  grafana_ds_up(check=prometheus-appstudio-ds) -> konflux_up(service=grafana, check=prometheus-appstudio-ds)
  ```

For more detailed documentation on [recording rules](https://docs.google.com/document/d/1Y72T10JGuJaeyeNexmS_qTHfDB8uxxq0zERRRSOZegg/edit?usp=sharing)

## Support

- Slack: [#forum-konflux-o11y](https://app.slack.com/client/E030G10V24F/C04FDFTF8EB)
