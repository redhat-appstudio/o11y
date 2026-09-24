package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ── Retryable HTTP Tests ──────────────────────────────────────────────────────

func TestRetryableHTTP(t *testing.T) {
	t.Run("success on first attempt", func(t *testing.T) {
		attempts := int32(0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[]}`))
		}))
		defer server.Close()

		exporter := newTestExporter(server.URL, 3, 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)

		resp, err := exporter.retryableHTTP(context.Background(), req)
		assertNoError(t, err)
		defer resp.Body.Close()

		assertEqual(t, "status", resp.StatusCode, http.StatusOK)
		assertEqual(t, "attempts", atomic.LoadInt32(&attempts), int32(1))
	})

	t.Run("network error recovery", func(t *testing.T) {
		attempts := int32(0)
		failUntil := int32(2)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := atomic.AddInt32(&attempts, 1)
			if current <= failUntil {
				if hj, ok := w.(http.Hijacker); ok {
					conn, _, _ := hj.Hijack()
					conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[]}`))
		}))
		defer server.Close()

		exporter := newTestExporter(server.URL, 3, 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)

		resp, err := exporter.retryableHTTP(context.Background(), req)
		assertNoError(t, err)
		defer resp.Body.Close()

		assertEqual(t, "attempts", atomic.LoadInt32(&attempts), failUntil+1)
	})

	t.Run("429 rate limit recovery", func(t *testing.T) {
		attempts := int32(0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&attempts, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[]}`))
		}))
		defer server.Close()

		exporter := newTestExporter(server.URL, 3, 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)

		resp, err := exporter.retryableHTTP(context.Background(), req)
		assertNoError(t, err)
		defer resp.Body.Close()

		assertEqual(t, "status", resp.StatusCode, http.StatusOK)
		assertEqual(t, "attempts", atomic.LoadInt32(&attempts), int32(2))
	})

	t.Run("5xx server error recovery", func(t *testing.T) {
		attempts := int32(0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&attempts, 1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"items":[]}`))
		}))
		defer server.Close()

		exporter := newTestExporter(server.URL, 3, 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)

		resp, err := exporter.retryableHTTP(context.Background(), req)
		assertNoError(t, err)
		defer resp.Body.Close()

		assertEqual(t, "attempts", atomic.LoadInt32(&attempts), int32(2))
	})

	t.Run("exhausts retries", func(t *testing.T) {
		attempts := int32(0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		maxRetries := 2
		exporter := newTestExporter(server.URL, maxRetries, 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)

		_, err := exporter.retryableHTTP(context.Background(), req)
		if err == nil {
			t.Fatal("expected error after exhausting retries")
		}

		assertEqual(t, "attempts", atomic.LoadInt32(&attempts), int32(maxRetries+1))
		assertContains(t, err.Error(), "max retries")
	})

	t.Run("4xx no retry", func(t *testing.T) {
		attempts := int32(0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		exporter := newTestExporter(server.URL, 3, 10*time.Millisecond)
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL, nil)

		resp, err := exporter.retryableHTTP(context.Background(), req)
		assertNoError(t, err)
		defer resp.Body.Close()

		assertEqual(t, "status", resp.StatusCode, http.StatusBadRequest)
		assertEqual(t, "attempts", atomic.LoadInt32(&attempts), int32(1))
	})

	t.Run("context cancelled no retry", func(t *testing.T) {
		attempts := int32(0)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		exporter := newTestExporter(server.URL, 3, 10*time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
		_, err := exporter.retryableHTTP(ctx, req)
		if err == nil {
			t.Fatal("expected context cancellation error")
		}

		if atomic.LoadInt32(&attempts) > 1 {
			t.Errorf("expected at most 1 attempt for context cancellation, got %d", attempts)
		}
	})

	t.Run("rejects requests with body", func(t *testing.T) {
		exporter := newTestExporter("http://example.com", 3, 10*time.Millisecond)
		req, _ := http.NewRequest("POST", "http://example.com/test", http.NoBody)
		req.Body = &testReadCloser{content: `{"key":"value"}`}

		_, err := exporter.retryableHTTP(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for request with body")
		}
		assertContains(t, err.Error(), "does not support requests with bodies")
	})
}

// ── Error Classification Tests ────────────────────────────────────────────────

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		resp      *http.Response
		wantLabel string
		wantRetry bool
	}{
		{"network timeout", &net.OpError{Op: "read", Err: &timeoutError{}}, nil, "network_timeout", true},
		{"network error", errors.New("connection refused"), nil, "network_error", true},
		{"context cancelled", context.Canceled, nil, "context_cancelled", false},
		{"429 rate limit", nil, &http.Response{StatusCode: 429}, "rate_limit", true},
		{"503 server error", nil, &http.Response{StatusCode: 503}, "server_error", true},
		{"400 client error", nil, &http.Response{StatusCode: 400}, "client_error_400", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, retry := classifyError(tt.err, tt.resp)
			assertEqual(t, "label", label, tt.wantLabel)
			assertEqual(t, "retry", retry, tt.wantRetry)
		})
	}
}

// ── Pagination Tests ──────────────────────────────────────────────────────────

func TestPagination(t *testing.T) {
	type pageSpec struct {
		itemCount     int
		continueToken string
	}

	tests := []struct {
		name          string
		pages         []pageSpec
		maxItems      int
		wantPages     int
		wantItems     int
		wantTruncated bool
	}{
		{
			name:          "single page",
			pages:         []pageSpec{{itemCount: 2, continueToken: ""}},
			maxItems:      100,
			wantPages:     1,
			wantItems:     2,
			wantTruncated: false,
		},
		{
			name: "multi page",
			pages: []pageSpec{
				{itemCount: 2, continueToken: "page2"},
				{itemCount: 2, continueToken: "page3"},
				{itemCount: 1, continueToken: ""},
			},
			maxItems:      100,
			wantPages:     3,
			wantItems:     5,
			wantTruncated: false,
		},
		{
			name: "truncated at maxItems",
			pages: []pageSpec{
				{itemCount: 3, continueToken: "page2"},
				{itemCount: 3, continueToken: "page3"},
			},
			maxItems:      5,
			wantPages:     2,
			wantItems:     5,
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageCount := 0
			itemIdx := 0

			e := &KAExporter{
				cluster:           "test-cluster",
				kaToken:           "test-token",
				kaHost:            "http://localhost",
				httpClient:        http.DefaultClient,
				truncationsTotal:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "truncations"}, []string{"cluster", "resource", "namespace"}),
				scrapeErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "errors"}, []string{"cluster", "reason"}),
			}

			e.httpClient.Transport = &mockRoundTripper{
				fn: func(req *http.Request) (*http.Response, error) {
					if pageCount >= len(tt.pages) {
						t.Fatalf("unexpected page request %d", pageCount)
					}

					spec := tt.pages[pageCount]
					pageCount++

					items := make([]PipelineRun, spec.itemCount)
					for i := 0; i < spec.itemCount; i++ {
						items[i] = NewPLR().UID(fmt.Sprintf("build-%d", itemIdx)).
							Name(fmt.Sprintf("build-%d", itemIdx)).CreatedAt(testBaseTime).Build()
						itemIdx++
					}

					response := ListResponse{
						Metadata: struct {
							Continue string `json:"continue"`
						}{Continue: spec.continueToken},
						Items: items,
					}

					return &http.Response{
						StatusCode: 200,
						Body:       newMockBody(response),
					}, nil
				},
			}

			ctx := context.Background()
			totalItems := 0
			wasTruncated, _, err := e.streamPLRs(ctx, "http://localhost/api/v1/pipelineruns", "", "", "test-ns", tt.maxItems, func(items []PipelineRun) {
				totalItems += len(items)
			})

			assertNoError(t, err)
			assertEqual(t, "wasTruncated", wasTruncated, tt.wantTruncated)
			assertEqual(t, "pageCount", pageCount, tt.wantPages)
			assertEqual(t, "totalItems", totalItems, tt.wantItems)
		})
	}
}

// ── Test Helpers ──────────────────────────────────────────────────────────────

func newTestExporter(kaHost string, maxRetries int, initialDelay time.Duration) *KAExporter {
	return &KAExporter{
		kaHost:     kaHost,
		kaToken:    "test-token",
		cluster:    "test-cluster",
		httpClient: &http.Client{Timeout: 5 * time.Second},
		retry: retryConfig{
			maxRetries:   maxRetries,
			initialDelay: initialDelay,
			maxDelay:     1 * time.Second,
			multiplier:   2.0,
		},
		retryAttemptsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_retry_attempts_total"},
			[]string{"cluster", "reason"},
		),
		retryExhaustedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "test_retry_exhausted_total"},
			[]string{"cluster", "reason"},
		),
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

// Mock timeout error
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

// Mock ReadCloser for testing request bodies
type testReadCloser struct {
	content string
	offset  int
}

func (f *testReadCloser) Read(p []byte) (n int, err error) {
	if f.offset >= len(f.content) {
		return 0, fmt.Errorf("EOF")
	}
	n = copy(p, f.content[f.offset:])
	f.offset += n
	return n, nil
}

func (f *testReadCloser) Close() error { return nil }
