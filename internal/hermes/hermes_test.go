package hermes

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
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

// TestGenerateConfig_PrimaryIsRoundTrippable guards the LiteLLM model_name
// contract end-to-end: whatever string the agent's `model.default` is set to
// MUST match a `model_name` entry in the LiteLLM ConfigMap byte-for-byte,
// because Hermes will pass it back as the `model` field on every
// chat-completion call. Stripping anywhere on this path causes the agent to
// call LiteLLM with a key that no longer matches a registered route — the
// flow-14 / ca820c9 regression.
func TestGenerateConfig_PrimaryIsRoundTrippable(t *testing.T) {
	cases := []struct {
		name    string
		primary string
	}{
		{"bare ollama tag", "llama3.1:8b"},
		{"bare claude id", "claude-opus-4-7"},
		{"bare openai id", "gpt-5.4"},
		// Wildcard-expanded entries can carry the provider prefix; the
		// agent must still send back the exact string LiteLLM published.
		{"provider-prefixed", "anthropic/claude-3-5-sonnet-latest"},
		// Custom endpoints write `model_name: <bare>` after the contract
		// fix; this case guards that the agent picks up that bare name
		// without re-namespacing.
		{"custom endpoint bare", "qwen36-fast"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := generateConfig(testConfig(t), tc.primary)
			if err != nil {
				t.Fatalf("generateConfig: %v", err)
			}
			var cfg map[string]any
			if err := yaml.Unmarshal(raw, &cfg); err != nil {
				t.Fatalf("yaml.Unmarshal: %v", err)
			}
			modelCfg, ok := cfg["model"].(map[string]any)
			if !ok {
				t.Fatalf("model config missing")
			}
			if got := modelCfg["default"]; got != tc.primary {
				t.Fatalf("model.default = %q, want %q (round-trip mismatch)", got, tc.primary)
			}
		})
	}
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
		"fsGroupChangePolicy: OnRootMismatch",
		"init-hermes-data",
		`Hermes binary missing from image: /opt/hermes/.venv/bin/hermes`,
		`Hermes image is missing required extras: web,messaging,mcp,pty,cli,acp,google`,
		`import fastapi, uvicorn, telegram, mcp, ptyprocess, simple_term_menu, googleapiclient`,
		`PRAGMA quick_check`,
		`state-db-corrupt-$ts`,
		`- "/opt/hermes/.venv/bin/hermes"`,
		`- "hermes-obol-agent.obol.stack"`,
		`- "obol-agent.obol.stack"`,
		"name: hermes-dashboard",
		"name: GATEWAY_HEALTH_URL",
	} {
		if !strings.Contains(values, needle) {
			t.Fatalf("generateValues() missing %q:\n%s", needle, values)
		}
	}

	for _, banned := range []string{
		"bootstrap-hermes-install",
		"git clone",
		"uv pip install",
		"/data/.hermes/hermes-agent",
		"init-hermes-perms",
		"chown -R 10000:10000 /data",
	} {
		if strings.Contains(values, banned) {
			t.Fatalf("generateValues() contains banned fragment %q:\n%s", banned, values)
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
		"/opt/hermes/.venv/bin/hermes",
		"skills",
		"audit",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hermesExecArgs() = %#v, want %#v", got, want)
	}
}

func TestResolveCLIInvocation_DefaultsToObolAgent(t *testing.T) {
	cfg := testConfig(t)
	mkdirInstance(t, cfg, agentruntime.DefaultInstanceID)
	mkdirInstance(t, cfg, "research")

	id, args, err := ResolveCLIInvocation(cfg, []string{"skills", "list"})
	if err != nil {
		t.Fatalf("ResolveCLIInvocation() error = %v", err)
	}
	if id != agentruntime.DefaultInstanceID {
		t.Fatalf("id = %q, want %q", id, agentruntime.DefaultInstanceID)
	}
	if !reflect.DeepEqual(args, []string{"skills", "list"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveCLIInvocation_UsesExplicitAgent(t *testing.T) {
	cfg := testConfig(t)
	mkdirInstance(t, cfg, agentruntime.DefaultInstanceID)
	mkdirInstance(t, cfg, "research")

	id, args, err := ResolveCLIInvocation(cfg, []string{"--agent", "research", "config", "show"})
	if err != nil {
		t.Fatalf("ResolveCLIInvocation() error = %v", err)
	}
	if id != "research" {
		t.Fatalf("id = %q, want research", id)
	}
	if !reflect.DeepEqual(args, []string{"config", "show"}) {
		t.Fatalf("args = %#v", args)
	}
}

func TestResolveCLIInvocation(t *testing.T) {
	tests := []struct {
		name      string
		instances []string
		input     []string
		wantID    string
		wantArgs  []string
		wantErr   string
	}{
		{
			name:    "no instances",
			input:   []string{"version"},
			wantErr: "no Hermes instances found",
		},
		{
			name:      "single instance fallback",
			instances: []string{"solo"},
			input:     []string{"version"},
			wantID:    "solo",
			wantArgs:  []string{"version"},
		},
		{
			name:      "multiple non-default instances require selector",
			instances: []string{"research", "ops"},
			input:     []string{"version"},
			wantErr:   "multiple Hermes instances found",
		},
		{
			name:      "explicit agent equals syntax",
			instances: []string{agentruntime.DefaultInstanceID, "research"},
			input:     []string{"--agent=research", "config", "show"},
			wantID:    "research",
			wantArgs:  []string{"config", "show"},
		},
		{
			name:      "separator preserves native flags",
			instances: []string{agentruntime.DefaultInstanceID},
			input:     []string{"--agent", agentruntime.DefaultInstanceID, "--", "--help"},
			wantID:    agentruntime.DefaultInstanceID,
			wantArgs:  []string{"--help"},
		},
		{
			name:      "missing agent value",
			instances: []string{agentruntime.DefaultInstanceID},
			input:     []string{"--agent"},
			wantErr:   "--agent requires an instance name",
		},
		{
			name:      "duplicate agent selector",
			instances: []string{agentruntime.DefaultInstanceID, "research"},
			input:     []string{"--agent", agentruntime.DefaultInstanceID, "--agent=research", "version"},
			wantErr:   "--agent specified multiple times",
		},
		{
			name:      "unknown explicit agent",
			instances: []string{agentruntime.DefaultInstanceID},
			input:     []string{"--agent", "missing", "version"},
			wantErr:   `Hermes instance "missing" not found`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(t)
			for _, id := range tt.instances {
				mkdirInstance(t, cfg, id)
			}

			gotID, gotArgs, err := ResolveCLIInvocation(cfg, tt.input)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveCLIInvocation() error = %v", err)
			}
			if gotID != tt.wantID {
				t.Fatalf("id = %q, want %q", gotID, tt.wantID)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Fatalf("args = %#v, want %#v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func mkdirInstance(t *testing.T, cfg *config.Config, id string) {
	t.Helper()
	if err := os.MkdirAll(DeploymentPath(cfg, id), 0o755); err != nil {
		t.Fatalf("create Hermes instance %q: %v", id, err)
	}
}
