package serviceoffercontroller

import (
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

	wantValues := map[string]string{
		"API_SERVER_MODEL_NAME": "qwen3.5:9b",
		"AGENT_NAMESPACE":       "agent-quant",
		"AGENT_WALLET_ADDRESS":  agent.Status.WalletAddress,
		"OBOL_SKILLS_DIR":       "/data/.hermes/obol-skills",
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
