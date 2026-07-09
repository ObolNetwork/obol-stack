# Release images (stack-owned)

Stack-owned container images (`x402-verifier`, `serviceoffer-controller`,
`x402-buyer`, `job-broker`, `demo-server`, storefront) are **not** digest-pinned
in git.

## Policy

| When | Image ref |
|------|-----------|
| Production CLI (`version.GitCommit` set) | `repo:<short-sha>@sha256:<index-digest>` |
| `OBOL_DEVELOPMENT=true` | `repo:dev-<sha>` (local k3d import tag) |
| Unknown / dirty build | `repo:latest` |

- Short-SHA tags are published by `docker-publish-x402.yml` on every relevant
  `main` push and on `v*` tags (bake → `docker-bake.hcl` / `Dockerfile.x402`).
- CI builds all five pure-Go images in **one bake job**: shared builder stage,
  native cross-compile (`BUILDPLATFORM` + `GOARCH`, no QEMU), shared GHA cache.
- The CLI embeds the same short SHA via goreleaser ldflags
  (`version.GitCommit`).
- `internal/images.Resolve` is the single policy. Embedded templates use the
  placeholder `:__OBOL_IMAGE__`; `CopyInfrastructure` rewrites it at apply time.
- Digests are **not committed to git**. On first resolve for a given
  `repo:short-sha`, the multi-arch index digest is fetched from GHCR and
  **persisted** under `$OBOL_CONFIG_DIR/image-digests.json`. Later applies with
  the same CLI version reuse that pin and do **not** re-query GHCR, so a
  retagged short-SHA cannot change images under an existing install.
  - `OBOL_SKIP_IMAGE_DIGEST=true` — never bind digests (tests / air-gap).
  - `OBOL_REFRESH_IMAGE_DIGESTS=true` — re-bind from GHCR and overwrite pins.

## Security considerations

| Risk | Mitigation |
|------|------------|
| Short-SHA tag on GHCR is overwritten | First-bind-then-persist digests; subsequent `stack up` reuses the pin. Operator policy: never retag published short SHAs. |
| Package write ACL on `ghcr.io/obolnetwork/*` | Restrict who can push; short SHA is only as trustworthy as that ACL. |
| Cross-host / fresh install | First resolve on a new host re-fetches GHCR (same short SHA should resolve to the same digest if tags were not retagged). |
| Digests-in-git (old model) | Stronger “same manifest everywhere forever”, but forced the repin PR train. We traded that for install-local durability. |

## Release train

```text
merge to main → docker-publish-x402 builds :shortsha → smoke → tag on main
```

No repin PR. No `release-prep` workflow.

Before tagging:

```bash
# Images for this commit on GHCR?
.github/scripts/verify-release-images.sh HEAD

# Offline dry-run (SHA only)
VERIFY_RELEASE_IMAGES_OFFLINE=true .github/scripts/verify-release-images.sh HEAD
```

If the gate fails, the commit never published images (path filter skipped the
push). Force a build:

```bash
gh workflow run docker-publish-x402.yml --ref <commit-or-main>
```

## Why not digests in git?

Embedding digests forced a continuous repin PR train (branch protection, verified
commits, rebuild loops). The binary's GitCommit already selects the correct
image generation; apply-time digest bind keeps supply-chain strength without
git ceremony.
