package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

const defaultTierConfigYAML = `build_duration:
  tiers:
    - { name: fast, baseline_max: 300, threshold: 600 }
    - { name: medium, baseline_max: 900, threshold: 1800 }
    - { name: slow, baseline_max: 2100, threshold: 3600 }
    - { name: heavy, baseline_max: null, threshold: 7200 }

integration_duration:
  tiers:
    - { name: fast, baseline_max: 300, threshold: 600 }
    - { name: medium, baseline_max: 1200, threshold: 2700 }
    - { name: slow, baseline_max: 3600, threshold: 7200 }
    - { name: heavy, baseline_max: null, threshold: 10800 }
  overrides:
    - match: { test_type: ec }
      tiers:
        - { name: ec, baseline_max: null, threshold: 600 }

release_duration:
  tiers:
    - { name: fast, baseline_max: 600, threshold: 1200 }
    - { name: medium, baseline_max: 1800, threshold: 3600 }
    - { name: slow, baseline_max: null, threshold: 5400 }
`

const overrideTierConfigYAML = `build_duration:
  tiers:
    - { name: fast, baseline_max: 720, threshold: 1500 }
    - { name: medium, baseline_max: 1200, threshold: 1800 }
    - { name: slow, baseline_max: 3600, threshold: 5400 }
    - { name: heavy, baseline_max: null, threshold: 7200 }

integration_duration:
  tiers:
    - { name: fast, baseline_max: 300, threshold: 600 }
    - { name: medium, baseline_max: 1200, threshold: 2700 }
    - { name: slow, baseline_max: 3600, threshold: 7200 }
    - { name: heavy, baseline_max: null, threshold: 10800 }
  overrides:
    - match: { test_type: ec }
      tiers:
        - { name: ec, baseline_max: null, threshold: 600 }

release_duration:
  tiers:
    - { name: release, baseline_max: null, threshold: 900 }
`

func loadTierConfigYAML(t *testing.T, content string) *TierConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "slo-tiers.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write tier config: %v", err)
	}
	cfg, err := loadTierConfigFile(path)
	if err != nil {
		t.Fatalf("load tier config: %v", err)
	}
	cfg.Sanitize()
	return cfg
}

func TestNewKAExporterTierWiring(t *testing.T) {
	t.Setenv(kaHostEnvVar, "https://kubearchive.example")
	t.Setenv(kaTokenEnvVar, "test-token")
	t.Setenv("KA_TOKEN_FILE", "")
	t.Setenv(namespaceEnvVar, "tenant") // fixed-tenant mode
	t.Setenv(clusterEnvVar, "test-cluster")
	t.Setenv(kaConfigFileEnv, "")

	tests := []struct {
		name      string
		config    string
		wantTiers bool
	}{
		{
			name:      "valid tier config",
			config:    defaultTierConfigYAML,
			wantTiers: true,
		},
		{
			name:      "missing tier config",
			wantTiers: false,
		},
		{
			name: "invalid tier config",
			config: `build_duration:
  tiers:
    - { name: fast, baseline_max: 300, threshold: 600 }
`,
			wantTiers: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tiers.yaml")
			if tt.config != "" {
				if err := os.WriteFile(path, []byte(tt.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			t.Setenv(kaSLOTiersFileEnv, path)

			exporter, err := NewKAExporter()
			if err != nil {
				t.Fatalf("NewKAExporter() error: %v", err)
			}

			if got := exporter.tierConfig != nil; got != tt.wantTiers {
				t.Fatalf("tier config present = %v, want %v", got, tt.wantTiers)
			}
			if tt.wantTiers && len(exporter.tierConfig.ByDomain) != 3 {
				t.Fatalf("got %d tier domains, want 3", len(exporter.tierConfig.ByDomain))
			}

			if exporter.tierPins == nil {
				t.Fatal("exporter tier pin store is nil")
			}
			modules := []*SLOGaugeSet{
				&exporter.buildSLO.SLOGaugeSet,
				&exporter.integrationSLO.SLOGaugeSet,
				&exporter.releaseSLO.SLOGaugeSet,
			}
			for _, module := range modules {
				if module.tierConfig != exporter.tierConfig {
					t.Error("module does not share exporter tier config")
				}
				if module.tierPins != exporter.tierPins {
					t.Error("module does not share exporter tier pin store")
				}
			}
		})
	}
}

func TestLoadAndResolveTierFixtures(t *testing.T) {
	defaultConfig := loadTierConfigYAML(t, defaultTierConfigYAML)
	overrideConfig := loadTierConfigYAML(t, overrideTierConfigYAML)

	tests := []struct {
		name      string
		config    *TierConfig
		domain    string
		labels    LabelSet
		baseline  float64
		wantName  string
		wantLimit float64
	}{
		{"default build fast", defaultConfig, metricBuildDuration, LabelSet{}, 240, "fast", 600},
		{"default build boundary", defaultConfig, metricBuildDuration, LabelSet{}, 300, "fast", 600},
		{"default build medium", defaultConfig, metricBuildDuration, LabelSet{}, 480, "medium", 1800},
		{"default build slow", defaultConfig, metricBuildDuration, LabelSet{}, 1500, "slow", 3600},
		{"default build heavy", defaultConfig, metricBuildDuration, LabelSet{}, 3000, "heavy", 7200},
		{"default integration", defaultConfig, metricIntegrationDuration, LabelSet{TestType: "integration"}, 900, "medium", 2700},
		{"default EC override", defaultConfig, metricIntegrationDuration, LabelSet{TestType: "ec"}, 9000, "ec", 600},
		{"override build fast", overrideConfig, metricBuildDuration, LabelSet{}, 480, "fast", 1500},
		{"override build medium", overrideConfig, metricBuildDuration, LabelSet{}, 900, "medium", 1800},
		{"override release catch-all", overrideConfig, metricReleaseDuration, LabelSet{}, 9000, "release", 900},
		{"override integration EC", overrideConfig, metricIntegrationDuration, LabelSet{TestType: "ec"}, 9000, "ec", 600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier, ok := tt.config.resolveTier(tt.domain, tt.labels, tt.baseline)
			if !ok {
				t.Fatal("expected a tier, got none")
			}
			if tier.Name != tt.wantName || tier.Threshold != tt.wantLimit {
				t.Fatalf("got tier %q threshold %g, want %q threshold %g", tier.Name, tier.Threshold, tt.wantName, tt.wantLimit)
			}
		})
	}

	if _, ok := defaultConfig.resolveTier("unknown", LabelSet{}, 100); ok {
		t.Error("unknown domain unexpectedly resolved")
	}
}

func TestThresholdMatchSupportsTestType(t *testing.T) {
	cfg := &SLOConfig{SLOThresholds: SLOThresholds{
		IntegrationDurationThresholdSeconds: &ThresholdValue{
			Default: float64Ptr(900),
			Matches: []ThresholdMatch{{TestType: "ec", Value: 600}},
		},
	}}
	cfg.Sanitize()

	value := cfg.IntegrationDurationThresholdSeconds
	if value == nil || len(value.Matches) != 1 || value.Matches[0].TestType != "ec" {
		t.Fatalf("test_type-only match was removed during sanitization: %+v", value)
	}

	ec := cfg.Resolve(LabelSet{TestType: "ec"}, metricIntegrationDuration)
	if ec.DurationThreshold == nil || *ec.DurationThreshold != 600 {
		t.Fatalf("EC match got threshold %v; want 600", ec.DurationThreshold)
	}

	nonEC := cfg.Resolve(LabelSet{TestType: "integration"}, metricIntegrationDuration)
	if nonEC.DurationThreshold == nil || *nonEC.DurationThreshold != 900 {
		t.Fatalf("non-EC match got threshold %v; want 900", nonEC.DurationThreshold)
	}
}

func TestTierOverrideFirstMatchWins(t *testing.T) {
	cfg := &TierConfig{ByDomain: map[string]TierSet{
		metricIntegrationDuration: {
			Tiers: []Tier{{Name: "default", Threshold: 1200}},
			Overrides: []TierOverride{
				{Match: map[string]string{"test_type": "ec"}, Tiers: []Tier{{Name: "first", Threshold: 600}}},
				{Match: map[string]string{"test_type": "ec"}, Tiers: []Tier{{Name: "second", Threshold: 900}}},
			},
		},
	}}
	tier, ok := cfg.resolveTier(metricIntegrationDuration, LabelSet{TestType: "ec"}, 100)
	if !ok || tier.Name != "first" || tier.Threshold != 600 {
		t.Fatalf("got tier %+v, present=%v; want first/600", tier, ok)
	}
}

func TestValidateTierList(t *testing.T) {
	max300 := 300.0
	max200 := 200.0
	tests := []struct {
		name  string
		tiers []Tier
	}{
		{
			name:  "non-positive threshold",
			tiers: []Tier{{Name: "all", Threshold: 0}},
		},
		{
			name: "non-increasing baseline",
			tiers: []Tier{
				{Name: "first", BaselineMax: &max300, Threshold: 600},
				{Name: "second", BaselineMax: &max200, Threshold: 900},
				{Name: "all", Threshold: 1200},
			},
		},
		{
			name: "catch-all is not last",
			tiers: []Tier{
				{Name: "all", Threshold: 1200},
				{Name: "bounded", BaselineMax: &max300, Threshold: 600},
			},
		},
		{
			name: "missing catch-all",
			tiers: []Tier{
				{Name: "bounded", BaselineMax: &max300, Threshold: 600},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateTierList(tt.tiers); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestTierConfigSanitize(t *testing.T) {
	t.Run("invalid default removes only its domain", func(t *testing.T) {
		max := 300.0
		cfg := &TierConfig{ByDomain: map[string]TierSet{
			metricBuildDuration: {
				Tiers: []Tier{{Name: "fast", BaselineMax: &max, Threshold: 600}}, // missing catch-all
			},
			metricReleaseDuration: {
				Tiers: []Tier{{Name: "release", BaselineMax: nil, Threshold: 720}},
			},
		}}
		cfg.Sanitize()
		if _, ok := cfg.ByDomain[metricBuildDuration]; ok {
			t.Error("invalid build domain was not removed")
		}
		if _, ok := cfg.ByDomain[metricReleaseDuration]; !ok {
			t.Error("valid release domain was removed")
		}
	})

	t.Run("invalid override is ignored while defaults remain", func(t *testing.T) {
		cfg := &TierConfig{ByDomain: map[string]TierSet{
			metricIntegrationDuration: {
				Tiers: []Tier{{Name: "all", Threshold: 600}},
				Overrides: []TierOverride{{
					Match: map[string]string{"unknown": "value"},
					Tiers: []Tier{{Name: "bad", Threshold: 100}},
				}},
			},
		}}
		cfg.Sanitize()
		set, ok := cfg.ByDomain[metricIntegrationDuration]
		if !ok {
			t.Fatal("valid domain defaults were removed")
		}
		if len(set.Overrides) != 0 {
			t.Fatalf("expected invalid override to be removed, got %d", len(set.Overrides))
		}
	})

	t.Run("impossible domain selectors are ignored", func(t *testing.T) {
		cases := map[string][]string{
			metricBuildDuration:       {"scenario", "automated", "test_type"},
			metricIntegrationDuration: {"build_type", "automated"},
			metricReleaseDuration:     {"scenario", "build_type", "test_type"},
		}
		for domain, keys := range cases {
			for _, key := range keys {
				t.Run(domain+"/"+key, func(t *testing.T) {
					cfg := &TierConfig{ByDomain: map[string]TierSet{
						domain: {
							Tiers: []Tier{{Name: "default", Threshold: 600}},
							Overrides: []TierOverride{{
								Match: map[string]string{key: "value"},
								Tiers: []Tier{{Name: "override", Threshold: 900}},
							}},
						},
					}}
					cfg.Sanitize()
					if got := len(cfg.ByDomain[domain].Overrides); got != 0 {
						t.Fatalf("expected impossible %s selector to be removed, got %d overrides", key, got)
					}
				})
			}
		}
	})

	t.Run("populated domain selectors are retained", func(t *testing.T) {
		cases := map[string][]string{
			metricBuildDuration:       {"build_type", "event_type"},
			metricIntegrationDuration: {"scenario", "event_type", "test_type"},
			metricReleaseDuration:     {"automated", "event_type"},
		}
		for domain, keys := range cases {
			for _, key := range keys {
				t.Run(domain+"/"+key, func(t *testing.T) {
					cfg := &TierConfig{ByDomain: map[string]TierSet{
						domain: {
							Tiers: []Tier{{Name: "default", Threshold: 600}},
							Overrides: []TierOverride{{
								Match: map[string]string{key: "value"},
								Tiers: []Tier{{Name: "override", Threshold: 900}},
							}},
						},
					}}
					cfg.Sanitize()
					if got := len(cfg.ByDomain[domain].Overrides); got != 1 {
						t.Fatalf("expected populated %s selector to remain, got %d overrides", key, got)
					}
				})
			}
		}
	})

	t.Run("malformed and missing files return errors", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "slo-tiers.yaml")
		if err := os.WriteFile(path, []byte("build_duration: ["), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadTierConfigFile(path); err == nil {
			t.Error("expected malformed YAML error")
		}
		if _, err := loadTierConfigFile(filepath.Join(dir, "missing.yaml")); err == nil {
			t.Error("expected missing-file error")
		}
	})
}

func recordSuccessfulBuilds(t *testing.T, store *Store, labels LabelSet, prefix string, duration float64, days, perDay int) {
	t.Helper()
	now := time.Now().UTC()
	for day := 0; day < days; day++ {
		for observation := 0; observation < perDay; observation++ {
			key := fmt.Sprintf("%s-%d-%d", prefix, day, observation)
			store.RecordObservation(metricBuildDuration, key, now.Add(-time.Duration(day*24)*time.Hour), labels, duration, 10, true, "")
		}
	}
}

func TestTierPinLifecycle(t *testing.T) {
	config := loadTierConfigYAML(t, defaultTierConfigYAML)
	labels := LabelSet{
		Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
		BuildType: "docker-builds", EventType: "push",
	}

	t.Run("pins only after both data gates and does not re-tier", func(t *testing.T) {
		store := NewStore()
		pins := newTierPins()
		slo := newBuildSLO30dWithTiers(nil, config, pins)

		recordSuccessfulBuilds(t, store, labels, "initial", 240, 3, 3) // 9 observations
		slo.updateGauges(store, nil)
		window := store.Data[metricBuildDuration][labels]
		if _, ok := pins.get(window); ok {
			t.Fatal("component pinned before reaching the success-count gate")
		}

		recordSuccessfulBuilds(t, store, labels, "gate", 240, 1, 1)
		slo.updateGauges(store, nil)
		pin, ok := pins.get(window)
		if !ok || pin.Name != "fast" || pin.Threshold != 600 {
			t.Fatalf("got pin %+v, present=%v; want fast/600", pin, ok)
		}

		// Raise the rolling mean well beyond the fast tier's 300s baseline cap.
		recordSuccessfulBuilds(t, store, labels, "regression", 1200, 3, 10)
		slo.updateGauges(store, nil)
		pin, _ = pins.get(window)
		if pin.Name != "fast" || pin.Threshold != 600 {
			t.Fatalf("pin changed after regression: %+v", pin)
		}
		metric := &dto.Metric{}
		slo.durationSLOBreach.WithLabelValues("c", "ns", "app", "comp", "docker-builds", "push", "fast").Write(metric) //nolint:errcheck
		if metric.GetGauge().GetValue() != 1 {
			t.Fatalf("expected regression to breach pinned threshold, got %g", metric.GetGauge().GetValue())
		}
	})

	t.Run("a new component pins when it later reaches the gate", func(t *testing.T) {
		store := NewStore()
		pins := newTierPins()
		slo := newBuildSLO30dWithTiers(nil, config, pins)
		newLabels := labels
		newLabels.Component = "new-component"

		recordSuccessfulBuilds(t, store, newLabels, "before", 480, 2, 5)
		slo.updateGauges(store, nil)
		window := store.Data[metricBuildDuration][newLabels]
		if _, ok := pins.get(window); ok {
			t.Fatal("component pinned before reaching the day-count gate")
		}
		recordSuccessfulBuilds(t, store, newLabels, "third-day", 480, 3, 1)
		slo.updateGauges(store, nil)
		pin, ok := pins.get(window)
		if !ok || pin.Name != "medium" || pin.Threshold != 1800 {
			t.Fatalf("got pin %+v, present=%v; want medium/1800", pin, ok)
		}
	})
}

func TestTieredBreachPrecedenceAndFallback(t *testing.T) {
	tierConfig := loadTierConfigYAML(t, defaultTierConfigYAML)
	labels := LabelSet{
		Cluster: "c", Namespace: "ns", Application: "app", Component: "comp",
		BuildType: "docker-builds", EventType: "push",
	}
	assertBuildBreachMetric := func(t *testing.T, slo *BuildSLO30d, tier string, value float64) {
		t.Helper()
		expected := fmt.Sprintf(`# HELP konflux_build_duration_slo_breach 1 if build duration SLO is breached (daily means exceed configured threshold for configured percentage of days), 0 otherwise. The tier label identifies the pinned tier or custom/statistical threshold source. Not emitted when data is insufficient.
# TYPE konflux_build_duration_slo_breach gauge
konflux_build_duration_slo_breach{application="app",build_type="docker-builds",cluster="c",component="comp",event_type="push",namespace="ns",tier="%s"} %g
`, tier, value)
		if err := testutil.CollectAndCompare(
			slo.durationSLOBreach,
			strings.NewReader(expected),
			"konflux_build_duration_slo_breach",
		); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("custom SLO threshold has priority over tiers", func(t *testing.T) {
		store := NewStore()
		recordSuccessfulBuilds(t, store, labels, "custom", 240, 3, 4)
		customConfig := &SLOConfig{SLOThresholds: SLOThresholds{
			BuildDurationThresholdSeconds: thresholdValueSimple(200),
		}}
		pins := newTierPins()
		slo := newBuildSLO30dWithTiers(customConfig, tierConfig, pins)
		slo.updateGauges(store, nil)

		window := store.Data[metricBuildDuration][labels]
		if _, ok := pins.get(window); ok {
			t.Error("tier was pinned even though a custom threshold took priority")
		}
		assertBuildBreachMetric(t, slo, customTierLabel, 1)
	})

	t.Run("missing tier config uses statistical fallback", func(t *testing.T) {
		store := NewStore()
		recordSuccessfulBuilds(t, store, labels, "fallback", 240, 3, 4)
		slo := newBuildSLO30dWithTiers(nil, nil, newTierPins())
		slo.updateGauges(store, nil)

		assertBuildBreachMetric(t, slo, statisticalTierLabel, 0)
	})

	t.Run("custom breach percentage applies to a tier threshold", func(t *testing.T) {
		store := NewStore()
		now := time.Now().UTC()
		for day := 0; day < 19; day++ {
			store.RecordObservation(metricBuildDuration, fmt.Sprintf("normal-%d", day), now.Add(-time.Duration(day*24)*time.Hour), labels, 200, 10, true, "")
		}
		store.RecordObservation(metricBuildDuration, "slow", now.Add(-19*24*time.Hour), labels, 700, 10, true, "")
		customConfig := &SLOConfig{SLOThresholds: SLOThresholds{
			BuildDurationBreachPercentage: thresholdValueSimple(0.10),
		}}
		slo := newBuildSLO30dWithTiers(customConfig, tierConfig, newTierPins())
		slo.updateGauges(store, nil)

		assertBuildBreachMetric(t, slo, "fast", 0)
	})
}
