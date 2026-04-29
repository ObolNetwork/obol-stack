package agentruntime

// RemoteSignerChartVersion is the single source of truth for the
// `remote-signer` Helm chart pin used by both Hermes and OpenClaw
// deployments. It MUST be updated as a single edit; bumping it here
// updates every consumer in lockstep.
//
// Chart 0.3.1 ships remote-signer image `v0.2.0`, which accepts the
// canonical-string signer contract (chain_id, value, etc. serialized
// as JSON strings) introduced by PR #359. Chart 0.3.0 ships `v0.1.0`,
// which only accepts the legacy u64 contract and breaks `obol sell
// register` for current obol-stack with HTTP 422 "chain_id: invalid
// type: string \"84532\", expected u64".
//
// renovate: datasource=helm depName=remote-signer registryUrl=https://obolnetwork.github.io/helm-charts/
const RemoteSignerChartVersion = "0.3.1"
