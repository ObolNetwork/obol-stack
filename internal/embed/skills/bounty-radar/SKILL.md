---
name: bounty-radar
description: "Multi-source bug bounty opportunity radar for authorized researchers. Combines Immunefi scope diffs, GitHub churn, DefiLlama TVL, and Code4rena/Sherlock contests — then ranks opportunity vs competition. Not vulnerability exploitation."
metadata: { "openclaw": { "emoji": "🎯", "requires": { "bins": ["python3"] } } }
---

# Bounty Radar

Help security researchers (and other agents) decide **where to hunt this week**.

You are **not** a thinner Immunefi UI and **not** “ask Claude.”
You run **live public tools**, combine several sources, and return a ranked brief
with concrete evidence.

Scripts never score. **You** rank (`opportunity`, `competition`, `why`).

## Value proposition (say this clearly)

Immunefi alone = a list of programs.
Claude alone = reasoning without your wired live pipeline.

Bounty Radar adds:
- freshness window (what moved recently)
- **scope diffs** (newly added/removed assets)
- GitHub release / interesting-branch signals
- optional **TVL** context (DefiLlama) for impact sizing
- overlapping **audit contests** (Code4rena + Sherlock) that steal hunter attention
- one x402-payable specialist endpoint other agents can call

## When to Use

- “Top opportunities in the last 7 days”
- Fresh Immunefi scope / metadata changes
- Compare competition vs new attack surface
- Brief a program before authorized research

## When NOT to Use

- Unauthorized scanning, exploitation, PoCs, bypass guidance
- Claiming a confirmed vulnerability
- Sending transactions — use `ethereum-local-wallet`
- Unrelated chain reads — use `ethereum-networks`

## Positioning (always follow)

- **Authorized public programs only.**
- Prefer **scope diffs + GitHub + contest crowding** over bounty size alone.
- If a source fails, continue with the others and mention `errors[]`.
- Never invent assets, TVL, contests, or payouts.
- No API keys are required. Optional `GITHUB_TOKEN` only raises GitHub rate limits.

## Workflow

Run from:

```bash
cd "${OBOL_SKILLS_DIR:-/data/.hermes/obol-skills}/bounty-radar/scripts"
```

### Top-N brief (default paid demo)

1. `immunefi_recent.py --days 7 --limit 20`
2. Shortlist 5–8 promising slugs (fresh update + non-trivial payout or new surface hints)
3. For each shortlist slug: `opportunity_pack.py <slug>`
   - bundles Immunefi detail + asset diff + GitHub + DefiLlama TVL + contest overlap
4. Optionally widen contest context: `contests_recent.py --days 21 --limit 25`
5. **You** synthesize top N (default 3) with evidence-backed `why[]`

Do **not** rank from memory alone.

### Single-program deep dive

```bash
python3 opportunity_pack.py nuva
# or step-by-step:
python3 immunefi_program.py nuva
python3 diff_assets.py nuva
python3 defillama_tvl.py nuva
python3 contests_recent.py --days 45 --query nuva
python3 github_repo.py https://github.com/org/repo   # if present
```

## Tools

| Script | Purpose | Failure mode |
|--------|---------|--------------|
| `immunefi_list.py` | All Immunefi programs from snapshot | hard fail if core feed down |
| `immunefi_recent.py` | Recently updated programs | hard fail if core feed down |
| `immunefi_program.py <slug>` | One program: assets, audits, metadata | hard fail for that slug |
| `diff_assets.py <slug>` | Added/removed scope vs local cache | hard fail for that slug |
| `github_repo.py <url>` | Latest release + interesting branches | soft via pack; script may error alone |
| `defillama_tvl.py <query>` | Public TVL fuzzy match | **soft-fail** → `ok:false` + `errors` |
| `contests_recent.py` | Code4rena + Sherlock public contests | **soft-fail per source** |
| `opportunity_pack.py <slug>` | Combine all of the above for one program | **partial pack**; see `errors[]` |

Optional env (never required):

- `BOUNTY_RADAR_GITHUB_TOKEN` / `GITHUB_TOKEN` — higher GitHub rate limits only
- `BOUNTY_RADAR_CACHE` — asset-diff cache dir (default `~/.cache/bounty-radar`)

## Ranking guidance

Raise **opportunity** when:
- scope assets were newly added (`diff_assets.new_attack_surface` / added count)
- recent GitHub release or sensitive branch names
- meaningful max bounty **and** fresh update
- audits are older / sparse relative to new surface (use audit dates; don’t invent)

Raise **competition** when:
- huge brand / mega max bounty everyone watches
- change has been public for days
- overlapping live/recent Code4rena or Sherlock contests for the same name

TVL is **context for impact**, not a vuln signal.

## Default paid-demo answer shape

```text
## Top opportunities (last 7d)

### 1. <program> (`<slug>`)
- opportunity: <0-100>
- competition: <0-100>
- likely_hunters: <rough estimate>
- new_attack_surface: true|false
- why:
  - <concrete signal from opportunity_pack / scripts>
  - <concrete signal from opportunity_pack / scripts>
- extras: TVL if present; overlapping contests if present
- links: Immunefi / GitHub / contest URLs from tools

### 2. ...
### 3. ...

Notes: which sources failed (if any); this is opportunity intel, not a vuln report.
```

Closing line every time: **authorized-program opportunity intel** — researchers must follow each program’s rules before any testing.

## Data sources (public / read-only)

- Immunefi snapshot mirror: `infosec-us-team/Immunefi-Bug-Bounty-Programs-Unofficial`
- GitHub public API
- DefiLlama `api.llama.fi/protocols`
- Code4rena `code4rena.com/api/v1/audits`
- Sherlock `audits.sherlock.xyz/api/contests`
