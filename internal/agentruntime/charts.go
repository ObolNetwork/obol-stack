package agentruntime

// RemoteSignerChartVersion is the single source of truth for the
// `remote-signer` Helm chart pin used by both Hermes and OpenClaw
// deployments. It MUST be updated as a single edit; bumping it here
// updates every consumer in lockstep.
//
// Chart 0.4.0 ships remote-signer image `v0.4.0`, which honours
// `SIGNER__AUTH__TOKEN` (the bearer token the controller mints into the
// keystore Secret and injects via env). Canonical Ethereum recovery-id
// signatures (`v=27/28`) from `/sign/.../message`, `/sign/.../typed-data`,
// and `/sign/.../hash` have been the baseline since chart 0.3.2 / image
// v0.3.0 — earlier images returned `v=0/1` (alloy y-parity) and forced
// the buy.py caller to renormalize for EIP-712 / ERC-3009 verifiers like
// USDC `transferWithAuthorization`.
//
// 0.4.0 also renders `spec.strategy` from `.Values.strategy` (defaulting to
// `Recreate`), so the RWO-keystore singleton no longer needs an imperative
// post-sync `kubectl patch` to stay off RollingUpdate. `values-remote-signer.yaml`
// pins `strategy.type: Recreate` explicitly so the intent is visible at the
// obol-stack layer and survives any future change to the chart default.
//
// renovate: datasource=helm depName=remote-signer registryUrl=https://obolnetwork.github.io/helm-charts/
const RemoteSignerChartVersion = "0.4.0"
