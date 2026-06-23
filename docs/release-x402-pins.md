# Releasing: x402 image pins

The embedded infrastructure manifests pin the x402 images
(`x402-verifier`, `serviceoffer-controller`, `x402-buyer`) by tag **and**
digest, in:

- `internal/embed/infrastructure/base/templates/x402.yaml`
- `internal/embed/infrastructure/base/templates/llm.yaml`

`release.yml`'s `verify-image-pins` gate refuses to tag a release whose pins are
stale — i.e. whose pinned build commit predates a change to anything in the
x402 binaries' import graph (which includes all of `internal/embed/**`). So the
pins must point at images built from the release commit before you tag.

## How the pins get bumped

Run the **Release Prep - repin x402 pins** workflow (`release-prep.yml`,
`workflow_dispatch`) with `ref` = the release commit (usually `main`). It:

1. builds the four x402 images for that commit (tagged by its SHA), then
2. opens an **auto-merging PR** that repins `x402.yaml` / `llm.yaml` to them.

The pin commit is made with the GraphQL `createCommitOnBranch` API onto a
feature branch, so it is **GitHub-verified** (satisfies the no-unsigned-commits
rule) and lands through **normal PR review — no branch-protection bypass**.

Then:

1. A maintainer approves the repin PR; it auto-merges.
2. `main` HEAD is now the repin commit (the release commit + correct pins).
3. Tag `vX.Y.Z` on that commit. `release.yml` passes `verify-image-pins`,
   builds binaries, and creates the draft release.

> This replaced the old push-time `repin-embedded-pins` job in
> `docker-publish-x402.yml`, which committed directly to protected `main` and
> was rejected on every run (`BRANCH_PROTECTION_RULE_VIOLATION`). Repinning is a
> release-time concern, so it now runs at release time, the right way.

## CI on the repin PR

GitHub does **not** trigger workflows for commits authored by the default
`GITHUB_TOKEN`, so the repin PR's required checks don't run on their own. Pick one:

- **Zero-touch (recommended):** create a minimal GitHub App — permissions
  **`contents: write`** + **`pull requests: write`**, installed on this repo,
  **not added to any ruleset bypass list** (so it has only normal-contributor
  power and can never push to protected `main`). Store its id in repo variable
  **`REPIN_APP_ID`** and its private key in secret **`REPIN_APP_PRIVATE_KEY`**.
  `release-prep` mints a short-lived token, the PR's checks run, and it
  auto-merges after one maintainer approval.
- **No secret:** leave those unset. The verified PR is still opened with
  `GITHUB_TOKEN`; a maintainer **closes & reopens** it to fire CI, approves, and
  it auto-merges.

Either way there is no bypass of `main` and the commit stays verified — the only
difference is whether CI on the PR starts automatically.

## Manual fallback

If you ever need to repin by hand (e.g. images for the release commit already
exist from a prior build), run the same script locally and open the PR yourself:

```bash
.github/scripts/repin-x402-images.sh <short-sha-with-images-in-ghcr>
# commit the two changed templates, push a branch, open a PR to main
```

`verify-x402-pins.sh [<ref>]` validates the result (pins share one build commit,
digests match GHCR, pin commit is an ancestor, no x402 import-graph drift after
the pin).
