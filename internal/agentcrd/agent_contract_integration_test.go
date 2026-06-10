//go:build integration

package agentcrd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// Agent external-contract integration test (PR #582)
//
// The unit tests in agent_test.go and serviceoffercontroller/agent_render_test.go
// only prove that we *render* the `.no-bundled-skills` marker and the capped
// hermes-config keys. They do NOT prove the Hermes image
// (nousresearch/hermes-agent:v2026.6.5) actually honors them. v2026.5.28
// shipped the marker check on the install/CLI path only; the per-launch
// sync_skills() call ignored it and re-seeded ~24 categories from the
// image-baked /opt/hermes/skills source on every boot, regardless of the
// marker. v2026.6.5 (commit 2ed96372a, "blank-slate skills") added the marker
// check to sync_skills() itself (tools/skills_sync.py:467), so the in-cluster
// contract is honored end-to-end. This test closes that gap on a LIVE cluster.
//
// CI coverage: flow-16-sell-agent.sh in release-smoke asserts the same
// pod-side bundled-skills-empty + marker-present invariants in bash. The
// two are belt-and-braces — flow-16 catches regressions in CI, this Go test
// is the developer-runnable white-box version. A Hermes image bump should
// re-run both before merging (the Renovate package rule for
// nousresearch/hermes-agent in renovate.json reminds the reviewer):
//
//	(1) the .no-bundled-skills marker exists on the agent's host PVC path
//	    (agentcrd.HostNoBundledSkillsMarkerPath), so Hermes' installer/sync
//	    skips seeding its ~80 bundled skills;
//	(2) the rendered hermes-config ConfigMap in the agent's namespace carries
//	    the capped knobs: lifetime_seconds: 90, max_turns: 30,
//	    reasoning_effort: low, and disabled_toolsets {memory, web};
//	(3) a BEHAVIORAL signal that bundled skills were actually skipped — see
//	    assertBundledSkillsSkippedInPod for why we assert pod filesystem state
//	    rather than grep a log line.
//
// Like the rest of the integration suite (internal/openclaw/*_integration_test.go)
// it t.Skip()s gracefully when prerequisites are missing, so `go test` without a
// cluster is a no-op.
//
// To run manually (a local OpenAI-compatible inference endpoint is available at
// host 100.117.108.5 for development — never hardcode it):
//
//	export OBOL_DEVELOPMENT=true \
//	  OBOL_CONFIG_DIR=$(pwd)/.workspace/config \
//	  OBOL_BIN_DIR=$(pwd)/.workspace/bin \
//	  OBOL_DATA_DIR=$(pwd)/.workspace/data
//	go build -o .workspace/bin/obol ./cmd/obol   # MUST rebuild after code changes
//	# point the stack at the dev endpoint first (see CLAUDE.md "Pointing the
//	# stack at an external OpenAI-compatible LLM"), then optionally:
//	export OBOL_LLM_ENDPOINT=http://100.117.108.5:8000/v1
//	go test -tags integration -run TestIntegration_AgentContract -v -timeout 15m ./internal/agentcrd/
//
// ─────────────────────────────────────────────────────────────────────────────

// acObolRun executes the obol binary and returns combined stdout/stderr,
// fataling on failure. Mirrors internal/openclaw.obolRun; redeclared here
// (with an `ac` prefix) because the two packages do not share a test helper
// file and the non-tagged agent_test.go in this package must keep compiling.
func acObolRun(t *testing.T, cfg *config.Config, args ...string) string {
	t.Helper()
	out, err := acObolRunErr(cfg, args...)
	if err != nil {
		t.Fatalf("obol %v failed: %v", args, err)
	}
	return out
}

// acObolRunErr executes the obol binary and returns output + error (no fatal).
func acObolRunErr(cfg *config.Config, args ...string) (string, error) {
	obolBinary := filepath.Join(cfg.BinDir, "obol")
	cmd := exec.Command(obolBinary, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("obol %v: %w\n%s", args, err, buf.String())
	}
	return buf.String(), nil
}

// acRequireCluster skips when no k3d cluster is reachable. Same shape as
// internal/openclaw.requireCluster: kubeconfig presence + a live cluster-info
// probe through the obol-managed KUBECONFIG.
func acRequireCluster(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Load()
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		t.Skip("no kubeconfig found — cluster not running")
	}
	if _, err := acObolRunErr(cfg, "kubectl", "cluster-info"); err != nil {
		t.Skipf("cluster not reachable: %v", err)
	}
	return cfg
}

// acRequireAgentCRD skips when the obol.org Agent CRD is not installed (the
// reconciler that provisions sub-agent pods watches it). Mirrors the
// requireCRD shape used by internal/openclaw for ServiceOffers.
func acRequireAgentCRD(t *testing.T, cfg *config.Config) {
	t.Helper()
	out, err := acObolRunErr(cfg, "kubectl", "get", "crd", "agents.obol.org")
	if err != nil || !strings.Contains(out, "agents.obol.org") {
		t.Skip("Agent CRD (agents.obol.org) not installed — run: obol stack up")
	}
}

// TestIntegration_AgentContract_HonorsRenderedConstraints deploys a real CRD
// sub-agent through the canonical `obol agent new` path and verifies the Hermes
// image honors the streamlined-sub-agent contract introduced in PR #582.
func TestIntegration_AgentContract_HonorsRenderedConstraints(t *testing.T) {
	cfg := acRequireCluster(t)
	acRequireAgentCRD(t, cfg)

	// Unique-ish name so reruns don't collide with a stuck previous agent.
	// Lowercase + dashes only, to satisfy ValidateName / the CRD pattern.
	const name = "test-contract"
	ns := Namespace(name)

	// Always tear the agent down first so a leftover from a crashed run doesn't
	// make `obol agent new` reject creation, then again at the end.
	cleanup := func() {
		if _, err := acObolRunErr(cfg, "agent", "delete", "--force", name); err != nil {
			t.Logf("cleanup of agent %q failed (non-fatal): %v", name, err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	// Deploy via the real CLI. We deliberately leave --model unset so the
	// controller auto-pins the cluster's top-of-rank LiteLLM model at first
	// reconcile — that keeps the test independent of which exact model is
	// configured. The skills are real embedded skills so the obol-skills
	// external_dirs is genuinely non-empty (needed for the behavioral signal).
	t.Logf("deploying CRD sub-agent via: obol agent new %s --skills addresses,gas", name)
	acObolRun(t, cfg, "agent", "new", name, "--skills", "addresses,gas")

	// ── Check (1): the .no-bundled-skills marker exists on the host PVC path ──
	// SeedHostFiles (called by `obol agent new`) writes it; the cluster mounts
	// HostHomePath into the pod via the data PVC, so its presence here is what
	// Hermes sees at /data/.hermes/.no-bundled-skills inside the container.
	marker := HostNoBundledSkillsMarkerPath(cfg, name)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no-bundled-skills marker missing on host PVC path %s: %v", marker, err)
	}
	t.Logf("✓ marker present on host PVC: %s", marker)

	// Wait for the reconciler to create the Deployment and for the pod to come
	// up. The agent pod carries app.kubernetes.io/instance=<name> (see
	// serviceoffercontroller.agentLabels). The startup probe gives Hermes up to
	// ~2m before it would be reported ready, so allow a generous timeout.
	waitForAgentPodReady(t, cfg, ns, name)

	// ── Check (2): rendered hermes-config ConfigMap carries the capped knobs ──
	cfgYAML := getHermesConfigYAML(t, cfg, ns)
	t.Logf("rendered hermes-config config.yaml:\n%s", cfgYAML)
	assertHermesConfigCaps(t, cfgYAML)

	// ── Check (3): behavioral skip-bundled-skills signal from the running pod ──
	assertBundledSkillsSkippedInPod(t, cfg, ns)

	// ── Optional: exercise the agent so we prove it actually boots and serves
	// under the capped config. This needs a live LLM, so it is gated on
	// OBOL_LLM_ENDPOINT and skipped (not failed) when unset — consistent with
	// the rest of the integration suite. The structural checks above are the
	// load-bearing contract assertions; this is corroboration that the capped
	// config doesn't wedge startup.
	if os.Getenv("OBOL_LLM_ENDPOINT") == "" {
		t.Log("OBOL_LLM_ENDPOINT unset — skipping the live inference exercise " +
			"(structural marker/config/pod checks above already passed). " +
			"Set OBOL_LLM_ENDPOINT (e.g. a dev endpoint at http://100.117.108.5:8000/v1) " +
			"after pointing the stack at it to exercise the agent API.")
		return
	}
	assertAgentHealthEndpointServes(t, cfg, ns)
}

// waitForAgentPodReady blocks until the reconciler-created Hermes pod for the
// sub-agent reports Ready. Driven through `obol kubectl wait` so we reuse the
// obol-managed KUBECONFIG rather than constructing a client-go client.
func waitForAgentPodReady(t *testing.T, cfg *config.Config, ns, name string) {
	t.Helper()
	// First wait for the Deployment to exist — the CR is applied, but the
	// reconciler creates the workload asynchronously. A short poll avoids a
	// `kubectl wait` against a not-yet-created selector.
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := acObolRunErr(cfg, "kubectl", "get", "deploy", hermesDeploymentName, "-n", ns); err == nil {
			break
		}
		time.Sleep(3 * time.Second)
	}

	t.Logf("waiting for agent pod readiness in %s", ns)
	acObolRun(t, cfg, "kubectl",
		"wait", "--for=condition=ready", "pod",
		"-l", "app.kubernetes.io/instance="+name,
		"-n", ns,
		"--timeout=240s",
	)
}

// getHermesConfigYAML returns the rendered config.yaml from the agent's
// hermes-config ConfigMap. Uses jsonpath to pull just the data key, matching
// the kubectl-helper style used elsewhere in the integration suite (e.g.
// internal/openclaw.getERPCConfigYAML).
func getHermesConfigYAML(t *testing.T, cfg *config.Config, ns string) string {
	t.Helper()
	out := acObolRun(t, cfg, "kubectl", "get", "configmap", hermesConfigMapName, "-n", ns,
		"-o", `jsonpath={.data.config\.yaml}`)
	if strings.TrimSpace(out) == "" {
		t.Fatalf("hermes-config ConfigMap in %s has empty config.yaml", ns)
	}
	return out
}

// assertHermesConfigCaps verifies the streamlined-sub-agent caps are present in
// the rendered config. These strings must match renderHermesConfig's output in
// internal/serviceoffercontroller/agent_render.go.
func assertHermesConfigCaps(t *testing.T, cfgYAML string) {
	t.Helper()
	for _, want := range []string{
		"lifetime_seconds: 90",
		"max_turns: 30",
		"reasoning_effort: low",
		"disabled_toolsets:",
	} {
		if !strings.Contains(cfgYAML, want) {
			t.Errorf("rendered hermes-config missing %q:\n%s", want, cfgYAML)
		}
	}
	// disabled_toolsets must actually drop both memory and web. They render as
	// YAML list items, so check for the "- memory" / "- web" entries rather
	// than a bare substring (which could match a comment or model name).
	for _, toolset := range []string{"memory", "web"} {
		if !containsListItem(cfgYAML, toolset) {
			t.Errorf("disabled_toolsets does not include %q as a list item:\n%s", toolset, cfgYAML)
		}
	}
}

// containsListItem reports whether the YAML body has a `- <item>` list entry
// (any indentation). Used to assert disabled_toolsets membership precisely.
func containsListItem(yamlBody, item string) bool {
	for _, line := range strings.Split(yamlBody, "\n") {
		if strings.TrimSpace(line) == "- "+item {
			return true
		}
	}
	return false
}

// assertBundledSkillsSkippedInPod is the behavioral proof that the marker was
// honored.
//
// We deliberately do NOT grep the Hermes pod logs for a "skipping bundled
// skills" / "no-bundled-skills" line: that string is owned by the external
// nousresearch/hermes-agent image, is not referenced anywhere in this repo, and
// could change wording or log level across image bumps without our knowledge —
// grepping for it would be a brittle, unverifiable contract. Earlier
// verification notes only ever treated the log line as an eyeball aid, not
// an assertion.
//
// The reliable, image-version-independent signal is the pod's on-disk skills
// layout: when the marker is honored, Hermes seeds NONE of its ~80 bundled
// skills into its native skills dir ($HERMES_HOME/skills), while the
// operator-chosen subset we layer in via OBOL_SKILLS_DIR
// (/data/.hermes/obol-skills, the external_dirs entry) is present and populated.
// So we assert both halves:
//
//	(a) /data/.hermes/obol-skills is present and non-empty (our subset survived);
//	(b) the native bundled-skills dir /data/.hermes/skills is absent or empty
//	    (the marker stopped bundled seeding).
//
// All exec is via `obol kubectl exec` into the "hermes" container (see
// agentPodSpec), the same non-interactive pattern internal/openclaw uses.
func assertBundledSkillsSkippedInPod(t *testing.T, cfg *config.Config, ns string) {
	t.Helper()

	// (precondition) The pod must actually SEE the marker on its mounted data
	// volume. Asserting this ties the "bundled dir empty" result in (b) to the
	// contract INPUT: without it, an empty bundled dir could equally mean a
	// future image simply uses a different bundled-skills path. Marker-visible
	// in-pod AND bundled-dir-empty together prove Hermes saw the marker and
	// honored it — not that we guessed the wrong directory name.
	podMarker := podHermesHome + "/.no-bundled-skills"
	if mOut, err := execInAgentPodErr(cfg, ns, "ls", "-la", podMarker); err != nil {
		t.Fatalf("the .no-bundled-skills marker is not visible inside the pod at %s "+
			"(the data PVC mount did not surface it): %v\n%s", podMarker, err, mOut)
	}
	t.Logf("✓ marker visible inside pod at %s", podMarker)

	// (a) obol-skills external_dirs is present and non-empty. `ls -A` omits
	// . and .., so any output means the dir has entries (our seeded skills).
	obolSkills := podHermesHome + "/" + obolSkillsExternalDir
	extOut, err := execInAgentPodErr(cfg, ns, "ls", "-A", obolSkills)
	if err != nil {
		t.Fatalf("obol-skills external dir %s not readable in pod: %v\n%s", obolSkills, err, extOut)
	}
	if strings.TrimSpace(extOut) == "" {
		t.Fatalf("obol-skills external dir %s is empty in pod — the operator skill subset did not land", obolSkills)
	}
	// Sanity: the skills we asked for should be among the entries.
	for _, skill := range []string{"addresses", "gas"} {
		if !strings.Contains(extOut, skill) {
			t.Errorf("expected seeded skill %q under %s; got:\n%s", skill, obolSkills, extOut)
		}
	}
	t.Logf("✓ obol-skills external_dirs populated in pod (%s):\n%s", obolSkills, strings.TrimSpace(extOut))

	// (b) Native bundled-skills dir is absent OR empty. We test for non-empty
	// rather than non-existent because a future Hermes image could create the
	// dir but leave it empty when the marker is set; both mean "no bundled
	// skills were seeded". Run `ls -A` and tolerate a non-zero exit (dir
	// missing). Any non-empty output is a contract violation: the marker was
	// not honored and Hermes seeded its bundled skills.
	bundled := podHermesHome + "/skills"
	bundledOut, _ := execInAgentPodErr(cfg, ns, "ls", "-A", bundled)
	bundledOut = strings.TrimSpace(bundledOut)
	// kubectl/sh may surface a "No such file or directory" message on stderr,
	// which our combined-output helper folds in; treat that as "absent".
	if bundledOut != "" && !strings.Contains(bundledOut, "No such file") {
		t.Fatalf("native bundled-skills dir %s is non-empty in pod — Hermes did NOT honor "+
			".no-bundled-skills and seeded bundled skills:\n%s", bundled, bundledOut)
	}
	t.Logf("✓ native bundled-skills dir %s absent/empty in pod — bundled seeding skipped", bundled)
}

// assertAgentHealthEndpointServes proves the agent pod actually serves its API
// under the capped config (it has not wedged on startup). We hit the unauth
// health path from inside the cluster via a one-shot busybox-style exec in the
// hermes container itself, since the per-agent route is x402-gated externally.
// Best-effort: any 2xx/expected health response is enough.
func assertAgentHealthEndpointServes(t *testing.T, cfg *config.Config, ns string) {
	t.Helper()
	// The hermes container is the only guaranteed exec target; it ships a
	// Python runtime (it's a Hermes image). Use stdlib urllib to GET the
	// in-pod health endpoint so we don't depend on curl/wget being present.
	script := fmt.Sprintf(`import urllib.request,sys
try:
    with urllib.request.urlopen("http://127.0.0.1:%d%s", timeout=10) as r:
        sys.stdout.write("HTTP %%d" %% r.status)
except Exception as e:
    sys.stdout.write("ERR %%s" %% e)
`, hermesContainerPort, hermesHealthPath)
	out, err := execInAgentPodErr(cfg, ns, "python3", "-c", script)
	t.Logf("agent in-pod health probe result: %q (err=%v)", strings.TrimSpace(out), err)
	if err != nil || !strings.Contains(out, "HTTP 200") {
		t.Errorf("agent health endpoint did not serve HTTP 200 under capped config: out=%q err=%v", out, err)
	}
}

// execInAgentPodErr runs a command in the "hermes" container of the sub-agent
// pod via `obol kubectl exec`, returning combined output + error. Non-fatal so
// callers can tolerate expected failures (e.g. ls on an absent dir).
func execInAgentPodErr(cfg *config.Config, ns string, args ...string) (string, error) {
	full := append([]string{
		"kubectl", "exec", "-i",
		"-n", ns, "deploy/" + hermesDeploymentName,
		"-c", hermesContainerName, "--",
	}, args...)
	return acObolRunErr(cfg, full...)
}

// Constants mirroring the in-cluster render in
// internal/serviceoffercontroller/agent_render.go. Kept local (and prefixed
// where they would otherwise be generic) so this test reads as an external
// contract: if the render changes these, the assertions here should be the
// thing that has to change too.
const (
	hermesDeploymentName  = "hermes"
	hermesContainerName   = "hermes"
	hermesConfigMapName   = "hermes-config"
	hermesContainerPort   = 8642 // hermesPort in agent_render.go
	hermesHealthPath      = "/health"
	podHermesHome         = "/data/.hermes" // HERMES_HOME inside the pod
	obolSkillsExternalDir = "obol-skills"   // matches OBOL_SKILLS_DIR / external_dirs
)
