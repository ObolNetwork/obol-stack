package main

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentcrd"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// agentNewCRD handles the CRD path of `obol agent new <name> [...]`.
// Just a thin shim over createCRDAgent that pulls flags off the cli.Command.
func agentNewCRD(cfg *config.Config, cmd *cli.Command, u *ui.UI) error {
	if cmd.NArg() != 1 {
		return fmt.Errorf("CRD-path agent creation requires exactly one positional name (got %d)", cmd.NArg())
	}
	return createCRDAgent(cfg, u, createCRDAgentOptions{
		Name:         strings.TrimSpace(cmd.Args().First()),
		Model:        cmd.String("model"),
		SkillsCSV:    cmd.String("skills"),
		Objective:    cmd.String("objective"),
		CreateWallet: cmd.Bool("create-wallet"),
		Interactive:  u.IsTTY() && !u.IsJSON() && !cmd.IsSet("model") && !cmd.IsSet("skills") && !cmd.IsSet("objective") && !cmd.IsSet("create-wallet"),
	})
}

// createCRDAgentOptions captures everything agentNewCRD needs without
// dragging the cli.Command shape across packages. Used both by the
// `obol agent new` action and the inline-create path of `obol sell
// agent` so the user gets the same prompts/defaults from either entry.
type createCRDAgentOptions struct {
	Name         string
	Model        string
	SkillsCSV    string
	Objective    string
	CreateWallet bool
	Interactive  bool // when true, prompt for any missing fields with sensible defaults
}

// createCRDAgent does the actual host-seed + kubectl-apply work. Returns
// when the Agent CR is in place; the controller takes over from there.
func createCRDAgent(cfg *config.Config, u *ui.UI, opts createCRDAgentOptions) error {
	if err := agentcrd.ValidateName(opts.Name); err != nil {
		return err
	}
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return fmt.Errorf("Obol Stack is not running. Start it with `obol stack up` first")
	}

	model := opts.Model
	skillsCSV := opts.SkillsCSV
	objective := opts.Objective
	createWallet := opts.CreateWallet

	if opts.Interactive {
		if model == "" {
			model = strings.TrimSpace(promptOrDefault(u, "Model (LiteLLM name; empty = controller auto-pins)", model))
		}
		if skillsCSV == "" {
			skillsCSV = strings.TrimSpace(promptOrDefault(u, "Skills (comma-separated)",
				"ethereum-networks,ethereum-local-wallet,addresses,gas"))
		}
		if objective == "" {
			objective = strings.TrimSpace(promptOrDefault(u, "Objective",
				"You are a focused sub-agent. Answer the user's task within scope; refuse anything outside your skills."))
		}
		// Wallet defaults to true on the prompt because most sub-agents
		// will want one; the operator can decline.
		ans := strings.TrimSpace(promptOrDefault(u, "Provision a wallet for this agent? [Y/n]", "Y"))
		createWallet = !strings.EqualFold(ans, "n") && !strings.EqualFold(ans, "no")
	}

	skills, err := agentcrd.ParseSkills(skillsCSV)
	if err != nil {
		return err
	}

	if existing, err := getAgentCR(cfg, opts.Name); err == nil && existing != "" {
		return fmt.Errorf("agent %q already exists in namespace %s — use `obol agent update %s` to change it",
			opts.Name, agentcrd.Namespace(opts.Name), opts.Name)
	}

	soulWritten, err := agentcrd.SeedHostFiles(cfg, opts.Name, skills, objective, agentcrd.SeedOptions{})
	if err != nil {
		return fmt.Errorf("seed host files: %w", err)
	}
	if soulWritten {
		u.Successf("SOUL.md seeded at %s", agentcrd.HostSoulPath(cfg, opts.Name))
	} else {
		u.Dim(fmt.Sprintf("SOUL.md already exists at %s, leaving as-is", agentcrd.HostSoulPath(cfg, opts.Name)))
	}
	if len(skills) > 0 {
		u.Successf("Skills written: %s", strings.Join(skills, ", "))
	}

	// Apply the namespace first — the Agent CR is namespaced, and the
	// controller-driven namespace creation is part of the reconcile loop
	// that doesn't run until the CR exists. Idempotent: re-running an
	// existing agent's `obol agent new` is a no-op for the namespace.
	nsManifest := map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": agentcrd.Namespace(opts.Name),
			"labels": map[string]any{
				"obol.org/agent-namespace":     "true",
				"app.kubernetes.io/managed-by": "obol-cli",
			},
		},
	}
	if _, err := kubectlApplyOutput(cfg, nsManifest); err != nil {
		return fmt.Errorf("apply namespace: %w", err)
	}

	manifest := agentcrd.BuildAgent(opts.Name, agentcrd.AgentOptions{
		Model:        model,
		Skills:       skills,
		Objective:    objective,
		CreateWallet: createWallet,
	})

	out, err := kubectlApplyOutput(cfg, manifest)
	if err != nil {
		return fmt.Errorf("apply Agent: %w", err)
	}
	action := "created"
	if strings.Contains(out, "configured") || strings.Contains(out, "unchanged") {
		action = "updated"
	}
	u.Successf("Agent %s/%s %s", agentcrd.Namespace(opts.Name), opts.Name, action)
	u.Infof("Reconciler will provision: namespace → %s deployment → status updates", "hermes")
	// Host-side chown for agent-<name>/hermes-data (#475). Master-agent Sync
	// already does this for hermes-obol-agent; CRD children skip helm Sync.
	hermes.EnsureCRDAgentHermesPVCOwnership(cfg, opts.Name, u)
	u.Infof("Inspect: kubectl get agent %s -n %s -o yaml", opts.Name, agentcrd.Namespace(opts.Name))
	return nil
}

// promptOrDefault wraps u.Input so empty input falls through to the
// printed default. The ui.UI returns a string + error; we treat any
// error as "no input" and let the default flow through.
func promptOrDefault(u *ui.UI, label, def string) string {
	out, err := u.Input(label, def)
	if err != nil {
		return def
	}
	if strings.TrimSpace(out) == "" {
		return def
	}
	return out
}

// getAgentCR returns the kubectl get output for an Agent or empty string
// + error when the resource is missing. Used to make `obol agent new`
// reject clobbering an existing agent.
func getAgentCR(cfg *config.Config, name string) (string, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", err
	}
	bin, kc := kubectl.Paths(cfg)
	out, err := kubectl.Output(bin, kc, "get", "agent", name, "-n", agentcrd.Namespace(name), "-o", "name", "--ignore-not-found")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// getAgentCRState reports both existence and whether the CR is mid-
// deletion. Callers that branch on "already exists, skip creation" must
// not treat a Terminating CR as "already exists" — that path silently
// no-ops `obol sell demo quant` while the previous Agent is still
// finalizing, leaving the user confused about why nothing got created.
type agentCRState struct {
	Exists       bool
	Terminating  bool
	ResourceName string // e.g. "agent.obol.org/demo-quant", empty when absent
}

func getAgentCRState(cfg *config.Config, name string) (agentCRState, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return agentCRState{}, err
	}
	bin, kc := kubectl.Paths(cfg)
	// jsonpath outputs the deletion timestamp (empty if not set) so we
	// don't need a second kubectl call to disambiguate present-but-
	// terminating from fully-present.
	out, err := kubectl.Output(bin, kc, "get", "agent", name, "-n", agentcrd.Namespace(name),
		"-o", `jsonpath={.metadata.name}{"|"}{.metadata.deletionTimestamp}`,
		"--ignore-not-found")
	if err != nil {
		return agentCRState{}, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" || trimmed == "|" {
		return agentCRState{Exists: false}, nil
	}
	parts := strings.SplitN(trimmed, "|", 2)
	state := agentCRState{
		Exists:       parts[0] != "",
		ResourceName: "agent.obol.org/" + parts[0],
	}
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		state.Terminating = true
	}
	return state, nil
}
