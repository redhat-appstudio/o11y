## Kanary Exporter

This `kanaryexporter` (implemented in `kanaryexporter.go`) is a Prometheus exporter designed to monitor Kanary signal values derived from a PostgreSQL database. It exposes two primary metrics: `kanary_up` and `kanary_error`.

---

### `kanary_up` Metric

The `kanary_up` metric indicates the health status of a target cluster based on recent KPI error readings. It is a `prometheus.GaugeVec` with the label `instance` (the name of the monitored cluster).

* **Metric Name:** `kanary_up`
* **Value `1` (UP):**
    * If data processing is successful and at least one of the last `requiredRecentReadings` KPI error counts is `0` or less (should not be possible but here just in case).
    * **Crucially, also set to `1` if the `kanary_error` metric for the instance is `1` (see below).**
* **Value `0` (DOWN):**
    * If data processing is successful and **all** of the last `requiredRecentReadings` KPI error counts are strictly greater than `0`.

**How `kanary_up` Works:**

* The exporter requires the PostgreSQL database connection URL via the `CONNECTION_STRING` environment variable.
* Upon starting, it connects to the specified database.
* Periodically (every 5 minutes by default, configurable by `scrapeInterval`), it iterates through a predefined list of `targetClusters`.
* For each cluster, it performs the following checks and actions:
    1.  **Data Freshness Check:** Verifies that the latest data point in the database for the cluster is not older than `delayInSeconds` (default is 10800 seconds / 3 hours). If it's too old, this is treated as an error (`no_test_results`), `kanary_error` is set to `1`, and `kanary_up` is set to `1`.
    2.  **Sufficient Data Check:** Ensures there's a minimum amount of data available for the cluster. If not (e.g., `countQuery` returns 0), this is treated as an error (`db_error`), `kanary_error` is set to `1`, and `kanary_up` is set to `1`.
    3.  **KPI Error Retrieval:** If the above checks pass, it queries for the last `requiredRecentReadings` (default is 3) of `__results_measurements_KPI_errors`. The query filters for entries where `label_values->>'.metadata.env.MEMBER_CLUSTER'` matches the target cluster and `(label_values->>'.repo_type' = 'nodejs-devfile-sample' OR NOT (label_values ? '.repo_type'))`. Results are ordered by `start` time descending.
    4.  **Metric Value Logic (if no errors in steps 1-3):**
        * If any issue occurs during data fetching, parsing, or if an insufficient number of readings (less than `requiredRecentReadings`) are returned, it's considered an error (`db_error`), `kanary_error` is set to `1`, and `kanary_up` is set to `1`.
        * If data is retrieved successfully:
            * If **all** retrieved KPI error counts are `> 0`, `kanary_up` is set to `0` (DOWN).
            * If **at least one** KPI error count is `<= 0`, `kanary_up` is set to `1` (UP).

---

### `kanary_error` Metric

The `kanary_error` metric is a `prometheus.GaugeVec` with labels `instance` and `reason`. It acts as a binary indicator (`0` or `1`) to signal issues encountered during the data fetching or processing pipeline for the `kanary_up` signal.

* **Metric Name:** `kanary_error`
  * **Error reasons:**
      * `"no_test_results"`: The latest data point for the cluster is older than `delayInSeconds` (e.g., no test results within the last 3 hours).
      * `"db_error"`: Indicates a problem related to database interaction or data validation. This includes:
          * Database query execution failures.
          * Errors scanning rows from the result set.
          * Errors during row iteration.
          * An insufficient number of data points returned by the query (e.g., fewer than `requiredRecentReadings`, or `datapointsCountQuery` initially found no data).
          * A retrieved `kpi_error_value` is NULL or an empty string.
          * Failure to parse a `kpi_error_value` as an integer.

**Impact of `kanary_error`:**

* When `kanary_error` is `1` for a given `instance`, it signals a problem with the data pipeline for that instance.
* In this state, the `kanary_up` metric for that `instance` is actively set to `1`. This behavior ensures that system alerts or dashboards dependent on `kanary_up` will still indicate an 'up' state when the underlying data source for determining the true Kanary signal is unreliable, rather than potentially showing 'down' due to missing data.

---

**General Exporter Behavior:**

* The exporter uses globally defined `prometheus.GaugeVec` instances (`kanaryUpMetric`, `kanaryErrorMetric`).
* These metrics are registered with a `prometheus.NewPedanticRegistry()`.
* Metrics are exposed via `promhttp.HandlerFor()` on the `/metrics` endpoint (port 8000 by default).
* The main loop fetches and exports metrics upon startup and then periodically (default every 300 seconds).

The o11y team provides this Kanary exporter and its configuration as a reference:

* [Kanary Exporter code](https://github.com/redhat-appstudio/o11y/tree/main/exporters/kanaryexporter.go)
* [Kanary Exporter and Service Monitor Kubernetes Resources](https://github.com/redhat-appstudio/o11y/tree/main/config/exporters/monitoring/kanary/base)
