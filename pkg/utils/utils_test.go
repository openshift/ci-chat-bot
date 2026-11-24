package utils

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
)

const testKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://explicit.example.com
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: test
`

func TestLoadKubeconfigFromFlagOrDefault(t *testing.T) {
	defaultConfig := &rest.Config{Host: "https://default.example.com"}

	got, err := LoadKubeconfigFromFlagOrDefault("", defaultConfig)
	if err != nil {
		t.Fatalf("unexpected error loading default kubeconfig: %v", err)
	}
	if got != defaultConfig {
		t.Errorf("expected default kubeconfig to be returned, got %#v", got)
	}

	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatalf("failed to write test kubeconfig: %v", err)
	}

	got, err = LoadKubeconfigFromFlagOrDefault(path, defaultConfig)
	if err != nil {
		t.Fatalf("unexpected error loading explicit kubeconfig: %v", err)
	}
	if got.Host != "https://explicit.example.com" {
		t.Errorf("expected explicit kubeconfig host, got %q", got.Host)
	}
}

func TestLoadKubeconfigFromFlagOrDefaultRejectsUnusableExplicitConfig(t *testing.T) {
	for name, contents := range map[string]string{
		"empty file":   "",
		"no clusters":  "apiVersion: v1\nkind: Config\n",
		"invalid yaml": "not: [valid",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kubeconfig")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatalf("failed to write test kubeconfig: %v", err)
			}
			if _, err := LoadKubeconfigFromFlagOrDefault(path, nil); err == nil {
				t.Fatal("expected an error for an unusable explicit kubeconfig, got nil")
			}
		})
	}
}

func TestResolveKubeconfigPath(t *testing.T) {
	got, err := ResolveKubeconfigPath("")
	if err != nil {
		t.Fatalf("unexpected error resolving empty path: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty path to be preserved, got %q", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(testKubeconfig), 0o600); err != nil {
		t.Fatalf("failed to write test kubeconfig: %v", err)
	}

	t.Chdir(dir)
	resolved, err := ResolveKubeconfigPath("kubeconfig")
	if err != nil {
		t.Fatalf("unexpected error resolving relative path: %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Errorf("expected an absolute path, got %q", resolved)
	}

	// A later working directory change must not affect loading the resolved path.
	t.Chdir(t.TempDir())
	cfg, err := LoadKubeconfigFromFlagOrDefault(resolved, nil)
	if err != nil {
		t.Fatalf("unexpected error loading resolved kubeconfig: %v", err)
	}
	if cfg.Host != "https://explicit.example.com" {
		t.Errorf("expected explicit kubeconfig host, got %q", cfg.Host)
	}
}
