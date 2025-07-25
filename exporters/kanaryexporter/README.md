## Kanary Exporter

This `kanaryexporter` (implemented in `kanaryexporter.go`) is a Prometheus exporter designed to monitor Kanary signal values derived from a PostgreSQL database. It exposes two primary metrics: `kanary_up` and `kanary_error`.

**General Exporter Behavior:**

- Accesses DB that has load test results for konflux clusters
- Calculates `kanary_up` and `kanary_error` metrics periodically
- Metrics are exposed on the `/metrics` endpoint
- Supports multiple e2e test types: **container** and **RPM**
- Each test type has its own cluster list and database queries

---

### `kanary_up` Metric

The `kanary_up` metric indicates the health status of a target cluster based on recent load test results.

- **Metric Name:** `kanary_up`
- **Labels:** `tested_cluster`, `type` (where `type` can be "container" or "rpm")
- **Value `1` (UP):**
  - If data processing is successful and at least one of the recent (number defined in code) tests succeeded.
  - **Crucially** if any error is encountered in the exporter.
    - This is a safety mechanism that ensures that alerts will not trigger when the kanary status cannot be determined.
- **Value `0` (DOWN):**
  - If data processing is successful and all the recent (number defined in code) tests failed.

---

### `kanary_error` Metric

The `kanary_error` metric reports any errors preventing the exporter from exporting the kanary signal.

- **Metric Name:** `kanary_error`
- **Labels:** `tested_cluster`, `type` (where `type` can be "container" or "rpm"), `reason`
- **Value `1`:**
  - **Error reasons (in metric label `reason`):**
    - `"no_test_results"`: The latest test result in the database for a given cluster is too old. This could be an issue with:
      - Test not being triggered by underlying automation
      - Test results are not exported to the database
    - `"db_error"`: Indicates a problem related to database interaction or data validation. This could be:
      - Failed query
      - Missing data
      - Connection issues
      - ...
- **Value `0`:**
  - No issues in exporting the kanary signal

---

## Test Types

The exporter supports two types of tests:

### Container Tests

- **Type:** `container`
- **Query Filter:** `horreum_testid = 372` AND `label_values->>'.repo_type' LIKE 'nodejs-devfile-sample%%'`
- **Clusters:** stone-stage-p01, stone-prod-p01, stone-prod-p02, stone-stg-rh01, stone-prd-rh01

### RPM Tests

- **Type:** `rpm`
- **Query Filter:** `horreum_testid = 372` AND `label_values->>'.repo_type' LIKE 'libecpg%%'`
- **Clusters:** stone-prod-p02, kflux-rhel-p01, stone-prd-rh01

> NOTE: `horreum_testid = 372` is a filter for data from both Container and RPM builds according to Perf&Scale Team.

---

## Development

- To run locally:
  - `CONNECTION_STRING="postgresql://{username}:{password}@{host}:{port}/{dbname}?sslmode=disable" go run ./kanaryexporter.go`

---

## Unit Tests

Comprehensive unit tests for the Kanary Exporter are provided in [`kanaryexporter_test.go`](https://github.com/redhat-appstudio/o11y/tree/main/exporters/kanaryexporter_test.go). These tests cover metric state logic, database interactions, and error handling to ensure exporter reliability.

---

The o11y team provides this Kanary exporter and its configuration as a reference:

- [Kanary Exporter code](https://github.com/redhat-appstudio/o11y/tree/main/exporters/kanaryexporter.go)
- [Kanary Exporter and Service Monitor Kubernetes Resources](https://github.com/redhat-appstudio/o11y/tree/main/config/exporters/monitoring/kanary/base)
