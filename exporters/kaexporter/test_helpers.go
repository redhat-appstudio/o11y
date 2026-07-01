package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ── PipelineRun Builder ───────────────────────────────────────────────────────

type PLRBuilder struct {
	plr PipelineRun
}

func NewPLR() *PLRBuilder {
	return &PLRBuilder{plr: PipelineRun{
		Metadata: struct {
			UID               string            `json:"uid"`
			Name              string            `json:"name"`
			Namespace         string            `json:"namespace"`
			Labels            map[string]string `json:"labels"`
			Annotations       map[string]string `json:"annotations"`
			CreationTimestamp string            `json:"creationTimestamp"`
		}{
			Labels: make(map[string]string),
		},
	}}
}

func (b *PLRBuilder) UID(uid string) *PLRBuilder {
	b.plr.Metadata.UID = uid
	return b
}

func (b *PLRBuilder) Name(name string) *PLRBuilder {
	b.plr.Metadata.Name = name
	return b
}

func (b *PLRBuilder) Namespace(ns string) *PLRBuilder {
	b.plr.Metadata.Namespace = ns
	return b
}

func (b *PLRBuilder) CreatedAt(ts string) *PLRBuilder {
	b.plr.Metadata.CreationTimestamp = ts
	return b
}

func (b *PLRBuilder) StartedAt(ts string) *PLRBuilder {
	b.plr.Status.StartTime = ts
	return b
}

func (b *PLRBuilder) CompletedAt(ts string) *PLRBuilder {
	b.plr.Status.CompletionTime = ts
	return b
}

func (b *PLRBuilder) Times(created, started, completed string) *PLRBuilder {
	b.plr.Metadata.CreationTimestamp = created
	b.plr.Status.StartTime = started
	b.plr.Status.CompletionTime = completed
	return b
}

func (b *PLRBuilder) Label(key, value string) *PLRBuilder {
	if b.plr.Metadata.Labels == nil {
		b.plr.Metadata.Labels = make(map[string]string)
	}
	b.plr.Metadata.Labels[key] = value
	return b
}

func (b *PLRBuilder) Labels(labels map[string]string) *PLRBuilder {
	b.plr.Metadata.Labels = labels
	return b
}

func (b *PLRBuilder) Pipeline(name string) *PLRBuilder {
	return b.Label(labelTektonPipeline, name)
}

func (b *PLRBuilder) EventType(eventType string) *PLRBuilder {
	return b.Label(labelEventType, eventType)
}

func (b *PLRBuilder) PACEventType(eventType string) *PLRBuilder {
	return b.Label(labelPACEventType, eventType)
}

func (b *PLRBuilder) TestScenario(scenario string) *PLRBuilder {
	return b.Label(labelTestScenario, scenario)
}

func (b *PLRBuilder) Optional(optional bool) *PLRBuilder {
	if optional {
		return b.Label(labelTestOptional, "true")
	}
	return b
}

func (b *PLRBuilder) Condition(condType, status, reason string) *PLRBuilder {
	b.plr.Status.Conditions = append(b.plr.Status.Conditions, Condition{
		Type:   condType,
		Status: status,
		Reason: reason,
	})
	return b
}

func (b *PLRBuilder) Succeeded() *PLRBuilder {
	b.plr.Status.Conditions = []Condition{{Type: "Succeeded", Status: "True"}}
	return b
}

func (b *PLRBuilder) Failed(reason string) *PLRBuilder {
	if reason == "" {
		reason = "Failed"
	}
	b.plr.Status.Conditions = []Condition{{Type: "Succeeded", Status: "False", Reason: reason}}
	return b
}

func (b *PLRBuilder) Build() PipelineRun {
	return b.plr
}

// ── Release Builder ───────────────────────────────────────────────────────────

type ReleaseBuilder struct {
	rel Release
}

func NewRelease() *ReleaseBuilder {
	return &ReleaseBuilder{rel: Release{
		Metadata: struct {
			Name              string            `json:"name"`
			Namespace         string            `json:"namespace,omitempty"`
			Labels            map[string]string `json:"labels"`
			CreationTimestamp string            `json:"creationTimestamp"`
		}{
			Labels: make(map[string]string),
		},
	}}
}

func (b *ReleaseBuilder) Name(name string) *ReleaseBuilder {
	b.rel.Metadata.Name = name
	return b
}

func (b *ReleaseBuilder) Namespace(ns string) *ReleaseBuilder {
	b.rel.Metadata.Namespace = ns
	return b
}

func (b *ReleaseBuilder) CreatedAt(ts string) *ReleaseBuilder {
	b.rel.Metadata.CreationTimestamp = ts
	return b
}

func (b *ReleaseBuilder) StartedAt(ts string) *ReleaseBuilder {
	b.rel.Status.StartTime = ts
	return b
}

func (b *ReleaseBuilder) CompletedAt(ts string) *ReleaseBuilder {
	b.rel.Status.CompletionTime = ts
	return b
}

func (b *ReleaseBuilder) Times(created, started, completed string) *ReleaseBuilder {
	b.rel.Metadata.CreationTimestamp = created
	b.rel.Status.StartTime = started
	b.rel.Status.CompletionTime = completed
	return b
}

func (b *ReleaseBuilder) Label(key, value string) *ReleaseBuilder {
	if b.rel.Metadata.Labels == nil {
		b.rel.Metadata.Labels = make(map[string]string)
	}
	b.rel.Metadata.Labels[key] = value
	return b
}

func (b *ReleaseBuilder) Labels(labels map[string]string) *ReleaseBuilder {
	b.rel.Metadata.Labels = labels
	return b
}

func (b *ReleaseBuilder) App(app string) *ReleaseBuilder {
	return b.Label(labelAppStudioApp, app)
}

func (b *ReleaseBuilder) Component(comp string) *ReleaseBuilder {
	return b.Label(labelAppStudioComp, comp)
}

func (b *ReleaseBuilder) PACEventType(eventType string) *ReleaseBuilder {
	return b.Label(labelPACEventType, eventType)
}

func (b *ReleaseBuilder) Automated(automated bool) *ReleaseBuilder {
	if automated {
		return b.Label(labelReleaseAutomated, "true")
	}
	return b.Label(labelReleaseAutomated, "false")
}

func (b *ReleaseBuilder) Snapshot(snapshot string) *ReleaseBuilder {
	b.rel.Spec.Snapshot = snapshot
	return b.Label(labelReleaseSnapshot, snapshot)
}

func (b *ReleaseBuilder) ReleasePlan(plan string) *ReleaseBuilder {
	b.rel.Spec.ReleasePlan = plan
	return b
}

func (b *ReleaseBuilder) Condition(condType, status, reason string) *ReleaseBuilder {
	b.rel.Status.Conditions = append(b.rel.Status.Conditions, Condition{
		Type:   condType,
		Status: status,
		Reason: reason,
	})
	return b
}

func (b *ReleaseBuilder) Succeeded() *ReleaseBuilder {
	b.rel.Status.Conditions = []Condition{{Type: "Released", Status: "True", Reason: "Succeeded"}}
	return b
}

func (b *ReleaseBuilder) Failed(reason string) *ReleaseBuilder {
	if reason == "" {
		reason = "Failed"
	}
	b.rel.Status.Conditions = []Condition{{Type: "Released", Status: "False", Reason: reason}}
	return b
}

func (b *ReleaseBuilder) Progressing() *ReleaseBuilder {
	b.rel.Status.Conditions = []Condition{{Type: "Released", Status: "False", Reason: "Progressing"}}
	return b
}

func (b *ReleaseBuilder) Build() Release {
	return b.rel
}

// ── Test Time Helpers ─────────────────────────────────────────────────────────

const testBaseTime = "2026-06-01T10:00:00Z"

func testTime(offsetMinutes int) string {
	base, _ := time.Parse(time.RFC3339, testBaseTime)
	return base.Add(time.Duration(offsetMinutes) * time.Minute).Format(time.RFC3339)
}

func daysAgo(days int) string {
	return time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
}

func hoursAgo(hours int) string {
	return time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
}

func secondsAgo(s int) string {
	return time.Now().UTC().Add(-time.Duration(s) * time.Second).Format(time.RFC3339)
}

// ── Mock HTTP Helpers ─────────────────────────────────────────────────────────

type mockRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.fn(req)
}

type mockReadCloser struct {
	content []byte
	offset  int
}

func newMockBody(v interface{}) *mockReadCloser {
	data, _ := json.Marshal(v)
	return &mockReadCloser{content: data}
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.offset >= len(m.content) {
		return 0, io.EOF
	}
	n = copy(p, m.content[m.offset:])
	m.offset += n
	return n, nil
}

func (m *mockReadCloser) Close() error { return nil }

// ── PLR/Release Factory for Pagination Tests ──────────────────────────────────

func makePLRBatch(count, startIdx int) []PipelineRun {
	items := make([]PipelineRun, count)
	for i := 0; i < count; i++ {
		idx := startIdx + i
		items[i] = NewPLR().
			UID(fmt.Sprintf("build-%d", idx)).
			Name(fmt.Sprintf("build-%d", idx)).
			CreatedAt(testBaseTime).
			Build()
	}
	return items
}
