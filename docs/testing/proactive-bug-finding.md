# Finding operator bugs before operators do

The Canary402 field report (10 issues) had one thing in common: every bug was
found by a real operator doing a **reasonable-but-unanticipated sequence on a
live deployment** — switch chain after registering, configure the tunnel
before selling, combine two documented flags, pass a path where an origin was
expected. None were "the code is wrong on the happy path." Example-based unit
tests never exercise those sequences, which is why humans found them first.

The approach that finds them without waiting for a human is **an automated
synthetic operator driving generated operation sequences, checked against
invariants instead of hand-written expectations.** A normal test asserts
"after X, status is Y"; an invariant asserts something that must hold after
*any* sequence. Once the invariants exist, anything can generate the traffic.

This document describes the four layers, cheapest first, and records what the
first run found.

## The invariants (oracles)

These are the properties that, if they ever break, mean a bug — regardless of
how the system got there:

1. **Derived state is a pure function of current spec.** After any spec
   mutation, every status field / published document / on-chain artifact
   reflects the *current* spec, never a stale prior value. (The worst field
   bug — cross-chain AgentID reuse — was this invariant broken for one field.)
2. **Generated names are injective over identity.** Any resource in a shared
   namespace has a name that is unique per owning offer's `(namespace, name)`.
3. **Status conditions reflect observed reality, not apply-success.** `Ready`,
   `RoutePublished`, `Registered`, tunnel "active", etc. are True only when a
   real probe confirms the dataplane/on-chain fact — and flip back False when
   the fact stops holding.
4. **Public documents contain only public-intent fields.** No internal CR
   field (agent objective, tool addresses, cluster-internal hostnames, model
   endpoints, raw Go errors) reaches openapi.json / agent-registration.json /
   skill.md / catalog / 402 body without an explicit public gate.
5. **Every URL emitted is fetchable as written** (scheme, host, port, path).
6. **No request reaches a paid upstream without a corresponding settle**, and
   no payment authorized for one (route, price, network) passes on another.

## The four layers

| Layer | What | Cost | Catches |
|---|---|---|---|
| 1. Invariant oracles | The properties above, written once as reusable checks | one-time | shared by all layers below |
| 2. Stateful model tests | Generated op-sequences run against the reconciler with the fake client; assert invariants after each step | CI, seconds | state-machine / staleness / name / status bugs |
| 3. Canary cluster | Nightly ephemeral k3s; the **real CLI** driven through a scenario + flag-combination matrix; invariants checked as black-box probes | nightly, minutes | everything mocks can't reach: Traefik route-age ties, router rejection, header stripping, nonce lag, real 402/settle |
| 4. Agent audit | Periodic hostile-operator LLM sweep over the code, each finding adversarially verified, output = a ranked triage list | weekly, on-demand | judgment-layer bugs with no mechanical oracle: misleading messages, unclear partial-failure reporting, docs-vs-behavior drift |

Layer 2 is the highest yield per unit cost and belongs in CI. Layer 3 is the
only layer that reproduces the six field bugs that are invisible without real
infrastructure. Layer 4 is the only layer that finds "this output would
mislead an operator," which has no assertion.

### Running each layer

- **Layer 2** — `go test ./internal/serviceoffercontroller/...`. The first
  invariant oracle lives in `name_injectivity_test.go` (property test over
  `sharedNamespaceNameBuilders`); add a builder there and any future
  shared-namespace child is covered.
- **Layer 3** — `hack/canary/run-canary.sh` (ephemeral k3s + scenario matrix +
  black-box invariant probes). Runnable skeleton; wire into nightly CI.
- **Layer 4** — `Workflow({ name: "obol-audit" })` from Claude Code (the
  `.claude/workflows/obol-audit.js` fan-out: N hostile-operator lenses → 2
  adversarial refuters each → ranked survivors). Re-run after any large
  change; feed the `KNOWN` list the already-fixed issues so it doesn't
  re-report them.

## First run — 2026-07-16

Layer 4 was run against the integration branch with all six Canary402 fixes
merged in (so it hunted for *new* bugs beyond the field report). 8 lenses × 2
adversarial refuters. **11 findings survived** verification; 3 were rejected by
a refuter (correctly — one was subsumed by the known cross-chain issue, one was
unreachable via the ForwardAuth path, one was harmless). The layer-1
name-injectivity oracle independently found the same #1 grant bug the agent
sweep did — two methods, one bug, high confidence.

| Sev | Finding | Location | Lens | Status |
|-----|---------|----------|------|--------|
| CRITICAL | `--price`/`--per-request` never validated → `decimalToAtomic` mis-prices ($0 on `0,01`) or panics on every request (`abc`/`""`/`$0.01`) | `internal/x402/chains.go` | cli-validation | **fixing** |
| HIGH | ReferenceGrant name `ns-name` dash-join not injective — `(foo-bar, baz)` vs `(foo, bar-baz)` collide in shared x402 ns (HTTP 500) | `internal/serviceoffercontroller/render.go` | name-injectivity | **fixed** (PR #767) |
| HIGH | PurchaseRequest `Ready` never re-checked once True — sidecar outage after configure is invisible | `internal/serviceoffercontroller/purchase.go` | status-truth | open |
| HIGH | Agent `Ready` + `status.WalletAddress` go True before the remote-signer holding the key is up | `internal/serviceoffercontroller/agent.go` | status-truth | open |
| HIGH | Raw Go network errors (internal cluster DNS/IP) leak through the public unauthenticated job-status endpoint | `internal/jobbroker/server.go` | public-surface-leak | open |
| HIGH | No payment dedup — concurrent duplicate `X-PAYMENT` all reach the paid upstream, only one settles (free ride) | `internal/x402/forwardauth.go` | payment-verification | open |
| HIGH | `SetMetadata`/`SetAgentURI` never check `receipt.Status` — a mined-but-reverted tx is reported as success | `internal/erc8004/client.go` | tx-sequencing | open |
| HIGH | Registration recovery by owner+URI trusts a historical Registered event without re-verifying current ownership | `cmd/obol/sell.go` | tx-sequencing | open |
| MEDIUM | `status.AgentResolution` survives a `spec.type` flip away from agent → stale model advertised in public catalog forever | `internal/serviceoffercontroller/agent_resolver.go` | state-staleness | open |
| MEDIUM | `obol tunnel status` reports "active" even when the public-reachability probe just failed | `internal/tunnel/tunnel.go` | status-truth | open |
| MEDIUM | SIWX JSON 401 challenge hardcodes `https://` (ignores `isLocalHost`), unlike every other URL builder in the package | `internal/x402/authgate.go` | url-construction | open |

Four of the eleven are the **status-truth** invariant (#3) broken in a new
place — the same class as the field report's "offer Ready while Traefik
rejected the router." That clustering is the signal to prioritize invariant #3
as a layer-2 sweep: gate every readiness condition on an observed probe, the
way `RoutePublished` now is.

## Why this beats more unit tests

Unit tests encode what the author expected. These bugs live precisely where the
author's expectation was wrong, so an assertion written from the same mental
model can't catch them. Invariants encode what must be true for *any* input, and
the three generators (sequence fuzzer, canary CLI matrix, hostile-operator
agent) explore the input space the author didn't think to.
