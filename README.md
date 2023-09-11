# RHTAP Observability 

This repository contains the following definitions for RHTAP:
  * Prometheus alerting rules (deployed to RHOBS)
  * Grafana dashboards (deployed to AppSRE's Grafana)

## Alerting Rules

The repository contains Prometheus alert rules [files](rhobs/alerting) for monitoring
RHTAP data plane clusters along with their [tests](test/promql).

Control plane clusters alert rules are maintained by the same team, but are kept in a
different
[repository](https://gitlab.cee.redhat.com/service/app-interface/-/tree/master/resources/stonesoup/argocd-control-plane/monitoring).

The different alerting rules in this repository are:

## Control Plane Alerts

* [**Alert Rule ProgressingArgocdApp**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-ProgressingArgocdApp.md)

* [**Alert Rule DegradedArgocdApp**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-degradedArgocdApp.md)

## Data Plane Alerts

* [**Alert Rule ControllerReconciliationErrors**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-controllerReconciliationErrors.md?ref_type=heads)

* [**Alert Rule Unschedulable**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-unschedualablePods.md)

* [**Alert Rule CrashLoopBackOff**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-crashLoopBackOff.md?ref_type=heads)

* [**Alert Rule PodsNotReady**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-PodsNotReady.md?ref_type=heads)

* [**Alert Rule PersistentVolumeIssues**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-pesistentVolumeIssues.md?ref_type=heads)

* [**Alert Rule QuotaExceeded**](https://gitlab.cee.redhat.com/rhtap/docs/sop/-/blob/main/o11y/alert-rule-QuotaExceeded.md)

### Updating Data Plane Alerts

Alert rules for data plane clusters are being deployed by app-interface to RHOBS, to where the data plane metrics are also being forwarded. For deploying the alert rules,
app-interface references the location of the rules together with a git reference -
branch name or commit hash.

It holds separate references to both staging and production RHOBS instances (monitoring
RHTAP staging and production deployments). For both environments, we maintain the
reference to the rules as a commit hash (rather than a branch). This means that any
changes to the rules will not take effect until the references are updated.

Steps for updating the rules:

1. Merge the necessary changes to this repository - alerts and tests.
2. Update the
[staging environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/0486ef164e70259e5b85c46ab749529238368414/data/services/osd-operators/cicd/saas/saas-rhtap-rules.yaml#L35)
reference in app-interface to the commit hash of the changes you made.
3. Once merged and ready to be promoted to production, update the
[production environment](https://gitlab.cee.redhat.com/service/app-interface/-/blob/0486ef164e70259e5b85c46ab749529238368414/data/services/osd-operators/cicd/saas/saas-rhtap-rules.yaml#L39) reference in a similar manner.

## Grafana Dashboards

Refer to the app-interface [instructions](
https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/docs/app-sre/monitoring.md#visualization-with-grafana)
to learn how to develop AppSRE dashboards for RHTAP. This repository serves as
versioned storage for the [dashboard definitions](dashboards/) and nothing more.

## Support

The RHTAP o11y team maintains this repository.
Reach out to us on our [slack channel](https://redhat-internal.slack.com/archives/C04FDFTF8EB)
for further assistance.
