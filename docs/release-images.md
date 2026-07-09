# Release images (stack-owned)

Stack-owned container images (`x402-verifier`, `serviceoffer-controller`,
`x402-buyer`, `job-broker`, `demo-server`, storefront) are **not** digest-pinned
in git.

## Policy

| When | Image ref |
|------|-----------|
| Production CLI (`version.GitCommit` set) | `repo:<short-sha>@sha256:<index-digest>` when GHCR is reachable at apply time; else `repo:<short-sha>` |
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
- Digests are **bound at apply time** from GHCR (multi-arch index digest), never
  committed. Set `OBOL_SKIP_IMAGE_DIGEST=true` for offline applies/tests.

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
