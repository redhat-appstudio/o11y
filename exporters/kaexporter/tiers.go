package main

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tier assigns a fixed SLO threshold to components whose baseline is at or
// below BaselineMax. A nil BaselineMax is the required final catch-all tier.
type Tier struct {
	Name        string   `yaml:"name"`
	BaselineMax *float64 `yaml:"baseline_max"`
	Threshold   float64  `yaml:"threshold"`
}

// TierOverride selects an alternate tier list when all Match labels apply.
type TierOverride struct {
	Match map[string]string `yaml:"match"`
	Tiers []Tier            `yaml:"tiers"`
}

// TierSet contains a domain's default tiers and first-match-wins overrides.
type TierSet struct {
	Tiers     []Tier         `yaml:"tiers"`
	Overrides []TierOverride `yaml:"overrides,omitempty"`
}

// TierConfig contains tier sets keyed by the rolling-store domain name.
type TierConfig struct {
	ByDomain map[string]TierSet
}

// PinnedTier is the process-lifetime tier assignment for one metric series.
type PinnedTier struct {
	Name      string
	Threshold float64
}

// TierPins keeps assignments keyed by the process-lifetime MetricWindow that
// owns the corresponding series. Store.Data retains stable window pointers,
// so this avoids duplicating the full LabelSet as a second map key.
// Production access is guarded by KAExporter.mu during gauge updates.
type TierPins struct {
	ByWindow map[*MetricWindow]PinnedTier
}

func newTierPins() *TierPins {
	return &TierPins{ByWindow: make(map[*MetricWindow]PinnedTier)}
}

func (p *TierPins) get(window *MetricWindow) (PinnedTier, bool) {
	if p == nil || window == nil {
		return PinnedTier{}, false
	}
	pin, ok := p.ByWindow[window]
	return pin, ok
}

func (p *TierPins) set(window *MetricWindow, tier Tier) PinnedTier {
	if p.ByWindow == nil {
		p.ByWindow = make(map[*MetricWindow]PinnedTier)
	}
	pin := PinnedTier{Name: tier.Name, Threshold: tier.Threshold}
	p.ByWindow[window] = pin
	return pin
}

// loadTierConfigFile loads tier configuration from path.
func loadTierConfigFile(path string) (*TierConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var domains map[string]TierSet
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&domains); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &TierConfig{ByDomain: domains}, nil
}

// Sanitize drops invalid domains or overrides and logs why. A malformed YAML
// document is handled by the loader before this method is called.
func (tc *TierConfig) Sanitize() {
	if tc == nil {
		return
	}
	for domain, set := range tc.ByDomain {
		if !knownTierDomain(domain) {
			log.Printf("WARNING: SLO tiers: unknown domain %q; ignoring domain", domain)
			delete(tc.ByDomain, domain)
			continue
		}
		if err := validateTierList(set.Tiers); err != nil {
			log.Printf("WARNING: SLO tiers: %s.tiers: %v; ignoring domain", domain, err)
			delete(tc.ByDomain, domain)
			continue
		}

		validOverrides := set.Overrides[:0]
		for i, override := range set.Overrides {
			prefix := fmt.Sprintf("%s.overrides[%d]", domain, i)
			if err := validateTierMatch(domain, override.Match); err != nil {
				log.Printf("WARNING: SLO tiers: %s.match: %v; ignoring override", prefix, err)
				continue
			}
			if err := validateTierList(override.Tiers); err != nil {
				log.Printf("WARNING: SLO tiers: %s.tiers: %v; ignoring override", prefix, err)
				continue
			}
			validOverrides = append(validOverrides, override)
		}
		set.Overrides = validOverrides
		tc.ByDomain[domain] = set
	}
}

func knownTierDomain(domain string) bool {
	switch domain {
	case metricBuildDuration, metricIntegrationDuration, metricReleaseDuration:
		return true
	default:
		return false
	}
}

func validateTierList(tiers []Tier) error {
	if len(tiers) == 0 {
		return fmt.Errorf("must contain at least one tier")
	}
	seenNames := make(map[string]bool, len(tiers))
	var previousMax float64
	havePreviousMax := false
	catchAllCount := 0
	for i, tier := range tiers {
		name := strings.TrimSpace(tier.Name)
		if name == "" {
			return fmt.Errorf("tier %d has an empty name", i)
		}
		if seenNames[name] {
			return fmt.Errorf("tier name %q is duplicated", name)
		}
		seenNames[name] = true
		if tier.Threshold <= 0 || math.IsNaN(tier.Threshold) || math.IsInf(tier.Threshold, 0) {
			return fmt.Errorf("tier %q threshold must be a finite value > 0, got %g", name, tier.Threshold)
		}

		if tier.BaselineMax == nil {
			catchAllCount++
			if i != len(tiers)-1 {
				return fmt.Errorf("tier %q has null baseline_max but is not last", name)
			}
			continue
		}
		max := *tier.BaselineMax
		if max < 0 || math.IsNaN(max) || math.IsInf(max, 0) {
			return fmt.Errorf("tier %q baseline_max must be a finite value >= 0, got %g", name, max)
		}
		if havePreviousMax && max <= previousMax {
			return fmt.Errorf("tier %q baseline_max %g must be greater than the previous value %g", name, max, previousMax)
		}
		previousMax = max
		havePreviousMax = true
	}
	if catchAllCount != 1 {
		return fmt.Errorf("must contain exactly one null baseline_max catch-all tier, found %d", catchAllCount)
	}
	return nil
}

func validateTierMatch(domain string, match map[string]string) error {
	if len(match) == 0 {
		return fmt.Errorf("must contain at least one label")
	}
	for key, value := range match {
		if !knownTierMatchKey(key) {
			return fmt.Errorf("unknown label %q", key)
		}
		if !domainMatchKeyAllowed(domain, key) {
			return fmt.Errorf("label %q is not populated for domain %q", key, domain)
		}
		if value == "" {
			return fmt.Errorf("label %q must not be empty", key)
		}
	}
	return nil
}

func knownTierMatchKey(key string) bool {
	switch key {
	case "scenario", "event_type", "build_type", "automated", "test_type":
		return true
	default:
		return false
	}
}

func domainMatchKeyAllowed(domain, key string) bool {
	switch domain {
	case metricBuildDuration:
		return key == "build_type" || key == "event_type"
	case metricIntegrationDuration:
		return key == "scenario" || key == "event_type" || key == "test_type"
	case metricReleaseDuration:
		return key == "automated" || key == "event_type"
	default:
		return false
	}
}

// resolveTier returns the tier selected by the first matching override, or by
// the domain's default tier list when no override applies.
func (tc *TierConfig) resolveTier(domain string, ls LabelSet, baselineSeconds float64) (Tier, bool) {
	if tc == nil {
		return Tier{}, false
	}
	set, ok := tc.ByDomain[domain]
	if !ok {
		return Tier{}, false
	}
	tiers := set.Tiers
	for _, override := range set.Overrides {
		if tierMatchApplies(override.Match, ls) {
			tiers = override.Tiers
			break
		}
	}
	for _, tier := range tiers {
		if tier.BaselineMax == nil || baselineSeconds <= *tier.BaselineMax {
			return tier, true
		}
	}
	return Tier{}, false
}

func tierMatchApplies(match map[string]string, ls LabelSet) bool {
	return matchLabelSet(ls, match["scenario"], match["event_type"], match["build_type"], match["automated"], match["test_type"])
}

func logActiveTierConfig(tc *TierConfig, source string) {
	if tc == nil || len(tc.ByDomain) == 0 {
		return
	}
	domains := make([]string, 0, len(tc.ByDomain))
	for domain := range tc.ByDomain {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	log.Printf("SLO tiers: loaded active configuration from %s", source)
	for _, domain := range domains {
		set := tc.ByDomain[domain]
		log.Printf("  SLO tiers: %s defaults: %s", domain, formatTiers(set.Tiers))
		for i, override := range set.Overrides {
			log.Printf("  SLO tiers: %s override %d [%s]: %s", domain, i, formatTierMatch(override.Match), formatTiers(override.Tiers))
		}
	}
}

func formatTiers(tiers []Tier) string {
	formatted := make([]string, 0, len(tiers))
	for _, tier := range tiers {
		max := "catch-all"
		if tier.BaselineMax != nil {
			max = fmt.Sprintf("<=%gs", *tier.BaselineMax)
		}
		formatted = append(formatted, fmt.Sprintf("%s(%s -> %gs)", tier.Name, max, tier.Threshold))
	}
	return strings.Join(formatted, ", ")
}

func formatTierMatch(match map[string]string) string {
	keys := make([]string, 0, len(match))
	for key := range match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	formatted := make([]string, 0, len(keys))
	for _, key := range keys {
		formatted = append(formatted, key+"="+match[key])
	}
	return strings.Join(formatted, ", ")
}
