package serviceoffercontroller

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func TestAgentManifests_KindCoverage(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"
	agent.Spec = monetizeapi.AgentSpec{
		Runtime: "hermes",
		Model:   "qwen3.5:9b",
		Skills:  []string{"addresses", "gas"},
	}

	out, err := agentManifests(agent, "litellm-key", "api-key")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}

	wantKinds := []string{
		"Namespace",
		"ServiceAccount",
		"PersistentVolumeClaim",
		"ConfigMap",
		"Secret",
		"Deployment",
		"Service",
		"NetworkPolicy",
	}
	got := make([]string, 0, len(out))
	for _, m := range out {
		got = append(got, m.GetKind())
	}
	if !equalSlice(got, wantKinds) {
		t.Errorf("kind order = %v, want %v", got, wantKinds)
	}

	// Every manifest except the cluster-scoped Namespace must land in the
	// agent's namespace. Drop in code review if this ever needs to change
	// — splitting an agent's resources across namespaces would break the
	// data PVC mapping.
	for _, m := range out {
		if m.GetKind() == "Namespace" {
			if m.GetNamespace() != "" {
				t.Errorf("Namespace must not have metadata.namespace, got %q", m.GetNamespace())
			}
			continue
		}
		if m.GetNamespace() != agent.Namespace {
			t.Errorf("%s in wrong ns: got %q want %q", m.GetKind(), m.GetNamespace(), agent.Namespace)
		}
	}
}

func TestAgentManifests_RejectsMissingPrereqs(t *testing.T) {
	cases := []struct {
		name  string
		agent *monetizeapi.Agent
	}{
		{"nil", nil},
		{"empty namespace", &monetizeapi.Agent{}},
		{"no model", func() *monetizeapi.Agent {
			a := &monetizeapi.Agent{}
			a.Name = "x"
			a.Namespace = "agent-x"
			return a
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := agentManifests(tc.agent, "k", "k"); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

func TestAgentManifests_DeploymentEnvCarriesContext(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"
	agent.Spec = monetizeapi.AgentSpec{Model: "qwen3.5:9b"}
	agent.Status.WalletAddress = "0xabcdef0123456789abcdef0123456789abcdef01"

	out, err := agentManifests(agent, "litellm", "api")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	var dep map[string]any
	for _, m := range out {
		if m.GetKind() == "Deployment" {
			dep = m.UnstructuredContent()
			break
		}
	}
	if dep == nil {
		t.Fatal("Deployment manifest missing")
	}

	containers := dep["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)
	c := containers[0].(map[string]any)
	envs := c["env"].([]any)
	envFrom := c["envFrom"].([]any)

	wantValues := map[string]string{
		"API_SERVER_MODEL_NAME": "qwen3.5:9b",
		"AGENT_NAMESPACE":       "agent-quant",
		"AGENT_WALLET_ADDRESS":  agent.Status.WalletAddress,
		"OBOL_SKILLS_DIR":       "/data/.hermes/obol-skills",
		// Must track HERMES_HOME: the upstream image bakes /opt/data and
		// denies all file-tool writes outside the safe root.
		"HERMES_WRITE_SAFE_ROOT": "/data/.hermes:/tmp",
	}
	got := make(map[string]string)
	for _, e := range envs {
		em := e.(map[string]any)
		if v, ok := em["value"].(string); ok {
			got[em["name"].(string)] = v
		}
	}
	for k, want := range wantValues {
		if got[k] != want {
			t.Errorf("env %s = %q, want %q", k, got[k], want)
		}
	}

	if len(envFrom) != 1 {
		t.Fatalf("envFrom length = %d, want 1", len(envFrom))
	}
	secretRef := envFrom[0].(map[string]any)["secretRef"].(map[string]any)
	if secretRef["name"] != hermesEnvSecret {
		t.Errorf("envFrom secret = %v, want %s", secretRef["name"], hermesEnvSecret)
	}
	if secretRef["optional"] != true {
		t.Errorf("envFrom secret optional = %v, want true", secretRef["optional"])
	}
}

func TestAgentManifests_DeploymentUsesFSGroup(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"
	agent.Spec = monetizeapi.AgentSpec{Model: "qwen3.5:9b"}

	out, err := agentManifests(agent, "litellm", "api")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	var dep map[string]any
	for _, m := range out {
		if m.GetKind() == "Deployment" {
			dep = m.UnstructuredContent()
			break
		}
	}
	if dep == nil {
		t.Fatal("Deployment manifest missing")
	}

	podSpec := dep["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	securityContext := podSpec["securityContext"].(map[string]any)
	if securityContext["runAsNonRoot"] != true {
		t.Fatalf("runAsNonRoot = %v, want true", securityContext["runAsNonRoot"])
	}
	if securityContext["runAsUser"] != int64(hermesContainerUID) {
		t.Fatalf("runAsUser = %v, want %d", securityContext["runAsUser"], hermesContainerUID)
	}
	if securityContext["runAsGroup"] != int64(hermesContainerGID) {
		t.Fatalf("runAsGroup = %v, want %d", securityContext["runAsGroup"], hermesContainerGID)
	}
	if securityContext["fsGroup"] != int64(hermesContainerGID) {
		t.Fatalf("fsGroup = %v, want %d", securityContext["fsGroup"], hermesContainerGID)
	}
	if securityContext["fsGroupChangePolicy"] != "Always" {
		t.Fatalf("fsGroupChangePolicy = %v, want Always", securityContext["fsGroupChangePolicy"])
	}
	if strat := dep["spec"].(map[string]any)["strategy"].(map[string]any)["type"]; strat != "Recreate" {
		t.Fatalf("strategy.type = %v, want Recreate", strat)
	}
	annotations := dep["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations["checksum/hermes-config"] == "" {
		t.Fatalf("checksum/hermes-config annotation missing")
	}
}

func TestAgentManifests_ConfigChecksumChangesWithModel(t *testing.T) {
	first := &monetizeapi.Agent{}
	first.Name = "quant"
	first.Namespace = "agent-quant"
	first.Spec = monetizeapi.AgentSpec{Model: "qwen3.5:9b"}

	second := &monetizeapi.Agent{}
	second.Name = "quant"
	second.Namespace = "agent-quant"
	second.Spec = monetizeapi.AgentSpec{Model: "qwen3.5:14b"}

	firstChecksum := agentConfigChecksum(t, first)
	secondChecksum := agentConfigChecksum(t, second)
	if firstChecksum == "" || secondChecksum == "" {
		t.Fatalf("missing checksum(s): first=%q second=%q", firstChecksum, secondChecksum)
	}
	if firstChecksum == secondChecksum {
		t.Fatalf("checksum/hermes-config did not change when rendered config changed: %s", firstChecksum)
	}
}

func TestAgentManifests_ConfigSeedWritesWritableRuntimeConfig(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"
	agent.Spec = monetizeapi.AgentSpec{Model: "qwen3.5:9b"}

	out, err := agentManifests(agent, "litellm", "api")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	var dep map[string]any
	for _, m := range out {
		if m.GetKind() == "Deployment" {
			dep = m.UnstructuredContent()
			break
		}
	}
	if dep == nil {
		t.Fatal("Deployment manifest missing")
	}

	podSpec := dep["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	inits := podSpec["initContainers"].([]any)
	if len(inits) != 2 {
		t.Fatalf("initContainers length = %d, want 2 (config-seed + profile-seed)", len(inits))
	}
	configSeed := inits[0].(map[string]any)
	if configSeed["name"] != "config-seed" {
		t.Fatalf("init[0] name = %v, want config-seed", configSeed["name"])
	}
	script := configSeed["args"].([]any)[0].(string)
	for _, must := range []string{
		"cp /config-seed/config.yaml /data/.hermes/config.yaml",
		"chmod 600 /data/.hermes/config.yaml",
	} {
		if !strings.Contains(script, must) {
			t.Errorf("config seed script missing %q\n---\n%s", must, script)
		}
	}

	containers := podSpec["containers"].([]any)
	mounts := containers[0].(map[string]any)["volumeMounts"].([]any)
	for _, mount := range mounts {
		m := mount.(map[string]any)
		if m["mountPath"] == "/data/.hermes/config.yaml" {
			t.Fatalf("runtime config must not be mounted from ConfigMap: %#v", m)
		}
	}
}

func TestAgentManifests_ProfileSeedInitContainer(t *testing.T) {
	agent := &monetizeapi.Agent{}
	agent.Name = "quant"
	agent.Namespace = "agent-quant"
	agent.Spec = monetizeapi.AgentSpec{Model: "qwen3.5:9b"}

	out, err := agentManifests(agent, "litellm", "api")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	var dep map[string]any
	for _, m := range out {
		if m.GetKind() == "Deployment" {
			dep = m.UnstructuredContent()
			break
		}
	}
	if dep == nil {
		t.Fatal("Deployment manifest missing")
	}

	podSpec := dep["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	inits := podSpec["initContainers"].([]any)
	if len(inits) != 2 {
		t.Fatalf("initContainers length = %d, want 2 (config-seed + profile-seed)", len(inits))
	}
	init := inits[1].(map[string]any)
	if init["name"] != "profile-seed" {
		t.Errorf("init[1] name = %v, want profile-seed", init["name"])
	}
	args := init["args"].([]any)
	if len(args) != 1 {
		t.Fatalf("init args length = %d, want 1", len(args))
	}
	script := args[0].(string)
	for _, must := range []string{
		"/profile-seed/profile.tar.gz",
		".obol-profile-seed-imported",
		"/data/.hermes/SOUL.md",
		"/data/.hermes/logs",
		"cp -R",
	} {
		if !strings.Contains(script, must) {
			t.Errorf("profile seed script missing %q\n---\n%s", must, script)
		}
	}

	volumes := podSpec["volumes"].([]any)
	var profileSeed map[string]any
	for _, v := range volumes {
		vm := v.(map[string]any)
		if vm["name"] == "profile-seed" {
			profileSeed = vm
			break
		}
	}
	if profileSeed == nil {
		t.Fatal("profile-seed volume missing")
	}
	secret := profileSeed["secret"].(map[string]any)
	if secret["secretName"] != hermesProfileSeed {
		t.Errorf("profile seed secretName = %v, want %s", secret["secretName"], hermesProfileSeed)
	}
	if secret["optional"] != true {
		t.Errorf("profile seed optional = %v, want true", secret["optional"])
	}
}

func agentConfigChecksum(t *testing.T, agent *monetizeapi.Agent) string {
	t.Helper()
	out, err := agentManifests(agent, "litellm", "api")
	if err != nil {
		t.Fatalf("agentManifests: %v", err)
	}
	for _, m := range out {
		if m.GetKind() != "Deployment" {
			continue
		}
		dep := m.UnstructuredContent()
		annotations := dep["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
		got, _ := annotations["checksum/hermes-config"].(string)
		return got
	}
	t.Fatal("Deployment manifest missing")
	return ""
}

func TestRenderHermesConfig_HasModelAndSkillsDir(t *testing.T) {
	cfg := renderHermesConfig("qwen3.5:9b", "lit-key")
	for _, must := range []string{
		`default: "qwen3.5:9b"`,
		`api_key: "lit-key"`,
		`http://litellm.llm.svc.cluster.local:4000/v1`,
		`/data/.hermes/obol-skills`,
	} {
		if !strings.Contains(cfg, must) {
			t.Errorf("hermes config missing %q\n---\n%s", must, cfg)
		}
	}
}

// Sub-agents share LiteLLM with the master, so we cannot cap output tokens
// per-model. Instead, every CRD-rendered agent runs under tighter Hermes
// knobs so a single sale stays inside the 100s Cloudflare free-tunnel
// window. If any of these drift it should fail loudly.
func TestRenderHermesConfig_SubAgentConstraints(t *testing.T) {
	cfg := renderHermesConfig("qwen3.5:9b", "lit-key")
	for _, must := range []string{
		`timeout: 80`,
		`lifetime_seconds: 90`,
		`max_turns: 30`,
		`reasoning_effort: low`,
		`disabled_toolsets:`,
		`- memory`,
		`- web`,
		// Paid sub-agents are served headless with no interactive channel for
		// the buyer to answer an approval prompt, so the dangerous-command /
		// execute_code gate must be off (HARDLINE floor still applies). Quoted
		// so YAML keeps it the string "off", not boolean false.
		`approvals:`,
		`mode: "off"`,
	} {
		if !strings.Contains(cfg, must) {
			t.Errorf("hermes config missing sub-agent constraint %q\n---\n%s", must, cfg)
		}
	}

	// A per-operation timeout larger than the whole session lifetime is
	// incoherent: a single tool/command could nominally outlive the session
	// the Cloudflare free tunnel caps at 100s. Parse both out of the rendered
	// config and assert timeout <= lifetime_seconds.
	timeout := parseTerminalInt(t, cfg, "timeout")
	lifetime := parseTerminalInt(t, cfg, "lifetime_seconds")
	if timeout > lifetime {
		t.Errorf("terminal.timeout (%d) must be <= lifetime_seconds (%d)\n---\n%s", timeout, lifetime, cfg)
	}
}

// parseTerminalInt extracts the integer value of a `key: <int>` line from the
// rendered Hermes config. Fails the test if the key is absent or unparsable.
func parseTerminalInt(t *testing.T, cfg, key string) int {
	t.Helper()
	for _, line := range strings.Split(cfg, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
		n, err := strconv.Atoi(val)
		if err != nil {
			t.Fatalf("parsing %q value %q: %v", key, val, err)
		}
		return n
	}
	t.Fatalf("config missing %q line\n---\n%s", key, cfg)
	return 0
}

func TestGenerateAPIKey_HexAndUnique(t *testing.T) {
	a, err := generateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("generateAPIKey must return distinct keys")
	}
	if len(a) != 64 {
		t.Errorf("API key length = %d, want 64 hex chars (32 bytes)", len(a))
	}
}
