# RHTAP Observability 

This repository contains the following definitions for RHTAP:
  * Prometheus alerting rules (deployed to RHOBS)
  * Grafana dashboards (deployed to AppSRE's Grafana)

## Alerting Rules

The repository contains Prometheus alert rules [files](rhobs/alerting) for monitoring
RHTAP data plane and control plane clusters along with their [tests](test/promql).


The different alerting rules in this repository are:

## Control Plane Alerts

* [**Alert Rule ProgressingArgocdApp**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-ProgressingArgocdApp.md)

* [**Alert Rule DegradedArgocdApp**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-degradedArgocdApp.md)

## Data Plane Alerts

* [**Alert Rule ControllerReconciliationErrors**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-controllerReconciliationErrors.md?ref_type=heads)

* [**Alert Rule Unschedulable**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-unschedualablePods.md)

* [**Alert Rule CrashLoopBackOff**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-crashLoopBackOff.md?ref_type=heads)

* [**Alert Rule PodNotReady**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-PodNotReady.md?ref_type=heads)

* [**Alert Rule PersistentVolumeIssues**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-pesistentVolumeIssues.md?ref_type=heads)

* [**Alert Rule QuotaExceeded**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-QuotaExceeded.md)

### SLO Alerts

SLO (Service Level Objective) alert rules are rules defined to monitor and alert 
when a service or system is not meeting its specified service level objectives.

#### Usage Guidelines:

Apply the `slo: true` label to alerts directly associated with Service Level Objectives.
These alerts typically indicate issues affecting the performance or reliability of the service.

#### Benefits of using the `slo: true` Label:

Labeling alerts with `slo: true` facilitates quicker incident response by 
promptly identifying and addressing issues that impact service level objectives.
  
#### How to apply the `slo: true` Label: 

Apply `slo: true` under labels section of any alerting rule. 
  ```
  labels:
      severity: warning
      slo: true   # Apply the slo:true label in here.
  ```

### Updating Alerts

Alert rules for data plane and control plane clusters are being deployed by app-interface 
to RHOBS, to where the metrics are also being forwarded. For deploying the 
alert rules, app-interface references the location of the rules together with a git 
reference - branch name or commit hash.

It holds separate references to both staging and production RHOBS instances (monitoring
RHTAP staging and production deployments). For both environments, we maintain the
reference to the rules as a commit hash (rather than a branch). This means that any
changes to the rules will not take effect until the references are updated.

Steps for updating the rules:

1. Merge the necessary changes to this repository - alerts and tests.
2. Update the
[data plane staging environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/osd-operators/cicd/saas/saas-rhtap-rules.yaml#L35) 
and/or the
[control plane staging environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/osd-operators/cicd/saas/saas-rhtap-rules.yaml#L49)
reference in app-interface to the commit hash of the changes you made.
3. Once merged and ready to be promoted to production, update the
[data plane production environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/osd-operators/cicd/saas/saas-rhtap-rules.yaml#L39) 
and/or the
[control plane production environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/osd-operators/cicd/saas/saas-rhtap-rules.yaml#L53) 
reference in a similar manner.

## Grafana Dashboards

Refer to the app-interface [instructions](
https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana)
to learn how to develop AppSRE dashboards for RHTAP. This repository serves as
versioned storage for the [dashboard definitions](dashboards/) and nothing more.

Dashboards are automatically deployed to [stage](https://grafana.stage.devshift.net) AppSRE Grafana when merged into the `main` branch.
Deploying to [production](https://grafana.app-sre.devshift.net/) requires an update of a commit
[reference](https://gitlab.cee.redhat.com/service/app-interface/-/blob/b03e4336a3223ec7b90dc9bc69707c9ee0ff9af6/data/services/stonesoup/cicd/saas-stonesoup-dashboards.yml#L37)
in app-interface.

## Support

The RHTAP o11y team maintains this repository.
Reach out to us on our [slack channel](https://redhat-internal.slack.com/archives/C04FDFTF8EB)
for further assistance.
