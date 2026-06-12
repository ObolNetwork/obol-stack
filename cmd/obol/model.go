package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/buy"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/model"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

func modelCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "model",
		Usage: "Manage LLM providers (LiteLLM gateway)",
		Commands: []*cli.Command{
			modelSetupCommand(cfg),
			modelStatusCommand(cfg),
			modelTokenCommand(cfg),
			modelSyncCommand(cfg),
			modelPullCommand(),
			modelListCommand(cfg),
			modelPreferCommand(cfg),
			modelDiscoverCommand(),
			modelRemoveCommand(cfg),
		},
	}
}

func modelTokenCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "token",
		Usage: "Print the LiteLLM master token for API access",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			token, err := model.GetMasterKey(cfg)
			if err != nil {
				return err
			}

			if u.IsJSON() {
				return u.JSON(map[string]string{"token": token})
			}

			u.Print(token)
			return nil
		},
	}
}

func modelSetupCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "Configure an LLM provider in the LiteLLM gateway",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "provider",
				Usage: "Provider id (anthropic, openai, ollama, venice, openrouter, nvidia, gmi, novita, huggingface). Run with no flags to pick interactively.",
			},
			&cli.StringFlag{
				Name:    "api-key",
				Usage:   "API key for the provider (BYOK; also read from the provider's env var if set)",
				Sources: cli.EnvVars("LLM_API_KEY"),
			},
			&cli.StringSliceFlag{
				Name:  "model",
				Usage: "Model(s) to configure (e.g. claude-sonnet-4-6, gpt-5.5, or an aggregator model id)",
			},
			&cli.BoolFlag{
				Name:  "free",
				Usage: "Seed only the provider's curated free-tier models (OpenRouter)",
			},
		},
		Commands: []*cli.Command{
			modelSetupCustomCommand(cfg),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			provider := cmd.String("provider")
			apiKey := cmd.String("api-key")
			models := cmd.StringSlice("model")

			// Interactive mode if flags not provided
			if provider == "" {
				creds := detectCredentials()
				providers, _ := model.GetAvailableProviders(cfg)

				options := make([]string, len(providers))
				for i, p := range providers {
					label := fmt.Sprintf("%s (%s)", p.Name, p.ID)
					if det, ok := creds[p.ID]; ok {
						label += " — detected: " + det.source
					}

					options[i] = label
				}

				idx, err := u.Select("Select a provider:", options, 0)
				if err != nil {
					return err
				}

				provider = providers[idx].ID

				// If a credential was detected for the chosen provider, offer to use it
				if det, ok := creds[provider]; ok && det.key != "" && apiKey == "" {
					u.Infof("%s API key detected (%s)", providers[idx].Name, det.source)

					if u.Confirm("Use detected credential?", true) {
						apiKey = det.key
					}
				}
			}

			// Provider-specific flow — dispatch off the registry, not a
			// hardcoded switch. Ollama is local; everything else is a
			// key-based cloud/BYOK provider handled by one generic path.
			prof, ok := model.ProviderByID(provider)
			if !ok {
				return fmt.Errorf("unknown provider %q — run `obol model setup` (no flags) to pick from the list, or `obol model setup custom --endpoint … --model …` for an unlisted OpenAI-compatible endpoint", provider)
			}
			if prof.ID == model.ProviderOllama {
				return setupOllama(cfg, u, models)
			}
			return setupCloudProvider(cfg, u, prof, apiKey, models, cmd.Bool("free"))
		},
	}
}

func setupOllama(cfg *config.Config, u *ui.UI, models []string) error {
	// Only auto-promote models the user named explicitly. Auto-detecting every
	// pulled model (the len==0 branch below) must not reshuffle the model_list.
	explicit := setupPromoteList(models)

	if len(models) == 0 {
		// Diagnostic: check Ollama connectivity
		u.Info("Checking Ollama connectivity...")

		ollamaModels, err := model.ListOllamaModels()
		if err != nil {
			u.Errorf("Ollama not reachable")
			u.Print("")
			u.Print("  Hint: Is Ollama running? Try: ollama serve")
			u.Print("  Hint: Using a custom host? Set OLLAMA_HOST=http://your-host:port")
			u.Print("  Hint: Install from https://ollama.ai")

			return fmt.Errorf("ollama is not running: %w", err)
		}

		u.Success("Ollama is reachable")

		if len(ollamaModels) == 0 {
			u.Warn("No models pulled in Ollama")
			u.Print("")
			u.Print("  Hint: Pull a model with: ollama pull qwen3.5:4b  (or qwen3.6:27b on hosts with ≥32GB RAM)")
			u.Print("  Hint: Or run: obol model pull")

			return errors.New("ollama is running but has no models")
		}

		u.Successf("Found %d pulled model(s)", len(ollamaModels))

		for _, m := range ollamaModels {
			name := m.Name
			if before, ok := strings.CutSuffix(name, ":latest"); ok {
				name = before
			}

			models = append(models, name)
		}

		u.Infof("Models: %s", strings.Join(models, ", "))
	}

	if err := model.ConfigureLiteLLM(cfg, u, "ollama", "", models); err != nil {
		return err
	}

	u.Successf("Ollama configured. To change later, run: obol model setup (or obol model remove <name>)")

	return promoteAndSync(cfg, u, explicit)
}

func setupCloudProvider(cfg *config.Config, u *ui.UI, prof model.ProviderInfo, apiKey string, models []string, free bool) error {
	if apiKey == "" {
		if prof.SignupURL != "" {
			u.Dim(fmt.Sprintf("Get a %s API key: %s", prof.Name, prof.SignupURL))
		}

		var err error
		apiKey, err = u.SecretInput(fmt.Sprintf("%s API key (%s)", prof.Name, prof.EnvVar))
		if err != nil {
			return err
		}

		if apiKey == "" {
			return errors.New("API key is required")
		}
	}

	// --free: seed the provider's curated free-tier models (unless the
	// operator already named explicit --model values).
	if free {
		if len(prof.Free) == 0 {
			return fmt.Errorf("--free is not available for %s (no curated free models); pass --model instead", prof.Name)
		}
		if len(models) == 0 {
			models = append([]string(nil), prof.Free...)
			u.Infof("Seeding %d curated free %s model(s)", len(models), prof.Name)
		}
	}

	// Resolve a model when none was given: the registry Default, else (for
	// BYOK aggregators with a rotating catalog) the live /v1/models list.
	if len(models) == 0 {
		chosen, err := resolveSetupModel(u, prof, apiKey)
		if err != nil {
			return err
		}
		if chosen != "" {
			models = []string{chosen}
		}
	}
	if len(models) == 0 {
		return fmt.Errorf("no model selected for %s — pass --model <id>", prof.Name)
	}

	if err := model.ConfigureLiteLLM(cfg, u, prof.ID, apiKey, models); err != nil {
		u.Print("")
		u.Print("  Hint: Configuration stored in: litellm-config ConfigMap (llm namespace)")

		return err
	}

	u.Print("")
	u.Successf("Model configured. To change later, run: obol model setup (or obol model remove <name>)")

	return promoteAndSync(cfg, u, models)
}

// resolveSetupModel picks a model when the operator passed none. A registry
// Default wins (overridable in a TTY). With no static default — BYOK
// aggregators whose catalog rotates — it lists the live /v1/models endpoint:
// a picker in a TTY, otherwise an error naming real ids so the operator can
// re-run with --model. Returns "" only when there is genuinely nothing to
// pick (the caller then errors).
func resolveSetupModel(u *ui.UI, prof model.ProviderInfo, apiKey string) (string, error) {
	if prof.Default != "" {
		if u.IsTTY() && !u.IsJSON() {
			input, err := u.Input(fmt.Sprintf("Model for %s", prof.ID), prof.Default)
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(input) != "" {
				return strings.TrimSpace(input), nil
			}
		}
		return prof.Default, nil
	}

	if !prof.IsBYOK() {
		return "", nil
	}

	ids, err := model.FetchOpenAICompatibleModels(prof.BaseURL, apiKey)
	if err != nil {
		u.Dim(fmt.Sprintf("Couldn't list %s models (%v)", prof.Name, err))
		if u.IsTTY() && !u.IsJSON() {
			return u.Input(fmt.Sprintf("Model id for %s", prof.Name), "")
		}
		return "", fmt.Errorf("could not resolve a model for %s: pass --model <id> (keys/models at %s)", prof.Name, prof.SignupURL)
	}

	if u.IsTTY() && !u.IsJSON() {
		shown := ids
		if len(shown) > 30 {
			shown = shown[:30]
		}
		idx, err := u.Select(fmt.Sprintf("Select a %s model:", prof.Name), shown, 0)
		if err != nil {
			return "", err
		}
		return shown[idx], nil
	}

	sample := ids
	if len(sample) > 8 {
		sample = sample[:8]
	}
	return "", fmt.Errorf("pass --model <id> for %s; available include: %s", prof.Name, strings.Join(sample, ", "))
}

// syncAgentModels re-renders the stack-managed Hermes default agent from the
// current LiteLLM model inventory.
func syncAgentModels(cfg *config.Config, u *ui.UI) error {
	return hermes.SyncDefaultModels(cfg, u)
}

// setupPromoteList decides which models a provider setup should promote to
// primary. Explicitly named models are promoted (so `obol model setup` makes
// the just-configured model the agent's primary). When a setup auto-discovers
// its full inventory instead (Ollama with no --model), the slice is empty and
// nothing is promoted — auto-detection must never silently reshuffle the
// operator's model_list (the spark2 footgun). Returns a fresh slice so the
// caller can mutate its own copy without aliasing this one.
func setupPromoteList(userSpecified []string) []string {
	return append([]string(nil), userSpecified...)
}

// promoteAndSync moves the just-configured model(s) to the head of the LiteLLM
// model_list so the first becomes the agent's primary, then syncs the agent.
// `obol model setup` configures providers by appending to the model_list, so a
// newly added model would otherwise sit at the tail and never become primary —
// users had to run `obol model prefer` manually for setup to "take". Promoting
// by default makes a freshly configured model take effect immediately; users
// reorder afterward with `obol model prefer`.
//
// Promotion is best-effort: the provider is already configured, so a promote
// failure (e.g. an unexpected name mismatch) warns and still syncs rather than
// failing the whole setup. An empty list (e.g. Ollama auto-detect of every
// pulled model) skips promotion and just syncs.
func promoteAndSync(cfg *config.Config, u *ui.UI, models []string) error {
	if len(models) > 0 {
		if err := model.PreferModels(cfg, u, models); err != nil {
			u.Warnf("Configured, but could not promote %s to primary: %v", strings.Join(models, ", "), err)
			u.Dim("  Set the primary yourself with: obol model prefer " + models[0])
		} else {
			u.Successf("Primary model is now %s (reorder anytime with: obol model prefer <model>)", models[0])
		}
	}
	// Record-on-write: every `obol model setup ...` variant funnels through
	// here after mutating the litellm ConfigMap, so this one snapshot keeps
	// the host-side record (replayed by `obol stack up`) current.
	model.RecordState(cfg, u)
	return syncAgentModels(cfg, u)
}

func modelSyncCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "sync",
		Usage: "Sync LiteLLM model list to the stack-managed Hermes agent",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			u.Info("Reading model list from LiteLLM...")

			return syncAgentModels(cfg, u)
		},
	}
}

func modelSetupCustomCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "custom",
		Usage: "Add a custom OpenAI-compatible endpoint (validates before adding)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "endpoint", Usage: "Full base URL (e.g. http://host:8000/v1)", Required: true},
			&cli.StringFlag{Name: "model", Usage: "Model identifier at the endpoint — this is also the LiteLLM model_name the agent will call", Required: true},
			&cli.StringFlag{Name: "api-key", Usage: "API key (optional, some endpoints don't require it)"},
			&cli.BoolFlag{Name: "disable-thinking", Usage: "Tells a model not to use its thinking mode to reason about turns for longer."},
			&cli.BoolFlag{Name: "no-sync", Usage: "Skip the agent model sync (batch with other model commands, then run `obol model sync` once)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			endpoint := cmd.String("endpoint")
			modelName := cmd.String("model")
			apiKey := cmd.String("api-key")

			options := model.CustomEndpointOptions{
				DisableThinking: cmd.Bool("disable-thinking"),
			}
			if err := model.AddCustomEndpointWithOptions(cfg, u, endpoint, modelName, apiKey, options); err != nil {
				return err
			}

			if cmd.Bool("no-sync") {
				return nil
			}
			return promoteAndSync(cfg, u, []string{modelName})
		},
	}
}

// modelStatusResult is the JSON-serialisable result for `model status`.
type modelStatusResult struct {
	Providers  []modelStatusProvider `json:"providers"`
	Discovered []discoverProvider    `json:"discovered,omitempty"`
}

type modelStatusProvider struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	APIKey  string   `json:"api_key"` // "set", "missing", or "n/a"
	Models  []string `json:"models"`
	EnvVar  string   `json:"env_var,omitempty"`
}

func modelStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show LiteLLM gateway provider status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			status, err := model.GetProviderStatus(cfg)
			if err != nil {
				return err
			}

			providers := make([]string, 0, len(status))
			for name := range status {
				providers = append(providers, name)
			}

			sort.Strings(providers)

			scanCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			discovered, _ := model.DiscoverLocalProviders(scanCtx)

			if u.IsJSON() {
				result := modelStatusResult{
					Discovered: discoveredProvidersToJSON(discovered),
				}
				for _, name := range providers {
					s := status[name]
					key := "n/a"
					if s.EnvVar != "" {
						if s.HasAPIKey {
							key = "set"
						} else {
							key = "missing"
						}
					}
					result.Providers = append(result.Providers, modelStatusProvider{
						Name:    name,
						Enabled: s.Enabled,
						APIKey:  key,
						Models:  s.Models,
						EnvVar:  s.EnvVar,
					})
				}
				return u.JSON(result)
			}

			u.Info("LiteLLM gateway providers:")
			u.Blank()
			u.Printf("  %-20s %-8s %-10s %-10s %s", "PROVIDER", "ENABLED", "API KEY", "MODELS", "ENV VAR")

			for _, name := range providers {
				s := status[name]
				key := "n/a"

				if s.EnvVar != "" {
					if s.HasAPIKey {
						key = "set"
					} else {
						key = "missing"
					}
				}

				modelCount := strconv.Itoa(len(s.Models))
				if len(s.Models) == 0 {
					modelCount = "-"
				}

				u.Printf("  %-20s %-8t %-10s %-10s %s", name, s.Enabled, key, modelCount, s.EnvVar)
			}

			u.Blank()
			if printModelRanking(u, cfg, "section") {
				u.Blank()
			}

			if printPaidPurchases(u, cfg) {
				u.Blank()
			}

			if len(discovered) > 0 {
				u.Infof("Discovered local inference servers (%d):", len(discovered))
				u.Blank()
				for _, p := range discovered {
					noun := "models"
					if len(p.Entries) == 1 {
						noun = "model"
					}
					u.Printf("  %-20s %s  (%d %s)", p.Label, p.HostEndpoint, len(p.Entries), noun)
				}
				u.Blank()
				u.Dim("Run 'obol model discover' for the full model list.")
			}

			u.Dim("Run 'obol model setup' to configure a provider.")
			u.Dim("Run 'obol model setup custom' to add a custom endpoint.")
			u.Dim("Run 'obol model prefer <name>' to promote a model to head-of-list.")

			return nil
		},
	}
}

func modelPullCommand() *cli.Command {
	return &cli.Command{
		Name:      "pull",
		Usage:     "Pull an Ollama model to the local machine",
		ArgsUsage: "[model]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			modelName := cmd.Args().First()

			if modelName == "" {
				var err error
				modelName, err = promptModelPull(u)
				if err != nil {
					return err
				}
			}

			u.Infof("Pulling model: %s", modelName)
			u.Blank()
			if err := model.PullOllamaModel(modelName); err != nil {
				return err
			}
			u.Blank()
			u.Successf("Model %s is ready.", modelName)
			return nil
		},
	}
}

// modelListResult is the JSON-serialisable result for `model list`.
type modelListResult struct {
	Local   []modelListLocal   `json:"local"`
	Gateway []modelListGateway `json:"gateway,omitempty"`
}

type modelListLocal struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type modelListGateway struct {
	Provider string   `json:"provider"`
	Enabled  bool     `json:"enabled"`
	Models   []string `json:"models"`
}

func modelListCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List pulled Ollama models and cloud provider status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			if u.IsJSON() {
				result := modelListResult{}

				if models, err := model.ListOllamaModels(); err == nil {
					for _, m := range models {
						result.Local = append(result.Local, modelListLocal{
							Name: m.Name,
							Size: m.Size,
						})
					}
				}

				if providerStatus, err := model.GetProviderStatus(cfg); err == nil {
					providers := make([]string, 0, len(providerStatus))
					for name := range providerStatus {
						providers = append(providers, name)
					}
					sort.Strings(providers)
					for _, name := range providers {
						s := providerStatus[name]
						result.Gateway = append(result.Gateway, modelListGateway{
							Provider: name,
							Enabled:  s.Enabled,
							Models:   s.Models,
						})
					}
				}

				return u.JSON(result)
			}

			// Section 1: local Ollama models.
			models, err := model.ListOllamaModels()
			if err != nil {
				u.Warnf("Local models (Ollama): not available (%s)", err)
			} else if len(models) == 0 {
				u.Info("Local models (Ollama):")
				u.Blank()
				u.Dim("  none pulled — obol model pull <name>")
			} else {
				u.Info("Local models (Ollama):")
				u.Blank()
				u.Printf("  %-35s %s", "NAME", "SIZE")
				for _, m := range models {
					u.Printf("  %-35s %s", m.Name, model.FormatBytes(m.Size))
				}
			}
			u.Blank()

			// Section 2: LiteLLM gateway by provider.
			providerStatus, err := model.GetProviderStatus(cfg)
			if err != nil {
				u.Info("LiteLLM gateway providers:")
				u.Blank()
				u.Dim("  cluster not running — run 'obol stack up' then 'obol model setup'")
				return nil
			}
			providers := make([]string, 0, len(providerStatus))
			for name := range providerStatus {
				providers = append(providers, name)
			}
			sort.Strings(providers)

			u.Info("LiteLLM gateway providers:")
			u.Blank()
			u.Printf("  %-20s %-10s %s", "PROVIDER", "STATUS", "MODELS")
			for _, name := range providers {
				s := providerStatus[name]
				status := "disabled"
				if s.Enabled {
					status = "enabled"
				}
				modelList := strings.Join(s.Models, ", ")
				if modelList == "" {
					modelList = "-"
				}
				u.Printf("  %-20s %-10s %s", name, status, modelList)
			}
			u.Blank()

			// Section 3: ranking (same helper as `model status` / `model prefer`).
			if printModelRanking(u, cfg, "section") {
				u.Blank()
				u.Dim("Promote a model with: obol model prefer <name>")
			}

			return nil
		},
	}
}

func modelPreferCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "prefer",
		Usage:     "Pull one or more models to the preferred choices (agents use these in order)",
		ArgsUsage: "[<model-name> ...]",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "no-sync", Usage: "Skip the agent model sync (batch with other model commands, then run `obol model sync` once)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			names := cmd.Args().Slice()
			if len(names) == 0 {
				// No args: show the current ranking + usage hint (read
				// view). Renders with the dedicated "prefer-empty" style
				// so users discover usage without re-running `--help`.
				if !printModelRanking(u, cfg, "prefer-empty") {
					return errors.New("LiteLLM gateway not reachable — run 'obol stack up' first")
				}
				u.Blank()
				u.Dim("Usage: obol model prefer <model-name> [<model-name> ...]")
				return nil
			}

			if err := model.PreferModels(cfg, u, names); err != nil {
				return err
			}
			model.RecordState(cfg, u)

			if cmd.Bool("no-sync") {
				return nil
			}
			if err := syncAgentModels(cfg, u); err != nil {
				return err
			}
			// `syncAgentModels` only re-renders the master Hermes agent.
			// Sub-agents keep their existing `model.default` until they're
			// individually synced — surface that explicitly so callers
			// aren't left wondering why a sub-agent stayed on the old model.
			printSubAgentSyncHint(cfg, u)
			return nil
		},
	}
}

// printPaidPurchases queries each agent for its pre-authorized inference
// purchases and renders a "Paid models credit:" section showing the
// remaining spendable balance per (agent, paid-model) pair. Returns
// false when there's nothing to show so the caller can skip the
// trailing blank.
//
// Credit framing: remaining × per-request price = atomic units still
// available to spend. Formatted with the token's own decimals so OBOL
// renders as "0.5 OBOL", USDC as "0.001 USDC". Drains show as "0 OBOL"
// with a `drained` state so the operator knows to top up.
func printPaidPurchases(u *ui.UI, cfg *config.Config) bool {
	type rowKey struct {
		runtime agentruntime.Runtime
		id      string
	}
	rows := make(map[rowKey][]buy.PurchaseSummary)
	var keys []rowKey

	for _, runtime := range []agentruntime.Runtime{agentruntime.Hermes, agentruntime.OpenClaw} {
		ids, err := agentruntime.ListInstanceIDs(cfg, runtime)
		if err != nil {
			continue
		}
		for _, id := range ids {
			purchases, err := buy.ListPurchases(cfg, runtime, id)
			if err != nil || len(purchases) == 0 {
				continue
			}
			k := rowKey{runtime: runtime, id: id}
			rows[k] = purchases
			keys = append(keys, k)
		}
	}
	if len(rows) == 0 {
		return false
	}

	u.Info("Paid models credit:")
	u.Blank()
	u.Printf("  %-22s %-40s %-18s %s", "AGENT", "MODEL", "CREDIT", "STATE")
	for _, k := range keys {
		agent := fmt.Sprintf("%s/%s", k.runtime, k.id)
		for _, p := range rows[k] {
			credit := formatPaidCredit(p)
			state := "ready"
			if p.Remaining == 0 {
				state = "drained"
			}
			if p.AutoRefill {
				state += " (auto-top-up)"
			}
			u.Printf("  %-22s %-40s %-18s %s", agent, truncateAlias(p.Alias, 40), credit, state)
		}
	}
	u.Blank()
	u.Dim("Top up a drained model: obol buy inference <seller-url>")
	u.Dim("  add --auto-refill to keep it funded automatically (top-ups draw from the agent's wallet).")
	return true
}

// formatPaidCredit returns "remaining × price" as a human token amount
// ("0.5 OBOL"). Falls back to "<remaining> auths" when price metadata
// is missing (older PurchaseRequests didn't surface asset decimals).
func formatPaidCredit(p buy.PurchaseSummary) string {
	if p.Price == "" || p.AssetSymbol == "" || p.AssetDecimals <= 0 {
		return fmt.Sprintf("%d auths", p.Remaining)
	}
	priceAtomic, ok := new(big.Int).SetString(strings.TrimSpace(p.Price), 10)
	if !ok || priceAtomic.Sign() < 0 {
		return fmt.Sprintf("%d auths", p.Remaining)
	}
	credit := new(big.Int).Mul(priceAtomic, big.NewInt(int64(p.Remaining)))
	return fmt.Sprintf("%s %s", formatAtomicTrimmed(credit, p.AssetDecimals), p.AssetSymbol)
}

// formatAtomicTrimmed mirrors cmd/obol/buy.go::formatTokenAmount but
// lives here so the model.go file stays import-light. Trims trailing
// zeros, drops the decimal point when not needed.
func formatAtomicTrimmed(v *big.Int, decimals int) string {
	if v == nil {
		return "?"
	}
	if decimals <= 0 {
		decimals = 6
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	r := new(big.Rat).SetFrac(v, scale)
	s := r.FloatString(decimals)
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	return s
}

func truncateAlias(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// printSubAgentSyncHint emits a dimmed reminder when sub-agents exist
// beyond the master Hermes instance. The master is already covered by
// syncAgentModels; the others need an explicit `obol agent sync <name>`
// to pick up the new head-of-list primary.
func printSubAgentSyncHint(cfg *config.Config, u *ui.UI) {
	hermesIDs, err := agentruntime.ListInstanceIDs(cfg, agentruntime.Hermes)
	if err != nil {
		return
	}
	openclawIDs, _ := agentruntime.ListInstanceIDs(cfg, agentruntime.OpenClaw)

	var subs []string
	for _, id := range hermesIDs {
		if id == agentruntime.DefaultInstanceID {
			continue
		}
		subs = append(subs, "hermes/"+id)
	}
	for _, id := range openclawIDs {
		subs = append(subs, "openclaw/"+id)
	}
	if len(subs) == 0 {
		return
	}
	u.Blank()
	if len(subs) == 1 {
		u.Dim(fmt.Sprintf("Sub-agent %s keeps its existing primary model. Run: obol agent sync %s",
			subs[0], strings.SplitN(subs[0], "/", 2)[1]))
		return
	}
	u.Dim("Sub-agents keep their existing primary model. Run, for each:")
	for _, s := range subs {
		u.Dim(fmt.Sprintf("  obol agent sync %s", strings.SplitN(s, "/", 2)[1]))
	}
}

// printModelRanking renders the configured LiteLLM model_list ordering
// with the head marked as primary. The caller picks the framing:
//
//   - "section" → green `==>` section header inline with the rest of
//     `model list` / `model status` (their natural section style).
//   - "prefer-empty" → the argless `obol model prefer` view: a top
//     title line, a dimmed "Current order:" subtitle, the star list,
//     and a usage hint footer. Cleaner for a read-only "show me the
//     current state" call.
//
// Returns false when no models are configured (caller can fall through
// to a "cluster not running" / "no models" message).
func printModelRanking(u *ui.UI, cfg *config.Config, style string) bool {
	models, err := model.GetConfiguredModels(cfg)
	if err != nil || len(models) == 0 {
		return false
	}
	primary, _ := model.Rank(models)

	switch style {
	case "prefer-empty":
		u.Info("Model ranking (which model agents use):")
		u.Blank()
		u.Dim("Current order:")
	default:
		// "section" — used by list/status. Green `==>` arrow keeps the
		// section header aligned with the rest of the command's output.
		u.Info("Model order (which model agents use in order):")
		u.Blank()
	}
	for i, name := range models {
		marker := " "
		if name == primary {
			marker = "★"
		}
		u.Printf("  %s %2d. %s", marker, i+1, name)
	}
	return true
}

func modelRemoveCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "Remove a model from the LiteLLM gateway",
		ArgsUsage: "<model-name>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "no-sync", Usage: "Skip the agent model sync (batch with other model commands, then run `obol model sync` once)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			modelName := cmd.Args().First()
			if modelName == "" {
				return errors.New("model name is required\n\nUsage: obol model remove <model-name>\n\nList configured models with: obol model list")
			}

			if err := model.RemoveModel(cfg, u, modelName); err != nil {
				return err
			}
			model.RecordState(cfg, u)

			if cmd.Bool("no-sync") {
				return nil
			}
			return syncAgentModels(cfg, u)
		},
	}
}

// detectedCredential describes a credential found in the environment.
type detectedCredential struct {
	key    string // the actual API key value (empty for Ollama)
	source string // human-readable description of where it was found
}

// detectCredentials checks the environment for existing provider credentials.
// It returns a map of provider ID to detected credential info. Only providers
// with a detected credential appear in the map.
func detectCredentials() map[string]detectedCredential {
	creds := make(map[string]detectedCredential)

	// Registry-driven: every provider's primary + alternate env vars are
	// checked via model.ResolveAPIKey, so a new provider row auto-detects
	// without editing this function. Ollama has no key — probe reachability.
	providers, _ := model.GetAvailableProviders(nil)
	for _, p := range providers {
		if p.ID == model.ProviderOllama {
			if ollamaModels, err := model.ListOllamaModels(); err == nil && len(ollamaModels) > 0 {
				creds[p.ID] = detectedCredential{
					source: fmt.Sprintf("%d model(s) available", len(ollamaModels)),
				}
			}
			continue
		}

		if key, envVar := model.ResolveAPIKey(p.ID); key != "" {
			creds[p.ID] = detectedCredential{key: key, source: envVar}
		}
	}

	return creds
}

// promptModelPull interactively asks the user which Ollama model to pull.
// When the UI is non-interactive (piped, CI, or JSON mode), it returns an
// error instructing the user to specify --model via flag.
func promptModelPull(u *ui.UI) (string, error) {
	if !u.IsTTY() || u.IsJSON() {
		return "", fmt.Errorf("model name required: use positional arg (obol model pull <model>)")
	}

	suggestions := []string{
		"qwen3.6:27b              (17 GB) — High-quality general-purpose (recommended, needs ≥32GB RAM)",
		"qwen3.6:27b-coding-mxfp8 (31 GB) — Code generation (Qwen3.6, MXFP8 quant)",
		"qwen3.5:9b               (6.6 GB) — Validated baseline; fits on most laptops",
		"qwen3.5:4b               (3.4 GB) — Smallest current Qwen, low-RAM laptops",
		"deepseek-r1:8b           (4.9 GB) — Reasoning",
		"gemma3:4b                (3.3 GB) — Lightweight, multilingual",
		"Other (enter name)",
	}
	modelNames := []string{
		"qwen3.6:27b",
		"qwen3.6:27b-coding-mxfp8",
		"qwen3.5:9b",
		"qwen3.5:4b",
		"deepseek-r1:8b",
		"gemma3:4b",
	}

	idx, err := u.Select("Select a model to pull:", suggestions, 0)
	if err != nil {
		return "", err
	}

	if idx < len(modelNames) {
		return modelNames[idx], nil
	}

	// Custom model name
	name, err := u.Input("Model name (e.g. mistral:7b)", "")
	if err != nil {
		return "", err
	}
	if name == "" {
		return "", errors.New("model name is required")
	}

	return name, nil
}

type discoverProvider struct {
	Label           string   `json:"label"`
	ServerType      string   `json:"server_type"`
	HostEndpoint    string   `json:"host_endpoint"`
	ClusterEndpoint string   `json:"cluster_endpoint"`
	Models          []string `json:"models"`
}

type discoverResult struct {
	Providers []discoverProvider `json:"providers"`
}

func discoveredProvidersToJSON(discovered []model.DiscoveredProvider) []discoverProvider {
	if len(discovered) == 0 {
		return nil
	}
	out := make([]discoverProvider, 0, len(discovered))
	for _, p := range discovered {
		names := make([]string, 0, len(p.Entries))
		for _, e := range p.Entries {
			names = append(names, e.ModelName)
		}
		out = append(out, discoverProvider{
			Label:           p.Label,
			ServerType:      p.ServerType,
			HostEndpoint:    p.HostEndpoint,
			ClusterEndpoint: p.ClusterEndpoint,
			Models:          names,
		})
	}
	return out
}

func modelDiscoverCommand() *cli.Command {
	return &cli.Command{
		Name:  "discover",
		Usage: "Detect other local inference servers (LM Studio, llama.cpp, vLLM, etc) and their models.",
		Description: "Read-only. Does not modify the existing cluster. This discovery runs every `obol stack up`.\n" +
			"Set OBOL_DISABLE_LOCAL_MODEL_DISCOVERY=true to skip local inference server auto-detection every startup.\n" +
			"Set OBOL_LOCAL_MODEL_DISCOVERY_PORTS=port[:label],... to manually add custom local inference servers.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			scanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			discovered, err := model.DiscoverLocalProviders(scanCtx)
			if err != nil {
				return fmt.Errorf("discovery scan failed: %w", err)
			}

			if u.IsJSON() {
				return u.JSON(discoverResult{Providers: discoveredProvidersToJSON(discovered)})
			}

			if len(discovered) == 0 {
				u.Info("No local OpenAI-compatible inference servers detected on well-known ports.")
				u.Blank()
				u.Dim("  Hint: start vLLM/sglang on :8000, llama.cpp on :8080, LM Studio on :1234,")
				u.Dim("  or add a custom port via OBOL_LOCAL_MODEL_DISCOVERY_PORTS=9000:vllm")
				return nil
			}

			u.Infof("Discovered %d local inference server(s):", len(discovered))
			for _, p := range discovered {
				u.Blank()
				u.Bold(fmt.Sprintf("  %s  (%s → %s)", p.Label, p.HostEndpoint, p.ClusterEndpoint))
				for _, e := range p.Entries {
					u.Print(fmt.Sprintf("    - %s", e.ModelName))
				}
			}
			u.Blank()
			u.Dim("  These are registered automatically on `obol stack up`. Disable with OBOL_DISABLE_LOCAL_MODEL_DISCOVERY=true.")
			return nil
		},
	}
}
