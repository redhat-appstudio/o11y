# RHTAP Alerting Rules
RHTAP alerting rules are deployed to RHOBS (Observatorium) using app-interface.
The rules are defined inside the [alerting](alerting) directory and are referenced using
the combination of the repository URL, the rules path and the git branch or commit hash.

In the case of RHTAP, we prefer the commit hash over the branch name as it provides
better control over changes to the rules being deployed.

This means that whenever the rules here have changed, a change in app-interface is
required before those rules get deployed to RHOBS.

To have app-interface reference the new rules, create a Merge Request towards
[app-interface's](https://gitlab.cee.redhat.com/service/app-interface) `master` branch
modifying the `ref` field under `rhtap-rhobs-rules/observatorium-mst-stage` and later
`rhtap-rhobs-rules/observatorium-mst-production` inside
[saas-rhobs-rules-and-dashboards.yaml](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/osd-operators/cicd/saas/saas-rhobs-rules-and-dashboards.yaml).

The rules should be verified for staging before applied to production.
