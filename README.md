# Konflux Observability 

This repository contains the following definitions for Konflux:
* Prometheus rules (deployed to RHOBS)
* Grafana dashboards (deployed to AppSRE's Grafana)
* Availability exporters

## Alerting Rules

The repository contains Prometheus alert rules [files](rhobs/alerting) for monitoring
Konflux data plane clusters along with their
[tests](test/promql/tests/data_plane/).

The different alerting rules in this repository are:

### SLO Alerts

SLO (Service Level Objective) alert rules are rules defined to monitor and alert 
when a service or system is not meeting its specified service level objectives.

Example of an SLO Alert:
```yaml
- alert: DescriptiveAlertName
  expr: |
    <expression for your alert>
  for: 5m # How long should the expression eval to True in order for the Alert to fire.
  labels:
    severity: critical
    slo: "true"
  annotations:
    summary: >-
        <Short summary of the alert>
    description: >-
        <Description that can contain more context>
    alert_team_handle: >-
      <!subteam^XXXXXXXXXXX>
    runbook_url: <sop-link>
    team: <team>
```

#### Usage Guidelines:

Apply the `slo` label to alerts directly associated with Service Level Objectives.
These alerts typically indicate issues affecting the performance or reliability of the service. SLO Alerts should have Runbooks (`runbook_url` annotation) included directly in the definition.

#### Benefits of Using the `slo` Label:

Using the `slo` label facilitates quicker incident response by
promptly identifying and addressing issues that impact service level objectives.
  
#### How to Apply the `slo` Label:

Apply `slo: "true"` under labels section of any alerting rule.
```yaml
labels:
    severity: critical
    slo: "true"
```
##### Note
SLO alerts should be labeled with `severity: critical`

### Miscellaneous Alerts

Alerts lacking the `slo: "true"` label are considered non-SLO, miscellaneous or misc
alerts.

Such alerting rules are intended to notify regarding issues requiring attention, but are
not directly affecting Service Level Objectives defined by any service.

Example of a non-SLO Alert:
```yaml
- alert: DescriptiveAlertName
  expr: |
    <Alert expression>
  for: 5m # How long should the expression eval to True in order for the Alert to fire.
  labels:
    severity: warning
  annotations:
    summary: >-
        <Short summary of the alert>
    description: >-
        <Description that can contain more context>
    alert_routing_key: "{{ $labels.namespace }}"
    team: <team>
```

#### Availability Metric Alerts

These are non-SLO alerts defined to monitor and alert if the `konflux_up` metric is
missing for any expected permutations of the `service` and `check` labels across
different environments.

### Alerts Tagging

Teams receive updates on alerts relevant to them through Slack notifications, 
where the team's handle is tagged in the alert message.

#### Usage Guidelines:

Apply the `alert_team_handle` and `team` annotations to SLO alerts in order to get notified about them.
  
#### How to Apply the `alert_team_handle` Annotation for SLO Alerts:

Apply the `alert_team_handle` key to the annotations section of any alerting rule,
with the relevant team's Slack group handle.
The format of the Slack handle is: `<!subteam^-slack_group_id->` (e.g.:
`<!subteam^S041261DDEW>`);
To obtain the Slack group ID, click on the team's group handle, then click the three
dots, and select "Copy group ID."

Make sure to also add the `team` annotation with the name of the relevant team for readability.
```yaml
annotations:
  summary: "PipelineRunFinish to SnapshotInProgress time exceeded"
  alert_team_handle: <!subteam^S04S21ECL8K>
  team: o11y
```

#### How to Apply the `alert_routing_key` Annotation for Misc Alersts:

For miscellaneous alerts routing works a bit differently. To clearly distinguish them from SLO Alerts namespace/team names are used and are paired to `alert_routing_key` annotation key. Specific teams are tagged by the routing matrix in [app-interface](https://gitlab.cee.redhat.com/service/app-interface/-/blame/master/resources/rhobs/production/alertmanager-routes-mst.secret.yaml?ref_type=heads#L53). `team` annotation helps to distinguish ownership of the alert when namespace routing is used. Otherwise its not needed for routing purposes.

```yaml
annotations:
  alert_routing_key: "{{ $labels.namespace }}"
  team: o11y
```

## Recording Rules

Recording rules allow us to precompute frequently needed or computationally expensive expressions and save their result as a new set of time series. Recording rules are the
go-to approach for speeding up the performance of queries that take too long to return.

Rules located in the [recording rules directory](rhobs/recording/) are deployed to RHOBS
which makes them present in [AppSRE Grafana](https://grafana.app-sre.devshift.net/).

Rules should be created together with the [unit tests](test/promql/tests/recording/).

### Faster Selective Rule Testing

To accelerate the development and validation of specific alerting or recording rules, a selective testing mechanism is available. This allows you to run the rules checker (e.g., `obsctl-reloader-rules-checker`, or as configured in the Makefile via the `CMD` variable) only on a chosen set of rule files and their corresponding test case files, rather than processing the entire suite. This is particularly useful for quick validation of changes and a faster feedback loop during development.

The selective testing process involves:
1.  Creating a temporary, isolated environment.
2.  Copying only the specified rule and test files into this environment.
3.  Executing the rules checker against these isolated files.
4.  Automatically cleaning up the temporary environment afterward.

#### Usage

You can invoke this selective test by running the following `make` command:

```sh
make selective-check-and-test RULE_FILES="<space_separated_rule_files>" TEST_CASE_FILES="<space_separated_test_files>"
```

## Updating Alert and Recording Rules

Alert rules for data plane clusters and recording rules are being deployed by
app-interface to RHOBS, to where the metrics are also being forwarded. For deploying the
alert rules and recording rules, app-interface references the location of the rules
together with a git reference - branch name or commit hash.

It holds separate references to both staging and production RHOBS instances (monitoring
Konflux staging and production deployments).

The staging environment references the `main` of this repo, so rule changes reaching
that branch are automatically deployed to RHOBS.

The production environment keeps the reference to the rules as a commit hash (rather
than a branch). This means that any changes to the rules will not take effect until the
references are updated.

Steps for updating the rules:

1. Merge the necessary changes to this repository - alerts, recording rules and tests.
2. Verify that the rules are visible as expected in AppSRE Grafana.
3. Once the changes are ready to be promoted to production, update the
[alerting rules production reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/c5bbcd98175450b4e51ed9e2d41bda394cea0f92/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L40) 
and/or the
[recording rules production reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/c5bbcd98175450b4e51ed9e2d41bda394cea0f92/data/services/stonesoup/cicd/saas-rhtap-rules.yaml#L54)
in app-interface to the commit hash of the changes you made.

## Grafana Dashboards

Refer to the app-interface [instructions](
https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana)
to learn how to develop AppSRE dashboards for Konflux. This repository serves as
versioned storage for the [dashboard definitions](dashboards/) and nothing more.

Dashboards are automatically deployed to [stage](https://grafana.stage.devshift.net)
AppSRE Grafana when merged into the `main` branch.
Deploying to [production](https://grafana.app-sre.devshift.net/) requires an update of a
commit
[reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/b03e4336a3223ec7b90dc9bc69707c9ee0ff9af6/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml#L37)
in app-interface.

When creating a dashboard config map resource, please use this snippet to start with:
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: <your-dashboard-name>.configmap
  labels:
    grafana_dashboard: "true"
  annotations:
    grafana-folder: /grafana-dashboard-definitions/RHTAP
data:
  <your-dashboard-name>.json: |-
```

Note: The dashboard UID must always be unique in each Grafana instance. Make sure to modify it by changing a few characters or deleting the test dashboard in staging instance. If the test dashboard is kept and the uid is not updated, glitches will occur insta grafana as it will juggle between the two dashboards with identical UIDs.

## Adding Metrics and Labels

Only a subset of the metrics and labels available within the Konflux clusters is
forwarded to RHOBS. If additional metrics or labels are needed, add them by following
the steps described for
[Troubleshooting Missing Metrics and Labels](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md?ref_type=heads)

## Availability Exporters

In order to be able to evaluate the overall availability of the Konflux ecosystem, we
need to be able to establish the availability of each of its components.

By leveraging the existing [Konflux monitoring stack](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/monitoring.md?ref_type=heads)
based on Prometheus, we create Prometheus exporters that generate metrics that are
scraped by the User Workload Monitoring Prometheus instance and remote-written to RHOBS.

### Availability Exporter Example
The o11y team provides an example availability exporter that can be used as reference,
especially in the case in which the exporter is external to the code it's monitoring.

- [Exporter code](https://github.com/redhat-appstudio/o11y/tree/main/exporters/dsexporter)
- [Exporter and service Monitor Kubernetes Resources](https://github.com/redhat-appstudio/o11y/tree/main/config/exporters/monitoring/grafana/base)

For more detailed documentation on [Availability exporters](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/availability_exporters.md?ref_type=heads)

### Availability Exporter Recording Rules

When teams want to go with their own metrics format for exporters they need to adapt to
the standard metric format by translating it using recording rules.

These recording rules should be put in the [rhobs/recording folder](rhobs/recording/). 

The standard format is a single availability metric `konflux_up` with labels `service`
and `check`. Each time series will have the service and check labels for the name of the
originating service and the availability check it performed, respectively.

The metric konflux_up should return either `0` or `1` based on the availability of the
component/service. If the service is up then the metric should return `1` else `0`.

The recording rule [example](rhobs/recording/exporter_recording_rules.yml) provided here
has the below format:

```
grafana_ds_up(check=prometheus-appstudio-ds) -> konflux_up(service=grafana, check=prometheus-appstudio-ds)
```

See detailed documentation on
[recording rules](https://docs.google.com/document/d/1Y72T10JGuJaeyeNexmS_qTHfDB8uxxq0zERRRSOZegg/edit?usp=sharing).

## Support

- Slack: [#forum-konflux-o11y](https://app.slack.com/client/E030G10V24F/C04FDFTF8EB)

