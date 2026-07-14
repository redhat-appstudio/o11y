package main

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// KAConfig represents the YAML configuration file for the exporter.
type KAConfig struct {
	ExcludeNamespaces []string `yaml:"excludeNamespaces"`
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

func newNamespaceFilter(cfg *KAConfig) *namespaceFilter {
	if cfg == nil {
		return &namespaceFilter{
			exactMatches: make(map[string]bool),
			source:       "none",
		}
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
			f.patterns = append(f.patterns, entry)
		} else {
			f.exactMatches[entry] = true
		}
	}
	return f
}

func (f *namespaceFilter) apply(namespaces []string) []string {
	var result []string
	for _, ns := range namespaces {
		if f.exactMatches[ns] {
			continue
		}
		excluded := false
		for _, pattern := range f.patterns {
			if matched, _ := path.Match(pattern, ns); matched {
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
