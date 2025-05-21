package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"slices"

	_ "github.com/lib/pq" // PostgreSQL driver
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	tableName = "data"
	// dbURLEnvVar is the environment variable name for the database connection string.
	dbURLEnvVar = "CONNECTION_STRING"
	// requiredNumberOfRows is the number of database entries to consider for the kanary_up.
	requiredNumberOfRows = 3
	// Amount of time in seconds allowed without a new entry in the database
	toleranceInSeconds int64 = 3 * 60 * 60
	// The interval at which metrics will be collected and exported
	scrapeInterval = 1 * time.Minute
)

var (
	kanaryUpMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kanary_up",
			// Help string updated to reflect the new logic: UP if at least one error reading <= 0, DOWN if all > 0.
			Help: fmt.Sprintf("Kanary signal: 1 if at least one of last %d error readings is 0 or less, 0 if all last %d error readings are greater than 0. Only updated when %d valid data points are available.", requiredNumberOfRows, requiredNumberOfRows, requiredNumberOfRows),
		},
		[]string{"instance"},
	)

	kanaryErrorMetric = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "kanary_error",
			Help: "Binary indicator of an error in processing for Kanary signal (1 if interrupted, 0 otherwise). An error prevents kanary_up from being updated. The 'reason' label provides details on the error type.",
		},
		[]string{"instance", "reason"},
	)

	// cluster shortnames.
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
	rowCountQuery = fmt.Sprintf(`
		SELECT COUNT(*)
		FROM %s
		WHERE
			label_values->>'.metadata.env.MEMBER_CLUSTER' LIKE $1
			-- This limits the results to container e2e test results
			AND label_values->>'.repo_type' = 'nodejs-devfile-sample';
	`, tableName)

	// Delay check
	delayCheckQuery = fmt.Sprintf(`
		SELECT
			-- The replace is to comply with the Standard ISO 8601 format
			EXTRACT(epoch FROM (REPLACE(label_values->>'.ended', ',', '.'))::timestamptz) AS ended_epoch
		FROM
			%s
		WHERE
			label_values->>'.metadata.env.MEMBER_CLUSTER' LIKE $1
			-- This limits the results to container e2e test results
			AND label_values->>'.repo_type' = 'nodejs-devfile-sample'
		ORDER BY
			EXTRACT(epoch FROM start) DESC
		LIMIT 1
	`, tableName)

	// Query fetches error counts for X number of most recent Konflux cluster load tests.
	dataQuery = fmt.Sprintf(`
		SELECT
			(label_values->>'__results_measurements_KPI_errors')::integer AS error_count_int
		FROM
			%s
		WHERE
			label_values->>'.metadata.env.MEMBER_CLUSTER' LIKE $1
			-- This limits the results to container e2e test results
			AND label_values->>'.repo_type' = 'nodejs-devfile-sample'
		ORDER BY
			EXTRACT(epoch FROM start) DESC
		LIMIT $2;
	`, tableName)
)

/*
setDataBaseErrorState updates Prometheus metrics for a cluster when a database error is encountered.

	It sets kanary_up to 1 (better than giving a false down kanary signal),
	kanary_error with reason "db_error" to 1, and kanary_error with reason "no_test_results" to 0.
*/
func setDataBaseErrorState(clusterName string) {
	kanaryUpMetric.WithLabelValues(clusterName).Set(1)
	kanaryErrorMetric.WithLabelValues(clusterName, "db_error").Set(1)
	kanaryErrorMetric.WithLabelValues(clusterName, "no_test_results").Set(0)
}

/*
setNoTestResultsErrorState updates Prometheus metrics for a cluster when recent test results are missing or too old.

	It sets kanary_up to 1 (better than giving a false down kanary signal),
	kanary_error with reason "no_test_results" to 1, and kanary_error with reason "db_error" to 0.
*/
func setNoTestResultsErrorState(clusterName string) {
	kanaryUpMetric.WithLabelValues(clusterName).Set(1)
	kanaryErrorMetric.WithLabelValues(clusterName, "db_error").Set(0)
	kanaryErrorMetric.WithLabelValues(clusterName, "no_test_results").Set(1)
}

/*
setKanaryUpState updates Prometheus metrics for a cluster to indicate it is healthy and operational (Kanary signal is UP).

	It sets kanary_up to 1 and clears any previous error states by setting kanary_error for "db_error" and "no_test_results" to 0.
*/
func setKanaryUpState(clusterName string) {
	kanaryUpMetric.WithLabelValues(clusterName).Set(1)
	kanaryErrorMetric.WithLabelValues(clusterName, "db_error").Set(0)
	kanaryErrorMetric.WithLabelValues(clusterName, "no_test_results").Set(0)
}

/*
setKanaryDownState updates Prometheus metrics for a cluster to indicate it is non-operational (Kanary signal is DOWN)

	It sets kanary_up to 0 and clears any previous error states by setting kanary_error for "db_error" and "no_test_results" to 0.
*/
func setKanaryDownState(clusterName string) {
	kanaryUpMetric.WithLabelValues(clusterName).Set(0)
	kanaryErrorMetric.WithLabelValues(clusterName, "db_error").Set(0)
	kanaryErrorMetric.WithLabelValues(clusterName, "no_test_results").Set(0)
}

/*
GetTestErrorCounts retrieves number of errors encounted in X most recent test results for the specified cluster.

	Returns a slice of error counts and an error if any database query or row scanning operation fails.
	Any errors encountered here should be treated as a "db_error"
*/
func GetTestErrorCounts(db *sql.DB, clusterName string, numTests int) ([]int, error) {
	clusterSubStringPattern := "%" + clusterName + "%"

	rows, err := db.Query(dataQuery, clusterSubStringPattern, numTests)
	if err != nil {
		return nil, fmt.Errorf("database query failed for cluster %s", clusterName)
	}
	defer rows.Close()

	var parsedErrorReadings []int

	for rows.Next() {
		var error_value int
		if err := rows.Scan(&error_value); err != nil {
			return nil, fmt.Errorf("failed to scan row for cluster %s", clusterName)
		}
		parsedErrorReadings = append(parsedErrorReadings, error_value)
	}

	if err := rows.Err(); err != nil {
		// Errors encountered during iteration (e.g., network issues during streaming) are database errors.
		return nil, fmt.Errorf("error during row iteration for cluster %s", clusterName)
	}

	if len(parsedErrorReadings) != numTests {
		return nil, fmt.Errorf("error extracting %d error counts (found %v) from cluster %s", numTests, parsedErrorReadings, clusterName)
	}

	return parsedErrorReadings, nil
}

/*
CheckSufficientTests checks if the database row count for clusterName is sufficient.

	These rows contain the results of the e2e tests of a given cluster.
	Returns nil if the number of fetched rows is sufficient. Otherwise, returns err.
	Any error encountered here should be treated as a "db_error"
*/
func CheckSufficientTests(db *sql.DB, clusterName string, minTestsCount int) error {
	clusterSubStringPattern := "%" + clusterName + "%"
	var testsCount int
	err := db.QueryRow(rowCountQuery, clusterSubStringPattern).Scan(&testsCount)
	if err != nil {
		if err == sql.ErrNoRows {
			testsCount = 0
		} else {
			return fmt.Errorf("database row count query failed for cluster %s", clusterName)
		}
	}

	if testsCount < minTestsCount {
		return fmt.Errorf("insufficient rows available for cluster %s (%d < %d)", clusterName, testsCount, minTestsCount)
	}

	return nil
}

/*
CheckLatestTestIsRecent checks if the latest test result (row) for a given cluster has ended within the last X seconds.

	Returns kanary error type ("db_error", "no_test_results"), and an error if there is an error.
	Returns kanary error type "", and nil error if the check is successful.
*/
func CheckLatestTestIsRecent(db *sql.DB, clusterName string, maxSecondsSinceLastRun int64) (string, error) {
	clusterSubStringPattern := "%" + clusterName + "%"

	var latestEndedEpoch float64
	err := db.QueryRow(delayCheckQuery, clusterSubStringPattern).Scan(&latestEndedEpoch)
	if err != nil {
		return "db_error", fmt.Errorf("delay condition check query failed for cluster %s", clusterName)
	}
	epochNow := time.Now().Unix()
	earliestEpochAllowed := float64(epochNow - maxSecondsSinceLastRun)
	lastRunHours := ((float64(epochNow) - latestEndedEpoch) / 60) / 60
	log.Printf("last run (%s): %.2f hours ago", clusterName, lastRunHours)

	if latestEndedEpoch < earliestEpochAllowed {
		return "no_test_results", fmt.Errorf("last datapoint for cluster %s was older than %d hours, it was %.2f hours ago", clusterName, (maxSecondsSinceLastRun/60)/60, lastRunHours)
	}

	return "", nil
}

/*
IsKanaryAlive determines if the Kanary signal should be considered "alive" based on a slice of error counts from recent tests.

	Returns true if at least one error count is 0 (meaning at least one test succeeded or had no errors).
	Returns flase if all provided error counts are greater than 0, indicating that all inspected tests failed.
*/
func IsKanaryAlive(errorCounts []int) bool {
	return slices.Contains(errorCounts, 0)
}

// fetchAndExportMetrics orchestrates fetching data and updating Prometheus metrics for all target clusters.
func fetchAndExportMetrics(db *sql.DB) {
	for _, clusterName := range targetClusters {
		log.Printf("----- %s -----\n", clusterName)
		err := CheckSufficientTests(db, clusterName, requiredNumberOfRows)

		if err != nil {
			log.Println(err)
			setDataBaseErrorState(clusterName)
			continue
		}

		kanaryErrorType, err := CheckLatestTestIsRecent(db, clusterName, toleranceInSeconds)

		if err != nil {
			if kanaryErrorType == "no_test_results" {
				setNoTestResultsErrorState(clusterName)
				continue
			} else {
				setDataBaseErrorState(clusterName)
				continue
			}
		}

		errorCounts, err := GetTestErrorCounts(db, clusterName, requiredNumberOfRows)

		if err != nil {
			setDataBaseErrorState(clusterName)
			continue
		}

		if IsKanaryAlive(errorCounts) {
			setKanaryUpState(clusterName)
			log.Printf("OK: Kanary signal for cluster '%s' is UP: %v.", clusterName, errorCounts)
		} else {
			setKanaryDownState(clusterName)
			log.Printf("KO: Kanary signal for cluster '%s' is DOWN (all last %d error readings > 0): %v.", clusterName, requiredNumberOfRows, errorCounts)
		}
	}
}

func main() {
	dataSourceName := os.Getenv(dbURLEnvVar)
	if dataSourceName == "" {
		log.Fatalf("FATAL: Environment variable %s is not set or is empty. Example: export %s=\"postgres://{user}:{pass}@{host}:{port}/{db}?sslmode=disable\"", dbURLEnvVar, dbURLEnvVar)
	}

	db, err := sql.Open("postgres", dataSourceName)
	if err != nil {
		log.Fatalf("FATAL: Error validating the DSN(Data Source Name) the from %s", dbURLEnvVar)
	}
	defer db.Close()

	// Verify DB is reachable and credentials valid
	if err = db.Ping(); err != nil {
		log.Fatalf("FATAL: Error pinging database")
	}
	log.Println("Successfully connected to the database.")

	reg := prometheus.NewRegistry()

	reg.MustRegister(kanaryUpMetric)
	reg.MustRegister(kanaryErrorMetric)

	// Expose the registered metrics via HTTP.
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
	log.Printf("Starting periodic metrics fetch every %v.", scrapeInterval)
	ticker := time.NewTicker(scrapeInterval)
	defer ticker.Stop()

	for range ticker.C {
		log.Println("Fetching and exporting metrics...")
		fetchAndExportMetrics(db)
		log.Println("Metrics fetch complete.")
	}
}
