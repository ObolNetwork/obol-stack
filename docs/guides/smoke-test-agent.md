# Selling a Smoke-Test Agent

This guide walks you (the **seller/operator**) through provisioning, selling,
and operating the **smoke-test agent**: a payment-gated agent that buyers hire
per run to health-check the public surface of an Obol Stack deployment.

For each paid run, the agent:

1. **Probes** a buyer-supplied target stack URL — strictly **read-only** GETs
   against the published public routes (`/skill.md`, `/api/services.json`,
   each advertised `/services/<name>/*` 402 challenge, and the informational
   `/.well-known/agent-registration.json`). It never sends an `X-PAYMENT`
   header and never writes anything to the target.
2. **Writes a report** — `report.md` (the canonical committed bytes) and
   `results.json` (machine-readable scores) in its workspace.
3. **Commits the report** to a **seller-owned public GitHub repo** at
   `reports/<target-host>/<runId>.md` and streams the buyer the
   `results.json` plus a commit-pinned permalink.
4. Leaves you with everything needed to **submit an ERC-8004
   ValidationRegistry verdict** from your own wallet via
   `obol smoke calldata`. The agent and the controller never sign on-chain
   transactions — same stance as the bounty pipeline.

> [!IMPORTANT]
> The monetize subsystem is alpha software. If you encounter an issue, please
> open a [GitHub issue](https://github.com/ObolNetwork/obol-stack/issues).

> [!WARNING]
> The buyer drives a prompt-injectable agent that holds a GitHub token in its
> environment. Scope that token to **one public report repo, contents
> read/write, nothing else** (see [Step 2](#step-2--create-the-github-secret)
> and [Production guidance](#production-guidance)). The accepted v0 blast
> radius is "an attacker can write junk to the one public report repo" —
> nothing more.

## System overview

```
BUYER (any x402 wallet)                       SELLER (your obol stack)

buy.py pay-agent ── x402 payment ──> Traefik /services/smoke-tester/*
  "smoke-test <target>"                 └─> x402-verifier ─> Hermes agent
                                                              │ smoke-test skill
                                                              │
                       read-only GETs (no X-PAYMENT, ever)    ▼
TARGET stack public surface  <──────────────────────  smoke.py probe
  /skill.md                                            smoke.py post ──> GitHub
  /api/services.json                                                     reports/<host>/<runId>.md
  /services/<name>/*   (expect 402)
  /.well-known/agent-registration.json (informational)

OPERATOR (you, out of band)
  obol smoke calldata ──> validationResponse calldata ──> cast send (YOUR wallet)
                                                          ERC-8004 ValidationRegistry
```

## Prerequisites

- A running Obol Stack (`obol stack up`) with the Cloudflare tunnel active so
  `/services/*` is publicly reachable.
- A **public** GitHub repository you own for reports (e.g.
  `<owner>/stack-smoke-reports`).
- A GitHub credential scoped to that repo only (see Step 2).
- For the on-chain verdict: a wallet with ETH for gas on the target chain
  (default `base-sepolia`).

## Step 1 — Provision the agent

Declare the agent with the `smoke-test` skill. No `--create-wallet` is needed
for v0: the agent never signs anything; you submit the verdict from your own
wallet.

```bash
obol agent new smoke-tester \
  --skills smoke-test \
  --objective "You are a smoke-test agent. When a buyer says 'smoke-test <url>', run the smoke-test skill: probe the target read-only, then post the report, then reply with results.json and the permalink."
```

This creates an Agent CR in namespace `agent-smoke-tester`; the
serviceoffer-controller provisions a Hermes runtime with the skill mounted at
`/data/.hermes/obol-skills/smoke-test/`.

## Step 2 — Create the GitHub Secret

The agent reads `GITHUB_TOKEN` and `GITHUB_REPORT_REPO` from its environment.
Both ride the **existing `hermes-env` Secret** — the runtime-env-override hook
every CRD agent already mounts (`envFrom`, optional). Do **not** invent a new
Secret name: `hermes-env` is the one whitelisted by the admission policy and
RBAC.

Create a **fine-grained personal access token** (GitHub → Settings →
Developer settings → Fine-grained tokens):

- **Repository access**: only the report repo (e.g.
  `<owner>/stack-smoke-reports`).
- **Permissions**: Contents → Read and write. Nothing else.
- **Expiration**: short (30–90 days) and rotate.

Then create the Secret and restart the agent (the Deployment's checksum
annotation only covers `hermes-config`, so a Secret change needs an explicit
restart):

```bash
obol kubectl -n agent-smoke-tester create secret generic hermes-env \
  --from-literal=GITHUB_TOKEN=github_pat_XXXXXXXXXXXXXXXXXXXXXX \
  --from-literal=GITHUB_REPORT_REPO=<owner>/stack-smoke-reports \
  --dry-run=client -o yaml | obol kubectl apply -f -

obol kubectl -n agent-smoke-tester rollout restart deploy/hermes
```

> [!CAUTION]
> The token lives **only** in the Secret's data. Never put it in the Agent
> CR spec, annotations, labels, status, or any file the agent commits.
> Explicit `env` entries on the Hermes container (e.g. `API_SERVER_KEY`,
> `REMOTE_SIGNER_TOKEN`) always take precedence over `envFrom`, so
> `hermes-env` cannot clobber the runtime's own credentials.

To rotate: re-run the same two commands with the new token.

## Step 3 — Sell it

```bash
obol sell agent smoke-tester \
  --price 0.05 \
  --token USDC \
  --chain base-sepolia \
  --pay-to 0xYourRevenueWallet \
  --description "Paid smoke test: read-only probe of an Obol Stack public surface, report committed to a public GitHub repo"
```

This wraps the agent in a `type=agent` ServiceOffer. Check progress with
`obol sell status smoke-tester -n agent-smoke-tester`; once
`UpstreamHealthy`, `PaymentGateReady`, and `RoutePublished` are `True`, the
agent is purchasable at `/services/smoke-tester/v1/chat/completions` on your
tunnel hostname.

## Step 4 — Buyer journey

The buyer pays per run with the `buy-x402` skill's one-shot streaming call.
From any buyer agent pod:

```bash
# 1. Discover pricing + the agent model id (extra.agentModel in the 402 body)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py probe \
  https://<seller-tunnel-host>/services/smoke-tester/v1/chat/completions --type agent

# 2. Pay for one run (streaming; agent runs can be slow, prefer pay-agent)
python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py pay-agent \
  https://<seller-tunnel-host>/services/smoke-tester/v1/chat/completions \
  --model "<extra.agentModel from the probe>" \
  --message 'smoke-test https://target-stack.example.com'
```

The message contract is `smoke-test <targetBaseURL>`. The agent generates a
run id of the form `<UTC yyyymmddTHHMMSSZ>-<6 hex>` (a buyer may suggest one
in the message; it must match `^[A-Za-z0-9._-]+$`). The streamed reply
contains the full `results.json` — including `passed`/`total`, `score255`,
`score100`, `reportSha256` — and the commit-pinned GitHub permalink.

Note for buyers: the report lands in the **seller's** public repo, so the
result is publicly auditable but the buyer needs no GitHub credentials. The
buyer's verification path is: fetch the permalink, check
`sha256(report bytes) == reportSha256`, and (once submitted) check the
on-chain validation entry.

## Step 5 — Where reports live

In the seller-owned report repo:

| Path | Content |
|---|---|
| `reports/<target-host>/<runId>.md` | The canonical per-run report (committed bytes are what `reportSha256` covers) |
| `reports/<target-host>/latest.md` | Best-effort pointer: run id, score line, permalink of the latest run |

`<target-host>` is the lowercase target hostname with `:<port>` rewritten to
`-<port>` (e.g. `obol.stack:8080` → `obol.stack-8080`). The permalink the
buyer receives is commit-pinned
(`https://github.com/<owner>/<repo>/blob/<commit-sha>/reports/...`), so later
runs can never silently rewrite what the buyer was shown.

Each run performs at most **two** repo writes: one commit for the report,
one best-effort commit for `latest.md`.

## Step 6 — Submit the on-chain verdict

The run's identity on-chain is:

```
requestHash = keccak256("obol/smoke-test/v1|<targetBaseURL>|<runId>")
```

with the target normalized exactly like the report (`strip()` whitespace,
strip trailing `/`). `results.json` deliberately does **not** contain the
request hash (the agent pod has no keccak256); `obol smoke calldata` derives
it for you:

```bash
obol smoke calldata \
  --target https://target-stack.example.com \
  --run-id 20260612T093000Z-3fa9c2 \
  --response 87 \
  --response-uri "https://github.com/<owner>/stack-smoke-reports/blob/<commit-sha>/reports/target-stack.example.com/20260612T093000Z-3fa9c2.md" \
  --response-hash 0x<reportSha256 from results.json> \
  --network base-sepolia
```

Flag-to-report mapping:

| Flag | Source |
|---|---|
| `--target`, `--run-id` | `results.json` `target` + `runId` (the same normalized values) |
| `--response` | `results.json` **`score100`** — the on-chain value. The deployed registry reverts above 100, so `score255` stays an off-chain field |
| `--response-uri` | the commit-pinned permalink |
| `--response-hash` | `0x` + `results.json` `reportSha256` (sha256 of the committed `report.md` bytes; optional, zero allowed) |

The command prints the request hash, the ValidationRegistry address for the
chosen network, and the ready-to-submit `validationResponse` calldata
(selector `0x3d659a96`). Submit it with **your own wallet** — never the
agent's:

```bash
cast send <ValidationRegistry-address> <calldata> \
  --rpc-url <your-rpc-url> \
  --private-key "$OPERATOR_KEY"
```

(Use an environment variable or a hardware/keystore signer — never paste a
private key inline.)

Anyone can then independently verify the verdict: recompute the request hash
from the public target + run id, fetch the permalink, and check
`sha256(report.md bytes)` against the submitted `responseHash`.

## Production guidance

> [!IMPORTANT]
> Read this section before selling runs for real money. It captures the v0
> trust model and the GitHub operational limits.

### Prefer GitHub App installation tokens over PATs

For production, replace the fine-grained PAT with a **GitHub App installation
token**:

- **Short-lived**: installation tokens expire after ~1 hour, so a leaked
  token (the realistic failure mode for a prompt-injected agent) has a small
  window. PATs live until rotated.
- **Per-repo by installation**: install the App on only the report repo;
  the token cannot be over-scoped by mistake.
- **Higher, separately-bucketed rate limits** than user PATs.

The trade-off is operational: something must mint a fresh installation token
and refresh the `hermes-env` Secret (`GITHUB_TOKEN`) on a schedule (e.g. a
host-side cron re-running the Step 2 commands). The agent contract is
unchanged — it just reads `GITHUB_TOKEN` from env.

### v0 trust model: seller-owned repo only

v0 posts to the **seller-owned public report repo**. There is deliberately
**no buyer token handoff** — a buyer cannot ask the agent to commit into a
buyer-owned repo, and the agent must never accept credentials passed through
chat. Buyer-repo delivery is explicitly out of scope for v0 and is planned as
a v1 feature with a proper credential channel. If a buyer needs a copy, the
report is public — mirror the permalink.

### GitHub rate limits and acceptable use

The posting script is built to stay well inside GitHub's
[acceptable use](https://docs.github.com/en/site-policy/acceptable-use-policies)
and secondary rate limits, and you should keep it that way:

- **Batch: one report commit per run** (plus one best-effort `latest.md`
  write) — never per-check or per-probe commits.
- Content writes are the expensive, secondary-rate-limited operation on
  GitHub's side; the script honors `Retry-After` (falling back to
  `x-ratelimit-reset`) on 403/429, retries 5xx with short exponential
  backoff, and gives up within a bounded budget rather than hammering.
- If you operate many sellers against one report repo, expect concurrent-
  write 409s (the script re-fetches the blob sha and retries once); beyond
  light contention, shard by repo.
- A failed post never loses the run: the report stays in the agent workspace
  and `post` is re-runnable.

### Blast radius recap

- The smoke agent **never signs or settles anything** — probe-only buyer
  side, no `X-PAYMENT`; the operator submits the validation transaction.
- The GitHub token is the only credential it holds; with the scoping above,
  the worst case from a hostile buyer prompt is junk commits in one public
  report repo. Rotate the token and clean up the repo history if it happens.

## CI / smoke coverage

`flows/flow-20-smoke-agent.sh` gates this feature: it compiles the skill
scripts, runs a probe-only self-smoke against the local stack's own public
catalog surface (validating `report.md`/`results.json` and the
`reportSha256` binding), exercises GitHub posting **only** when
`GITHUB_TOKEN` + `GITHUB_REPORT_REPO` are exported (explicit SKIP otherwise,
so CI never needs GitHub), and asserts `obol smoke calldata` emits
`validationResponse` calldata with selector `0x3d659a96`.
