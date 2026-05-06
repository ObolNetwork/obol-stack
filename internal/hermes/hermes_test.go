package hermes

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".stack-id"), []byte("test-cluster"), 0o644); err != nil {
		t.Fatalf("write .stack-id: %v", err)
	}

	return &config.Config{ConfigDir: dir, DataDir: dir, BinDir: dir}
}

func TestGenerateConfig_UsesLiteLLMCustomProvider(t *testing.T) {
	raw, err := generateConfig(testConfig(t), "gpt-5.2")
	if err != nil {
		t.Fatalf("generateConfig() error = %v", err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}

	modelCfg, ok := cfg["model"].(map[string]any)
	if !ok {
		t.Fatalf("model config missing or wrong type: %#v", cfg["model"])
	}

	if got := modelCfg["default"]; got != "gpt-5.2" {
		t.Fatalf("model.default = %#v, want %q", got, "gpt-5.2")
	}

	if got := modelCfg["provider"]; got != "custom" {
		t.Fatalf("model.provider = %#v, want %q", got, "custom")
	}

	if got := modelCfg["base_url"]; got != "http://litellm.llm.svc.cluster.local:4000/v1" {
		t.Fatalf("model.base_url = %#v", got)
	}

	if got := modelCfg["api_key"]; got != "sk-obol-test-cluster" {
		t.Fatalf("model.api_key = %#v, want stack-derived LiteLLM key", got)
	}

	terminalCfg, ok := cfg["terminal"].(map[string]any)
	if !ok {
		t.Fatalf("terminal config missing or wrong type: %#v", cfg["terminal"])
	}

	if got := terminalCfg["cwd"]; got != "/data/.hermes/workspace" {
		t.Fatalf("terminal.cwd = %#v, want %q", got, "/data/.hermes/workspace")
	}

	skillsCfg, ok := cfg["skills"].(map[string]any)
	if !ok {
		t.Fatalf("skills config missing or wrong type: %#v", cfg["skills"])
	}
	if got := fmt.Sprint(skillsCfg["external_dirs"]); !strings.Contains(got, "/data/.hermes/obol-skills") {
		t.Fatalf("skills.external_dirs = %#v, want Obol external skills dir", skillsCfg["external_dirs"])
	}
}

func TestGenerateValues_UsesHermesNativeNames(t *testing.T) {
	values := generateValues(
		"hermes-obol-agent",
		"hermes-obol-agent.obol.stack",
		"obol-agent.obol.stack",
		"https://agent.example.com",
		"secret-token",
		"gpt-5.2",
		[]byte("model:\n  default: gpt-5.2\n"),
	)

	for _, needle := range []string{
		"name: hermes",
		"name: hermes-config",
		"name: hermes-data",
		`API_SERVER_KEY: "secret-token"`,
		`value: "https://agent.example.com"`,
		"AGENT_NAMESPACE",
		`value: "hermes-obol-agent"`,
		"OBOL_SKILLS_DIR",
		"/data/.hermes/obol-skills",
		"containerPort: 8642",
		"containerPort: 9119",
		"init-hermes-data",
		"bootstrap-hermes-install",
		`install_dir="/data/.hermes/hermes-agent"`,
		`repo_url="https://github.com/NousResearch/hermes-agent.git"`,
		"uv venv --python python3 --system-site-packages venv",
		`import fastapi, uvicorn`,
		`uv pip install -e ".[web]"`,
		`PRAGMA quick_check`,
		`state-db-corrupt-$ts`,
		`- "/data/.hermes/hermes-agent/venv/bin/hermes"`,
		`- "hermes-obol-agent.obol.stack"`,
		`- "obol-agent.obol.stack"`,
		"name: hermes-dashboard",
		"name: GATEWAY_HEALTH_URL",
	} {
		if !strings.Contains(values, needle) {
			t.Fatalf("generateValues() missing %q:\n%s", needle, values)
		}
	}

	var parsed any
	if err := yaml.Unmarshal([]byte(values), &parsed); err != nil {
		t.Fatalf("generateValues() produced invalid YAML: %v\n%s", err, values)
	}
}

func TestDashboardHostname_UsesDefaultAgentHostAndHermesUIHostForNamedInstances(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{
			id:   "obol-agent",
			want: "obol-agent.obol.stack",
		},
		{
			id:   "research-agent",
			want: "hermes-research-agent-ui.obol.stack",
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := dashboardHostname(tt.id); got != tt.want {
				t.Fatalf("dashboardHostname(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestHermesExecArgs_UsesNativeHermesBinary(t *testing.T) {
	got := hermesExecArgs("hermes-obol-agent", []string{"skills", "audit"}, false)
	want := []string{
		"exec", "-i",
		"-c", "hermes",
		"-n", "hermes-obol-agent",
		"deploy/hermes",
		"--",
		"/data/.hermes/hermes-agent/venv/bin/hermes",
		"skills",
		"audit",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hermesExecArgs() = %#v, want %#v", got, want)
	}
}
