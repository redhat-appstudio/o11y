package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
)

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

// fetchPage executes a single GET request and closes the response body before returning.
func (e *KAExporter) fetchPage(ctx context.Context, pageURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+e.kaToken)

	resp, err := e.httpClient.Do(req)
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

	resp, err := e.httpClient.Do(req)
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
