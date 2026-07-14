package main

import (
	"os"
	"path/filepath"
	"testing"
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

			f := newNamespaceFilter(cfg)

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
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestNamespaceFilter_Apply(t *testing.T) {
	tests := []struct {
		name       string
		filter     *namespaceFilter
		input      []string
		want       []string
	}{
		{
			name:   "nil config excludes nothing",
			filter: newNamespaceFilter(nil),
			input:  []string{"my-tenant", "rhtap-releng-tenant", "managed-foo", "managed-bar", "other-ns"},
			want:   []string{"my-tenant", "rhtap-releng-tenant", "managed-foo", "managed-bar", "other-ns"},
		},
		{
			name: "exact match only",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{"special-ns"},
			}),
			input: []string{"special-ns", "special-ns-2", "other"},
			want:  []string{"special-ns-2", "other"},
		},
		{
			name: "prefix match only",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{"test-*"},
			}),
			input: []string{"test-foo", "test-bar", "my-test", "production"},
			want:  []string{"my-test", "production"},
		},
		{
			name: "mixed exact and prefix",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{"exact-ns", "prefix-*"},
			}),
			input: []string{"exact-ns", "prefix-one", "prefix-two", "keep-me"},
			want:  []string{"keep-me"},
		},
		{
			name: "empty config excludes nothing",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{},
			}),
			input: []string{"rhtap-releng-tenant", "managed-foo", "anything"},
			want:  []string{"rhtap-releng-tenant", "managed-foo", "anything"},
		},
		{
			name:   "empty input",
			filter: newNamespaceFilter(nil),
			input:  nil,
			want:   nil,
		},
		{
			name: "leading wildcard",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{"*-managed"},
			}),
			input: []string{"foo-managed", "bar-managed", "managed-foo", "other"},
			want:  []string{"managed-foo", "other"},
		},
		{
			name: "mid-string wildcard",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{"konflux-perfscale-*-tenant"},
			}),
			input: []string{"konflux-perfscale-large-tenant", "konflux-perfscale-small-tenant", "konflux-perfscale", "other-ns"},
			want:  []string{"konflux-perfscale", "other-ns"},
		},
		{
			name: "all excluded",
			filter: newNamespaceFilter(&KAConfig{
				ExcludeNamespaces: []string{"ns-a", "ns-b"},
			}),
			input: []string{"ns-a", "ns-b"},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.filter.apply(tt.input)

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
