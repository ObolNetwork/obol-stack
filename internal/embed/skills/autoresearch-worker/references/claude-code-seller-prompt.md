# Claude Code seller onboarding prompt

Use this prompt when a GPU owner opens `mutable-state-inc/autoresearch-at-home` in Claude Code and you want Claude to do more than merely join the Ensue swarm.

The key distinction is:

- **joining autoresearch@home** => collaborative contributor mode
- **selling GPU through Obol Stack** => contributor mode **plus** worker API **plus** x402-gated seller exposure

## Prompt

```text
Read this repository carefully and do NOT stop at merely joining the autoresearch@home swarm.

Primary goal:
Turn this machine into a real autoresearch contributor AND, if possible on this machine, a paid GPU seller for remote autoresearch experiments via Obol Stack/x402.

You must optimize for actual working state, not just partial setup.

Success criteria, in order:
1. Verify this machine has a usable CUDA GPU and can run autoresearch experiments.
2. Join the autoresearch@home Ensue swarm correctly.
3. Start a working autoresearch worker API on this machine that can accept remote experiments.
4. If Obol Stack is available on this machine, expose that worker as a paid x402-gated service and register it with useful metadata for discovery.
5. Leave behind a concise status report with:
   - chosen codename
   - swarm join status
   - local worker status
   - paid seller status
   - public/cluster endpoint(s)
   - exact blockers if anything could not be completed
   - exact next command the human should run

Important:
- Do not confuse “joined autoresearch@home” with “selling GPU”.
- Joining the swarm is necessary but NOT sufficient.
- Paid seller behavior requires a worker endpoint plus Obol/x402 exposure.
- Be honest about what is actually verified.
- Do not claim success for any step you did not verify.
- Work autonomously and do not ask for confirmation unless blocked by a true human-only step.
- Human-only steps include:
  - Ensue claim_url / email verification
  - entering third-party tokens if not already available
  - cloud account login
- If blocked, print a minimal, exact unblock request and otherwise keep going.

Read these files fully first:
- README.md
- program.md
- collab.md
- coordinator.py
- prepare.py
- train.py

Then execute this plan:

PHASE 1 — GPU + local autoresearch readiness
1. Run `nvidia-smi`.
   - If no GPU is available, stop the paid-seller path and print:
     “BLOCKED: no CUDA GPU available”
   - If GPU exists, record GPU model and VRAM.
2. Run `uv sync`.
3. Check whether `~/.cache/autoresearch/` already contains prepared data/tokenizer artifacts.
   - If missing, run `uv run prepare.py`.
4. Ensure `results.tsv` exists with the correct header if needed.
5. Create or switch to a fresh branch for this run if appropriate.

PHASE 2 — Join autoresearch@home correctly
6. Check for `ENSUE_API_KEY` or `.autoresearch-key`.
7. If neither exists:
   - pick 3 good single-word codename suggestions
   - ask the human to choose one if required
   - register the agent using the Ensue API
   - save the `api_key` to `.autoresearch-key`
   - show the human the exact `claim_url` and verification code
   - pause only until this human verification step is complete
8. Initialize the coordinator:
   - `from coordinator import Coordinator`
   - `coord = Coordinator()`
   - set `coord.agent_id` to the chosen codename
9. Join the hub with the invite token from this repo.
10. Run `coord.announce()`.
11. Pull the best config for this hardware tier:
   - prefer `coord.pull_best_config_for_tier()`
   - fall back to the global best if needed
12. If the pulled config is better than local baseline, adopt it into `train.py` and commit that change.

PHASE 3 — Do not stop at contributor mode; build seller mode
13. Determine whether this machine can support Obol seller flow:
   - check whether `obol` CLI exists
   - check whether an Obol Stack cluster is running
   - check whether `obol sell http` is available
14. If Obol Stack is not available:
   - still get the local worker API running
   - print clearly that paid seller mode is blocked by missing Obol Stack
   - continue contributing to the swarm if possible
15. If Obol Stack is available, continue to full seller setup.

PHASE 4 — Get a real worker API running
16. You need a worker API that accepts remote experiments.
17. If this repo already has a working worker API, use it.
18. If not, fetch the current implementation from Obol Stack:
   - inspect `ObolNetwork/obol-stack`
   - prefer main if merged
   - otherwise use the current feature work if needed
   - specifically look for the autoresearch worker implementation and current seller flow
19. Start a worker API on port 8080 that exposes:
   - GET /health or /healthz
   - GET /status
   - GET /best
   - GET /experiments/<id>
   - POST /experiment
20. Verify locally with real requests that:
   - health endpoint works
   - a trivial experiment submission works
   - the worker stores results and returns `val_bpb` if available
21. If the worker cannot be started, print the exact blocker and stop seller mode.

PHASE 5 — Paid seller mode through Obol Stack
22. If the worker runs on the same machine as Obol Stack, choose the simplest viable path to expose it to the cluster.
23. If `obol sell http` requires an in-cluster Service, create the smallest safe relay/proxy needed so the cluster can reach the worker.
24. Monetize the worker with `obol sell http`.
   Use a path like `/services/autoresearch-worker`.
25. Register useful metadata, including at minimum:
   - GPU model
   - framework=autoresearch
   - runtime or source indicating this machine
   - optionally current best_val_bpb if known
26. Use OASF discovery metadata appropriate for model optimization.
27. Verify:
   - ServiceOffer exists
   - ServiceOffer reaches Ready if possible
   - registration JSON exists
   - registration JSON includes x402Support
   - registration JSON includes a service endpoint
   - registration JSON includes OASF skills/domains
   - registration JSON includes metadata fields
28. If a tunnel/public URL is available, record it.
29. If no public URL is available, still record the cluster-local seller state and exact blocker to public access.

PHASE 6 — Start contributing for real
30. Once worker/seller setup is done or blocked clearly, begin the real collaborative experiment loop:
   - THINK
   - CLAIM
   - RUN
   - PUBLISH
31. Do at least one real experiment cycle if feasible.
32. Publish:
   - result
   - insight
   - hypothesis

OUTPUT REQUIREMENTS
At the end, create a short file in the repo root called `SELLER_STATUS.md` containing:
- machine GPU info
- Ensue join status
- codename
- worker API status
- paid seller status
- commands that were run
- URLs/endpoints created
- blockers and next steps

Also print a concise terminal summary:
- CONTRIBUTOR: READY / BLOCKED
- WORKER API: READY / BLOCKED
- PAID SELLER: READY / BLOCKED
- NEXT ACTION REQUIRED FROM HUMAN: <exact one-liner or NONE>

Behavior constraints:
- Do not stop after “join autoresearch@home”.
- Do not just explain what should be done; do it.
- If a step fails, debug it before giving up.
- If you cannot fully complete paid seller mode, still leave the machine in the best possible partial state and explain the exact remaining blocker.
```

## Why this prompt exists

The default upstream collaborative flow is optimized for joining the Ensue swarm and contributing experiments. That is useful, but it does not automatically turn the machine into a paid seller.

This prompt forces the agent to:
- distinguish contributor mode from seller mode
- stand up a worker API
- attempt Obol/x402 exposure when possible
- verify what actually works
- leave behind a concrete status report

## Best use cases

Use this prompt when:
- a GPU owner wants to join `autoresearch-at-home`
- you also want that machine to become a paid worker if possible
- you want Claude Code to operate autonomously and not stop at partial onboarding

## Related references

- `worker-api.md` — what the worker must expose
- `k3s-gpu-worker.md` — cluster deployment pattern for sellers
- `../SKILL.md` — overall autoresearch-worker operator guidance
