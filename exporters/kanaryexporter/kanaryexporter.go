package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	tableName = "data"
	// dbURLEnvVar is the environment variable name for the database connection string.
	dbURLEnvVar = "CONNECTION_STRING"
	// requiredRecentReadings is the number of recent KPI error readings to consider for the kanary_up.
	requiredRecentReadings = 3
	// Amount of time in seconds allowed without a new entry in the database
	delayInSeconds = 10800
)

var (
	kanaryUpMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kanary_up",
			// Help string updated to reflect the new logic: UP if at least one error reading <= 0, DOWN if all > 0.
			Help: fmt.Sprintf("Kanary signal: 1 if at least one of last %d error readings is 0 or less, 0 if all last %d error readings are greater than 0. Only updated when %d valid data points are available.", requiredRecentReadings, requiredRecentReadings, requiredRecentReadings),
		},
		[]string{"instance"},
	)

	kanaryErrorMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kanary_error",
			Help: fmt.Sprintf("Binary indicator of an error in processing for Kanary signal (1 if interrupted, 0 otherwise). An error prevents kanary_up from being updated. The 'reason' label provides details on the error type."),
		},
		[]string{"instance", "reason"},
	)

	// targetClusters is the list of cluster API URLs to monitor.
	targetClusters = []string{
		// Private Clusters
		"stone-stage-p01",
		"stone-prod-p01",
		"stone-prod-p02",
		// Public Clusters
		"stone-stg-rh01",
		"stone-prd-rh01",
	}

    // --- Db Queries

    // Entries in Db check
	datapointsCountQuery = fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE
			label_values->>'.metadata.env.MEMBER_CLUSTER' LIKE $1
			AND ( label_values->>'.repo_type' = 'nodejs-devfile-sample' OR NOT (label_values ? '.repo_type') );
	`, tableName)

    // Delay check
    delayCheckQuery = fmt.Sprintf(`
		WITH LatestRowByStart AS (
			SELECT
				-- The replace is to comply with the Standard ISO 8601 format
				EXTRACT(epoch FROM (REPLACE(label_values->>'.ended', ',', '.'))::timestamptz) AS ended_epoch,
				(EXTRACT(epoch FROM CURRENT_TIMESTAMP) - %d) AS earliest_allowed_ended_epoch
			FROM
				%s
			WHERE
				label_values->>'.metadata.env.MEMBER_CLUSTER' LIKE $1
				AND (label_values->>'.repo_type' = 'nodejs-devfile-sample' OR NOT (label_values ? '.repo_type'))
			ORDER BY
				EXTRACT(epoch FROM start) DESC
			LIMIT 1
		)
		SELECT COUNT(*)
		FROM LatestRowByStart
		WHERE ended_epoch >= earliest_allowed_ended_epoch
	`, delayInSeconds, tableName)

    // Query fetches KPI error values, filtering by member cluster using LIKE and optionally by repo_type.
	dataQuery = fmt.Sprintf(`
		SELECT
			label_values->>'__results_measurements_KPI_errors' AS kpi_error_value
		FROM
			%s
		WHERE
			label_values->>'.metadata.env.MEMBER_CLUSTER' LIKE $1
			AND ( label_values->>'.repo_type' = 'nodejs-devfile-sample' OR NOT (label_values ? '.repo_type') )
		ORDER BY
			EXTRACT(epoch FROM start) DESC
		LIMIT %d;
	`, tableName, requiredRecentReadings)
)

// getKpiErrorReadings fetches and validates the last 'requiredRecentReadings' KPI error counts for a given cluster.
// It returns a slice of int64 values if successful, an internal status string for the error reason, and an error if any issue occurs.
func getKpiErrorReadings(db *sql.DB, clusterName string) ([]int64, string, error) {
	clusterSubStringPattern := "%" + clusterName + "%"

    // Sufficient datapoints for cluster in db check
    var datapointsCount int
	err := db.QueryRow(datapointsCountQuery, clusterSubStringPattern).Scan(&datapointsCount)
	if err != nil {
		if err == sql.ErrNoRows {
			datapointsCount = 0
		} else {
			return nil, "db_error", fmt.Errorf("database count query failed for cluster %s: %w", clusterName, err)
		}
	}

	if datapointsCount == 0 {
		return nil, "db_error", fmt.Errorf("database count query failed for cluster %s: %w", clusterName, err)
	}


    // Latest datapoint entry not delayed check
	var delayConditionMetCount int
	err = db.QueryRow(delayCheckQuery, clusterSubStringPattern).Scan(&delayConditionMetCount)
	if err != nil {
		return nil, "db_error", fmt.Errorf("delay condition check query failed for cluster %s: %w", clusterName, err)
	}

	if delayConditionMetCount == 0 {
		return nil, "no_test_results", fmt.Errorf("last datapoint for cluster %s was older than %d hours", clusterName, (delayInSeconds/60)/60)
	}

    // KPI Errors check for the health of the given cluster
	rows, err := db.Query(dataQuery, clusterSubStringPattern)
	if err != nil {
		return nil, "db_error", fmt.Errorf("database query failed for cluster %s: %w", clusterName, err)
	}
	defer rows.Close()

	var parsedErrorReadings []int64
	var rawValuesForLog []string

	for rows.Next() {
		var kpiErrorValueStr sql.NullString
		if err := rows.Scan(&kpiErrorValueStr); err != nil {
			// Error during row scan is considered a database error.
			return nil, "db_error", fmt.Errorf("failed to scan row for cluster %s: %w", clusterName, err)
		}

		if !kpiErrorValueStr.Valid || kpiErrorValueStr.String == "" {
			// NULL or empty values are data processing issues.
			return nil, "db_error", fmt.Errorf("found NULL or empty kpi_error_value in one of the last %d rows for cluster %s. Raw values so far: %v", requiredRecentReadings, clusterName, rawValuesForLog)
		}
		rawValuesForLog = append(rawValuesForLog, kpiErrorValueStr.String)

		kpiErrorCount, err := strconv.ParseInt(kpiErrorValueStr.String, 10, 64)
		if err != nil {
			// Failure to parse the error count is a data processing issue.
			return nil, "db_error", fmt.Errorf("failed to parse kpi_error_value '%s' as integer for cluster %s: %w", kpiErrorValueStr.String, clusterName, err)
		}
		parsedErrorReadings = append(parsedErrorReadings, kpiErrorCount)
	}

	if err := rows.Err(); err != nil {
		// Errors encountered during iteration (e.g., network issues during streaming) are database errors.
		return nil, "db_error", fmt.Errorf("error during row iteration for cluster %s: %w", clusterName, err)
	}

	if len(parsedErrorReadings) < requiredRecentReadings {
		// Not enough data points is considered a database/query issue.
		return nil, "db_error", fmt.Errorf("expected %d data points for cluster %s, but query returned %d. Raw values: %v", requiredRecentReadings, clusterName, len(parsedErrorReadings), rawValuesForLog)
	}

	return parsedErrorReadings, "data_ok", nil
}

// fetchAndExportMetrics orchestrates fetching data and updating Prometheus metrics for all target clusters.
func fetchAndExportMetrics(db *sql.DB) {
	for _, clusterName := range targetClusters {
		reasonForError := ""
		kpiErrorReadings, internalStatusMsg, err := getKpiErrorReadings(db, clusterName)

		if err != nil {
			// An error occurred (DB error, parse error, insufficient data, etc.).
			reasonForError = internalStatusMsg
			log.Printf("Error for cluster '%s': %s. Error details: %v", clusterName, reasonForError, err)
			log.Printf("kanary_up metric will be set to 1 for cluster %s due to kanary_error != 0: %s. Error details: %v", clusterName, reasonForError, err)
			kanaryErrorMetric.WithLabelValues(clusterName, reasonForError).Set(1)
			// Keep kanary up, incase of kanary_error
            kanaryUpMetric.WithLabelValues(clusterName).Set(1)
		} else {
			// Successfully retrieved and parsed data; no error.
			kanaryErrorMetric.WithLabelValues(clusterName, "db_error").Set(0)
			kanaryErrorMetric.WithLabelValues(clusterName, "no_test_results").Set(0)

			// Determine signal status: UP if at least one error reading is <= 0, DOWN if all are > 0.
			down := true
			if len(kpiErrorReadings) == 0 {
				// This case should ideally be caught by getKpiErrorReadings returning an error.
				// If it somehow occurs, treat as not all readings being strictly positive.
				down = false
			} else {
				for _, errorCount := range kpiErrorReadings {
					if errorCount <= 0 {
						down = false
						break
					}
				}
			}

			if down {
				// All recent error readings are > 0, so the signal is DOWN.
				kanaryUpMetric.WithLabelValues(clusterName).Set(0)
				log.Printf("KO: Kanary signal for cluster '%s' is DOWN (all last %d error readings > 0): %v. Error status: %s", clusterName, requiredRecentReadings, kpiErrorReadings, reasonForError)
			} else {
				// At least one recent error reading is <= 0, so the signal is UP.
				kanaryUpMetric.WithLabelValues(clusterName).Set(1)
				log.Printf("OK: Kanary signal for cluster '%s' is UP: %v.", clusterName, kpiErrorReadings)
			}
		}
	}
}

func main() {
	databaseURL := os.Getenv(dbURLEnvVar)
	if databaseURL == "" {
		log.Fatalf("FATAL: Environment variable %s is not set or is empty. Example: export %s=\"postgres://user:pass@host:port/db?sslmode=disable\"", dbURLEnvVar, dbURLEnvVar)
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("FATAL: Error connecting to the database using DSN from %s: %v", dbURLEnvVar, err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatalf("FATAL: Error pinging database: %v", err)
	}
	log.Println("Successfully connected to the database.")

	// Create a new PedanticRegistry.
	reg := prometheus.NewPedanticRegistry()

	// Register metrics with the new PedanticRegistry.
	reg.MustRegister(kanaryUpMetric)
	reg.MustRegister(kanaryErrorMetric)

	// Expose the registered metrics via HTTP.
	// Use promhttp.HandlerFor to specify the PedanticRegistry.
	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	go func() {
		log.Println("Prometheus exporter starting on :8000/metrics ...")
		if err := http.ListenAndServe(":8000", nil); err != nil {
			log.Fatalf("FATAL: Error starting Prometheus HTTP server: %v", err)
		}
	}()

	log.Println("Performing initial metrics fetch...")
	fetchAndExportMetrics(db)
	log.Println("Initial metrics fetch complete.")

	// Periodically fetch metrics. The interval could be made configurable.
	scrapeInterval := 300 * time.Second
	log.Printf("Starting periodic metrics fetch every %v.", scrapeInterval)
	ticker := time.NewTicker(scrapeInterval)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Fetching and exporting metrics...")
		fetchAndExportMetrics(db)
		log.Println("Metrics fetch complete.")
	}
}
