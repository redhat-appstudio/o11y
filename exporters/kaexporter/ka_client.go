package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ── Retry logic with exponential backoff ─────────────────────────────────────

// retryableHTTP wraps HTTP requests with exponential backoff retry logic.
// Retries on transient errors: network failures, 429 (rate limit), 5xx (server errors).
// Does NOT retry on permanent errors: 4xx (except 429), context cancellation.
func (e *KAExporter) retryableHTTP(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error

	delay := e.retry.initialDelay

	for attempt := 0; attempt <= e.retry.maxRetries; attempt++ {
		// Apply exponential backoff delay (skip on first attempt)
		if attempt > 0 {
			// Add jitter (±25%) to prevent thundering herd
			jitter := time.Duration(rand.Int63n(int64(delay) / 2))
			sleepTime := delay - delay/4 + jitter

			select {
			case <-time.After(sleepTime):
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			}

			// Exponential backoff: multiply delay for next iteration
			delay = time.Duration(float64(delay) * e.retry.multiplier)
			if delay > e.retry.maxDelay {
				delay = e.retry.maxDelay
			}
		}

		// Execute HTTP request
		resp, err := e.httpClient.Do(req)

		// Success case: 2xx or 3xx response
		if err == nil && resp.StatusCode < 400 {
			return resp, nil
		}

		// Non-retryable 4xx errors (except 429 rate limit)
		if err == nil && resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			return resp, nil // Return immediately, caller will handle
		}

		// Check if error/response is retryable
		reason, retryable := classifyError(err, resp)
		if !retryable {
			// Permanent error, don't retry
			if resp != nil {
				return resp, err
			}
			return nil, err
		}

		// Close response body before retry (prevent leak)
		if resp != nil {
			io.Copy(io.Discard, resp.Body) // Drain body
			resp.Body.Close()
			lastResp = nil // Don't return this response
		}

		// Track retry attempt
		e.retryAttemptsTotal.WithLabelValues(e.cluster, reason).Inc()

		// Log retry (only after first failure)
		if attempt < e.retry.maxRetries {
			log.Printf("KubeArchive API retry %d/%d for %s: %s (delay: %v)",
				attempt+1, e.retry.maxRetries, req.URL.Path, reason, delay)
		}

		lastErr = err
	}

	// Max retries exhausted
	reason, _ := classifyError(lastErr, lastResp)
	e.retryExhaustedTotal.WithLabelValues(e.cluster, reason).Inc()

	if lastErr != nil {
		return nil, fmt.Errorf("max retries (%d) exhausted: %w", e.retry.maxRetries, lastErr)
	}
	if lastResp != nil {
		return lastResp, fmt.Errorf("max retries (%d) exhausted, status: %d", e.retry.maxRetries, lastResp.StatusCode)
	}
	return nil, fmt.Errorf("max retries (%d) exhausted", e.retry.maxRetries)
}

// classifyError determines if an error/response is retryable and returns a reason label.
// Returns (reason, retryable).
func classifyError(err error, resp *http.Response) (string, bool) {
	// Network errors (timeout, connection refused, DNS failure) are retryable
	if err != nil {
		// Context cancellation is NOT retryable
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "context_cancelled", false
		}

		// Check for network errors
		var netErr net.Error
		if errors.As(err, &netErr) {
			if netErr.Timeout() {
				return "network_timeout", true
			}
			return "network_error", true
		}

		// Other errors (DNS, connection refused, etc.)
		return "network_error", true
	}

	// HTTP response errors
	if resp != nil {
		switch {
		case resp.StatusCode == 429:
			return "rate_limit", true
		case resp.StatusCode >= 500:
			return "server_error", true
		case resp.StatusCode >= 400:
			// 4xx errors (except 429) are NOT retryable
			return fmt.Sprintf("client_error_%d", resp.StatusCode), false
		}
	}

	// Unknown error, don't retry
	return "unknown", false
}

// pageURL appends pagination parameters and an optional creation-timestamp filter to base.
func pageURL(base, continueToken, since string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("limit", strconv.Itoa(kaPageLimit))
	if continueToken != "" {
		q.Set("continue", continueToken)
	}
	if since != "" {
		q.Set("creationTimestampAfter", since)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// fetchPage executes a single GET request with retry logic and closes the response body before returning.
func (e *KAExporter) fetchPage(ctx context.Context, pageURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+e.kaToken)

	resp, err := e.retryableHTTP(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// streamPLRs fetches PipelineRuns page-by-page, calling fn once per page.
// since is an RFC3339 timestamp passed as creationTimestampAfter to KubeArchive,
// limiting results to the configured look-back window (KA_WINDOW_HOURS).
// KubeArchive returns items newest-first; callers may rely on this ordering.
// fn receives the page slice and must not retain references beyond its return.
func (e *KAExporter) streamPLRs(ctx context.Context, baseURL, since, namespace string, fn func(page []PipelineRun)) error {
	continueToken := ""
	total := 0
	for {
		u, err := pageURL(baseURL, continueToken, since)
		if err != nil {
			return err
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("API returned status %d: %s", status, string(body))
		}
		var page ListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		fn(page.Items)
		total += len(page.Items)
		if total >= kaMaxItems {
			log.Printf("WARNING: streamPLRs %s: reached kaMaxItems cap (%d); "+
				"check KubeArchive retention — results may be incomplete", baseURL, kaMaxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "pipelineruns", namespace).Inc()
			break
		}
		if page.Metadata.Continue == "" {
			break
		}
		continueToken = page.Metadata.Continue
		log.Printf("streamPLRs %s: processed %d items, continuing (token len=%d)",
			baseURL, total, len(continueToken))
	}
	return nil
}

// streamSnapshots fetches Snapshot CRs page-by-page, calling fn once per page.
// since limits results to the configured look-back window (KA_WINDOW_HOURS).
// Intended to be called with snapshotIndex.add so pages are indexed and then freed.
func (e *KAExporter) streamSnapshots(ctx context.Context, baseURL, since, namespace string, fn func(page []Snapshot)) error {
	continueToken := ""
	total := 0
	for {
		u, err := pageURL(baseURL, continueToken, since)
		if err != nil {
			return err
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return fmt.Errorf("API returned status %d: %s", status, string(body))
		}
		var page SnapshotListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		fn(page.Items)
		total += len(page.Items)
		if total >= kaMaxItems {
			log.Printf("WARNING: streamSnapshots %s: reached kaMaxItems cap (%d); "+
				"check KubeArchive retention — results may be incomplete", baseURL, kaMaxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "snapshots", namespace).Inc()
			break
		}
		if page.Metadata.Continue == "" {
			break
		}
		continueToken = page.Metadata.Continue
	}
	return nil
}

// fetchReleases fetches all Release CRs from a KubeArchive endpoint with pagination.
// since limits results to the configured look-back window (KA_WINDOW_HOURS).
func (e *KAExporter) fetchReleases(ctx context.Context, baseURL, since, namespace string) ([]Release, error) {
	var all []Release
	continueToken := ""
	for {
		u, err := pageURL(baseURL, continueToken, since)
		if err != nil {
			return nil, err
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("API returned status %d: %s", status, string(body))
		}
		var page ReleaseListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if len(all) >= kaMaxItems {
			log.Printf("WARNING: fetchReleases %s: reached kaMaxItems cap (%d); "+
				"check KubeArchive retention — results may be incomplete", baseURL, kaMaxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "releases", namespace).Inc()
			break
		}
		if page.Metadata.Continue == "" {
			break
		}
		continueToken = page.Metadata.Continue
		log.Printf("fetchReleases %s: fetched %d items so far, continuing (token len=%d)",
			baseURL, len(all), len(continueToken))
	}
	return all, nil
}

// fetchSpecificPipelineRun fetches a single PipelineRun by name without time filtering.
// Used by the lookback mechanism to fetch builds for orphaned releases.
// Returns nil with no error if the PLR is not found (404).
func (e *KAExporter) fetchSpecificPipelineRun(ctx context.Context, namespace, name string) (*PipelineRun, error) {
	url := fmt.Sprintf("%s/apis/tekton.dev/v1/namespaces/%s/pipelineruns/%s",
		e.kaHost, namespace, name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.kaToken)

	resp, err := e.retryableHTTP(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found - build may be pre-retention
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var plr PipelineRun
	if err := json.NewDecoder(resp.Body).Decode(&plr); err != nil {
		return nil, err
	}

	return &plr, nil
}
