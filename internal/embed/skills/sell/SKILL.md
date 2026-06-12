---
name: sell
description: "Sell access to services via x402 payment gating. Create ServiceOffer CRDs that automatically health-check upstreams, create payment-gated routes, and optionally pull models and register on ERC-8004. Supports inference, HTTP, fine-tuning, agent, and skill (paid skill-bundle download) service types."
metadata: { "openclaw": { "emoji": "\ud83d\udcb0", "requires": { "bins": ["python3"] } } }
---

# Sell

Sell access to services via ServiceOffer custom resources. Each ServiceOffer describes a service to expose publicly with x402 micropayments (USDC via EIP-3009 or OBOL via Permit2, selected with `--token`). The cluster's `serviceoffer-controller` performs reconciliation; `monetize.py process` now waits for controller convergence and refreshes `/skill.md`.

## When to Use

- Exposing a local Ollama model for paid inference
- Creating payment-gated routes for any upstream service
- Selling one of your own skills as a paid, hash-pinned bundle download (`type=skill` — see "Selling a Skill Bundle" below)
- Checking the status of monetized services
- Listing or deleting existing service offers
- Processing pending offers that haven't been fully reconciled

## When NOT to Use

- Read-only Ethereum queries — use `ethereum-networks`
- Signing transactions — use `ethereum-local-wallet`
- Cluster diagnostics — use `obol-stack`

## Quick Start

```bash
# List all service offers across namespaces
python3 scripts/monetize.py list

# Create a new offer to monetize a local Ollama model
python3 scripts/monetize.py create my-inference \
  --model qwen3.5:9b \
  --runtime ollama \
  --upstream ollama \
  --namespace llm \
  --port 11434 \
  --per-request 0.001 \
  --network base-sepolia \
  --pay-to 0xYourWalletAddress

# Check status of an offer
python3 scripts/monetize.py status my-inference --namespace llm

# Process all pending offers (waits for controller convergence)
python3 scripts/monetize.py process --all

# Process a single offer
python3 scripts/monetize.py process my-inference --namespace llm

# Delete an offer (cascades Middleware + HTTPRoute via OwnerRef)
python3 scripts/monetize.py delete my-inference --namespace llm
```

## Commands

| Command | Description |
|---------|-------------|
| `list` | List all ServiceOffer CRs across namespaces |
| `status <name> --namespace <ns>` | Show conditions and endpoint for one offer |
| `create <name> --model ... --namespace ...` | Create a new ServiceOffer CR |
| `process <name> --namespace <ns>` | Wait for a single offer to converge |
| `process --all` | Wait for all non-Ready offers to converge |
| `delete <name> --namespace <ns>` | Delete an offer and its owned resources |

## Selling a Skill Bundle (type=skill)

A skill — a directory with a top-level `SKILL.md` plus optional `scripts/`
and `references/` — can itself be sold as a single downloadable, ratable
unit. A `type=skill` ServiceOffer points at a ConfigMap holding the gzipped
bundle; the `serviceoffer-controller` hash-verifies the bytes, renders a
static bundle server (`so-<offer>-bundle`, busybox httpd on port 8080) in the
offer's namespace, and gates `/services/<offer>/*` behind x402 like any
other offer. Buyers pay the flat `perRequest` price per download.

Two ways to publish:

1. **Host CLI (operator runs it)**: `obol sell skill <offer> --from <dir>`
   or `--from-embedded <skill-name>` packs canonically, writes the ConfigMap
   (server-side apply), and creates the offer in one shot. Prefer this when
   a human is driving.
2. **Raw K8s objects (you, the agent)**: create the bundle ConfigMap and the
   ServiceOffer yourself with the RBAC you already have. Documented below.

### Where your objects must live (RBAC)

Your ServiceAccount can CRUD `serviceoffers` cluster-wide, but ConfigMap
writes are granted ONLY in your own namespace (`hermes-obol-agent`) through
the namespaced `hermes-skill-publish` Role (verbs: create/get/update/patch —
no list, watch, or delete). The controller reads the bundle ConfigMap from
the **offer's** namespace. Consequence: create BOTH the ConfigMap AND the
ServiceOffer in your own namespace, side by side.

### Packaging contract (deterministic)

The artifact is a gzipped tar of the skill directory. The canonical packer
(`obol sell skill` / `internal/skillpkg.Pack`) normalizes:

- A top-level `SKILL.md` is REQUIRED — a bundle without it is not a skill.
- `__pycache__/` dirs and `*.pyc` files are skipped; symlinks are rejected.
- Entries are sorted by slash-separated path; tar format is USTAR.
- File mode normalized to `0644` (`0755` when any exec bit is set on the
  source); directories `0755`; mtime epoch 0; uid/gid 0; empty uname/gname.
- gzip at max compression with an empty name, mtime 0, OS byte 255.
- **Cap**: the compressed bundle must be <= 900000 bytes (`MaxSkillBundleBytes`,
  enforced by the CLI and again by the controller). Trim the skill if over.
- `spec.skill.sha256` is the lowercase hex SHA-256 of the **gzipped bytes** —
  it MUST equal the hash of the exact bytes stored in
  `binaryData["bundle.tar.gz"]`, or the controller refuses to publish
  (`BundleHashMismatch`).

Determinism caveat: the same source tree packed by the canonical Go packer
always yields the same hash (audit-friendly, keeps an on-chain pin stable
across republish). DEFLATE output is implementation-specific, so a Python
repack of identical files produces a different — still valid — hash. The
binding contract is only ever `sha256(uploaded bytes) == spec.skill.sha256`;
whatever bytes you upload, hash those.

### Pack + publish from inside the pod

One self-contained script: pack (mirroring the canonical normalization),
upload the ConfigMap, create the offer. Adjust `SRC`, names, price, and
`payTo` (your wallet from `signer.py accounts`).

```python
import base64, gzip, hashlib, io, json, os, sys, tarfile

SKILLS = os.environ.get("OBOL_SKILLS_DIR", "/data/.hermes/obol-skills")
sys.path.insert(0, os.path.join(SKILLS, "obol-stack", "scripts"))
from kube import load_sa, make_ssl_context, api_get, api_post, api_patch

SRC = os.path.join(SKILLS, "my-skill")   # must contain SKILL.md at top level
NS = "hermes-obol-agent"                 # YOUR namespace — see RBAC note
OFFER, VERSION = "my-skill", "0.1.0"
CM = f"{OFFER}-skill-bundle"
PAY_TO = "0xYourWalletAddress"

# -- canonical pack ----------------------------------------------------
if not os.path.isfile(os.path.join(SRC, "SKILL.md")):
    raise SystemExit("not a skill: top-level SKILL.md missing")
paths = []
for root, dirs, files in os.walk(SRC):
    dirs[:] = sorted(d for d in dirs if d != "__pycache__")
    for name in sorted(dirs + files):
        p = os.path.join(root, name)
        if os.path.islink(p):
            raise SystemExit(f"symlink not allowed: {p}")
        if not name.endswith(".pyc"):
            paths.append(p)
paths.sort(key=lambda p: os.path.relpath(p, SRC).replace(os.sep, "/"))
tar_buf = io.BytesIO()
with tarfile.open(fileobj=tar_buf, mode="w", format=tarfile.USTAR_FORMAT) as tf:
    for p in paths:
        rel = os.path.relpath(p, SRC).replace(os.sep, "/")
        info = tarfile.TarInfo(rel + "/" if os.path.isdir(p) else rel)
        info.mtime = info.uid = info.gid = 0
        info.uname = info.gname = ""
        if os.path.isdir(p):
            info.type, info.mode = tarfile.DIRTYPE, 0o755
            tf.addfile(info)
        else:
            data = open(p, "rb").read()
            info.size = len(data)
            info.mode = 0o755 if os.stat(p).st_mode & 0o111 else 0o644
            tf.addfile(info, io.BytesIO(data))
gz_buf = io.BytesIO()
# filename="" keeps the gzip FNAME header empty (determinism rule).
with gzip.GzipFile(filename="", fileobj=gz_buf, mode="wb", compresslevel=9, mtime=0) as gz:
    gz.write(tar_buf.getvalue())
bundle = gz_buf.getvalue()
if len(bundle) > 900000:
    raise SystemExit(f"bundle {len(bundle)} bytes > 900000-byte cap — trim the skill")
sha = hashlib.sha256(bundle).hexdigest()
print(f"bundle: {len(bundle)} bytes  sha256={sha}")

# -- bundle ConfigMap (create, or merge-patch when it exists) ----------
token, _ = load_sa()
ctx = make_ssl_context()
cm = {"apiVersion": "v1", "kind": "ConfigMap",
      "metadata": {"name": CM, "namespace": NS},
      "binaryData": {"bundle.tar.gz": base64.b64encode(bundle).decode()}}
try:
    api_get(f"/api/v1/namespaces/{NS}/configmaps/{CM}", token, ctx, quiet=True)
    api_patch(f"/api/v1/namespaces/{NS}/configmaps/{CM}", cm, token, ctx)
except SystemExit:
    api_post(f"/api/v1/namespaces/{NS}/configmaps", cm, token, ctx)

# -- the ServiceOffer ---------------------------------------------------
offer = {
    "apiVersion": "obol.org/v1alpha1", "kind": "ServiceOffer",
    "metadata": {"name": OFFER, "namespace": NS},
    "spec": {
        "type": "skill",
        "skill": {"name": OFFER, "version": VERSION, "sha256": sha,
                  "bundleConfigMap": CM,
                  "displayName": "My Skill",
                  "description": "What the skill does, one line."},
        # Anti-spoof invariants — the controller rejects anything else:
        "upstream": {"service": f"so-{OFFER}-bundle",  # MUST be so-<offer>-bundle
                     "namespace": NS,                  # MUST equal offer namespace
                     "port": 8080,                     # MUST be 8080
                     "healthPath": "/skill.json"},
        "payment": {"scheme": "exact", "network": "base-sepolia",
                    "payTo": PAY_TO, "maxTimeoutSeconds": 300,
                    "price": {"perRequest": "0.25"}},
        "registration": {"enabled": False},
    },
}
api_post(f"/apis/obol.org/v1alpha1/namespaces/{NS}/serviceoffers", offer, token, ctx)
print(f"ServiceOffer {NS}/{OFFER} created")
```

Equivalent standalone YAML (for an operator with kubectl):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-skill-skill-bundle
  namespace: hermes-obol-agent
binaryData:
  bundle.tar.gz: <base64 of the gzipped bundle, <=900000 bytes raw>
---
apiVersion: obol.org/v1alpha1
kind: ServiceOffer
metadata:
  name: my-skill
  namespace: hermes-obol-agent
spec:
  type: skill
  skill:
    name: my-skill            # ^[a-z0-9][a-z0-9-]*$, max 64
    version: "0.1.0"          # ^[A-Za-z0-9][A-Za-z0-9._-]*$, max 64
    sha256: "<64-char lowercase hex of the gzipped bundle bytes>"
    bundleConfigMap: my-skill-skill-bundle
    displayName: "My Skill"
    description: "What the skill does, one line."
  upstream:
    service: so-my-skill-bundle     # MUST be so-<offer-name>-bundle
    namespace: hermes-obol-agent    # MUST equal the offer namespace
    port: 8080                      # MUST be 8080
    healthPath: /skill.json
  payment:
    scheme: exact
    network: base-sepolia
    payTo: "0xYourWalletAddress"
    maxTimeoutSeconds: 300
    price:
      perRequest: "0.25"
  registration:
    enabled: false
```

Note: apply the ConfigMap **server-side** (`kubectl apply --server-side`).
Client-side apply writes the whole object into the last-applied-configuration
annotation, which blows the 256KiB annotation cap for bundles over ~190KB.

### Watch reconciliation

```bash
python3 scripts/monetize.py status my-skill --namespace hermes-obol-agent
```

The usual ladder applies (ModelReady is `True/Skipped` for skills). Before
`UpstreamHealthy` can pass, the controller verifies the bundle; skill-specific
`UpstreamHealthy=False` reasons:

| Reason | Meaning / fix |
|--------|---------------|
| `InvalidSkillUpstream` | `spec.upstream` is not the controller-rendered bundle server (`so-<offer>-bundle` / offer namespace / port 8080). Fix the spec — a skill offer may only ever advertise its own bundle server. |
| `BundleMissing` | `spec.skill.bundleConfigMap` not found in the offer's namespace. Create it (controller requeues automatically). |
| `BundleTooLarge` | Compressed bytes exceed 900000. Trim the skill and republish. |
| `BundleHashMismatch` | `sha256(binaryData["bundle.tar.gz"]) != spec.skill.sha256`. Re-hash the exact uploaded bytes. |

Republishing new bundle bytes + updating `spec.skill.sha256` rolls the bundle
server pod automatically (content-hash annotation).

### What buyers see (pre-purchase integrity)

An unpaid `GET /services/<offer>/bundle.tar.gz` returns 402 with the skill
identity in `accepts[0].extra.skill`:

```json
{"name": "my-skill", "version": "0.1.0", "sha256": "<64-hex>"}
```

Point buyers at that `extra.skill.sha256` BEFORE they pay: it is the same
hash the controller verified against the served bytes, so after a paid
download they verify with `sha256sum bundle.tar.gz`. Paid paths on the route:
`/services/<offer>/bundle.tar.gz` (the artifact) and
`/services/<offer>/skill.json` (metadata JSON: name, version, sha256,
displayName, description, offer, namespace). Each request costs one
`perRequest` payment.

### Alternative: sell the skill as a live service instead

If buyers should *invoke* the skill rather than download it, sell your agent
with the skill installed — thin sugar over the existing `type=agent` path
(host CLI, run by the operator):

```bash
obol sell skill <offer-name> --as-service --agent <agent-name> \
  --skill-name <skill> --skill-version 0.1.0 --price 0.001 --chain base-sepolia
```

The skill must already be in the Agent CR's skill list (`obol agent new
... --skills`); the offer carries no `spec.skill` block and the 402
surfaces `extra.agentSkills` via the normal agent machinery.

### On-chain integrity + rating (OPERATOR-submitted — never you)

Skill hash pinning and ratings ride ERC-8004 with the tag convention
`tag1="asr:skill"`, `tag2="eip155:<chainId>:<identityRegistry>:<agentId>:<name>@<version>"`.
The obol CLI only PRINTS calldata; a human operator submits it with their
own wallet. The controller never signs, and **you must never sign or submit
these transactions either** — surface the commands to the user instead:

```bash
# Pin sha256(bundle) under metadata key skill.sha256:<name>@<version>
obol skills calldata set-hash --agent-id <id> --skill my-skill@0.1.0 --bundle <bundle.tar.gz> --chain base-sepolia

# Rate a skill 0-100 (buyer side; self-feedback from the owner reverts on-chain)
obol skills calldata feedback --agent-id <seller-id> --skill my-skill@0.1.0 --value 95 --chain base-sepolia
```

## Reconciliation Flow

The `serviceoffer-controller` drives these stages:

1. **ModelReady** — Pull the model via Ollama API (if runtime is ollama)
2. **UpstreamHealthy** — Health-check the upstream service
3. **PaymentGateReady** — Create a Traefik ForwardAuth Middleware pointing at x402-verifier
4. **RoutePublished** — Create a Gateway API HTTPRoute with the middleware
5. **Registered** — (Optional) create a `RegistrationRequest`; the controller publishes `/.well-known/agent-registration.json` and performs ERC-8004 side effects when configured
6. **Ready** — All conditions met, service is live

The x402-verifier watches published ServiceOffers directly, so deleting or pausing the offer removes enforcement without a separate rendered route object.

## Payment (x402-aligned)

- `payment.payTo`: USDC recipient wallet address (x402: payTo)
- `payment.network`: Chain for payments (e.g., base-sepolia, base)
- `payment.price.perRequest`: Flat per-request price in USDC
- `payment.price.perMTok`: Per-million-tokens price in USDC (inference)
- `payment.price.perHour`: Per-compute-hour price in USDC (fine-tuning)
- `payment.scheme`: Payment scheme (default: exact)

Phase 1 pricing behavior:

- `perRequest` or `price` wins if explicitly provided
- otherwise `perMTok` is accepted and converted to a temporary enforced request price using `perMTok / 1000`
- the fixed approximation input is `approxTokensPerRequest = 1000`
- the original `perMTok` value is still persisted in pricing metadata and shown in status output

## Architecture

```
ServiceOffer CR (obol.org/v1alpha1)
    |
    +-- serviceoffer-controller
    |     +-- Health-check upstream
    |     +-- Create Middleware (ForwardAuth -> x402-verifier)
    |     +-- Create HTTPRoute (path -> upstream, with middleware)
    |     +-- Register on-chain (ERC-8004, optional)
    |
    +-- x402-verifier
    |     +-- Watch published ServiceOffers
    |     +-- Derive in-memory pricing rules + upstream auth
    |
    +-- monetize.py process
          +-- Wait for controller convergence (no-op — controller owns all resources)
```

## References

- `references/serviceoffer-spec.md` — Full CRD field reference
- `references/registrationrequest-spec.md` — Child CRD used for publication and ERC-8004 side effects
- `references/x402-pricing.md` — x402 pricing model details
