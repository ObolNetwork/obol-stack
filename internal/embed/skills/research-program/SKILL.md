---
name: research-program
description: Stand up a decentralized auto-research program on the Obol Stack — publish a research ID, admit worker runners over the open internet, collect hypotheses/results in a private collective knowledge base, and distribute rewards proportional to validated impact. Wraps `obol research` + the worker runner; true to karpathy/autoresearch.
---

# Research Program (decentralized auto-research)

Publish a **research ID**, let **worker runners on any machine** join over the
open internet, have them run real experiments and post results to a **collective
knowledge base private to the group**, and pay out **proportional to validated
impact**. The owner is a pure coordinator — it never runs an experiment.

This wraps two commands: `obol research` (owner) and `scripts/worker.py` (runner).

## Declarative model (true to autoresearch)

A program is essentially a `TASK.md` frontmatter: an **arbitrary metric**, a
**direction**, and a **KEEP rule**. Any domain lands without a schema change.

```
metric     val_bpb            # any string: val_bpb, auc, latency_ms, ΔΔG, …
direction  minimize|maximize
accept     beats-champion|threshold
split      by-impact|champion-takes-all
membership open|invite
```

Operational policy (how a runner sets up GPUs, which hypotheses to try) is
off-chain — it lives in `program.md` and in the runner, exactly as
AutoScientists keeps `LAUNCH.md` off-chain.

## 1. Owner — publish the program (on your machine)

```bash
obol research publish nanogpt-valbpb \
  --objective "Drive nanoGPT val_bpb down" \
  --metric val_bpb --direction minimize --accept beats-champion \
  --baseline 1.20 --pool 100 --token OBOL --network base-sepolia \
  --membership invite --split by-impact
```

This starts the KB + membership server on your machine and opens a **Cloudflare
tunnel**, printing a public URL like `https://<x>.trycloudflare.com`. Workers on
other machines reach the KB at that URL; every KB route is gated by a member
token (the device-auth flow below), so the program stays private to the group
while being reachable over the open internet. Runs in the foreground.

## 2. Runner — join and contribute (on each GPU machine)

```bash
python3 worker.py --kb https://<x>.trycloudflare.com \
  --program nanogpt-valbpb --worker spark1 --time-budget 60
```

The runner prints a **join code** and waits. Default experiment is the real
karpathy/autoresearch nanoGPT loop (`uv run train.py`, parsing `val_bpb:`); pass
`--experiment "<shell that prints '<metric>: <float>'>"` for any other task. The
runner needs the autoresearch repo prepared once (`uv run prepare.py`).

## 3. Owner — admit the runner (the membership decision)

```bash
obol research approve <join-code>
```

Only the owner can approve, so the owner alone decides who joins. With
`--membership open`, runners are auto-admitted and this step is skipped.

## 4. Owner — watch progress and settle

```bash
obol research status nanogpt-valbpb
```

Shows the roster, every submitted result, the current champion, and the
impact-proportional payout split. First-verified-wins on duplicate
improvements; payout share ∝ each accepted result's validated metric gain.

## What's private vs. public

- **Private to the group** (token-gated): the KB — `/task`, `/champion`,
  `/results`, `/status`. A request without a valid member token for THIS
  program gets 401/403.
- **Public** (the secret is the device code, RFC 8628): `/auth/device/code`,
  `/auth/device/token`. Owner-only: `/auth/device/approve`.

Never expose the KB as an open public route — membership is the whole point.
