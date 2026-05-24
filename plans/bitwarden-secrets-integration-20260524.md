# Bitwarden Secrets Manager integration proposal

## Summary

Integrate Hermes' native Bitwarden Secrets Manager support as the stack's
optional centralized secret source for Hermes runtime environment variables.
The first version should not replace Kubernetes Secrets, LiteLLM's existing
`litellm-secrets`, or remote-signer keystores. It should only make the
Hermes process able to resolve provider and application API keys from
Bitwarden at startup by wiring the bootstrap `BWS_ACCESS_TOKEN` and the
`secrets.bitwarden.*` config fields into stack-managed Hermes deployments.

Hermes already implements the runtime path upstream: with
`secrets.bitwarden.enabled: true`, it loads `~/.hermes/.env`, runs
`bws secret list <project_id>`, and exports returned secret names into
`os.environ` before the gateway, CLI, or cron starts. The stack should expose
that capability through Obol CLI and manifests. `obol model setup` may also
use the same Bitwarden project as a provider-key source so LiteLLM can be
configured without the operator pasting API keys into the terminal.

## Goals

- Give operators one place to rotate Hermes-facing API keys across multiple
  Obol Stack machines and agent pods.
- Keep the bootstrap token in Kubernetes Secret data, never in generated
  ConfigMaps, plan files, PR text, logs, or git-tracked deployment files.
- Make the default `obol-agent` and CRD-created child Hermes agents support
  the same Bitwarden shape.
- Let `obol model setup` read and validate Bitwarden-backed provider secrets
  when the operator chooses Bitwarden as the API-key source.
- Preserve current local-first behavior: if Bitwarden is not configured,
  `obol stack up`, `obol agent init`, `obol model setup`, and child-agent
  creation continue to work exactly as they do today.
- Keep failure non-fatal. Hermes' Bitwarden integration already warns and
  continues with existing environment values when sync fails; Obol should not
  add stricter startup gates.

## Non-goals

- Do not store remote-signer wallet material in Bitwarden in v1. Wallets stay
  in encrypted V3 keystores and remote-signer Secrets/PVCs.
- Do not make LiteLLM read Bitwarden directly in v1. `obol model setup` may
  fetch a provider key from Bitwarden and then continue writing the active key
  into `llm/litellm-secrets`, because LiteLLM's process does not run Hermes'
  Bitwarden sync path.
- Do not add a new in-cluster Bitwarden controller or external-secrets
  dependency in v1.
- Do not make Bitwarden mandatory for local single-machine installs.
- Do not support OpenClaw Bitwarden wiring. The Bitwarden subcommands should
  reject `--runtime openclaw`.

## Proposed user flow

1. Operator creates a Bitwarden Secrets Manager machine account, grants read
   access to a project, and creates an access token.
2. Operator stores provider keys in that project using environment-variable
   names, for example `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
   `OPENROUTER_API_KEY`, `GITHUB_TOKEN`, or app-specific keys.
3. Operator runs:

   ```bash
   obol agent secrets bitwarden setup obol-agent
   ```

   Interactive mode prompts for the access token, server region, and project.
   Non-interactive mode accepts:

   ```bash
   obol agent secrets bitwarden setup obol-agent \
     --access-token "$BWS_ACCESS_TOKEN" \
     --server-url https://vault.bitwarden.com \
     --project-id <project-uuid>
   ```

4. Obol writes or updates an in-cluster Secret named `hermes-env` in the
   Hermes namespace with only the bootstrap token and optional server URL:

   ```yaml
   stringData:
     BWS_ACCESS_TOKEN: <redacted>
     BWS_SERVER_URL: https://vault.bitwarden.com
   ```

5. Obol rewrites the Hermes `config.yaml` with:

   ```yaml
   secrets:
     bitwarden:
       enabled: true
       access_token_env: BWS_ACCESS_TOKEN
       project_id: <project-uuid>
       server_url: https://vault.bitwarden.com
       cache_ttl_seconds: 300
       override_existing: true
       auto_install: true
   ```

6. Obol restarts the Hermes Deployment. On next start, Hermes resolves the
   Bitwarden project secrets into process environment variables.
7. Operator can then run provider setup without pasting the provider key:

   ```bash
   obol model setup --provider openai --api-key-source bitwarden
   ```

   Obol reads the selected Hermes instance's non-secret Bitwarden metadata,
   reads `BWS_ACCESS_TOKEN` from that instance's `hermes-env` Secret, fetches
   the expected provider secret, validates it by configuring/probing LiteLLM
   as `model setup` already does, writes the active value to
   `llm/litellm-secrets`, and redacts the key from all output.

## Runtime design

### Default stack-managed Hermes

The host-rendered Hermes path lives in `internal/hermes/hermes.go`. Today it
creates `hermes-api-server`, `hermes-config`, the data PVC, and the Deployment,
but it does not mount an operator-provided env Secret.

Add an optional `hermes-env` Secret reference to the main Hermes container:

```yaml
envFrom:
  - secretRef:
      name: hermes-env
      optional: true
```

This mirrors the child-agent render path and keeps undeclared installs
unchanged. The Secret should be managed by a new CLI helper rather than by
the generated values file, so the access token never lands in
`$OBOL_CONFIG_DIR/applications/hermes/<id>/values-hermes.yaml`.

Extend `generateConfig` to accept an optional Bitwarden config struct and
write `secrets.bitwarden` only when enabled. For disabled/default installs,
the generated YAML should stay byte-for-byte equivalent except for normal
marshal ordering changes covered by tests.

### CRD-created child Hermes agents

The controller-rendered path in `internal/serviceoffercontroller/agent_render.go`
already includes optional `envFrom.secretRef.name: hermes-env`, and the
agent-factory skill already knows how to create a `hermes-env` Secret.

Add `spec.secrets.bitwarden` to the Agent CRD, for example:

```yaml
spec:
  secrets:
    bitwarden:
      enabled: true
      projectID: <project-uuid>
      serverURL: https://vault.bitwarden.com
      accessTokenSecretName: hermes-env
      accessTokenKey: BWS_ACCESS_TOKEN
      overrideExisting: true
      cacheTTLSeconds: 300
      autoInstall: true
```

The controller should render these fields into Hermes `config.yaml`, but it
should not read or copy the access token. The token stays in `hermes-env`,
created either by the host CLI or by the agent-factory path.

Admission policy already allows agent-created Secrets named `hermes-env`
inside `agent-*` namespaces. Keep that constraint; do not broaden it for
arbitrary Secret names in v1.

### Agent factory

Extend `agent-factory/scripts/factory.py` with flags:

```text
--bitwarden-project-id <uuid>
--bitwarden-server-url <url>
--bitwarden-access-token-env BWS_ACCESS_TOKEN
--bitwarden-cache-ttl 300
--bitwarden-no-override-existing
```

The existing `--env KEY=VALUE` mechanism can already populate
`BWS_ACCESS_TOKEN` in `hermes-env`. The factory should refuse
`--bitwarden-project-id` unless the env Secret includes the selected
bootstrap-token key, so spawned children do not enter a misleading
"enabled but no token" state.

## CLI design

Add native Obol commands under the existing agent-management surface as thin
wrappers around manifest/config wiring:

```text
obol agent secrets bitwarden setup [instance-name] [--runtime hermes] [--access-token ...] [--server-url ...] --project-id ...
obol agent secrets bitwarden status [instance-name] [--runtime hermes]
obol agent secrets bitwarden disable [instance-name] [--runtime hermes]
```

Use `obol agent` here because the command mutates Obol-managed Kubernetes
Secrets, host deployment metadata, and rendered runtime config. Keep
`obol hermes` reserved for native Hermes CLI passthrough against a running pod.

Responsibilities:

- Reject non-Hermes runtimes; no OpenClaw support in v1.
- Validate the project ID shape and server URL.
- Apply or update `hermes-env` with `BWS_ACCESS_TOKEN` and `BWS_SERVER_URL`.
- Persist non-secret Bitwarden config in the host deployment metadata for
  default/named Hermes instances.
- Re-render `values-hermes.yaml` and sync/restart the selected Hermes
  instance.
- Redact `BWS_ACCESS_TOKEN` from all command output.
- `status` reports only Obol-managed state: metadata enabled/disabled,
  project/server fields, whether `hermes-env` exists, and whether it contains
  the expected bootstrap-token key. It does not shell into Hermes or call
  Bitwarden.

Do not shell into the pod and run `hermes secrets bitwarden setup` as the
primary path. That upstream wizard writes into pod-local files, while Obol
needs reproducible host-side deployment state and Kubernetes Secret wiring.
Operators who want upstream runtime diagnostics can still call native Hermes
through the passthrough, for example `obol hermes --agent <id> secrets
bitwarden status`, after the pod is running.

### Model setup integration

Extend `obol model setup` with an explicit API-key source:

```text
obol model setup --provider openai --api-key-source bitwarden [--agent obol-agent]
obol model setup --provider anthropic --api-key-source bitwarden [--agent obol-agent]
```

When `--api-key-source bitwarden` is selected, `model setup` maps provider to
the expected secret name:

| Provider | Bitwarden secret name |
| --- | --- |
| `openai` | `OPENAI_API_KEY` |
| `anthropic` | `ANTHROPIC_API_KEY` |

Flow:

1. Resolve the target Hermes instance, defaulting to `obol-agent`.
2. Load that instance's `bitwarden.yaml`.
3. Read `BWS_ACCESS_TOKEN` from the instance namespace's `hermes-env` Secret.
4. Fetch the provider secret from the configured Bitwarden project.
5. Validate the fetched key through the existing provider setup/probe path.
6. Patch `llm/litellm-secrets` and sync Hermes models exactly as the current
   `obol model setup --api-key ...` path does.

The fetched provider key is transient CLI process data. It should never be
written to host deployment metadata or printed.

## Config persistence

For host-managed Hermes instances, create a gitignored metadata file beside
the deployment:

```text
$OBOL_CONFIG_DIR/applications/hermes/<id>/bitwarden.yaml
```

Contents:

```yaml
enabled: true
project_id: <project-uuid>
server_url: https://vault.bitwarden.com
access_token_env: BWS_ACCESS_TOKEN
cache_ttl_seconds: 300
override_existing: true
auto_install: true
```

This file contains no access token, so it is safe in the user's config dir
but still should not be committed. `writeDeploymentFiles` reads it when
rendering Hermes config. `disable` flips `enabled: false` and restarts
Hermes, leaving the Kubernetes Secret in place so the operator can re-enable
without re-entering the token.

## Secret mapping

Use Bitwarden secret names exactly as Hermes expects: each secret name becomes
an environment variable name. Recommended project entries:

| Secret name | Consumer |
| --- | --- |
| `OPENAI_API_KEY` | Hermes tools, direct provider use, optional model setup |
| `ANTHROPIC_API_KEY` | Hermes tools or provider-specific flows |
| `OPENROUTER_API_KEY` | Hermes model/provider plugins |
| `GITHUB_TOKEN` | Agent tools that need GitHub API access |
| App-specific names | Child-agent workloads |

Do not store `BWS_ACCESS_TOKEN` in the same Bitwarden project. Hermes skips
overwriting its own bootstrap token, but keeping the bootstrap token outside
the fetched project reduces accidental self-reference and blast radius.

## Security model

- `BWS_ACCESS_TOKEN` is equivalent to read access for every secret in the
  Bitwarden project granted to the machine account. Treat it as a high-value
  bearer token.
- Store the token only in `hermes-env` Kubernetes Secret data, with no log
  echoing and no generated YAML-on-disk copy.
- Keep one project per trust boundary: default operator agent, child agents,
  and paid public agents should not all share one project unless they should
  all read the same secrets.
- Prefer machine accounts scoped to read-only access on a single project.
- Rotating an API key happens in Bitwarden; rotating the bootstrap token
  requires updating `hermes-env` and restarting the Hermes pod.
- If Bitwarden is unreachable, Hermes continues with existing environment
  values. Operators should monitor warning logs rather than relying on pod
  crash loops.

## Implementation plan

1. Add `hermes-env` optional `envFrom` to the default Hermes Deployment
   renderer and tests.
2. Add a small `BitwardenSecretsConfig` type in `internal/hermes`, plus
   load/save helpers for `<deploymentDir>/bitwarden.yaml`.
3. Extend `generateConfig` and `renderHermesConfig` to render
   `secrets.bitwarden` when configured.
4. Add CLI subcommands under `obol agent secrets bitwarden`.
5. Extend `obol model setup` with `--api-key-source bitwarden` and provider
   secret-name mapping.
6. Add Agent CRD schema for `spec.secrets.bitwarden` and controller render
   support.
7. Extend `agent-factory` flags to set the Agent spec and validate token
   presence in `hermes-env`.
8. Add docs covering setup, rotation, disable, model setup, and the boundary with
   LiteLLM/remote-signer secrets.

## Tests

- `internal/hermes` unit test: default values include optional
  `envFrom.secretRef.name: hermes-env`.
- `internal/hermes` unit test: `generateConfig` renders no
  `secrets.bitwarden` block unless enabled.
- `internal/hermes` unit test: enabled Bitwarden config renders the Hermes
  upstream field names exactly.
- CLI unit/helper tests: token redaction, metadata persistence, disable path.
- CLI unit/helper tests: `status` reports only metadata and Secret/key
  presence without calling Bitwarden or Hermes.
- `model setup` tests: Bitwarden source maps provider to the expected secret
  name, rejects missing Bitwarden config/token, validates fetched secret
  plumbing, redacts fetched values, and then follows the existing LiteLLM
  Secret patch path.
- Runtime validation tests: `obol agent secrets bitwarden * --runtime openclaw`
  returns a clear unsupported-runtime error.
- `internal/serviceoffercontroller` tests: Agent CRD Bitwarden fields render
  into the child Hermes ConfigMap and do not require controller Secret reads.
- Admission/CRD tests: existing `hermes-env` Secret constraint remains narrow.
- Smoke test: create a Bitwarden-enabled Hermes instance with a fake/local
  Secret value and verify the pod receives `BWS_ACCESS_TOKEN` plus a
  `secrets.bitwarden.enabled: true` config. Do not hit real Bitwarden in CI.

## Rollout

Ship behind explicit opt-in only:

1. Add renderer support and CLI setup/status/disable.
2. Add `obol model setup --api-key-source bitwarden` for provider keys.
3. Document default-agent setup.
4. Add child-agent CRD/factory support.
5. Later, consider LiteLLM support through External Secrets Operator or a
   first-class secret-sync sidecar if operators want cluster-wide provider
   keys outside Hermes.
