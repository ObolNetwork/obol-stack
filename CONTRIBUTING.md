# Obol Stack - Developer Rules

These rules are non-negotiable for implementation work. The source specs are [SPEC.md](SPEC.md), [ARCHITECTURE.md](ARCHITECTURE.md), and [BEHAVIORS_AND_EXPECTATIONS.md](BEHAVIORS_AND_EXPECTATIONS.md).

## 1. Keep Specs and Behavior Together

Any behavior change must update at least one of: `SPEC.md`, `BEHAVIORS_AND_EXPECTATIONS.md`, `features/*.feature`, or `docs/adr/*.md`.

Run before handing off docs:

```bash
git diff --check
```

## 2. Do Not Add MVP Smart Contracts

The MVP uses deployed ERC-20, Permit2/EIP-3009, x402, and ERC-8004 contracts. Do not introduce new contracts for escrow, fees, staking, slashing, or registry logic without a new ADR.

## 3. Treat CRDs as Intent

CLIs apply specs. Controllers own status and child resources. Do not mark CRDs ready from CLI code except in tests.

Key CRDs:

- `ServiceOffer`
- `PurchaseRequest`
- `Agent`
- `AgentIdentity`
- `RegistrationRequest`

## 4. Keep Buyer Runtime Signer-Free

`x402-buyer` must never receive a private key, seed phrase, remote-signer URL, or wallet keystore. It may only receive pre-signed auths and public payment metadata.

## 5. Do Not Call PurchaseRequest Escrow

`PurchaseRequest` is a bounded pre-signed authorization pool. Naming, docs, UI copy, and tests must preserve that distinction.

## 6. Preserve the Public Tunnel Allowlist

Public tunnel routes may expose only:

- `/services/*`
- `/skill.md`
- `/api/services.json`
- `/.well-known/agent-registration.json`
- `/`

Frontend, eRPC, LiteLLM, and monitoring must remain local-only or hostname-restricted to `obol.stack`.

## 7. Do Not Overclaim Agent Isolation

`Agent.spec.skills` seeds skills into a Hermes profile/home. It is not sandboxing. Any service that claims privacy, medical safety, legal safety, or tool confinement needs explicit policy/sandbox implementation and tests.

## 8. Keep OBOL Asset Metadata Complete

OBOL-priced offers must carry address, symbol, decimals, transfer method, and EIP-712 domain metadata. Buyer paths must reject wrong-token purchases before signing.

## 9. Use Existing Flow Harnesses

Prefer existing Go tests and `flows/` before adding ad hoc scripts.

Common checks:

```bash
go test ./cmd/obol ./internal/serviceoffercontroller ./internal/x402 ./internal/x402/buyer ./internal/erc8004 ./internal/agentcrd ./internal/agentruntime ./internal/tunnel -count=1
bash -n flows/*.sh
```

Integration gates live in `flows/README.md`; do not duplicate flow plumbing.

## 10. Keep Generated Specs Token-Efficient

Use stable IDs, compact tables, and cross-links. Do not paste long code excerpts or repeated architecture prose into spec docs.
