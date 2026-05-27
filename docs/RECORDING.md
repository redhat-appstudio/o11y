## Recording rules overview

Recording rules pre-compute PromQL expressions and store the results as new time series in RHOBS. They are used to create derived metrics that would be too expensive or complex to compute at query time or to precompute them if they are in high demand — most commonly the `konflux_up` availability metric that powers the SLO dashboard and the [Tactical Status Page](https://tsp.status.redhat.com).

Recording rules are defined as [`PrometheusRule`](https://github.com/prometheus-operator/prometheus-operator/blob/main/Documentation/api-reference/api.md#monitoring.coreos.com/v1.PrometheusRule) resources in the `rhobs/recording/` directory. They share the same Kubernetes kind, deployment pipeline, and testing toolchain as alerting rules.

## Creating a new recording rule

1. Create a new rule file in `rhobs/recording/` — use the [recording rule template](templates/recording_rule_template.yaml) as a starting point
2. Write a corresponding test file in `test/promql/tests/recording/` — use the [test template](templates/recording_rule_test_template.yaml) and see [Test file format](#test-file-format)
3. Run validation — see [Validation and testing](#validation-and-testing)
4. Open a PR

## Validation and testing

Recording rules share the same validation toolchain as alerting rules. All commands run inside a container — no local tool install needed.

```
make check-and-test
```

For faster iteration on a single rule:
```
make selective-check-and-test \
  RULE_FILES="rhobs/recording/my_recording_rules.yaml" \
  TEST_CASE_FILES="test/promql/tests/recording/my_recording_rules_test.yaml"
```

YAML linting:
```
make sync_pipenv   # first time only
make lint_yamls
```

### Test file format

Tests use promtool's unit test format — use the [recording rule test template](templates/recording_rule_test_template.yaml) as a starting point.

Test both the expected-up and expected-down cases. For rules with namespace or label filters, include a test case where the input data doesn't match the filter to verify the rule produces no output.

## Architecture

Recording rules in this repository are evaluated by RHOBS (Red Hat Observability Service), not by in-cluster Prometheus. Any metric used in a recording rule expression must first be [forwarded to RHOBS](https://gitlab.cee.redhat.com/konflux/docs/sop/-/blob/main/o11y/monitoring/tshoot-missing-metrics.md) via [infra-deployments](https://github.com/redhat-appstudio/infra-deployments). A rule referencing a metric that isn't forwarded will silently produce no data.

### Cluster-level recording rules

Recording rules that need to be evaluated by in-cluster Prometheus (rather than RHOBS) are defined in [infra-deployments](https://github.com/redhat-appstudio/infra-deployments). They are deployed as `PrometheusRule` resources into the `openshift-user-workload-monitoring` namespace via kustomize patches.

Environment overlays patches: `components/monitoring/prometheus/{staging,production}/base/monitoringstack/prometheusrule-uwm.yaml`

Use cluster-level recording rules when the derived metric is only needed on-cluster.

## Deployment Model

Recording rules are deployed via the same pipeline as alerting rules — see [ALERTS.md § Deployment Model](ALERTS.md#deployment-model). Both use the same [app-interface saas file](https://gitlab.cee.redhat.com/service/app-interface/-/blob/master/data/services/stonesoup/cicd/saas-rhtap-rules.yaml).

## Conventions

### PrometheusRule structure

Use the [recording rule template](templates/recording_rule_template.yaml) as a starting point. All fields are documented with `[required]` / `[optional]` tags in the template.

### The `konflux_up` availability pattern

`konflux_up` is a standardized boolean metric that every Konflux service should expose. It enables a unified SLO dashboard and the Tactical Status Page to show fleet-wide availability. See [ALERTS.md § Availability alerts](ALERTS.md#availability-alerts-konflux_up-pattern) for the full specification of required labels and how signals are created.

Use the [availability recording rule template](templates/recording_rule_availability_template.yaml) for the standard deployment replica pattern. The template includes inline comments explaining each step.
