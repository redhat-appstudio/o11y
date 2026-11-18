package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testTokenFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokenFileRotation(t *testing.T) {
	path := testTokenFile(t)
	t.Setenv("KA_TOKEN_FILE", path)
	t.Setenv("KA_TOKEN", "ignored-static-token")
	resolved, err := resolveTokenSource()
	if err != nil {
		t.Fatal(err)
	}
	e := &KAExporter{kaTokenFile: resolved}
	for _, want := range []string{"test-token", "rotated-token"} {
		if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := e.bearerToken()
		if err != nil || got != want {
			t.Fatalf("bearerToken() = %q, %v; want %q", got, err, want)
		}
	}
}

func TestMissingExplicitTokenFile(t *testing.T) {
	t.Setenv("KA_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("KA_TOKEN", "must-not-be-used")
	if _, err := resolveTokenSource(); err == nil {
		t.Fatal("expected an error for missing explicit token file")
	}
}

func TestStaticTokenDoesNotReplaceMissingServiceAccountToken(t *testing.T) {
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); !os.IsNotExist(err) {
		t.Skip("requires an environment without a mounted ServiceAccount token")
	}
	t.Setenv("KA_TOKEN_FILE", "")
	t.Setenv("KA_TOKEN", "must-not-be-used")
	if _, err := resolveTokenSource(); err == nil {
		t.Fatal("KA_TOKEN must not replace the missing ServiceAccount token")
	}
}
