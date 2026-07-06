# Vendored plugin: pay_mcp

This directory is a **verbatim copy** of the `pay_mcp` hermes-agent plugin,
embedded into the obol-stack binary so the stack can seed it onto every agent's
profile by default (the same way embedded skills are seeded). See
`internal/embed/embed.go` → `CopyPlugins` / `GetEmbeddedPluginNames`.

## Source

- Upstream: hermes-agent, `plugins/pay_mcp/`
  (branch `feat/pay-mcp-plugin` on `bussyjd/hermes-agent`).
- Synced at hermes commit `d8ca58055` (relative-import fix for user-dir load).
- Files: `__init__.py`, `x402.py`, `rails.py`, `payment.py`, `recovery.py`,
  `plugin.yaml`.

## Why a copy (not a submodule)

obol pins the **upstream** `nousresearch/hermes-agent` image and does not build
it, so the plugin can't ride in via the image. Embedding the source in the
obol-stack binary lets `obol` drop it into the agent's user-plugins dir
(`/data/.hermes/plugins/pay_mcp/`, i.e. `$HERMES_HOME/plugins`) on stack-up /
agent reconcile, with no image rebuild. The agent then auto-loads it (the obol
config seeds `plugins.enabled: [pay_mcp]`) and it self-activates from the
`REMOTE_SIGNER_URL` already on the pod.

## Invariants (do not break when re-syncing)

- **Relative imports only.** Intra-package imports must be `from . import x402`,
  never `from plugins.pay_mcp import x402`. Hermes loads a user-dir plugin under
  the synthetic package name `hermes_plugins.pay_mcp`, and a stock image has no
  bundled `plugins.pay_mcp` to satisfy an absolute import. (Locked upstream by
  `tests/plugins/test_pay_mcp_userdir_load.py`.)
- **No secrets.** Only public addresses/constants. `import secrets` is the
  Python stdlib module (nonce generation), not a credential.
- **Inert by default.** `register()` builds no rails and wires nothing unless a
  signer is configured, so it is safe to ship everywhere.

## Re-syncing

Re-copy the six files from the upstream plugin dir, keep the relative imports,
and re-run `go test ./internal/embed/...` (the content-parity test checks the
expected files exist, are non-empty, and contain no absolute self-imports).
