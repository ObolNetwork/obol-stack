# Per-agent remote-signer keystore isolation

Status: **design / RFC** — no controller code yet. Companion to the RBAC scoping
in `security/controller-secrets-rbac-scope` (PR #570), which is the immediate
hardening; this doc tracks the deeper isolation follow-up that #570 explicitly
defers.

## Problem

Every agent's signer keystore is a Secret named `remote-signer-keystore`, one
per agent namespace (`agent-<petname>`, `hermes-obol-agent`, …). After #570 the
`serviceoffer-controller` ClusterRole reads it with:

```yaml
resourceNames: ["litellm-secrets", "hermes-api-server", "remote-signer-keystore"]
verbs: ["get", "delete"]
```

`resourceNames` on a **ClusterRole** matches a name in *any* namespace. Because
all agents share the name `remote-signer-keystore`, the controller's
ServiceAccount can `get` (and `delete`) **every agent's keystore Secret** —
which contains the V3 keystore JSON **and** the decryption password
(`internal/serviceoffercontroller/agent_wallet.go::buildSignerKeystoreSecret`).

There are two distinct sub-risks, verified in code:

1. **Standing cross-agent read.** A compromised controller pod can `GET` every
   agent's keystore + password → derive every agent's signer key → drain every
   agent wallet. This is the blast radius.
2. **In-process custody at mint.** `openclaw.GenerateKeystoreInMemory()`
   (`internal/openclaw/wallet.go:136`) runs *inside the controller process*. The
   controller generates and holds the private key + password at provisioning
   time, regardless of RBAC.

Note: in normal operation the controller reads only the **address annotation**
(`obol.org/wallet-address`) on the reuse path — never the key data after mint
(`ensureSignerKeystore`). But `GET` on a Secret returns all keys, so the
standing capability exposes the key material even though the code does not use
it.

## Threat model

- **In scope:** a compromised/abused `serviceoffer-controller` (supply-chain on
  its image, RCE in a reconcile path, a malicious ClusterRole edit) reading or
  deleting *other* agents' signer keys.
- **Out of scope:** an attacker who already controls a specific agent's own
  pod/namespace (they already hold that agent's key by design).

The controller is trusted infra, so this is defense-in-depth / blast-radius
reduction, not a remotely-exploitable hole. It matters because the asset is
spendable signer keys for N tenants behind one shared identity.

## Options

### Option 0 — Accept + document (baseline)
Keep #570 as the final state; document that the controller is in the keystore
TCB. Zero additional work. Rejected as the *end* state because the asset
(multi-tenant signer keys) warrants real isolation, but it is the honest
fallback if Option B proves too costly for the current milestone.

### Option A — Per-namespace Role + RoleBinding minted by the controller
Remove keystore verbs from the ClusterRole; at provisioning, the controller
creates in `agent-<x>` a Role (get/delete on the keystore in that ns) + a
RoleBinding to the controller's SA.

**Rejected — this is isolation theater.** The controller manages *all* agents,
so it would mint a RoleBinding for itself in *every* agent namespace, retaining
full reach. It also needs cluster-wide `create` to bootstrap the keystore
before the Role exists (chicken-and-egg). Net: more RBAC surface, same reach.

### Option B — Agent self-mints the keystore (controller out of custody) — RECOMMENDED
The keypair is generated **inside the agent's own namespace/pod**, never in the
controller. The controller never gains `get`/`create`/`delete` on
`remote-signer-keystore`.

Moving parts:
1. **In-pod keystore generation.** Either (a) the `remote-signer` image
   self-generates a keystore on first boot when none is mounted, or (b) an init
   container runs a tiny keystore-gen tool (reuse the logic in
   `openclaw.GenerateKeystoreInMemory`, shipped as a minimal binary) and writes
   the Secret. **OPEN QUESTION** — see below.
2. **Namespaced write RBAC for the agent SA.** The controller creates, once per
   agent namespace, a Role granting the *agent's* ServiceAccount
   `create`/`get` on `remote-signer-keystore` in its own namespace, plus a
   RoleBinding. The agent SA can only ever touch its own namespace — true
   isolation.
3. **Address reporting via a non-secret channel.** Today the controller learns
   the address from `mat.Address` at mint. With self-mint it must learn it
   without reading the Secret. Options: the agent patches
   `Agent.status.walletAddress` (its SA gets `patch` on `agents/status` in its
   ns), or writes a non-secret ConfigMap the controller reads. Either avoids a
   keystore `GET`.
4. **Controller RBAC shrinks** to: `litellm-secrets` get (fixed ns),
   `hermes-api-server` get/create/delete, and **no** `remote-signer-keystore`
   access at all.

### Option C — Unique per-agent keystore names + per-namespace Roles
Name the keystore `<agent>-remote-signer-keystore`. On its own this does not
help a ClusterRole (resourceNames is a fixed list, no wildcards) and still needs
per-namespace Roles → collapses into Option A's theater problem. Only useful as
a hygiene change layered onto Option B.

## Recommendation

**Option B, phased:**

- **Phase 1 (decision-gated):** confirm the in-pod mint mechanism (B.1). If the
  remote-signer image can self-generate, this is small; if not, build the
  init-container mint tool.
- **Phase 2:** controller mints the namespaced Role/RoleBinding for the agent
  SA + the address-reporting channel; switch `ensureAgentWallet` to *wait for*
  the agent-reported address instead of minting it.
- **Phase 3:** drop `remote-signer-keystore` from the controller ClusterRole;
  add a guard test asserting the controller has **no** get/create/delete on it.

If Phase 1 reveals B is disproportionately expensive for this milestone, fall
back to Option 0 and revisit — but do not ship Option A as a substitute.

## Open questions (resolve before Phase 1 code)

1. Does `ghcr.io/obolnetwork/remote-signer:v0.3.0` support generating a keystore
   on first boot when the keystore dir is empty? (Check the `ObolNetwork`
   remote-signer repo / chart.) If yes → no init container needed.
2. Is `Agent.status.walletAddress` the right address channel, and can the agent
   SA be granted `patch` on `agents/status` scoped to its own namespace?
3. Does anything else consume the keystore Secret directly besides the
   remote-signer pod? (Grep across runtimes before removing controller access.)

## Acceptance criteria

- Controller ClusterRole has **no** verbs on `remote-signer-keystore` (guarded
  by an extension of `TestServiceOfferControllerSecretRBAC_Scoped`).
- The agent SA's keystore write access is a namespaced **Role**, never a
  ClusterRole.
- `obol agent init` still populates `Agent.status.walletAddress`; teardown still
  cleans up; release-smoke sell→buy→teardown stays green.
- Pre-production: no keystore migration needed (greenfield).
