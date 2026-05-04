package main

import (
	"os"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestAgentCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := agentCommand(cfg)

	expected := map[string]bool{
		"init":   false,
		"new":    false,
		"sync":   false,
		"setup":  false,
		"auth":   false,
		"list":   false,
		"delete": false,
		"wallet": false,
	}

	for _, sub := range cmd.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing agent subcommand %q", name)
		}
	}
}

func TestAgentNewCommand_DefaultsToHermes(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := agentCommand(cfg)
	newCmd := findSubcommand(t, cmd, "new")
	flags := flagMap(newCmd)

	assertStringDefault(t, flags, "runtime", "hermes")
	requireFlags(t, flags, "id", "force", "no-sync")
}

func TestAgentWalletCommand_Structure(t *testing.T) {
	cfg := newTestConfig(t)
	cmd := agentCommand(cfg)
	wallet := findSubcommand(t, cmd, "wallet")

	expected := map[string]bool{
		"address": false,
		"list":    false,
		"backup":  false,
		"restore": false,
	}

	for _, sub := range wallet.Commands {
		if _, ok := expected[sub.Name]; ok {
			expected[sub.Name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("missing wallet subcommand %q", name)
		}
	}
}

func TestAgentWalletCommand_UsageIsRuntimeNeutral(t *testing.T) {
	cfg := newTestConfig(t)
	wallet := findSubcommand(t, agentCommand(cfg), "wallet")

	for _, name := range []string{"backup", "restore"} {
		t.Run(name, func(t *testing.T) {
			sub := findSubcommand(t, wallet, name)
			if strings.Contains(sub.Usage, "OpenClaw") {
				t.Fatalf("%s usage still says OpenClaw-only: %q", name, sub.Usage)
			}
			if !strings.Contains(sub.Usage, "agent instance") {
				t.Fatalf("%s usage = %q, want generic agent instance wording", name, sub.Usage)
			}
		})
	}
}

func TestResolveAgentTarget(t *testing.T) {
	tests := []struct {
		name        string
		instances   []agentTarget
		runtimeFlag string
		args        []string
		want        agentTarget
		wantErr     string
	}{
		{
			name:    "no instances",
			wantErr: "no agent instances found",
		},
		{
			name: "prefers default Hermes when present",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
				{Runtime: agentruntime.Hermes, ID: "research"},
				{Runtime: agentruntime.OpenClaw, ID: "legacy"},
			},
			want: agentTarget{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
		},
		{
			name: "falls back to single OpenClaw instance",
			instances: []agentTarget{
				{Runtime: agentruntime.OpenClaw, ID: "legacy"},
			},
			want: agentTarget{Runtime: agentruntime.OpenClaw, ID: "legacy"},
		},
		{
			name: "resolves explicit runtime default Hermes",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
				{Runtime: agentruntime.Hermes, ID: "research"},
			},
			runtimeFlag: "hermes",
			want:        agentTarget{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
		},
		{
			name: "resolves explicit runtime and instance",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
				{Runtime: agentruntime.Hermes, ID: "research"},
			},
			runtimeFlag: "hermes",
			args:        []string{"research"},
			want:        agentTarget{Runtime: agentruntime.Hermes, ID: "research"},
		},
		{
			name: "resolves openclaw by instance name without runtime",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
				{Runtime: agentruntime.OpenClaw, ID: "legacy"},
			},
			args: []string{"legacy"},
			want: agentTarget{Runtime: agentruntime.OpenClaw, ID: "legacy"},
		},
		{
			name: "errors on same id across runtimes",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: "shared"},
				{Runtime: agentruntime.OpenClaw, ID: "shared"},
			},
			args:    []string{"shared"},
			wantErr: "exists in multiple runtimes",
		},
		{
			name: "errors on unknown instance",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: agentruntime.DefaultInstanceID},
			},
			args:    []string{"missing"},
			wantErr: `agent instance "missing" not found`,
		},
		{
			name:        "errors on invalid runtime",
			runtimeFlag: "bad",
			wantErr:     "unsupported agent runtime",
		},
		{
			name: "errors on multiple non-default instances without selector",
			instances: []agentTarget{
				{Runtime: agentruntime.Hermes, ID: "research"},
				{Runtime: agentruntime.OpenClaw, ID: "legacy"},
			},
			wantErr: "multiple agent instances found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTestConfig(t)
			for _, instance := range tt.instances {
				mkdirAgentInstance(t, cfg, instance.Runtime, instance.ID)
			}

			got, err := resolveAgentTarget(cfg, tt.runtimeFlag, tt.args)
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
				t.Fatalf("resolveAgentTarget() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveAgentTarget() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResolveRuntimeInstance(t *testing.T) {
	tests := []struct {
		name          string
		runtime       agentruntime.Runtime
		instances     []string
		args          []string
		preferDefault bool
		want          string
		wantErr       string
	}{
		{
			name:    "no instances",
			runtime: agentruntime.Hermes,
			wantErr: "no Hermes instances found",
		},
		{
			name:          "prefers default Hermes",
			runtime:       agentruntime.Hermes,
			instances:     []string{"research", agentruntime.DefaultInstanceID},
			preferDefault: true,
			want:          agentruntime.DefaultInstanceID,
		},
		{
			name:      "uses single instance",
			runtime:   agentruntime.OpenClaw,
			instances: []string{"legacy"},
			want:      "legacy",
		},
		{
			name:      "uses explicit instance",
			runtime:   agentruntime.Hermes,
			instances: []string{"research", agentruntime.DefaultInstanceID},
			args:      []string{"research"},
			want:      "research",
		},
		{
			name:      "errors on unknown explicit instance",
			runtime:   agentruntime.Hermes,
			instances: []string{agentruntime.DefaultInstanceID},
			args:      []string{"missing"},
			wantErr:   `Hermes instance "missing" not found`,
		},
		{
			name:      "errors on multiple instances without default preference",
			runtime:   agentruntime.Hermes,
			instances: []string{agentruntime.DefaultInstanceID, "research"},
			wantErr:   "multiple Hermes instances found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTestConfig(t)
			for _, id := range tt.instances {
				mkdirAgentInstance(t, cfg, tt.runtime, id)
			}

			got, err := resolveRuntimeInstance(cfg, tt.runtime, tt.args, tt.preferDefault)
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
				t.Fatalf("resolveRuntimeInstance() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveRuntimeInstance() = %q, want %q", got, tt.want)
			}
		})
	}
}

func mkdirAgentInstance(t *testing.T, cfg *config.Config, runtime agentruntime.Runtime, id string) {
	t.Helper()
	if err := os.MkdirAll(agentruntime.DeploymentPath(cfg, runtime, id), 0o755); err != nil {
		t.Fatalf("create %s instance %q: %v", runtime, id, err)
	}
}
