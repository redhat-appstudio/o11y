package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// KAConfig represents the YAML configuration file for the exporter.
type KAConfig struct {
	ExcludeNamespaces []string   `yaml:"excludeNamespaces"`
	CustomSLO         *SLOConfig `yaml:"customSLO,omitempty"`
}

// ThresholdMatch is a label-conditional override: if all non-empty fields
// match the current LabelSet, Value is used. Empty fields are wildcards.
type ThresholdMatch struct {
	Scenario  string  `yaml:"scenario,omitempty"`
	EventType string  `yaml:"event_type,omitempty"`
	BuildType string  `yaml:"build_type,omitempty"`
	Automated string  `yaml:"automated,omitempty"`
	Value     float64 `yaml:"value"`
}

// ThresholdValue supports two YAML forms:
//   - simple scalar:  build_duration_threshold_seconds: 7200
//   - object form:    build_duration_threshold_seconds:
//                       default: 7200
//                       matches:
//                         - event_type: push
//                           value: 5400
type ThresholdValue struct {
	Default *float64         `yaml:"default,omitempty"`
	Matches []ThresholdMatch `yaml:"matches,omitempty"`
}

func (tv *ThresholdValue) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		v, err := strconv.ParseFloat(value.Value, 64)
		if err != nil {
			return fmt.Errorf("threshold value: %w", err)
		}
		tv.Default = &v
		return nil
	}
	type plain ThresholdValue
	return value.Decode((*plain)(tv))
}

// ResolveValue checks matches first (first match wins), falls back to Default.
// Returns (0, false) when neither matches nor default apply.
func (tv *ThresholdValue) ResolveValue(ls LabelSet) (float64, bool) {
	if tv == nil {
		return 0, false
	}
	for _, m := range tv.Matches {
		if m.Scenario != "" && m.Scenario != ls.Scenario {
			continue
		}
		if m.EventType != "" && m.EventType != ls.EventType {
			continue
		}
		if m.BuildType != "" && m.BuildType != ls.BuildType {
			continue
		}
		if m.Automated != "" && m.Automated != ls.Automated {
			continue
		}
		return m.Value, true
	}
	if tv.Default != nil {
		return *tv.Default, true
	}
	return 0, false
}

// SLOThresholds holds overridable SLO parameters. Pointer fields: nil means
// "inherit from parent level." Set at any level in the tenant -> application
// -> component hierarchy.
type SLOThresholds struct {
	BuildDurationThresholdSeconds       *ThresholdValue `yaml:"build_duration_threshold_seconds,omitempty"`
	BuildDurationBreachPercentage       *ThresholdValue `yaml:"build_duration_breach_percentage,omitempty"`
	IntegrationDurationThresholdSeconds *ThresholdValue `yaml:"integration_duration_threshold_seconds,omitempty"`
	IntegrationDurationBreachPercentage *ThresholdValue `yaml:"integration_duration_breach_percentage,omitempty"`
	ReleaseDurationThresholdSeconds     *ThresholdValue `yaml:"release_duration_threshold_seconds,omitempty"`
	ReleaseDurationBreachPercentage     *ThresholdValue `yaml:"release_duration_breach_percentage,omitempty"`
}

func (t *SLOThresholds) durationThresholdFor(domain string) *ThresholdValue {
	switch domain {
	case metricBuildDuration:
		return t.BuildDurationThresholdSeconds
	case metricIntegrationDuration:
		return t.IntegrationDurationThresholdSeconds
	case metricReleaseDuration:
		return t.ReleaseDurationThresholdSeconds
	}
	return nil
}

func (t *SLOThresholds) breachPercentageFor(domain string) *ThresholdValue {
	switch domain {
	case metricBuildDuration:
		return t.BuildDurationBreachPercentage
	case metricIntegrationDuration:
		return t.IntegrationDurationBreachPercentage
	case metricReleaseDuration:
		return t.ReleaseDurationBreachPercentage
	}
	return nil
}

// ComponentSLOConfig holds SLO overrides for a specific component.
type ComponentSLOConfig struct {
	SLOThresholds `yaml:",inline"`
}

// ApplicationSLOConfig holds SLO overrides for an application and its components.
type ApplicationSLOConfig struct {
	SLOThresholds `yaml:",inline"`
	Components    map[string]*ComponentSLOConfig `yaml:"components,omitempty"`
}

// TenantSLOConfig holds SLO overrides for a tenant namespace and its applications.
type TenantSLOConfig struct {
	SLOThresholds `yaml:",inline"`
	Applications  map[string]*ApplicationSLOConfig `yaml:"applications,omitempty"`
}

// SLOConfig holds the full SLO override hierarchy: defaults -> tenants ->
// applications -> components.
type SLOConfig struct {
	SLOThresholds `yaml:",inline"`
	Tenants       map[string]*TenantSLOConfig `yaml:"tenants,omitempty"`
}

// ResolvedSLO holds the effective SLO parameters for a specific label set
// after cascading through the config hierarchy. nil fields mean "use
// hardcoded defaults."
type ResolvedSLO struct {
	DurationThreshold *float64
	BreachPercentage  *float64
}

// Sanitize walks the SLO config hierarchy and nils out any invalid values,
// logging a warning for each. The exporter starts normally with valid
// overrides applied and invalid ones ignored.
func (c *SLOConfig) Sanitize() {
	if c == nil {
		return
	}
	c.sanitize("customSLO")
	for name, tenant := range c.Tenants {
		if tenant == nil {
			continue
		}
		prefix := fmt.Sprintf("customSLO.tenants.%s", name)
		tenant.sanitize(prefix)
		for appName, app := range tenant.Applications {
			if app == nil {
				continue
			}
			appPrefix := fmt.Sprintf("%s.applications.%s", prefix, appName)
			app.sanitize(appPrefix)
			for compName, comp := range app.Components {
				if comp == nil {
					continue
				}
				compPrefix := fmt.Sprintf("%s.components.%s", appPrefix, compName)
				comp.sanitize(compPrefix)
			}
		}
	}
}

func (t *SLOThresholds) sanitize(prefix string) {
	thresholds := []*struct {
		name string
		val  **ThresholdValue
	}{
		{"build_duration_threshold_seconds", &t.BuildDurationThresholdSeconds},
		{"integration_duration_threshold_seconds", &t.IntegrationDurationThresholdSeconds},
		{"release_duration_threshold_seconds", &t.ReleaseDurationThresholdSeconds},
	}
	for _, th := range thresholds {
		sanitizeThresholdValue(th.val, prefix, th.name, func(v float64) bool { return v > 0 }, "must be > 0")
	}

	percentages := []*struct {
		name string
		val  **ThresholdValue
	}{
		{"build_duration_breach_percentage", &t.BuildDurationBreachPercentage},
		{"integration_duration_breach_percentage", &t.IntegrationDurationBreachPercentage},
		{"release_duration_breach_percentage", &t.ReleaseDurationBreachPercentage},
	}
	for _, pct := range percentages {
		sanitizeThresholdValue(pct.val, prefix, pct.name, func(v float64) bool { return v > 0 && v <= 1 }, "must be in (0, 1]")
	}
}

func sanitizeThresholdValue(tvp **ThresholdValue, prefix, name string, valid func(float64) bool, rule string) {
	tv := *tvp
	if tv == nil {
		return
	}
	if tv.Default != nil && !valid(*tv.Default) {
		log.Printf("WARNING: %s.%s: %s, got %g; ignoring default", prefix, name, rule, *tv.Default)
		tv.Default = nil
	}
	validMatches := tv.Matches[:0]
	for _, m := range tv.Matches {
		if !valid(m.Value) {
			log.Printf("WARNING: %s.%s: match %s, got %g; ignoring match entry", prefix, name, rule, m.Value)
			continue
		}
		if m.Scenario == "" && m.EventType == "" && m.BuildType == "" && m.Automated == "" {
			log.Printf("WARNING: %s.%s: match entry has no filter fields (matches everything); use 'default' instead; ignoring match entry", prefix, name)
			continue
		}
		validMatches = append(validMatches, m)
	}
	tv.Matches = validMatches
	if tv.Default == nil && len(tv.Matches) == 0 {
		*tvp = nil
	}
}

// Resolve returns the effective SLO thresholds for a specific label set,
// cascading: component -> application -> tenant (namespace) -> defaults.
// At each level, a ThresholdValue is only applied when ResolveValue returns
// ok=true for the given labels, preserving higher-level values when a
// lower-level ThresholdValue has no matching entry.
func (c *SLOConfig) Resolve(ls LabelSet, domain string) ResolvedSLO {
	if c == nil {
		return ResolvedSLO{}
	}

	var result ResolvedSLO

	resolveThreshold := func(tv *ThresholdValue) {
		if tv == nil {
			return
		}
		if v, ok := tv.ResolveValue(ls); ok {
			result.DurationThreshold = &v
		}
	}
	resolvePercentage := func(tv *ThresholdValue) {
		if tv == nil {
			return
		}
		if v, ok := tv.ResolveValue(ls); ok {
			result.BreachPercentage = &v
		}
	}

	resolveThreshold(c.durationThresholdFor(domain))
	resolvePercentage(c.breachPercentageFor(domain))

	tenantCfg := c.Tenants[ls.Namespace]
	if tenantCfg == nil {
		return result
	}
	resolveThreshold(tenantCfg.durationThresholdFor(domain))
	resolvePercentage(tenantCfg.breachPercentageFor(domain))

	appCfg := tenantCfg.Applications[ls.Application]
	if appCfg == nil {
		return result
	}
	resolveThreshold(appCfg.durationThresholdFor(domain))
	resolvePercentage(appCfg.breachPercentageFor(domain))

	compCfg := appCfg.Components[ls.Component]
	if compCfg == nil {
		return result
	}
	resolveThreshold(compCfg.durationThresholdFor(domain))
	resolvePercentage(compCfg.breachPercentageFor(domain))

	return result
}

func logSLOOverrides(c *SLOConfig) {
	if c == nil {
		return
	}
	logThresholdValue := func(prefix, name string, tv *ThresholdValue) {
		if tv == nil {
			return
		}
		if tv.Default != nil {
			log.Printf("  SLO override: %s.%s = %g", prefix, name, *tv.Default)
		}
		for _, m := range tv.Matches {
			var filters []string
			if m.Scenario != "" {
				filters = append(filters, "scenario="+m.Scenario)
			}
			if m.EventType != "" {
				filters = append(filters, "event_type="+m.EventType)
			}
			if m.BuildType != "" {
				filters = append(filters, "build_type="+m.BuildType)
			}
			if m.Automated != "" {
				filters = append(filters, "automated="+m.Automated)
			}
			log.Printf("  SLO override: %s.%s [%s] = %g", prefix, name, strings.Join(filters, ", "), m.Value)
		}
	}
	logThresholds := func(prefix string, t *SLOThresholds) {
		logThresholdValue(prefix, "build_duration_threshold_seconds", t.BuildDurationThresholdSeconds)
		logThresholdValue(prefix, "build_duration_breach_percentage", t.BuildDurationBreachPercentage)
		logThresholdValue(prefix, "integration_duration_threshold_seconds", t.IntegrationDurationThresholdSeconds)
		logThresholdValue(prefix, "integration_duration_breach_percentage", t.IntegrationDurationBreachPercentage)
		logThresholdValue(prefix, "release_duration_threshold_seconds", t.ReleaseDurationThresholdSeconds)
		logThresholdValue(prefix, "release_duration_breach_percentage", t.ReleaseDurationBreachPercentage)
	}

	logThresholds("defaults", &c.SLOThresholds)
	for tName, tenant := range c.Tenants {
		if tenant == nil {
			continue
		}
		prefix := fmt.Sprintf("tenants.%s", tName)
		logThresholds(prefix, &tenant.SLOThresholds)
		for aName, app := range tenant.Applications {
			if app == nil {
				continue
			}
			aPrefix := fmt.Sprintf("%s.applications.%s", prefix, aName)
			logThresholds(aPrefix, &app.SLOThresholds)
			for cName, comp := range app.Components {
				if comp == nil {
					continue
				}
				cPrefix := fmt.Sprintf("%s.components.%s", aPrefix, cName)
				logThresholds(cPrefix, &comp.SLOThresholds)
			}
		}
	}
}

// namespaceFilter holds parsed exclusion rules for namespace filtering.
type namespaceFilter struct {
	exactMatches map[string]bool
	patterns     []string
	source       string // "config" or "none"
}

func loadConfig(path string) (*KAConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	var cfg KAConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}

	return &cfg, nil
}

func newNamespaceFilter(cfg *KAConfig) (*namespaceFilter, error) {
	if cfg == nil {
		return &namespaceFilter{
			exactMatches: make(map[string]bool),
			source:       "none",
		}, nil
	}

	f := &namespaceFilter{
		exactMatches: make(map[string]bool),
		source:       "config",
	}
	for _, entry := range cfg.ExcludeNamespaces {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "*") {
			if _, err := path.Match(entry, "validate"); err != nil {
				return nil, fmt.Errorf("invalid glob pattern %q: %w", entry, err)
			}
			f.patterns = append(f.patterns, entry)
		} else {
			f.exactMatches[entry] = true
		}
	}
	return f, nil
}

func (f *namespaceFilter) apply(namespaces []string) []string {
	var result []string
	for _, ns := range namespaces {
		if f.exactMatches[ns] {
			continue
		}
		excluded := false
		for _, pattern := range f.patterns {
			matched, err := path.Match(pattern, ns)
			if err != nil {
				log.Printf("WARNING: invalid glob pattern %q (skipping): %v", pattern, err)
				continue
			}
			if matched {
				excluded = true
				break
			}
		}
		if !excluded {
			result = append(result, ns)
		}
	}
	return result
}
