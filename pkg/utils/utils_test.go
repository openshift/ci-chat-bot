package utils

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/rest"
)

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
	if err := os.WriteFile(path, []byte(`apiVersion: v1
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
`), 0o600); err != nil {
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
