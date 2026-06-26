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

// retryableHTTP retries transient errors (network, 429, 5xx) with exponential backoff.
// Only supports requests without bodies (GET, HEAD).
func (e *KAExporter) retryableHTTP(ctx context.Context, req *http.Request) (*http.Response, error) {
	if req.Body != nil && req.Body != http.NoBody {
		return nil, fmt.Errorf("retryableHTTP does not support requests with bodies (body consumed on first attempt)")
	}

	var lastErr error
	var lastReason string // for final metrics

	delay := e.retry.initialDelay

	for attempt := 0; attempt <= e.retry.maxRetries; attempt++ {
		if attempt > 0 {
			// Add jitter (±25%) to prevent thundering herd
			jitter := time.Duration(rand.Int63n(int64(delay) / 2))
			sleepTime := delay - delay/4 + jitter

			select {
			case <-time.After(sleepTime):
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled during retry backoff: %w", ctx.Err())
			}

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

		if err == nil && resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			return resp, nil // Return immediately, caller will handle
		}

		// Check if error/response is retryable
		reason, retryable := classifyError(err, resp)
		lastReason = reason

		if !retryable {
			// Permanent error, don't retry
			if resp != nil {
				return resp, err
			}
			return nil, err
		}

		// Drain and close body before retry
		if resp != nil {
			io.Copy(io.Discard, resp.Body) // Drain body
			resp.Body.Close()
		}

		e.retryAttemptsTotal.WithLabelValues(e.cluster, reason).Inc()

		if attempt < e.retry.maxRetries {
			log.Printf("KubeArchive API retry %d/%d for %s: %s (delay: %v)",
				attempt+1, e.retry.maxRetries, req.URL.Path, reason, delay)
		}

		lastErr = err
	}

	// Max retries exhausted - use preserved lastReason (not re-classify, which would return "unknown")
	e.retryExhaustedTotal.WithLabelValues(e.cluster, lastReason).Inc()

	if lastErr != nil {
		return nil, fmt.Errorf("max retries (%d) exhausted: %w", e.retry.maxRetries, lastErr)
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

// pageURL appends pagination parameters and optional creation-timestamp filters to base.
// since sets creationTimestampAfter, until sets creationTimestampBefore (for gap-filling).
func pageURL(base, continueToken, since, until string) (string, error) {
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
	if until != "" {
		q.Set("creationTimestampBefore", until)
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

	token, err := e.bearerToken()
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := e.retryableHTTP(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// bearerToken returns the current KubeArchive bearer token.
// If kaTokenFile is set, re-reads on each call to support kubelet-rotated projected tokens.
// Otherwise returns the static kaToken value.
func (e *KAExporter) bearerToken() (string, error) {
	if e.kaTokenFile != "" {
		return readTokenFromFile(e.kaTokenFile)
	}
	return e.kaToken, nil
}

// streamPLRs fetches PipelineRuns page-by-page, calling fn once per page.
// since/until are RFC3339 timestamps for creationTimestampAfter/Before filters.
// KubeArchive returns items newest-first
// oldestCreationTimestamp is the Metadata.CreationTimestamp of the last item seen.
func (e *KAExporter) streamPLRs(ctx context.Context, baseURL, since, until, namespace string, maxItems int, fn func(page []PipelineRun)) (bool, string, error) {
	continueToken := ""
	total := 0
	var oldestCreationTS string // Track oldest item's creation timestamp
	wasTruncated := false

	for {
		u, err := pageURL(baseURL, continueToken, since, until)
		if err != nil {
			return false, "", fmt.Errorf("build page URL for %s: %w", baseURL, err)
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return false, "", fmt.Errorf("fetch page from %s: %w", baseURL, err)
		}
		if status != http.StatusOK {
			msg := string(body)
			if len(msg) > 1024 {
				msg = msg[:1024] + "..."
			}
			return false, "", fmt.Errorf("API returned status %d: %s", status, msg)
		}
		var page ListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return false, "", fmt.Errorf("unmarshal PLR page from %s: %w", baseURL, err)
		}

		remaining := maxItems - total
		if remaining <= 0 {
			// Scenario 1: Boundary hit - fetched page but already at cap
			log.Printf("WARNING: streamPLRs %s: reached maxItems cap (%d) on boundary; stopping",
				baseURL, maxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "pipelineruns", namespace).Inc()
			wasTruncated = true
			break
		}

		items := page.Items
		truncated := false

		if len(items) > remaining {
			items = items[:remaining]
			truncated = true
		}

		// Track oldest creation timestamp before processing
		if len(items) > 0 {
			oldestCreationTS = items[len(items)-1].Metadata.CreationTimestamp
		}

		fn(items)
		total += len(items)

		if truncated {
			// Scenario 2: Partial page - processed some, dropped rest
			log.Printf("WARNING: streamPLRs %s: reached maxItems cap (%d); processed partial page (%d/%d items)",
				baseURL, maxItems, remaining, len(page.Items))
			e.truncationsTotal.WithLabelValues(e.cluster, "pipelineruns", namespace).Inc()
			wasTruncated = true
			break
		}

		if page.Metadata.Continue == "" {
			break
		}

		continueToken = page.Metadata.Continue
		log.Printf("streamPLRs %s: processed %d items, continuing (token len=%d)",
			baseURL, total, len(continueToken))
	}
	return wasTruncated, oldestCreationTS, nil
}

// fetchReleases fetches all Release CRs from a KubeArchive endpoint with pagination.
// since limits results to the configured look-back window.
func (e *KAExporter) fetchReleases(ctx context.Context, baseURL, since, namespace string, maxItems int) ([]Release, error) {
	var all []Release
	continueToken := ""
	for {
		u, err := pageURL(baseURL, continueToken, since, "")
		if err != nil {
			return nil, fmt.Errorf("build page URL for %s: %w", baseURL, err)
		}
		body, status, err := e.fetchPage(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("fetch page from %s: %w", baseURL, err)
		}
		if status != http.StatusOK {
			msg := string(body)
			if len(msg) > 1024 {
				msg = msg[:1024] + "..."
			}
			return nil, fmt.Errorf("API returned status %d: %s", status, msg)
		}
		var page ReleaseListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("unmarshal Release page from %s: %w", baseURL, err)
		}

		remaining := maxItems - len(all)
		if remaining <= 0 {
			// Scenario 1: Boundary hit - fetched page but already at cap
			log.Printf("WARNING: fetchReleases %s: reached maxItems cap (%d) on boundary; stopping",
				baseURL, maxItems)
			e.truncationsTotal.WithLabelValues(e.cluster, "releases", namespace).Inc()
			break
		}

		items := page.Items
		truncated := false

		if len(items) > remaining {
			items = items[:remaining]
			truncated = true
		}

		all = append(all, items...)

		if truncated {
			// Scenario 2: Partial page - appended some, dropped rest
			log.Printf("WARNING: fetchReleases %s: reached maxItems cap (%d); processed partial page (%d/%d items)",
				baseURL, maxItems, remaining, len(page.Items))
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
