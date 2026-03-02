# Obol CLI Wrappers & Commands

## Architecture

The `obol` CLI wraps standard Kubernetes tools with automatic `KUBECONFIG` injection. All passthrough commands inherit the cluster's kubeconfig from `$CONFIG_DIR/kubeconfig.yaml`.

```go
// Pattern: all passthrough commands follow this template
cmd := exec.Command(binaryPath, args...)
cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
cmd.Stdin = os.Stdin
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
return cmd.Run()
```

## Command Map

### Stack Lifecycle

| Command | Description | Implementation |
|---------|-------------|----------------|
| `obol stack init` | Generate k3d config + cluster ID | `internal/stack/stack.go:Init()` |
| `obol stack up` | Create k3d cluster, deploy defaults | `internal/stack/stack.go:Up()` |
| `obol stack down` | Stop cluster (preserves data) | `internal/stack/stack.go:Down()` |
| `obol stack purge -f` | Remove cluster + data | `internal/stack/stack.go:Purge()` |

### Model Provider Management (Tier 1: llmspy)

| Command | Description | Implementation |
|---------|-------------|----------------|
| `obol model setup` | Interactive provider config | `cmd/obol/model.go` |
| `obol model setup --provider anthropic --api-key KEY` | Non-interactive | `model.ConfigureLLMSpy()` |
| `obol model status` | Show enabled providers | `model.GetProviderStatus()` |

### OpenClaw Instance Management (Tier 2)

| Command | Description | Implementation |
|---------|-------------|----------------|
| `obol openclaw onboard [--id ID] [--force]` | Interactive create + deploy | `openclaw.Onboard()` |
| `obol openclaw sync <id>` | Deploy/update instance | `openclaw.Sync()` |
| `obol openclaw token <id>` | Print gateway Bearer token | `openclaw.Token()` |
| `obol openclaw list` | List all instances | `openclaw.List()` |
| `obol openclaw delete --force <id>` | Remove instance | `openclaw.Delete()` |
| `obol openclaw setup <id>` | Reconfigure model provider | `openclaw.Setup()` |
| `obol openclaw dashboard <id>` | Open web UI | `openclaw.Dashboard()` |
| `obol openclaw cli <id> -- <args>` | Run openclaw CLI in pod | `openclaw.CLI()` |
| `obol openclaw skills sync <id> --from ./dir` | Push skills to pod | `openclaw.SkillsSync()` |

### Passthrough Commands

| Command | Wraps | Notes |
|---------|-------|-------|
| `obol kubectl <args>` | `kubectl` | Auto-sets KUBECONFIG |
| `obol helm <args>` | `helm` | Auto-sets KUBECONFIG |
| `obol helmfile <args>` | `helmfile` | Auto-sets KUBECONFIG |
| `obol k9s` | `k9s` | Auto-sets KUBECONFIG |

### Network Management

| Command | Description |
|---------|-------------|
| `obol network list` | Show available networks |
| `obol network install <network> [flags]` | Create deployment config |
| `obol network sync [<network>[/<id>]]` | Deploy to cluster (auto-resolves: no arg, type, or type/id) |
| `obol network sync --all` | Sync all network deployments |
| `obol network delete [<network>[/<id>]]` | Remove deployment (auto-resolves: no arg, type, or type/id) |

### Application Management

| Command | Description |
|---------|-------------|
| `obol app install <chart>` | Install Helm chart as app |
| `obol app sync [<app>[/<id>]]` | Deploy to cluster (auto-resolves: no arg, type, or type/id) |
| `obol app list` | List installed apps |
| `obol app delete [<app>[/<id>]]` | Remove app (auto-resolves: no arg, type, or type/id) |

### Tunnel Management

| Command | Description |
|---------|-------------|
| `obol tunnel status` | Show Cloudflare tunnel status |
| `obol tunnel login --hostname <host>` | Set up persistent tunnel |
| `obol tunnel provision --hostname <host> --account-id ... --zone-id ... --api-token ...` | Provision via API |

## Important CLI Quirks

### Flag Ordering (urfave/cli v2)

**Flags must come BEFORE positional arguments**:

```bash
# CORRECT
obol openclaw delete --force my-instance

# WRONG — flag is treated as argument, not parsed
obol openclaw delete my-instance --force
```

This affects `--force`, `--id`, and all other flags.

### SkipFlagParsing

The `obol openclaw cli` command has `SkipFlagParsing: true`, meaning all arguments after the ID are passed through verbatim to the openclaw CLI in the pod:

```bash
obol openclaw cli default -- gateway health
obol openclaw cli default -- doctor
```

### Hidden Commands

`obol bootstrap` is a hidden command used by the installer (`obolup.sh`). It runs `stack init` + `stack up` + opens browser. Not shown in `--help`.

## Using the CLI Programmatically (from Go tests)

```go
// Run obol command and capture output
func obolRun(t *testing.T, cfg *config.Config, args ...string) string {
    obolBinary := filepath.Join(cfg.BinDir, "obol")
    cmd := exec.Command(obolBinary, args...)
    var buf bytes.Buffer
    cmd.Stdout = &buf
    cmd.Stderr = &buf
    if err := cmd.Run(); err != nil {
        t.Fatalf("obol %v failed: %v\n%s", args, err, buf.String())
    }
    return buf.String()
}

// Usage:
obolRun(t, cfg, "openclaw", "sync", id)
obolRun(t, cfg, "model", "setup", "--provider", "anthropic", "--api-key", apiKey)
token := strings.TrimSpace(obolRun(t, cfg, "openclaw", "token", id))
obolRun(t, cfg, "openclaw", "delete", "--force", id)
```

## Deployment Path (What Gets Exercised)

When you run `obol openclaw sync <id>`, the full path is:

```
obol CLI (cmd/obol/openclaw.go)
  → openclaw.Sync(cfg, id)
    → doSync(cfg, id)
      → applyUserSecretsIfPresent()  — K8s Secret with API keys
      → helmfile sync -f helmfile.yaml
        → Helm chart: obol/openclaw
          → templates/_helpers.tpl renders providers into OpenClaw JSON
          → Deployment, Service, ConfigMap, Secret created
          → HTTPRoute for Gateway API
```

This is why testing through `obol openclaw sync` is critical — it exercises the full stack from CLI argument parsing through helm chart rendering to Kubernetes resource creation.
