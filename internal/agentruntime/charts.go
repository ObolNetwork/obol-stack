package agentruntime

// RemoteSignerChartVersion is the single source of truth for the
// `remote-signer` Helm chart pin used by both Hermes and OpenClaw
// deployments. It MUST be updated as a single edit; bumping it here
// updates every consumer in lockstep.
//
// Chart 0.3.2 ships remote-signer image `v0.2.1`, which emits canonical
// Ethereum recovery-id signatures (`v=27/28`) from `/sign/.../message`,
// `/sign/.../typed-data`, and `/sign/.../hash`. Earlier images returned
// `v=0/1` (alloy y-parity), which was rejected by EIP-712 / ERC-3009
// verifiers like USDC `transferWithAuthorization` and forced the buy.py
// caller to renormalize. Chart 0.3.1 ships `v0.2.0`, which is otherwise
// identical but exposes the y-parity bug for typed-data signers.
//
// renovate: datasource=helm depName=remote-signer registryUrl=https://obolnetwork.github.io/helm-charts/
const RemoteSignerChartVersion = "0.3.2"
