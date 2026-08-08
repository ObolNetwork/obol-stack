package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestBuildStorefrontPreviewHTTPRouteSecurity(t *testing.T) {
	route := buildStorefrontPreviewHTTPRoute()
	data, err := json.Marshal(route)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(data)

	for _, want := range []string{
		`"hostnames":["storefront-preview.obol.stack"]`,
		`"name":"Content-Security-Policy"`,
		`frame-ancestors 'self' http://obol.stack:* https://obol.stack:*`,
		`"name":"Permissions-Policy"`,
		`camera=(), microphone=(), geolocation=(), payment=()`,
		`"name":"X-Content-Type-Options","value":"nosniff"`,
		`"name":"tunnel-storefront","port":3000`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preview route missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, `"hostnames":[]`) {
		t.Fatalf("preview route must never become hostless:\n%s", rendered)
	}
}

func TestStorefrontImageUsesPersistedDevTag(t *testing.T) {
	t.Setenv("OBOL_DEVELOPMENT", "true")
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := os.WriteFile(
		filepath.Join(cfg.ConfigDir, ".dev-image-tag"),
		[]byte("dev-abc1234"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	const want = "ghcr.io/obolnetwork/obol-stack-public-storefront:dev-abc1234"
	if got := storefrontImage(cfg); got != want {
		t.Fatalf("storefrontImage() = %q, want %q", got, want)
	}
}

func TestWaitReadyTimeout(t *testing.T) {
	t.Setenv("FLOW_TUNNEL_TIMEOUT", "")
	if got := waitReadyTimeout(); got != defaultWaitReadyTimeout {
		t.Errorf("default: got %s, want %s", got, defaultWaitReadyTimeout)
	}

	t.Setenv("FLOW_TUNNEL_TIMEOUT", "90s")
	if got := waitReadyTimeout(); got != 90*time.Second {
		t.Errorf("duration override: got %s, want 90s", got)
	}

	t.Setenv("FLOW_TUNNEL_TIMEOUT", "120")
	if got := waitReadyTimeout(); got != 120*time.Second {
		t.Errorf("integer-seconds override: got %s, want 120s", got)
	}

	t.Setenv("FLOW_TUNNEL_TIMEOUT", "not-a-duration")
	if got := waitReadyTimeout(); got != defaultWaitReadyTimeout {
		t.Errorf("invalid override: got %s, want default %s", got, defaultWaitReadyTimeout)
	}

	t.Setenv("FLOW_TUNNEL_TIMEOUT", "0")
	if got := waitReadyTimeout(); got != defaultWaitReadyTimeout {
		t.Errorf("zero override should fall back to default: got %s, want %s", got, defaultWaitReadyTimeout)
	}
}

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"stack.example.com", "stack.example.com"},
		{"https://stack.example.com", "stack.example.com"},
		{"http://stack.example.com/", "stack.example.com"},
		{"https://stack.example.com/foo?bar=baz#x", "stack.example.com"},
		{"  stack.example.com  ", "stack.example.com"},
	}

	for _, tt := range tests {
		if got := normalizeHostname(tt.in); got != tt.want {
			t.Fatalf("normalizeHostname(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseQuickTunnelURL(t *testing.T) {
	logs := `
2026-01-14T12:00:00Z INF | Your quick tunnel URL is:                   |
2026-01-14T12:00:00Z INF | https://seasonal-deck-organisms-sf.trycloudflare.com |
`

	url, ok := parseQuickTunnelURL(logs)
	if !ok {
		t.Fatalf("expected ok=true")
	}

	if url != "https://seasonal-deck-organisms-sf.trycloudflare.com" {
		t.Fatalf("unexpected url: %q", url)
	}
}

func TestParseQuickTunnelURL_PicksLatest(t *testing.T) {
	logs := `
2026-01-14T12:00:00Z INF | https://old-quick-tunnel.trycloudflare.com |
2026-01-14T12:05:00Z INF | https://new-quick-tunnel.trycloudflare.com |
`

	url, ok := parseQuickTunnelURL(logs)
	if !ok {
		t.Fatalf("expected ok=true")
	}

	if url != "https://new-quick-tunnel.trycloudflare.com" {
		t.Fatalf("unexpected url: %q", url)
	}
}

func TestBuildLocalManagedConfigYAMLRoutesOnlyRequestedHostname(t *testing.T) {
	out := string(buildLocalManagedConfigYAML([]string{"stack.example.com"}, "00000000-0000-0000-0000-000000000000"))

	for _, want := range []string{
		"tunnel: 00000000-0000-0000-0000-000000000000",
		"- hostname: stack.example.com",
		"service: http://traefik.traefik.svc.cluster.local:80",
		"- service: http_status:404",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("config missing %q:\n%s", want, out)
		}
	}

	if strings.Count(out, "hostname:") != 1 {
		t.Fatalf("persistent tunnel config should expose exactly one hostname:\n%s", out)
	}
	for _, unexpected := range []string{"obol-agent.obol.stack", "hermes-obol-agent.obol.stack", "*.obol.stack"} {
		if strings.Contains(out, unexpected) {
			t.Fatalf("persistent tunnel config exposes local agent hostname %q:\n%s", unexpected, out)
		}
	}
}

// TestBuildLocalManagedConfigYAMLMultiHostname proves two domains coexist in a
// single connector ingress: one rule per hostname, both to the same Traefik
// service, terminated by exactly one catch-all (last). This is the core "deploy
// one domain, then another" invariant for local-managed tunnels.
func TestBuildLocalManagedConfigYAMLMultiHostname(t *testing.T) {
	out := string(buildLocalManagedConfigYAML(
		[]string{"a.example.com", "b.example.com"},
		"00000000-0000-0000-0000-000000000000",
	))

	for _, want := range []string{"- hostname: a.example.com", "- hostname: b.example.com", "- service: http_status:404"} {
		if !strings.Contains(out, want) {
			t.Fatalf("multi-hostname config missing %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "hostname:"); got != 2 {
		t.Fatalf("expected exactly 2 hostname rules, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "http_status:404"); got != 1 {
		t.Fatalf("expected exactly one catch-all rule, got %d:\n%s", got, out)
	}
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "service: http_status:404") {
		t.Fatalf("catch-all rule must be last:\n%s", out)
	}
	if got := strings.Count(out, "service: http://traefik.traefik.svc.cluster.local:80"); got != 2 {
		t.Fatalf("expected both hostnames to route to Traefik, got %d service rules:\n%s", got, out)
	}
}

// TestBuildLocalManagedConfigYAMLDeduplicates ensures duplicate/empty/mixed-case
// hostnames are normalized so a re-add or casing slip cannot produce two
// conflicting ingress rules for the same name.
func TestBuildLocalManagedConfigYAMLDeduplicates(t *testing.T) {
	out := string(buildLocalManagedConfigYAML(
		[]string{"A.Example.com", "", "a.example.com", "https://a.example.com/path"},
		"00000000-0000-0000-0000-000000000000",
	))
	if got := strings.Count(out, "hostname:"); got != 1 {
		t.Fatalf("expected duplicates/empties collapsed to 1 hostname rule, got %d:\n%s", got, out)
	}
	if !strings.Contains(out, "- hostname: a.example.com") {
		t.Fatalf("expected normalized lowercase hostname:\n%s", out)
	}
}

// writeFakeKubectl drops an executable kubectl stub into cfg.BinDir that logs
// every invocation (argv) to logPath and, for a `get serviceoffers.obol.org`
// query, prints boundHostname as the sole offer-bound hostname -- just enough
// to drive offerBoundHostnames()/DeleteStorefront() without a real cluster.
// Apply stdin is appended so callers can assert which resources were published.
func writeFakeKubectl(t *testing.T, cfg *config.Config, logPath, boundHostname string) {
	t.Helper()
	if err := os.MkdirAll(cfg.BinDir, 0o755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$*" in
  *"get serviceoffers.obol.org"*) echo %q ;;
  *apply*)
    echo "---APPLY---" >> %q
    cat >> %q
    echo >> %q
    ;;
esac
exit 0
`, logPath, boundHostname, logPath, logPath, logPath)
	if err := os.WriteFile(filepath.Join(cfg.BinDir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
}

// TestCreateStorefront_TearsDownWhenAllHostsOfferBound guards the Canary402
// fix: when every hostname CreateStorefront was asked to serve turns out to
// be offer-bound, it must tear down the previously-applied public
// tunnel-storefront HTTPRoute instead of leaving a stale wider-hostname
// route shadowing the offer's dedicated-origin route. The local preview
// renderer (Deployment/Service + storefront-preview route) is retained.
func TestCreateStorefront_TearsDownWhenAllHostsOfferBound(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)

	logPath := filepath.Join(cfg.ConfigDir, "kubectl.log")
	writeFakeKubectl(t, cfg, logPath, "example.com")

	if err := CreateStorefront(cfg, "example.com"); err != nil {
		t.Fatalf("CreateStorefront: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read kubectl log: %v", err)
	}
	log := string(logBytes)

	if !strings.Contains(log, "delete httproute/tunnel-storefront") {
		t.Fatalf("CreateStorefront must tear down the stale tunnel-storefront HTTPRoute when every requested hostname is offer-bound; kubectl invocations:\n%s", log)
	}
	if !strings.Contains(log, "apply") {
		t.Fatalf("CreateStorefront must retain the local storefront renderer when every requested hostname is offer-bound; kubectl invocations:\n%s", log)
	}
	if !strings.Contains(log, `"hostnames":["storefront-preview.obol.stack"]`) {
		t.Fatalf("CreateStorefront must keep the local-only preview HTTPRoute; kubectl invocations:\n%s", log)
	}
	if strings.Contains(log, `"hostnames":["example.com"]`) {
		t.Fatalf("CreateStorefront must not re-publish the public catch-all for offer-bound hostnames; kubectl invocations:\n%s", log)
	}
	if strings.Contains(log, "delete httproute/storefront-preview") ||
		strings.Contains(log, "delete deployment/tunnel-storefront") ||
		strings.Contains(log, "delete service/tunnel-storefront") {
		t.Fatalf("CreateStorefront must not delete the local preview renderer when only public hosts are offer-bound; kubectl invocations:\n%s", log)
	}
}

// TestDeleteStorefront_KeepsLocalPreview ensures tearing down the public
// catch-all (tunnel delete / last quick-tunnel offer) still leaves the
// operator branding editor a local renderer to iframe.
func TestDeleteStorefront_KeepsLocalPreview(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)

	logPath := filepath.Join(cfg.ConfigDir, "kubectl.log")
	writeFakeKubectl(t, cfg, logPath, "")

	if err := DeleteStorefront(cfg); err != nil {
		t.Fatalf("DeleteStorefront: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read kubectl log: %v", err)
	}
	log := string(logBytes)

	if !strings.Contains(log, "delete httproute/tunnel-storefront") {
		t.Fatalf("DeleteStorefront must remove the public catch-all; kubectl invocations:\n%s", log)
	}
	if strings.Contains(log, "delete httproute/storefront-preview") ||
		strings.Contains(log, "delete deployment/tunnel-storefront") ||
		strings.Contains(log, "delete service/tunnel-storefront") {
		t.Fatalf("DeleteStorefront must not tear down the local preview renderer; kubectl invocations:\n%s", log)
	}
	if !strings.Contains(log, `"hostnames":["storefront-preview.obol.stack"]`) {
		t.Fatalf("DeleteStorefront must re-ensure the local preview route; kubectl invocations:\n%s", log)
	}
}

// TestRefreshStorefront_NoOpWithoutTunnelState guards a first `obol sell
// ... --hostname X --no-register` before any persistent tunnel has ever been
// created: storefrontHostnames("") has nothing to report (no persistent
// tunnel state, no quick-tunnel URL to parse), so RefreshStorefront must
// no-op quietly instead of calling CreateStorefront with zero hostnames and
// surfacing its "requires at least one hostname" error as a confusing
// warning. EnsureTunnelForSell reconciles the storefront later once the
// tunnel exists.
func TestRefreshStorefront_NoOpWithoutTunnelState(t *testing.T) {
	cfg := newHostnameTestConfig(t)
	writeFakeKubeconfig(t, cfg)

	logPath := filepath.Join(cfg.ConfigDir, "kubectl.log")
	writeFakeKubectl(t, cfg, logPath, "")

	if err := RefreshStorefront(cfg); err != nil {
		t.Fatalf("RefreshStorefront should no-op without tunnel state, got error: %v", err)
	}

	if _, err := os.ReadFile(logPath); err == nil {
		t.Fatal("RefreshStorefront must not invoke kubectl when there is no hostname to publish")
	}
}

func TestPatchAgentBaseURL_Insert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values-obol.yaml")

	original := `extraEnv:
  - name: REMOTE_SIGNER_URL
    value: http://remote-signer:9000

skills:
  enabled: false
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchAgentBaseURL(path, "https://mystack.example.com"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "name: AGENT_BASE_URL") {
		t.Errorf("patched file missing AGENT_BASE_URL:\n%s", content)
	}

	if !strings.Contains(content, "value: https://mystack.example.com") {
		t.Errorf("patched file missing tunnel URL value:\n%s", content)
	}

	if !strings.Contains(content, "REMOTE_SIGNER_URL") {
		t.Errorf("patched file lost REMOTE_SIGNER_URL:\n%s", content)
	}
}

func TestPatchAgentBaseURL_Update(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values-obol.yaml")

	original := `extraEnv:
  - name: REMOTE_SIGNER_URL
    value: http://remote-signer:9000
  - name: AGENT_BASE_URL
    value: https://old.example.com

skills:
  enabled: false
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchAgentBaseURL(path, "https://new.example.com"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "value: https://new.example.com") {
		t.Errorf("patched file missing updated URL:\n%s", content)
	}

	if strings.Contains(content, "old.example.com") {
		t.Errorf("patched file still has old URL:\n%s", content)
	}
	// Should only have one AGENT_BASE_URL (no duplicate insertion).
	if strings.Count(content, "AGENT_BASE_URL") != 1 {
		t.Errorf("expected exactly 1 AGENT_BASE_URL entry:\n%s", content)
	}
}

func TestPatchAgentBaseURL_InsertHermesManifestIndentation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "values-hermes.yaml")

	original := `resources:
  - apiVersion: apps/v1
    kind: Deployment
    spec:
      template:
        spec:
          containers:
            - name: openclaw
              env:
                - name: REMOTE_SIGNER_URL
                  value: http://remote-signer:9000
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := patchAgentBaseURL(path, "https://mystack.example.com"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if !strings.Contains(content, "                - name: AGENT_BASE_URL") {
		t.Fatalf("patched Hermes manifest missing preserved indent for name:\n%s", content)
	}

	if !strings.Contains(content, "                  value: https://mystack.example.com") {
		t.Fatalf("patched Hermes manifest missing preserved indent for value:\n%s", content)
	}
}
