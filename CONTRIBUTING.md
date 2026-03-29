# Contributing to Obol Stack

This document defines the non-negotiable contribution rules for the consolidated Obol Stack codebase and spec bundle.

---

## 1. Canonical Documents

The canonical specification bundle lives at repo root:

- `SPEC.md`
- `ARCHITECTURE.md`
- `BEHAVIORS_AND_EXPECTATIONS.md`
- `CONTRIBUTING.md`
- `features/`
- `docs/adr/`

Supporting material in `docs/guides/` can remain useful, but it is **not** authoritative once the root-level bundle covers the same topic.
Planning or architecture notes must be folded into `SPEC.md` phase sections or `docs/adr/` instead of living as parallel sources of truth under `docs/plans/` or `plans/`.

If code and docs disagree:
- code is the temporary source of truth
- the root-level bundle must be updated in the same change or immediately after

---

## 2. Actor Priority

When making product or UX tradeoffs, preserve this order:

1. Local operator
2. Agent developer
3. Remote buyer

This affects:
- defaults
- failure handling
- public exposure rules
- CLI ergonomics
- phased rollout decisions

---

## 3. Documentation Update Rules

Any change touching these areas is spec-impacting and must update the canonical bundle when it changes behavior:

- `cmd/obol/`
- `internal/stack/`
- `internal/model/`
- `internal/network/`
- `internal/openclaw/`
- `internal/agent/`
- `internal/x402/`
- `internal/x402/buyer/`
- `internal/tunnel/`
- `internal/erc8004/`
- `internal/inference/`
- `internal/embed/infrastructure/`
- `internal/embed/skills/`
- `internal/app/`
- `internal/schemas/`

Rules:
- describe only behavior that is actually implemented on the branch
- move future work into `Phase 2+` sections and ADR follow-ups
- do not silently broaden support claims
- do not collapse different chain domains into one “supported networks” statement

Current chain domains that must stay distinct:
- installable local networks
- eRPC remote RPC aliases
- sell-side payment chains
- ERC-8004 registration networks

---

## 4. Feature and ADR Discipline

Feature files:
- live under `features/`
- start with `@bdd`
- reference both `SPEC.md` and `BEHAVIORS_AND_EXPECTATIONS.md`
- use `@phase1`, `@phase2`, etc. when phases matter

ADRs:
- live under `docs/adr/`
- record durable architectural decisions, not transient implementation chatter
- must note the affected `SPEC.md` sections

---

## 5. Development Expectations

Baseline validation before sending a substantial code change:

```bash
go build ./...
go test ./...
```

When the change touches the monetization path, strongly prefer validating one or more of:

```bash
./flows/flow-06-sell-setup.sh
./flows/flow-07-sell-verify.sh
./flows/flow-08-buy.sh
./flows/flow-10-anvil-facilitator.sh
```

When the change touches embedded skills or sell-side metadata, also consider:

```bash
python3 tests/skills_smoke_test.py
python3 tests/test_sell_registration_metadata.py
python3 tests/test_autoresearch_worker.py
```

---

## 6. Security and Exposure Guardrails

Never merge a change that:
- exposes frontend, eRPC, monitoring, or similar operator surfaces to the public tunnel
- gives the buyer sidecar live signer access
- changes sell-side chain support claims without updating both CLI behavior and docs
- enables write-capable public RPC forwarding by default
- removes the `OffChainOnly` degradation path without a replacement operator-safe fallback

---

## 7. Hook-Based Drift Detection

Repo-local Codex hooks should be treated as guardrails, not as a substitute for human judgment.

Intended behavior:
- session-start hooks remind Codex that the root-level bundle is canonical
- stop hooks block or warn when spec-impacting code changed but the canonical bundle did not

To enable Codex hooks locally:

```toml
# ~/.codex/config.toml
[features]
codex_hooks = true
```

The repository hook entrypoint is:

- `.codex/hooks.json`

Hook scripts belong under:

- `.codex/hooks/`

This repository currently ships:

- `.codex/hooks/workspace_context.py`
- `.codex/hooks/stop_spec_sync.py`

If hooks and code ever disagree, fix the hooks or the bundle. Do not paper over the mismatch.

---

## 8. Pull Request Checklist

- [ ] Behavior changes are reflected in the root-level canonical bundle
- [ ] Future work is isolated into phases or ADR follow-ups
- [ ] Operator-facing claims match the actual CLI and runtime surface
- [ ] Security exposure boundaries were preserved
- [ ] Tests or flow validations were run, or the omission is explicitly stated
