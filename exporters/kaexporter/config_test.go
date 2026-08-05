package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantExact   []string
		wantPattern  []string
		wantErr     bool
	}{
		{
			name: "exact and prefix patterns",
			content: `excludeNamespaces:
  - rhtap-releng-tenant
  - "managed-*"
`,
			wantExact:  []string{"rhtap-releng-tenant"},
			wantPattern: []string{"managed-*"},
		},
		{
			name: "only exact matches",
			content: `excludeNamespaces:
  - ns-a
  - ns-b
`,
			wantExact:  []string{"ns-a", "ns-b"},
			wantPattern: nil,
		},
		{
			name: "only prefix matches",
			content: `excludeNamespaces:
  - "test-*"
  - "staging-*"
`,
			wantExact:  nil,
			wantPattern: []string{"test-*", "staging-*"},
		},
		{
			name: "mid-string wildcard pattern",
			content: `excludeNamespaces:
  - "konflux-perfscale-*-tenant"
`,
			wantExact:  nil,
			wantPattern: []string{"konflux-perfscale-*-tenant"},
		},
		{
			name:       "empty exclusion list",
			content:    "excludeNamespaces: []\n",
			wantExact:  nil,
			wantPattern: nil,
		},
		{
			name:       "no excludeNamespaces key",
			content:    "someOtherKey: true\n",
			wantExact:  nil,
			wantPattern: nil,
		},
		{
			name:    "malformed YAML",
			content: "excludeNamespaces:\n  - [invalid",
			wantErr: true,
		},
	}

	invalidGlobTests := []struct {
		name    string
		content string
	}{
		{
			name: "invalid glob pattern",
			content: `excludeNamespaces:
  - "bad[*"
`,
		},
		{
			name: "invalid glob among valid entries",
			content: `excludeNamespaces:
  - exact-ns
  - "valid-*"
  - "[-*"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("write temp file: %v", err)
			}

			cfg, err := loadConfig(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			f, filterErr := newNamespaceFilter(cfg)
			if filterErr != nil {
				t.Fatalf("unexpected newNamespaceFilter error: %v", filterErr)
			}

			if len(tt.wantExact) == 0 && len(f.exactMatches) != 0 {
				t.Errorf("exactMatches: want empty, got %v", f.exactMatches)
			}
			for _, e := range tt.wantExact {
				if !f.exactMatches[e] {
					t.Errorf("exactMatches missing %q", e)
				}
			}

			if len(tt.wantPattern) == 0 && len(f.patterns) != 0 {
				t.Errorf("prefixes: want empty, got %v", f.patterns)
			}
			for i, p := range tt.wantPattern {
				if i >= len(f.patterns) {
					t.Errorf("prefixes[%d]: want %q, got nothing", i, p)
				} else if f.patterns[i] != p {
					t.Errorf("prefixes[%d]: want %q, got %q", i, p, f.patterns[i])
				}
			}
		})
	}

	for _, tt := range invalidGlobTests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.content), 0644); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			cfg, err := loadConfig(path)
			if err != nil {
				t.Fatalf("unexpected loadConfig error: %v", err)
			}
			_, err = newNamespaceFilter(cfg)
			if err == nil {
				t.Fatal("expected error for invalid glob pattern, got nil")
			}
		})
	}
}

func TestLoadConfigWithCustomSLO(t *testing.T) {
	t.Run("full customSLO hierarchy parses correctly", func(t *testing.T) {
		content := `excludeNamespaces:
  - rhtap-releng-tenant
customSLO:
  build_duration_breach_percentage: 0.05
  tenants:
    a-team-tenant:
      integration_duration_threshold_seconds: 3600
      applications:
        heavy-app:
          integration_duration_breach_percentage: 0.10
          components:
            slow-builder:
              build_duration_threshold_seconds: 7200
`
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CustomSLO == nil {
			t.Fatal("CustomSLO is nil")
		}
		if cfg.CustomSLO.BuildDurationBreachPercentage == nil || cfg.CustomSLO.BuildDurationBreachPercentage.Default == nil || *cfg.CustomSLO.BuildDurationBreachPercentage.Default != 0.05 {
			t.Error("global build_duration_breach_percentage not parsed")
		}
		tenant := cfg.CustomSLO.Tenants["a-team-tenant"]
		if tenant == nil {
			t.Fatal("tenant a-team-tenant not parsed")
		}
		if tenant.IntegrationDurationThresholdSeconds == nil || tenant.IntegrationDurationThresholdSeconds.Default == nil || *tenant.IntegrationDurationThresholdSeconds.Default != 3600 {
			t.Error("tenant integration_duration_threshold_seconds not parsed")
		}
		app := tenant.Applications["heavy-app"]
		if app == nil {
			t.Fatal("application heavy-app not parsed")
		}
		if app.IntegrationDurationBreachPercentage == nil || app.IntegrationDurationBreachPercentage.Default == nil || *app.IntegrationDurationBreachPercentage.Default != 0.10 {
			t.Error("application integration_duration_breach_percentage not parsed")
		}
		comp := app.Components["slow-builder"]
		if comp == nil {
			t.Fatal("component slow-builder not parsed")
		}
		if comp.BuildDurationThresholdSeconds == nil || comp.BuildDurationThresholdSeconds.Default == nil || *comp.BuildDurationThresholdSeconds.Default != 7200 {
			t.Error("component build_duration_threshold_seconds not parsed")
		}
	})

	t.Run("customSLO only without excludeNamespaces", func(t *testing.T) {
		content := `customSLO:
  build_duration_breach_percentage: 0.10
  tenants:
    my-tenant:
      build_duration_threshold_seconds: 3600
`
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.ExcludeNamespaces) != 0 {
			t.Errorf("ExcludeNamespaces should be empty, got %v", cfg.ExcludeNamespaces)
		}
		if cfg.CustomSLO == nil {
			t.Fatal("CustomSLO should not be nil")
		}
		if cfg.CustomSLO.BuildDurationBreachPercentage == nil || cfg.CustomSLO.BuildDurationBreachPercentage.Default == nil || *cfg.CustomSLO.BuildDurationBreachPercentage.Default != 0.10 {
			t.Error("global build_duration_breach_percentage not parsed")
		}
		tenant := cfg.CustomSLO.Tenants["my-tenant"]
		if tenant == nil || tenant.BuildDurationThresholdSeconds == nil || tenant.BuildDurationThresholdSeconds.Default == nil || *tenant.BuildDurationThresholdSeconds.Default != 3600 {
			t.Error("tenant threshold not parsed")
		}
		f, err := newNamespaceFilter(cfg)
		if err != nil {
			t.Fatalf("newNamespaceFilter error: %v", err)
		}
		got := f.apply([]string{"ns-a", "ns-b"})
		if len(got) != 2 {
			t.Errorf("filter should exclude nothing, got %v", got)
		}
	})

	t.Run("object-form ThresholdValue parses and resolves through loadConfig", func(t *testing.T) {
		content := `customSLO:
  tenants:
    my-tenant:
      integration_duration_threshold_seconds: 2000
      applications:
        my-app:
          components:
            my-comp:
              integration_duration_threshold_seconds:
                default: 1800
                matches:
                  - scenario: ec-scan
                    value: 300
                  - scenario: long-test
                    event_type: push
                    value: 5400
              build_duration_breach_percentage:
                matches:
                  - event_type: push
                    value: 0.10
`
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CustomSLO == nil {
			t.Fatal("CustomSLO is nil")
		}

		// Verify struct was parsed correctly
		comp := cfg.CustomSLO.Tenants["my-tenant"].Applications["my-app"].Components["my-comp"]
		if comp == nil {
			t.Fatal("component not parsed")
		}
		tv := comp.IntegrationDurationThresholdSeconds
		if tv == nil {
			t.Fatal("integration threshold not parsed")
		}
		if tv.Default == nil || *tv.Default != 1800 {
			t.Errorf("expected Default=1800, got %v", tv.Default)
		}
		if len(tv.Matches) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(tv.Matches))
		}
		bpct := comp.BuildDurationBreachPercentage
		if bpct == nil {
			t.Fatal("breach percentage not parsed")
		}
		if bpct.Default != nil {
			t.Errorf("expected nil Default for matches-only, got %v", *bpct.Default)
		}
		if len(bpct.Matches) != 1 || bpct.Matches[0].EventType != "push" || bpct.Matches[0].Value != 0.10 {
			t.Errorf("breach percentage match: got %+v", bpct.Matches)
		}

		// Verify resolution through the full YAML-parsed config
		cfg.CustomSLO.Sanitize()

		// ec-scan scenario -> match fires, threshold=300
		r := cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", Scenario: "ec-scan"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 300 {
			t.Errorf("ec-scan: expected threshold=300, got %v", r.DurationThreshold)
		}

		// long-test + push -> match fires, threshold=5400
		r = cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", Scenario: "long-test", EventType: "push"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 5400 {
			t.Errorf("long-test+push: expected threshold=5400, got %v", r.DurationThreshold)
		}

		// long-test + pull_request -> no match, falls to component default=1800
		r = cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", Scenario: "long-test", EventType: "pull_request"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 1800 {
			t.Errorf("long-test+pull_request: expected default threshold=1800, got %v", r.DurationThreshold)
		}

		// unknown-scenario -> no match, falls to component default=1800 (not tenant 2000)
		r = cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", Scenario: "other"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 1800 {
			t.Errorf("other scenario: expected component default=1800, got %v", r.DurationThreshold)
		}

		// unknown component -> falls to tenant=2000 (component matches don't apply)
		r = cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "other-comp", Scenario: "ec-scan"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 2000 {
			t.Errorf("other component: expected tenant threshold=2000, got %v", r.DurationThreshold)
		}

		// breach percentage: push event -> match fires, 0.10
		r = cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", EventType: "push"}, metricBuildDuration)
		if r.BreachPercentage == nil || *r.BreachPercentage != 0.10 {
			t.Errorf("push breach pct: expected 0.10, got %v", r.BreachPercentage)
		}

		// breach percentage: pull_request -> no match, no default -> nil (built-in default used)
		r = cfg.CustomSLO.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", EventType: "pull_request"}, metricBuildDuration)
		if r.BreachPercentage != nil {
			t.Errorf("pull_request breach pct: expected nil (no match, no default), got %v", *r.BreachPercentage)
		}
	})

	t.Run("no customSLO section is backward compatible", func(t *testing.T) {
		content := `excludeNamespaces:
  - rhtap-releng-tenant
`
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.CustomSLO != nil {
			t.Errorf("CustomSLO should be nil when not in YAML, got %+v", cfg.CustomSLO)
		}
	})
}

// ── ThresholdValue Tests ─────────────────────────────────────────────────────

func thresholdValueSimple(v float64) *ThresholdValue {
	return &ThresholdValue{Default: float64Ptr(v)}
}

func TestThresholdValueUnmarshalYAML(t *testing.T) {
	t.Run("simple scalar", func(t *testing.T) {
		var tv ThresholdValue
		content := []byte("7200")
		if err := yaml.Unmarshal(content, &tv); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tv.Default == nil || *tv.Default != 7200 {
			t.Errorf("expected Default=7200, got %v", tv.Default)
		}
		if len(tv.Matches) != 0 {
			t.Errorf("expected no Matches, got %v", tv.Matches)
		}
	})

	t.Run("object with default and matches", func(t *testing.T) {
		content := []byte(`
default: 7200
matches:
  - event_type: push
    value: 5400
  - scenario: ec-scan
    event_type: pull_request
    value: 300
`)
		var tv ThresholdValue
		if err := yaml.Unmarshal(content, &tv); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tv.Default == nil || *tv.Default != 7200 {
			t.Errorf("expected Default=7200, got %v", tv.Default)
		}
		if len(tv.Matches) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(tv.Matches))
		}
		if tv.Matches[0].EventType != "push" || tv.Matches[0].Value != 5400 {
			t.Errorf("match[0]: got %+v", tv.Matches[0])
		}
		if tv.Matches[1].Scenario != "ec-scan" || tv.Matches[1].EventType != "pull_request" || tv.Matches[1].Value != 300 {
			t.Errorf("match[1]: got %+v", tv.Matches[1])
		}
	})

	t.Run("object with only matches, no default", func(t *testing.T) {
		content := []byte(`
matches:
  - scenario: aap-conforma
    value: 0.10
`)
		var tv ThresholdValue
		if err := yaml.Unmarshal(content, &tv); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tv.Default != nil {
			t.Errorf("expected nil Default, got %v", *tv.Default)
		}
		if len(tv.Matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(tv.Matches))
		}
	})

	t.Run("invalid scalar is rejected", func(t *testing.T) {
		var tv ThresholdValue
		content := []byte(`"not-a-number"`)
		if err := yaml.Unmarshal(content, &tv); err == nil {
			t.Fatal("expected error for non-numeric scalar")
		}
	})

	t.Run("backward compat: simple value in SLOThresholds", func(t *testing.T) {
		content := []byte(`build_duration_threshold_seconds: 3600`)
		var th SLOThresholds
		if err := yaml.Unmarshal(content, &th); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if th.BuildDurationThresholdSeconds == nil {
			t.Fatal("expected non-nil BuildDurationThresholdSeconds")
		}
		if th.BuildDurationThresholdSeconds.Default == nil || *th.BuildDurationThresholdSeconds.Default != 3600 {
			t.Errorf("expected Default=3600, got %v", th.BuildDurationThresholdSeconds)
		}
	})

	t.Run("object form in SLOThresholds", func(t *testing.T) {
		content := []byte(`
integration_duration_threshold_seconds:
  default: 1800
  matches:
    - scenario: ec-scan
      value: 300
`)
		var th SLOThresholds
		if err := yaml.Unmarshal(content, &th); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		tv := th.IntegrationDurationThresholdSeconds
		if tv == nil {
			t.Fatal("expected non-nil IntegrationDurationThresholdSeconds")
		}
		if tv.Default == nil || *tv.Default != 1800 {
			t.Errorf("expected Default=1800, got %v", tv.Default)
		}
		if len(tv.Matches) != 1 || tv.Matches[0].Scenario != "ec-scan" {
			t.Errorf("expected 1 match for ec-scan, got %v", tv.Matches)
		}
	})
}

func TestThresholdValueResolveValue(t *testing.T) {
	tests := []struct {
		name      string
		tv        *ThresholdValue
		ls        LabelSet
		wantValue float64
		wantOK    bool
	}{
		{"nil returns false", nil, LabelSet{}, 0, false},
		{"default only", thresholdValueSimple(7200), LabelSet{}, 7200, true},
		{
			"match hits before default",
			&ThresholdValue{Default: float64Ptr(7200), Matches: []ThresholdMatch{{EventType: "push", Value: 5400}}},
			LabelSet{EventType: "push"}, 5400, true,
		},
		{
			"no match falls to default",
			&ThresholdValue{Default: float64Ptr(7200), Matches: []ThresholdMatch{{EventType: "push", Value: 5400}}},
			LabelSet{EventType: "pull_request"}, 7200, true,
		},
		{
			"no match and no default returns false",
			&ThresholdValue{Matches: []ThresholdMatch{{EventType: "push", Value: 5400}}},
			LabelSet{EventType: "pull_request"}, 0, false,
		},
		{
			"partial label match: only scenario set",
			&ThresholdValue{Matches: []ThresholdMatch{{Scenario: "ec-scan", Value: 300}}},
			LabelSet{Scenario: "ec-scan", EventType: "push"}, 300, true,
		},
		{
			"first match wins",
			&ThresholdValue{Matches: []ThresholdMatch{
				{EventType: "push", Value: 1000},
				{EventType: "push", Scenario: "integration", Value: 2000},
			}},
			LabelSet{EventType: "push", Scenario: "integration"}, 1000, true,
		},
		{
			"multi-field match requires all fields",
			&ThresholdValue{Matches: []ThresholdMatch{{Scenario: "ec-scan", EventType: "push", Value: 300}}},
			LabelSet{Scenario: "ec-scan", EventType: "pull_request"}, 0, false,
		},
		{
			"build_type match",
			&ThresholdValue{Matches: []ThresholdMatch{{BuildType: "docker-builds", Value: 9000}}},
			LabelSet{BuildType: "docker-builds"}, 9000, true,
		},
		{
			"automated match",
			&ThresholdValue{Matches: []ThresholdMatch{{Automated: "true", Value: 600}}},
			LabelSet{Automated: "true"}, 600, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := tt.tv.ResolveValue(tt.ls)
			if ok != tt.wantOK || v != tt.wantValue {
				t.Errorf("expected (%g, %v), got (%g, %v)", tt.wantValue, tt.wantOK, v, ok)
			}
		})
	}
}

// ── SLO Config Resolution Tests ──────────────────────────────────────────────

func float64Ptr(v float64) *float64 { return &v }

func TestSLOConfigResolve(t *testing.T) {
	ls := func(ns, app, comp string) LabelSet {
		return LabelSet{Namespace: ns, Application: app, Component: comp}
	}

	resolveTests := []struct {
		name           string
		cfg            *SLOConfig
		ls             LabelSet
		domain         string
		wantThreshold  *float64
		wantBreachPct  *float64
	}{
		{
			name:   "nil SLOConfig returns zero ResolvedSLO",
			cfg:    nil,
			ls:     ls("ns", "app", "comp"),
			domain: metricBuildDuration,
		},
		{
			name:   "empty SLOConfig returns zero ResolvedSLO",
			cfg:    &SLOConfig{},
			ls:     ls("ns", "app", "comp"),
			domain: metricBuildDuration,
		},
		{
			name: "global defaults apply when no tenant match",
			cfg: &SLOConfig{SLOThresholds: SLOThresholds{
				BuildDurationBreachPercentage: thresholdValueSimple(0.10),
			}},
			ls:            ls("unknown-tenant", "app", "comp"),
			domain:        metricBuildDuration,
			wantBreachPct: float64Ptr(0.10),
		},
		{
			name: "tenant override cascades",
			cfg: &SLOConfig{
				SLOThresholds: SLOThresholds{BuildDurationBreachPercentage: thresholdValueSimple(0.05)},
				Tenants: map[string]*TenantSLOConfig{
					"my-tenant": {SLOThresholds: SLOThresholds{BuildDurationThresholdSeconds: thresholdValueSimple(3600)}},
				},
			},
			ls:            ls("my-tenant", "app", "comp"),
			domain:        metricBuildDuration,
			wantThreshold: float64Ptr(3600),
			wantBreachPct: float64Ptr(0.05),
		},
		{
			name: "application override cascades over tenant",
			cfg: newSLOCfg().
				tenant("my-tenant").buildThreshold(3600).
				app("my-app").buildThreshold(1800).
				build(),
			ls:            ls("my-tenant", "my-app", "comp"),
			domain:        metricBuildDuration,
			wantThreshold: float64Ptr(1800),
		},
		{
			name: "component override cascades over application",
			cfg: newSLOCfg().
				tenant("my-tenant").buildThreshold(3600).
				app("my-app").buildThreshold(1800).
				comp("my-comp", SLOThresholds{BuildDurationThresholdSeconds: thresholdValueSimple(7200)}).
				build(),
			ls:            ls("my-tenant", "my-app", "my-comp"),
			domain:        metricBuildDuration,
			wantThreshold: float64Ptr(7200),
		},
		{
			name: "partial override: percentage at component, threshold from tenant",
			cfg: newSLOCfg().
				tenant("my-tenant").buildThreshold(3600).
				app("my-app").
				comp("my-comp", SLOThresholds{BuildDurationBreachPercentage: thresholdValueSimple(0.15)}).
				build(),
			ls:            ls("my-tenant", "my-app", "my-comp"),
			domain:        metricBuildDuration,
			wantThreshold: float64Ptr(3600),
			wantBreachPct: float64Ptr(0.15),
		},
		{
			name: "domain isolation: build threshold does not affect integration",
			cfg: &SLOConfig{SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: thresholdValueSimple(3600),
			}},
			ls:     ls("ns", "app", "comp"),
			domain: metricIntegrationDuration,
		},
		{
			name: "unknown tenant falls back to global",
			cfg: &SLOConfig{
				SLOThresholds: SLOThresholds{BuildDurationBreachPercentage: thresholdValueSimple(0.08)},
				Tenants: map[string]*TenantSLOConfig{
					"known-tenant": {SLOThresholds: SLOThresholds{BuildDurationBreachPercentage: thresholdValueSimple(0.20)}},
				},
			},
			ls:            ls("unknown-tenant", "app", "comp"),
			domain:        metricBuildDuration,
			wantBreachPct: float64Ptr(0.08),
		},
		{
			name: "integration domain cascades correctly",
			cfg: newSLOCfg().
				tenant("my-tenant").integrationThreshold(1800).
				app("my-app").
				comp("my-comp", SLOThresholds{IntegrationDurationBreachPercentage: thresholdValueSimple(0.15)}).
				build(),
			ls:            ls("my-tenant", "my-app", "my-comp"),
			domain:        metricIntegrationDuration,
			wantThreshold: float64Ptr(1800),
			wantBreachPct: float64Ptr(0.15),
		},
		{
			name: "release domain cascades correctly",
			cfg: newSLOCfg().releaseBreachPct(0.08).
				tenant("my-tenant").
				app("my-app").releaseThreshold(7200).
				build(),
			ls:            ls("my-tenant", "my-app", "my-comp"),
			domain:        metricReleaseDuration,
			wantThreshold: float64Ptr(7200),
			wantBreachPct: float64Ptr(0.08),
		},
		{
			name: "unknown application falls back to tenant",
			cfg: &SLOConfig{
				Tenants: map[string]*TenantSLOConfig{
					"my-tenant": {
						SLOThresholds: SLOThresholds{BuildDurationThresholdSeconds: thresholdValueSimple(3600)},
						Applications: map[string]*ApplicationSLOConfig{
							"known-app": {SLOThresholds: SLOThresholds{BuildDurationThresholdSeconds: thresholdValueSimple(1800)}},
						},
					},
				},
			},
			ls:            ls("my-tenant", "unknown-app", "comp"),
			domain:        metricBuildDuration,
			wantThreshold: float64Ptr(3600),
		},
	}
	for _, tt := range resolveTests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.cfg.Resolve(tt.ls, tt.domain)
			if tt.wantThreshold == nil {
				if r.DurationThreshold != nil {
					t.Errorf("expected nil threshold, got %v", *r.DurationThreshold)
				}
			} else {
				if r.DurationThreshold == nil || *r.DurationThreshold != *tt.wantThreshold {
					t.Errorf("expected threshold=%v, got %v", *tt.wantThreshold, r.DurationThreshold)
				}
			}
			if tt.wantBreachPct == nil {
				if r.BreachPercentage != nil {
					t.Errorf("expected nil breach_percentage, got %v", *r.BreachPercentage)
				}
			} else {
				if r.BreachPercentage == nil || *r.BreachPercentage != *tt.wantBreachPct {
					t.Errorf("expected breach_percentage=%v, got %v", *tt.wantBreachPct, r.BreachPercentage)
				}
			}
		})
	}

	t.Run("match-based threshold at component level", func(t *testing.T) {
		c := newSLOCfg().
			tenant("my-tenant").
			app("my-app").
			comp("my-comp", SLOThresholds{
				IntegrationDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(1800),
					Matches: []ThresholdMatch{
						{Scenario: "ec-scan", Value: 300},
						{EventType: "pull_request", Value: 900},
					},
				},
			}).
			build()
		r := c.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", Scenario: "ec-scan"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 300 {
			t.Errorf("expected match threshold=300, got %v", r.DurationThreshold)
		}
		r = c.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", EventType: "pull_request"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 900 {
			t.Errorf("expected match threshold=900, got %v", r.DurationThreshold)
		}
		r = c.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", EventType: "push"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 1800 {
			t.Errorf("expected default threshold=1800, got %v", r.DurationThreshold)
		}
	})

	t.Run("no-match at lower level preserves parent value", func(t *testing.T) {
		c := newSLOCfg().
			tenant("my-tenant").integrationThreshold(5000).
			app("my-app").
			comp("my-comp", SLOThresholds{
				IntegrationDurationThresholdSeconds: &ThresholdValue{
					Matches: []ThresholdMatch{
						{Scenario: "ec-scan", Value: 300},
					},
				},
			}).
			build()
		r := c.Resolve(LabelSet{Namespace: "my-tenant", Application: "my-app", Component: "my-comp", Scenario: "full-integration"}, metricIntegrationDuration)
		if r.DurationThreshold == nil || *r.DurationThreshold != 5000 {
			t.Errorf("expected parent threshold=5000 preserved when no match at component, got %v", r.DurationThreshold)
		}
	})
}

func TestSLOConfigSanitize(t *testing.T) {
	t.Run("nil config does not panic", func(t *testing.T) {
		var c *SLOConfig
		c.Sanitize()
	})

	t.Run("valid config is unchanged", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: thresholdValueSimple(3600),
				BuildDurationBreachPercentage: thresholdValueSimple(0.05),
			},
			Tenants: map[string]*TenantSLOConfig{
				"my-tenant": {
					SLOThresholds: SLOThresholds{
						IntegrationDurationBreachPercentage: thresholdValueSimple(0.10),
					},
				},
			},
		}
		c.Sanitize()
		if c.BuildDurationThresholdSeconds == nil || c.BuildDurationThresholdSeconds.Default == nil || *c.BuildDurationThresholdSeconds.Default != 3600 {
			t.Error("valid threshold should be unchanged")
		}
		if c.BuildDurationBreachPercentage == nil || c.BuildDurationBreachPercentage.Default == nil || *c.BuildDurationBreachPercentage.Default != 0.05 {
			t.Error("valid percentage should be unchanged")
		}
		tenantPct := c.Tenants["my-tenant"].IntegrationDurationBreachPercentage
		if tenantPct == nil || tenantPct.Default == nil || *tenantPct.Default != 0.10 {
			t.Error("valid tenant percentage should be unchanged")
		}
	})

	t.Run("negative threshold is nilled out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: thresholdValueSimple(-100),
			},
		}
		c.Sanitize()
		if c.BuildDurationThresholdSeconds != nil {
			t.Error("negative threshold should be nilled out")
		}
	})

	t.Run("zero threshold is nilled out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				IntegrationDurationThresholdSeconds: thresholdValueSimple(0),
			},
		}
		c.Sanitize()
		if c.IntegrationDurationThresholdSeconds != nil {
			t.Error("zero threshold should be nilled out")
		}
	})

	t.Run("breach percentage > 1 is nilled out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationBreachPercentage: thresholdValueSimple(20),
			},
		}
		c.Sanitize()
		if c.BuildDurationBreachPercentage != nil {
			t.Error("percentage > 1 should be nilled out")
		}
	})

	t.Run("breach percentage = 0 is nilled out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				ReleaseDurationBreachPercentage: thresholdValueSimple(0),
			},
		}
		c.Sanitize()
		if c.ReleaseDurationBreachPercentage != nil {
			t.Error("percentage = 0 should be nilled out")
		}
	})

	t.Run("invalid value at component level is nilled out", func(t *testing.T) {
		c := newSLOCfg().
			tenant("my-tenant").
			app("my-app").
			comp("my-comp", SLOThresholds{BuildDurationThresholdSeconds: thresholdValueSimple(-50)}).
			build()
		c.Sanitize()
		if c.Tenants["my-tenant"].Applications["my-app"].Components["my-comp"].BuildDurationThresholdSeconds != nil {
			t.Error("invalid component threshold should be nilled out")
		}
	})

	t.Run("breach percentage = 1 is valid and unchanged", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationBreachPercentage: thresholdValueSimple(1.0),
			},
		}
		c.Sanitize()
		if c.BuildDurationBreachPercentage == nil || c.BuildDurationBreachPercentage.Default == nil || *c.BuildDurationBreachPercentage.Default != 1.0 {
			t.Error("percentage = 1.0 should be valid and unchanged")
		}
	})

	t.Run("valid values survive alongside invalid ones", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: thresholdValueSimple(3600),
				BuildDurationBreachPercentage: thresholdValueSimple(20),
			},
		}
		c.Sanitize()
		if c.BuildDurationThresholdSeconds == nil || c.BuildDurationThresholdSeconds.Default == nil || *c.BuildDurationThresholdSeconds.Default != 3600 {
			t.Error("valid threshold should survive")
		}
		if c.BuildDurationBreachPercentage != nil {
			t.Error("invalid percentage should be nilled out")
		}
	})

	t.Run("invalid match entries are filtered out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(3600),
					Matches: []ThresholdMatch{
						{EventType: "push", Value: 5400},
						{EventType: "pull_request", Value: -100},
						{Scenario: "ec-scan", Value: 300},
					},
				},
			},
		}
		c.Sanitize()
		tv := c.BuildDurationThresholdSeconds
		if tv == nil {
			t.Fatal("ThresholdValue should not be nil (has valid default and matches)")
		}
		if len(tv.Matches) != 2 {
			t.Fatalf("expected 2 valid matches, got %d", len(tv.Matches))
		}
		if tv.Matches[0].Value != 5400 || tv.Matches[1].Value != 300 {
			t.Errorf("wrong surviving matches: %+v", tv.Matches)
		}
	})

	t.Run("invalid default nilled, valid matches survive", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(-1),
					Matches: []ThresholdMatch{
						{EventType: "push", Value: 5400},
					},
				},
			},
		}
		c.Sanitize()
		tv := c.BuildDurationThresholdSeconds
		if tv == nil {
			t.Fatal("ThresholdValue should survive (has valid match)")
		}
		if tv.Default != nil {
			t.Error("invalid default should be nilled")
		}
		if len(tv.Matches) != 1 {
			t.Errorf("valid match should survive, got %d matches", len(tv.Matches))
		}
	})

	t.Run("all invalid: entire ThresholdValue nilled out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(-1),
					Matches: []ThresholdMatch{
						{EventType: "push", Value: -100},
					},
				},
			},
		}
		c.Sanitize()
		if c.BuildDurationThresholdSeconds != nil {
			t.Error("entirely invalid ThresholdValue should be nilled out")
		}
	})

	t.Run("filter-less match entry is rejected", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: &ThresholdValue{
					Default: float64Ptr(3600),
					Matches: []ThresholdMatch{
						{Value: 5400},
						{EventType: "push", Value: 1800},
					},
				},
			},
		}
		c.Sanitize()
		tv := c.BuildDurationThresholdSeconds
		if tv == nil {
			t.Fatal("ThresholdValue should not be nil (has valid default and filtered match)")
		}
		if len(tv.Matches) != 1 {
			t.Fatalf("expected 1 surviving match (filter-less dropped), got %d", len(tv.Matches))
		}
		if tv.Matches[0].EventType != "push" || tv.Matches[0].Value != 1800 {
			t.Errorf("wrong surviving match: %+v", tv.Matches[0])
		}
	})

	t.Run("filter-less match as only match with no default nils ThresholdValue", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationThresholdSeconds: &ThresholdValue{
					Matches: []ThresholdMatch{
						{Value: 5400},
					},
				},
			},
		}
		c.Sanitize()
		if c.BuildDurationThresholdSeconds != nil {
			t.Error("ThresholdValue with only a filter-less match and no default should be nilled out")
		}
	})

	t.Run("invalid breach percentage matches are filtered out", func(t *testing.T) {
		c := &SLOConfig{
			SLOThresholds: SLOThresholds{
				BuildDurationBreachPercentage: &ThresholdValue{
					Default: float64Ptr(0.05),
					Matches: []ThresholdMatch{
						{EventType: "push", Value: 0.10},
						{EventType: "pull_request", Value: 1.5},
						{Scenario: "ec-scan", Value: 0},
					},
				},
			},
		}
		c.Sanitize()
		tv := c.BuildDurationBreachPercentage
		if tv == nil {
			t.Fatal("ThresholdValue should not be nil (has valid default and match)")
		}
		if len(tv.Matches) != 1 {
			t.Fatalf("expected 1 valid match, got %d", len(tv.Matches))
		}
		if tv.Matches[0].EventType != "push" || tv.Matches[0].Value != 0.10 {
			t.Errorf("wrong surviving match: %+v", tv.Matches[0])
		}
	})

	t.Run("nil map values in hierarchy do not panic", func(t *testing.T) {
		c := &SLOConfig{
			Tenants: map[string]*TenantSLOConfig{
				"nil-tenant": nil,
				"real-tenant": {
					Applications: map[string]*ApplicationSLOConfig{
						"nil-app": nil,
						"real-app": {
							Components: map[string]*ComponentSLOConfig{
								"nil-comp": nil,
							},
						},
					},
				},
			},
		}
		c.Sanitize()
	})
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestNamespaceFilter_Apply(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *KAConfig
		input  []string
		want   []string
	}{
		{
			name:  "nil config excludes nothing",
			cfg:   nil,
			input: []string{"my-tenant", "rhtap-releng-tenant", "managed-foo", "managed-bar", "other-ns"},
			want:  []string{"my-tenant", "rhtap-releng-tenant", "managed-foo", "managed-bar", "other-ns"},
		},
		{
			name: "exact match only",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{"special-ns"},
			},
			input: []string{"special-ns", "special-ns-2", "other"},
			want:  []string{"special-ns-2", "other"},
		},
		{
			name: "prefix match only",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{"test-*"},
			},
			input: []string{"test-foo", "test-bar", "my-test", "production"},
			want:  []string{"my-test", "production"},
		},
		{
			name: "mixed exact and prefix",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{"exact-ns", "prefix-*"},
			},
			input: []string{"exact-ns", "prefix-one", "prefix-two", "keep-me"},
			want:  []string{"keep-me"},
		},
		{
			name: "empty config excludes nothing",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{},
			},
			input: []string{"rhtap-releng-tenant", "managed-foo", "anything"},
			want:  []string{"rhtap-releng-tenant", "managed-foo", "anything"},
		},
		{
			name:  "empty input",
			cfg:   nil,
			input: nil,
			want:  nil,
		},
		{
			name: "leading wildcard",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{"*-managed"},
			},
			input: []string{"foo-managed", "bar-managed", "managed-foo", "other"},
			want:  []string{"managed-foo", "other"},
		},
		{
			name: "mid-string wildcard",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{"konflux-perfscale-*-tenant"},
			},
			input: []string{"konflux-perfscale-large-tenant", "konflux-perfscale-small-tenant", "konflux-perfscale", "other-ns"},
			want:  []string{"konflux-perfscale", "other-ns"},
		},
		{
			name: "all excluded",
			cfg: &KAConfig{
				ExcludeNamespaces: []string{"ns-a", "ns-b"},
			},
			input: []string{"ns-a", "ns-b"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := newNamespaceFilter(tt.cfg)
			if err != nil {
				t.Fatalf("unexpected newNamespaceFilter error: %v", err)
			}
			got := filter.apply(tt.input)

			if len(got) != len(tt.want) {
				t.Fatalf("apply() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("apply()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
