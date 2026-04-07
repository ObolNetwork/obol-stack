# PR Review Transcript: `feat/monetize-path`

**Reviewer**: Oisin + Claude Code | **Date**: 2026-04-07 | **Branch**: `feat/monetize-path` into `main`

212 files changed, ~29K insertions. Bulk of the diff is the new serviceoffer-controller (Go), autoresearch skills, flow validation scripts, and lint/cosmetic cleanup.

---

## 1. Reth ERC-8004 Indexer — Removed

**Finding**: A standalone Rust crate (`reth-erc8004-indexer/`) and `Dockerfile.reth-erc8004-indexer` were added but had zero consumers. Discovery uses on-chain RPC; autoresearch coordinator hardcodes 8004scan.io. The `internal/network/validate.go` had config validation for indexer flags but `validateInstallOptions()` was never called from production code, and the `values.yaml.gotmpl` was never updated with the template fields.

**Decision**: Removed entirely. The ExEx-on-archive-reth concept is sound — when a consumer needs indexed ERC-8004 data, it can be brought back behind an `--archive` flag on `obol network install ethereum` (gated to reth-only, piggybacking on reth's execution extension system).

**Removed**:
- `reth-erc8004-indexer/` (entire Rust crate)
- `Dockerfile.reth-erc8004-indexer`
- `internal/network/validate.go` + `validate_test.go` (dead code)
- `ralph-m3.md` (Codex prompt for indexer validation)
- Indexer references in `autoresearch-coordinator/SKILL.md` (`OBOL_INDEXER_API_URL` env var, "internal indexer preference" language)

---

## 2. Significant Non-Cosmetic Changes Identified

| Change | Scope |
|--------|-------|
| **serviceoffer-controller** (Go) | ~2400 lines. Replaces Python `monetize.py` reconciler with a proper K8s controller watching ServiceOffers, creating child resources (Middleware, HTTPRoute, RegistrationRequest). |
| **RegistrationRequest CRD** | New resource decoupling ERC-8004 registration from ServiceOffer lifecycle. |
| **x402 verifier ServiceOffer source** | Verifier now watches ServiceOffers directly for live pricing routes via `WatchServiceOffers()` + `ConfigAccumulator`, instead of relying solely on the static `x402-pricing` ConfigMap. |
| **`monetize.py` rewrite** | Gutted to a compatibility shim — creates/deletes ServiceOffers via K8s API, waits for controller convergence. `/skill.md` publishing is a no-op. |
| **Autoresearch skills** | 3 new skills (coordinator, worker, autoresearch) with `Dockerfile.worker`. Deployment path is agent-driven via the skill + `obol sell http`. |
| **`flows/`** | 10 end-to-end validation scripts covering the full user journey. |
| **dev_registry.go** | Local k3d registry mirrors for docker.io/ghcr.io/quay.io. |

---

## 3. x402 ConfigMap vs ServiceOffer Informer — Tech Debt Assessment

**Question**: Is the dual route source (ConfigMap file watcher + ServiceOffer informer) tech debt?

**Answer**: Partially. The `x402-pricing` ConfigMap is still needed for **global identity** (wallet, chain, facilitatorURL) — the verifier needs these at startup before any ServiceOffer exists. However, the `routes[]` array in the ConfigMap is vestigial. Nothing in the current flow writes routes to it; the controller creates routes via the informer path. `AddRoute()`, `DeleteStaticOfferRoute()`, `DeletePaymentRoute()`, `WritePricingConfig()`, and all `RouteOption` types were dead code — only called from one integration test.

**Action**: Removed all dead route-management functions from `setup.go`. Replaced the integration test's `addPricingRoute()` helper with a no-op + sleep (controller handles routes now). Cleaned up `setup_test.go` to remove tests for deleted functions. Updated `docs/x402-test-plan.md`.

---

## 4. Bugs Fixed

### Critical
| Bug | Location | Fix |
|-----|----------|-----|
| **Silent tombstone failure** — `SetMetadata()` error discarded with `_` on the on-chain deactivation call. Deleted service stays registered as active. | `controller.go:778` | Log the error so operators know metadata wasn't cleared. |
| **Port parsing ignores errors** — `strconv.Atoi(port)` error discarded, malformed port silently becomes 0, creating broken K8s Services. | `sell.go:1931, 2012` | Validate and return user-facing error. Changed `buildInferenceServiceOfferSpec` to return `(map, error)`. |
| **Deletion requeue bug** — `reconcileDeletingOffer()` returns `nil` when cleanup isn't ready, causing `Forget()` on the workqueue. Offer stalls in deleting state. | `controller.go:366` | Return error to trigger `AddRateLimited` requeue. |

### High
| Bug | Location | Fix |
|-----|----------|-----|
| **CRD port field unvalidated** — no min/max, user could set port 0 or 99999. | `serviceoffer-crd.yaml:102` | Added `minimum: 1, maximum: 65535`. |
| **CRD path field unvalidated** — user could set `/../admin` or empty string. | `serviceoffer-crd.yaml:182` | Added `pattern: "^/[a-zA-Z0-9/_.-]*$"`. |
| **Probe URL unsanitized** — endpoint from CRD metadata concatenated directly into URL. | `sell.go:998` | Validate endpoint starts with `/`, reject `..` traversal. |
| **K8s name overflow** — `childName("so-" + name)` had no length check. Long ServiceOffer names exceed 253-char DNS limit. | `render.go:445` | Added `safeName()` helper with hash-based truncation. New tests in `render_test.go`. |

### Medium
| Bug | Location | Fix |
|-----|----------|-----|
| **`/skill.md` doc mismatch** — `monetize.py` disables publishing (no-op) but `SKILL.md` documents it as active. | `sell/SKILL.md:110` | Updated architecture diagram to reflect controller ownership. |
| **Metadata sync not surfaced** — error logged but status set to "Registered" anyway. | `controller.go:680` | Improved logging with explicit success/failure messages. |
| **No log on key loading** — silent whether ERC-8004 signing key loaded or not. | `controller.go:82` | Added startup log indicating key presence or absence. |

---

## 5. Dead Code Removed

| Item | Reason |
|------|--------|
| `sellInfoCommand` — defined but not registered | Wired it up (user's choice to keep it). |
| `AddRoute()`, `RouteOption`, `With*` options, `DeleteStaticOfferRoute`, `DeletePaymentRoute`, `WritePricingConfig`, `sameRouteIdentity` | ConfigMap route path replaced by ServiceOffer informer. |
| `ralph-m1.md`, `ralph-m2.md`, `ralph-m3.md` | Codex agent prompt files, not project artifacts. |

---

## 6. Rename: `sell probe` to `sell test`

Renamed `obol sell probe` to `obol sell test` with a simplified user-facing description ("Test that a service is live and requiring payment"). Updated across:
- `cmd/obol/sell.go` (command name, usage, description, examples, error messages)
- `internal/embed/skills/monetize-guide/SKILL.md`
- `internal/embed/skills/monetize-guide/references/seller-prompt.md`

---

## 7. Deferred / Not Addressed

| Item | Reason |
|------|--------|
| **Controller RBAC too broad** — cluster-wide Services/ConfigMaps/Deployments mutation. | Intentionally deferred; tightening namespace scope is a follow-up. |
| **Test coverage gaps** — controller has 101 lines of tests for 1225 LOC; sell_test.go tests structure only, not actions. | Flagged, not blocking. Controller needs integration tests before handling real money. |
| **Dual creation path** — both Go CLI and monetize.py can create ServiceOffers without coordination. | Architectural debt from the migration. monetize.py is documented as a compatibility shim. |

---

## 8. Retracted Concerns

| Initial Concern | Why Retracted |
|----------------|---------------|
| Race condition in `registrationOwner()` | Informer store (`cache.Store`) is thread-safe — uses internal locking. |
| `Dockerfile.worker` has no deployment path | It does — via the `autoresearch-worker` skill + `obol sell http`. Agent-driven, not CLI-driven. |

---

## 9. Verification

All changes verified:
- `go build ./...` — clean
- `go test ./...` — all 22 test packages pass, zero failures
- `grep` sweep — no stale references to removed code
