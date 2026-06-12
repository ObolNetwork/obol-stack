---
name: smoke-test
description: "Sellable read-only smoke test of an Obol Stack public surface. The buyer pays per run (x402); the agent GET-probes the target's discovery + payment-gating endpoints, writes a scored report, commits it to the seller-owned public GitHub repo, and hands back the exact `obol smoke calldata` command the OPERATOR runs to derive the ERC-8004 validationResponse calldata. The agent never pays, never signs, never submits chain transactions."
metadata: { "openclaw": { "emoji": "🔍", "requires": { "bins": ["python3"] } } }
---

# Smoke Test

Probe a TARGET Obol Stack public surface **read-only**, score it, publish the
report, and emit the verdict-grounding command. You are the seller side of a
paid smoke-test service: a buyer paid (via x402) for one run against one target.

Hard rules — these are the product's trust model, never break them:

- **GET only.** Never send an `X-PAYMENT` header, never sign anything, never
  settle anything, never submit a chain transaction. The OPERATOR submits the
  on-chain validationResponse from their own wallet — identical to the bounty
  stance (the agent/controller never signs validation txs).
- **Never probe cross-host.** The scripts reduce catalog endpoints to their
  path and re-join onto the target base URL. Don't hand-probe URLs from the
  target's responses.
- **Never echo `GITHUB_TOKEN`** (or any `Authorization` header), never pass it
  on a command line. The scripts read it from env only and redact it from
  errors. To check it's configured, test presence only:
  `[ -n "$GITHUB_TOKEN" ] && [ -n "$GITHUB_REPORT_REPO" ] && echo configured || echo missing`
- **Exactly ≤ 2 GitHub writes per run** (report.md + best-effort latest.md);
  `results.json` is never committed.

## Inputs (from the buyer message)

The buyer message looks like `smoke-test <targetBaseURL>`, optionally with a
run id.

- **target** (required): an absolute http(s) base URL, e.g.
  `https://<tunnel-host>` or `http://obol.stack:8080`. If the buyer gave a
  bare host, prepend the scheme (`https://` for public hostnames, `http://`
  for local stack addresses) BEFORE running the script — the normalized
  target (whitespace-stripped, trailing `/` stripped) is hashed into the
  on-chain requestHash, so it must be unambiguous.
- **run id** (optional): must match `^[A-Za-z0-9._-]+$`. When absent the
  script generates `<UTC yyyymmddTHHMMSSZ>-<6 hex>`.

## Run procedure — TWO separate terminal calls

Terminal calls on CRD agents time out at 80s. The probe alone can take up to
~60s (up to 8 checks × 8s). **Never combine probe and post in one call.**

**Call 1 — probe (no network writes):**

```bash
python3 ${OBOL_SKILLS_DIR:-/data/.hermes/obol-skills}/smoke-test/scripts/smoke.py probe <targetBaseURL> [--run-id <id>]
```

Prints `results.json` to stdout and writes
`./smoke/<target-host>/<runId>/{report.md,results.json}` in the workspace
(`<target-host>` = lowercase hostname with `:<port>` → `-<port>`, e.g.
`obol.stack:8080` → `obol.stack-8080`). Exit 0 even when checks fail — the
score IS the verdict. Non-zero only on operational errors.

**Call 2 — post (only when BOTH `GITHUB_TOKEN` and `GITHUB_REPORT_REPO` are set):**

```bash
python3 ${OBOL_SKILLS_DIR:-/data/.hermes/obol-skills}/smoke-test/scripts/gh_post.py ./smoke/<target-host>/<runId>
```

Commits `report.md` to `reports/<target-host>/<runId>.md` in the seller repo,
updates the local `results.json` with the commit-pinned permalink, best-effort
updates `reports/<target-host>/latest.md`. The only stdout payload lines are:

```
permalink: https://github.com/<owner>/<repo>/blob/<commit-sha>/reports/<target-host>/<runId>.md
content-sha: <blob sha>
```

If the GitHub env is absent, **degrade gracefully**: skip Call 2, tell the
buyer the report is local-only (no permalink), and still return the full
results + calldata command (without `--response-uri`).

If Call 2 fails (non-zero exit), the report stays local and `post` is
re-runnable: `python3 .../scripts/smoke.py post ./smoke/<target-host>/<runId>`
(prints the updated results.json).

## What gets probed

All checks are GET-only, 8s timeout, 1 MiB body cap, no redirects, User-Agent
`obol-smoke-test/1.0 (+https://github.com/ObolNetwork/obol-stack)`:

1. `skill-md` — `<target>/skill.md` → 200 + non-empty body (counted)
2. `services-json` — `<target>/api/services.json` → 200 + bare JSON **list**
   of objects with non-empty string `name` and `endpoint` (counted; an empty
   catalog passes)
3. `x402-402:<name>` — per advertised service (first 5, sorted by name),
   GET the endpoint's **path** on the target → 402 with a valid x402 body:
   `x402Version` present, non-empty `accepts`, each entry with non-empty
   `scheme`/`network`, 0x40-hex `payTo`/`asset`, and a positive digits-only
   `maxAmountRequired` or `amount` (one counted check per service)
4. `agent-registration` — `<target>/.well-known/agent-registration.json` →
   200 + JSON object (**informational** — excluded from passed/total/score)

Scoring over counted checks only: `score100 = floor(100*passed/total)` (the
on-chain value — the deployed registry rejects responses above 100) and
`score255 = floor(255*passed/total)` (off-chain field kept in results.json).

## Reply to the buyer

After the run, reply with — in this order:

1. The check table (from `report.md`): check name, ok, latency, detail.
2. The score line: `<passed>/<total> checks passed — score <score100>/100`
   (mention `score255` from results.json as the off-chain value).
3. The GitHub permalink (when posted) and the `reportSha256` from
   results.json (sha256 of the exact committed `report.md` bytes).
4. The full `results.json` content.
5. The EXACT command the operator runs to derive the ERC-8004
   validationResponse calldata (fill in the real values; the agent itself
   NEVER runs this and never submits the transaction):

```bash
obol smoke calldata \
  --target "<normalized targetBaseURL>" \
  --run-id "<runId>" \
  --response <score100> \
  --response-uri "<permalink>" \
  --response-hash 0x<reportSha256> \
  --network base-sepolia
```

Notes for that command:

- It derives `requestHash = keccak256("obol/smoke-test/v1|<normalized
  targetBaseURL>|<runId>")` — keccak256 is computed by the CLI, not in-pod
  (there is no reliable in-pod keccak; `hashlib.sha3_256` is NIST SHA-3, NOT
  keccak256). That is why `requestHash` is deliberately absent from
  results.json.
- `--response` is **score100** (0–100), not score255.
- `--response-hash` is `0x` + the 64-hex `reportSha256` (sha256 of the
  committed report.md bytes). Omit `--response-uri`/`--response-hash` when the
  GitHub post didn't run (a zero response hash is allowed).
- The CLI prints the ValidationRegistry address + calldata; the operator
  submits with THEIR wallet.

## Seller/operator setup (one-time, host side — not the agent)

GitHub credentials ride the existing `hermes-env` Secret (already whitelisted
by the admission policy and RBAC — do NOT invent a new Secret name):

```bash
obol kubectl -n agent-<name> create secret generic hermes-env \
  --from-literal=GITHUB_TOKEN=<fine-grained PAT> \
  --from-literal=GITHUB_REPORT_REPO=<owner>/<repo> \
  --dry-run=client -o yaml | obol kubectl apply -f -
obol kubectl -n agent-<name> rollout restart deploy/hermes
```

**Token scope is the blast radius.** The buyer drives a prompt-injectable
agent that holds this token in env, so it MUST be a fine-grained PAT scoped to
ONLY the one public report repo, with `contents: read+write` and nothing
else. Accepted v0 worst case: an attacker writes junk to that one public
repo. Never use a classic PAT or broader scopes. The token lives only in
Secret data — never in the Agent CR spec/annotations/status.

Sell the agent:

```bash
obol agent new <name> --skills smoke-test --objective "Paid read-only smoke tests of Obol Stack public surfaces"
obol sell agent <name> --per-request <price> --chain <chain> --pay-to 0x<wallet>
```

Buyers reach it via `buy.py pay-agent <url> --model <id> --message "smoke-test <targetBaseURL>"`
(streaming). v0: no buyer token handoff — reports always land in the
seller-owned repo.

## Artifacts

```
./smoke/<target-host>/<runId>/
├── report.md      # canonical committed bytes; sha256 = reportSha256
└── results.json   # version obol/smoke-test/v1; stays local + in chat reply
```

results.json fields: `version`, `target` (normalized), `runId`, `timestamp`,
`checks[]` (`name`, `ok`, `detail`, `ms`, optional `informational`),
`passed`, `total`, `score255`, `score100`, `reportSha256` (64 hex, no 0x),
`permalink` (empty until post succeeds).
