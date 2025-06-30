package main

import (
	"testing"

	"errors"

	"regexp"

	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestIsKanaryAlive(t *testing.T) {
	tests := []struct {
		name        string
		errorCounts []int
		want        bool
	}{
		{
			name:        "At least one zero (alive)",
			errorCounts: []int{1, 0, 2},
			want:        true,
		},
		{
			name:        "All non-zero (not alive)",
			errorCounts: []int{1, 2, 3},
			want:        false,
		},
		{
			name:        "All zero (alive)",
			errorCounts: []int{0, 0, 0},
			want:        true,
		},
		{
			name:        "Empty slice (not alive)",
			errorCounts: []int{},
			want:        false,
		},
		{
			name:        "Negative values (not alive)",
			errorCounts: []int{-1, -2, -3},
			want:        false,
		},
		{
			name:        "Zero at end (alive)",
			errorCounts: []int{5, 4, 0},
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsKanaryAlive(tt.errorCounts)
			if got != tt.want {
				t.Errorf("IsKanaryAlive(%v) = %v; want %v", tt.errorCounts, got, tt.want)
			}
		})
	}
}

func TestSetDataBaseErrorState(t *testing.T) {
	cluster := "test-cluster"

	// Reset metrics before test
	kanaryUpMetric.Reset()
	kanaryErrorMetric.Reset()

	setDataBaseErrorState(cluster)

	// Check kanary_up = 1
	if got := testutil.ToFloat64(kanaryUpMetric.WithLabelValues(cluster)); got != 1 {
		t.Errorf("kanaryUpMetric = %v; want 1", got)
	}
	// Check kanary_error{reason="db_error"} = 1
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "db_error")); got != 1 {
		t.Errorf("kanaryErrorMetric[db_error] = %v; want 1", got)
	}
	// Check kanary_error{reason="no_test_results"} = 0
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "no_test_results")); got != 0 {
		t.Errorf("kanaryErrorMetric[no_test_results] = %v; want 0", got)
	}
}

func TestSetNoTestResultsErrorState(t *testing.T) {
	cluster := "test-cluster"

	// Reset metrics before test
	kanaryUpMetric.Reset()
	kanaryErrorMetric.Reset()

	setNoTestResultsErrorState(cluster)

	// Check kanary_up = 1
	if got := testutil.ToFloat64(kanaryUpMetric.WithLabelValues(cluster)); got != 1 {
		t.Errorf("kanaryUpMetric = %v; want 1", got)
	}
	// Check kanary_error{reason="db_error"} = 0
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "db_error")); got != 0 {
		t.Errorf("kanaryErrorMetric[db_error] = %v; want 0", got)
	}
	// Check kanary_error{reason="no_test_results"} = 1
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "no_test_results")); got != 1 {
		t.Errorf("kanaryErrorMetric[no_test_results] = %v; want 1", got)
	}
}

func TestSetKanaryUpState(t *testing.T) {
	cluster := "test-cluster"

	// Reset metrics before test
	kanaryUpMetric.Reset()
	kanaryErrorMetric.Reset()

	setKanaryUpState(cluster)

	// Check kanary_up = 1
	if got := testutil.ToFloat64(kanaryUpMetric.WithLabelValues(cluster)); got != 1 {
		t.Errorf("kanaryUpMetric = %v; want 1", got)
	}
	// Check kanary_error{reason="db_error"} = 0
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "db_error")); got != 0 {
		t.Errorf("kanaryErrorMetric[db_error] = %v; want 0", got)
	}
	// Check kanary_error{reason="no_test_results"} = 0
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "no_test_results")); got != 0 {
		t.Errorf("kanaryErrorMetric[no_test_results] = %v; want 0", got)
	}
}

func TestSetKanaryDownState(t *testing.T) {
	cluster := "test-cluster"

	// Reset metrics before test
	kanaryUpMetric.Reset()
	kanaryErrorMetric.Reset()

	setKanaryDownState(cluster)

	// Check kanary_up = 0
	if got := testutil.ToFloat64(kanaryUpMetric.WithLabelValues(cluster)); got != 0 {
		t.Errorf("kanaryUpMetric = %v; want 0", got)
	}
	// Check kanary_error{reason="db_error"} = 0
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "db_error")); got != 0 {
		t.Errorf("kanaryErrorMetric[db_error] = %v; want 0", got)
	}
	// Check kanary_error{reason="no_test_results"} = 0
	if got := testutil.ToFloat64(kanaryErrorMetric.WithLabelValues(cluster, "no_test_results")); got != 0 {
		t.Errorf("kanaryErrorMetric[no_test_results] = %v; want 0", got)
	}
}

func TestGetTestErrorCounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock database: %v", err)
	}
	defer db.Close()

	cluster := "test-cluster"
	queryRegex := regexp.QuoteMeta(dataQuery)
	testCases := []struct {
		name         string
		mockSetup    func()
		expectResult []int
		expectErr    bool
	}{
		{
			name: "success",
			mockSetup: func() {
				rows := sqlmock.NewRows([]string{"error_count_int"}).
					AddRow(0).
					AddRow(1).
					AddRow(2)
				mock.ExpectQuery(queryRegex).
					WithArgs("%"+cluster+"%", 3).
					WillReturnRows(rows)
			},
			expectResult: []int{0, 1, 2},
			expectErr:    false,
		},
		{
			name: "scan error",
			mockSetup: func() {
				rows := sqlmock.NewRows([]string{"error_count_int"}).
					AddRow("not-an-int").
					AddRow(1).
					AddRow(2)
				mock.ExpectQuery(queryRegex).
					WithArgs("%"+cluster+"%", 3).
					WillReturnRows(rows)
			},
			expectResult: nil,
			expectErr:    true,
		},
		{
			name: "row count mismatch",
			mockSetup: func() {
				rows := sqlmock.NewRows([]string{"error_count_int"}).
					AddRow(1).
					AddRow(2) // Only 2 rows, but expect 3
				mock.ExpectQuery(queryRegex).
					WithArgs("%"+cluster+"%", 3).
					WillReturnRows(rows)
			},
			expectResult: nil,
			expectErr:    true,
		},
		{
			name: "query error",
			mockSetup: func() {
				mock.ExpectQuery(queryRegex).
					WithArgs("%"+cluster+"%", 3).
					WillReturnError(errors.New("db error"))
			},
			expectResult: nil,
			expectErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mock.ExpectationsWereMet() // Clear any previous expectations
			tc.mockSetup()
			result, err := GetTestErrorCounts(db, cluster, 3)
			if tc.expectErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if len(result) != len(tc.expectResult) {
					t.Errorf("result length = %d; want %d", len(result), len(tc.expectResult))
				}
				for i := range tc.expectResult {
					if result[i] != tc.expectResult[i] {
						t.Errorf("result[%d] = %d; want %d", i, result[i], tc.expectResult[i])
					}
				}
			}
		})
	}
}

func TestCheckSufficientTests(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock database: %v", err)
	}
	defer db.Close()

	cluster := "test-cluster"
	rowCountQueryRegex := regexp.QuoteMeta(rowCountQuery)

	testCases := []struct {
		name      string
		mockSetup func()
		wantErr   bool
	}{
		{
			name: "sufficient rows",
			mockSetup: func() {
				mock.ExpectQuery(rowCountQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(5))
			},
			wantErr: false,
		},
		{
			name: "insufficient rows",
			mockSetup: func() {
				mock.ExpectQuery(rowCountQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
			},
			wantErr: true,
		},
		{
			name: "query error",
			mockSetup: func() {
				mock.ExpectQuery(rowCountQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnError(errors.New("db error"))
			},
			wantErr: true,
		},
		{
			name: "no rows returned",
			mockSetup: func() {
				mock.ExpectQuery(rowCountQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnRows(sqlmock.NewRows([]string{"count"}))
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mock.ExpectationsWereMet() // Clear any previous expectations
			tc.mockSetup()
			err := CheckSufficientTests(db, cluster, 3)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestCheckLatestTestIsRecent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock database: %v", err)
	}
	defer db.Close()

	cluster := "test-cluster"
	delayCheckQueryRegex := regexp.QuoteMeta(delayCheckQuery)
	epochNow := time.Now().Unix()
	tolerance := int64(3600) // 1 hour

	testCases := []struct {
		name       string
		mockSetup  func()
		expectType string
		expectErr  bool
	}{
		{
			name: "recent test (OK)",
			mockSetup: func() {
				// ended_epoch is now, so it's recent
				mock.ExpectQuery(delayCheckQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnRows(sqlmock.NewRows([]string{"ended_epoch"}).AddRow(float64(epochNow)))
			},
			expectType: "",
			expectErr:  false,
		},
		{
			name: "test too old",
			mockSetup: func() {
				// ended_epoch is far in the past
				oldEpoch := float64(epochNow - tolerance - 100)
				mock.ExpectQuery(delayCheckQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnRows(sqlmock.NewRows([]string{"ended_epoch"}).AddRow(oldEpoch))
			},
			expectType: "no_test_results",
			expectErr:  true,
		},
		{
			name: "query error",
			mockSetup: func() {
				mock.ExpectQuery(delayCheckQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnError(errors.New("db error"))
			},
			expectType: "db_error",
			expectErr:  true,
		},
		{
			name: "no rows returned",
			mockSetup: func() {
				mock.ExpectQuery(delayCheckQueryRegex).
					WithArgs("%" + cluster + "%").
					WillReturnRows(sqlmock.NewRows([]string{"ended_epoch"}))
			},
			expectType: "db_error",
			expectErr:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mock.ExpectationsWereMet() // Clear any previous expectations
			tc.mockSetup()
			typeStr, err := CheckLatestTestIsRecent(db, cluster, tolerance)
			if tc.expectErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				if typeStr != tc.expectType {
					t.Errorf("expected type '%s', got '%s'", tc.expectType, typeStr)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if typeStr != tc.expectType {
					t.Errorf("expected type '%s', got '%s'", tc.expectType, typeStr)
				}
			}
		})
	}
}
