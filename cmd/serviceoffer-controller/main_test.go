package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig_FromKubeconfigFile asserts loadConfig parses an explicit
// kubeconfig path. This is the local-dev codepath used when --leader-elect=false.
func TestLoadConfig_FromKubeconfigFile(t *testing.T) {
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kc, []byte(minimalKubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	cfg, err := loadConfig(kc)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Host != "https://example.invalid:6443" {
		t.Fatalf("unexpected host: %q", cfg.Host)
	}
}

// TestLoadConfig_FromKubeconfigEnv mirrors the path used when KUBECONFIG is set
// (e.g. obol kubectl/helm passthrough during local dev).
func TestLoadConfig_FromKubeconfigEnv(t *testing.T) {
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(kc, []byte(minimalKubeconfig), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	t.Setenv("KUBECONFIG", kc)
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Host != "https://example.invalid:6443" {
		t.Fatalf("unexpected host: %q", cfg.Host)
	}
}

// TestLeaderElectionDefaults locks in the lease parameters chosen for fast
// failover on single-node k3d. If you tune these for a multi-zone deployment,
// update this test and the PR-description rationale.
func TestLeaderElectionDefaults(t *testing.T) {
	if leaseDuration <= renewDeadline {
		t.Fatalf("leaseDuration (%s) must exceed renewDeadline (%s)", leaseDuration, renewDeadline)
	}
	if renewDeadline <= retryPeriod {
		t.Fatalf("renewDeadline (%s) must exceed retryPeriod (%s)", renewDeadline, retryPeriod)
	}
	if leaseName != "serviceoffer-controller" {
		t.Fatalf("leaseName drifted from RBAC + Deployment expectation: %q", leaseName)
	}
	if defaultLockNamespace != "x402" {
		t.Fatalf("defaultLockNamespace drifted from infrastructure manifest: %q", defaultLockNamespace)
	}
}

const minimalKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://example.invalid:6443
    insecure-skip-tls-verify: true
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: test-token
`
