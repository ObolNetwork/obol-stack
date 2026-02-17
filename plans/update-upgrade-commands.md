# Plan: `obol update` & `obol upgrade` Commands

## Summary

Add two new CLI commands for keeping the Obol Stack current:

| Command | Purpose |
|---------|---------|
| `obol update` | Refresh helm repo indexes and check for newer chart versions & CLI releases |
| `obol upgrade` | Apply available helm chart upgrades; advise user to re-run installer for CLI binary upgrades |

## Design

### `obol update`

**What it does (in order):**

1. **Update helm repos** — Run `helm repo update` (via `cfg.BinDir/helm`) with KUBECONFIG set. This refreshes the local index for all repos defined in the infrastructure helmfile (traefik, prometheus-community, obol, ethereum, bedag, stakater).

2. **Check installed chart versions vs available** — For each pinned release in the defaults helmfile (`internal/embed/infrastructure/helmfile.yaml`), compare the installed version with the latest available in the repo. Use `helm search repo <chart> --versions --output json` to get the latest, then compare against the pinned version in the helmfile. Print a table of results:
   ```
   Chart                                      Installed   Latest    Status
   prometheus-community/kube-prometheus-stack  79.5.0      80.1.0    Update available
   traefik/traefik                             38.0.2      38.0.2    Up to date
   stakater/reloader                           2.2.7       2.3.0     Update available
   obol/obol-app                               0.1.1       0.1.1     Up to date
   ```
   **Note:** Charts without a pinned `version:` in the helmfile (e.g., erpc, cloudflared local charts) are skipped or shown as "unpinned — will use latest on upgrade".

3. **Check for installed network chart updates** — Scan `cfg.ConfigDir/networks/` for installed deployments. For each, parse the `helmfile.yaml.gotmpl` to find chart references and pinned versions. Report any that have newer versions available. This is informational — network chart versions are embedded in the binary, so updating them requires a CLI binary update.

4. **Check CLI binary version** — HTTP GET `https://api.github.com/repos/ObolNetwork/obol-stack/releases/latest`, parse `tag_name` from the JSON response. Compare against `version.Short()` (strip leading `v` from tag). Print result:
   ```
   Obol CLI   v0.1.0    v0.2.0    Update available
   ```
   or:
   ```
   Obol CLI   v0.2.0    v0.2.0    Up to date
   ```
   If the current version is `dev` (development mode), print:
   ```
   Obol CLI   dev       v0.2.0    Development build (skipped)
   ```

5. **Print summary** — If any updates are available:
   - Helm chart updates: `Run 'obol upgrade' to apply helm chart updates.`
   - CLI binary update: `Run the following command to update the obol CLI:\n  bash <(curl -s https://stack.obol.org)`

**Flags:**
- `--json` — Output results as JSON (for scripting/automation)

**Does NOT require a running cluster** for the CLI version check. The helm repo update and chart checks DO require a running cluster (kubeconfig must exist).

### `obol upgrade`

**What it does (in order):**

1. **Verify cluster is running** — Check kubeconfig exists at `cfg.ConfigDir/kubeconfig.yaml`. Fail with "stack not running, use 'obol stack up' first" if missing.

2. **Run helm repo update** — Same as `obol update` step 1. Always refresh before upgrading.

3. **Re-sync default infrastructure** — Call the existing `syncDefaults()` pattern (helmfile sync on the defaults helmfile). This upgrades all default stack charts (Traefik, Prometheus, Reloader, eRPC, frontend, cloudflared) to whatever versions are pinned in the embedded helmfile. Since `helmfile sync` is idempotent, charts already at the correct version are no-ops.

4. **Re-sync installed networks** — Iterate over `cfg.ConfigDir/networks/<network>/<id>/` directories. For each installed deployment, run `helmfile sync` (same as `obol network sync <network>/<id>`). This ensures network deployments pick up any chart changes. Skip with a warning if the helmfile or values.yaml is missing.

5. **Re-sync installed apps** — Iterate over `cfg.ConfigDir/apps/` directories. For each installed app, run `helmfile sync`. This ensures custom apps are also upgraded.

6. **Check CLI binary version** — Same check as `obol update` step 4. If a newer CLI version is available, print:
   ```
   A newer version of the obol CLI is available (v0.1.0 → v0.2.0).
   To update the CLI binary, run:

     bash <(curl -s https://stack.obol.org)

   The installer will detect your existing installation and upgrade safely.
   ```
   The CLI does NOT self-update. The installer handles binary replacement because it also manages dependency versions (kubectl, helm, k3d, helmfile, k9s) and system configuration (PATH, /etc/hosts).

**Flags:**
- `--defaults-only` — Only upgrade default infrastructure, skip networks and apps
- `--dry-run` — Show what would be upgraded without applying changes (pass `--args '--dry-run'` to helmfile, or use `helmfile diff`)

### Why the CLI cannot self-update

The obolup.sh installer manages more than just the `obol` binary:
- Pinned dependency versions (kubectl, helm, k3d, helmfile, k9s)
- System PATH configuration
- `/etc/hosts` entry for `obol.stack`
- Smart binary discovery (symlinks vs downloads)
- Platform-specific binary downloads

A CLI self-update would only replace the Go binary and miss all dependency version bumps. The installer is the correct mechanism for holistic upgrades.

## Implementation

### New Files

| File | Purpose |
|------|---------|
| `internal/update/update.go` | Core update logic: helm repo update, chart version checks, GitHub release check |
| `internal/update/github.go` | GitHub Releases API client (check latest release, compare versions) |
| `internal/update/charts.go` | Helm chart version comparison logic (parse helmfile, query helm search) |
| `internal/update/hint.go` | Lightweight embedded-vs-on-disk helmfile comparison for `stack up` hint |
| `cmd/obol/update.go` | CLI command definitions for `obol update` and `obol upgrade` |

### Modified Files

| File | Change |
|------|--------|
| `cmd/obol/main.go` | Register `updateCommand()` and `upgradeCommand()` in the Commands slice; add to help template |
| `internal/stack/stack.go` | Call `update.HintIfStale(cfg)` at end of `Up()` to show upgrade hint when charts are stale |
| `go.mod` | Add `golang.org/x/mod` for `semver.Compare()` (standard Go semver library) |

### Package: `internal/update`

#### `github.go` — GitHub Release Checker

```go
package update

// LatestRelease holds info about the latest GitHub release
type LatestRelease struct {
    TagName string // e.g., "v0.2.0"
    Version string // e.g., "0.2.0" (tag stripped of leading "v")
    URL     string // HTML URL to the release page
}

// CheckLatestRelease queries GitHub API for the latest obol-stack release.
// Returns the latest release info, or an error if the API call fails.
// Uses stdlib net/http with a 15s timeout. No auth required (public repo).
func CheckLatestRelease() (*LatestRelease, error)

// CompareVersions compares two semver strings (without "v" prefix).
// Returns:
//   -1 if current < latest (update available)
//    0 if current == latest (up to date)
//   +1 if current > latest (ahead, e.g., dev build)
// Uses golang.org/x/mod/semver for reliable comparison.
func CompareVersions(current, latest string) int
```

**Implementation notes:**
- GitHub API endpoint: `https://api.github.com/repos/ObolNetwork/obol-stack/releases/latest`
- Parse JSON response: only need `tag_name` and `html_url` fields
- Strip leading `v` from `tag_name` for comparison
- Use `golang.org/x/mod/semver` — already part of Go's extended toolchain, reliable semver comparison
- If `version.Short()` returns `"dev"`, skip comparison and report as development build
- Handle rate limiting gracefully (403 response → "GitHub rate limit reached, try again later")

#### `charts.go` — Helm Chart Version Checker

```go
package update

// ChartStatus represents the update status of a single helm chart
type ChartStatus struct {
    Chart     string // e.g., "traefik/traefik"
    Installed string // Currently pinned version, e.g., "38.0.2"
    Latest    string // Latest available in repo
    Status    string // "Up to date", "Update available", "Unpinned"
}

// CheckChartVersions runs `helm search repo` for each pinned chart in the
// defaults helmfile and compares versions.
// Requires: helm binary at cfg.BinDir, KUBECONFIG for repo access.
func CheckChartVersions(cfg *config.Config) ([]ChartStatus, error)

// UpdateHelmRepos runs `helm repo update` to refresh all repo indexes.
func UpdateHelmRepos(cfg *config.Config) error
```

**Implementation notes:**
- Parse the defaults helmfile (`cfg.ConfigDir/defaults/helmfile.yaml`) for releases with `chart:` and `version:` fields. Use simple YAML parsing (the helmfile is already on disk after `stack init`).
- For each pinned chart, run `helm search repo <chart> --versions --output json -l 1` to get the latest version.
- Charts without `version:` field (local charts like `./base`, `./cloudflared`) are reported as "Local chart" and skipped.
- Charts like `bedag/raw` are utility charts — include them but they rarely update.

#### `update.go` — Orchestrator

```go
package update

// UpdateResult holds the complete results of an update check
type UpdateResult struct {
    HelmRepoUpdated  bool
    ChartStatuses    []ChartStatus
    CLIRelease       *LatestRelease
    CLIUpdateAvail   bool
    ChartUpdatesAvail bool
    IsDev            bool
}

// CheckForUpdates runs all update checks and returns a unified result.
// If clusterRunning is false, skips helm-related checks and only checks CLI version.
func CheckForUpdates(cfg *config.Config, clusterRunning bool) (*UpdateResult, error)

// ApplyUpgrades runs helmfile sync on defaults and all installed deployments.
func ApplyUpgrades(cfg *config.Config, defaultsOnly bool, dryRun bool) error
```

### Package: `cmd/obol`

#### `update.go` — CLI Commands

```go
// updateCommand returns the `obol update` command
func updateCommand(cfg *config.Config) *cli.Command {
    return &cli.Command{
        Name:  "update",
        Usage: "Check for available updates to helm charts and the obol CLI",
        Flags: []cli.Flag{
            &cli.BoolFlag{Name: "json", Usage: "Output results as JSON"},
        },
        Action: func(c *cli.Context) error {
            // 1. Check if cluster is running (for helm checks)
            // 2. If running: helm repo update + chart version checks
            // 3. Always: check CLI version against GitHub releases
            // 4. Print results table (or JSON)
            // 5. Print action summary
        },
    }
}

// upgradeCommand returns the `obol upgrade` command
func upgradeCommand(cfg *config.Config) *cli.Command {
    return &cli.Command{
        Name:  "upgrade",
        Usage: "Apply available helm chart upgrades to the running stack",
        Flags: []cli.Flag{
            &cli.BoolFlag{Name: "defaults-only", Usage: "Only upgrade default infrastructure"},
            &cli.BoolFlag{Name: "dry-run", Usage: "Show what would be upgraded without applying"},
        },
        Action: func(c *cli.Context) error {
            // 1. Verify cluster running
            // 2. helm repo update
            // 3. Re-copy embedded defaults (embed.CopyDefaults) to pick up new chart versions from binary
            // 4. helmfile sync defaults
            // 5. If !defaultsOnly: iterate networks + apps, helmfile sync each
            // 6. Check CLI version, print upgrade instructions if newer available
        },
    }
}
```

### Registration in `main.go`

Add to the `Commands` slice:
```go
updateCommand(cfg),
upgradeCommand(cfg),
```

Add to the help template under a new section or the existing "Other" section:
```
   update     Check for available updates
   upgrade    Apply available helm chart upgrades
```

### Upgrade flow for embedded defaults

A key subtlety: the defaults helmfile is copied from embedded assets to `cfg.ConfigDir/defaults/` during `stack init`. When a new CLI binary is installed (via `bash <(curl -s https://stack.obol.org)`), the embedded assets change but the on-disk copy does NOT automatically update.

`obol upgrade` should:
1. Re-run `embed.CopyDefaults()` to overwrite the on-disk defaults with the (potentially newer) embedded versions from the current binary
2. Run the hostname migration (same as `syncDefaults` does)
3. Then run `helmfile sync` on the updated defaults

This ensures that even if the user updated the CLI binary, the on-disk helmfile reflects the new chart versions.

### Upgrade hint on `obol stack up`

After a successful `stack up` (both fresh create and restart paths), run a **non-blocking, best-effort** background check for outdated charts. This keeps the happy path fast while nudging users toward `obol upgrade`.

**Behaviour:**
1. After the "Stack started successfully" / "Stack restarted successfully" message, spawn a goroutine (or just inline — the check is fast) that calls `update.CheckChartVersions()` on the on-disk defaults helmfile.
2. Compare the pinned versions in the on-disk `defaults/helmfile.yaml` against the versions embedded in the current binary. If the on-disk helmfile has older pins than what the binary ships, charts are stale.
3. If any charts are outdated, print a single low-disruption hint **after** all other output:
   ```
   Hint: Some stack components have updates available. Run 'obol upgrade' to apply.
   ```
4. If all charts are current, print nothing — zero noise on the happy path.
5. If the check fails (no network, helm not ready yet, etc.), silently swallow the error — this is purely informational.

**Implementation location:** Add the hint at the bottom of `stack.Up()` in `internal/stack/stack.go`, after the final success message and DNS setup. Call a lightweight function from the `update` package (e.g., `update.HintIfStale(cfg)`) that returns a string or empty.

**Why compare embedded vs on-disk (not remote)?**
- No network call required — instant, no latency added to `stack up`
- The embedded helmfile in the binary represents "what this CLI version ships". The on-disk copy is "what was deployed". If they differ, the user needs `obol upgrade` to sync them.
- Remote repo checks (`helm search repo`) are slow and belong in `obol update`, not on the startup path.

**Modified files:**

| File | Change |
|------|--------|
| `internal/stack/stack.go` | Add `update.HintIfStale(cfg)` call after success output in both the create and restart branches of `Up()` |
| `internal/update/hint.go` | New file: `HintIfStale(cfg) string` — compares embedded vs on-disk helmfile chart versions |

**Example UX:**
```
$ obol stack up
Starting stack: obol-stack-nervous-otter (id: nervous-otter)
Creating k3d cluster...
...
Default infrastructure deployed
Stack ID: nervous-otter

Stack started successfully.
Visit http://obol.stack in your browser to get started.
Try setting up an agent with `obol agent init` next.

Hint: Some stack components have updates available. Run 'obol upgrade' to apply.
```

When everything is current — no hint line at all.



### Phase 1: GitHub Release Checker (no cluster required)

1. **Add `golang.org/x/mod` dependency** — `go get golang.org/x/mod`
2. **Create `internal/update/github.go`** — GitHub API client with `CheckLatestRelease()` and `CompareVersions()`
3. **Write tests for `github.go`** — Mock HTTP responses, test version comparison edge cases (dev, equal, ahead, behind, pre-release)

### Phase 2: Helm Chart Version Checker

4. **Create `internal/update/charts.go`** — Helmfile parser + `helm search repo` wrapper
5. **Create `internal/update/update.go`** — Orchestrator combining both checks
6. **Write tests for `charts.go`** — Test helmfile parsing, mock helm output

### Phase 3: CLI Commands

7. **Create `cmd/obol/update.go`** — Both `obol update` and `obol upgrade` command definitions
8. **Modify `cmd/obol/main.go`** — Register commands, update help template
9. **Manual integration testing** — Run against a live cluster

### Phase 4: Stack Up Hint

10. **Create `internal/update/hint.go`** — `HintIfStale(cfg)` compares embedded vs on-disk helmfile chart pins
11. **Modify `internal/stack/stack.go`** — Call `update.HintIfStale(cfg)` at the end of both `Up()` paths (create + restart)
12. **Write test for `hint.go`** — Test with matching versions (no hint), mismatched versions (hint), missing files (no error)

### Phase 5: Polish

13. **Error handling** — Network failures, missing binaries, rate limiting
14. **Output formatting** — Aligned table output, color support if terminal
15. **JSON output** — Structured output for `--json` flag

## Testing Strategy

| Test | Type | What it validates |
|------|------|-------------------|
| `TestCompareVersions` | Unit | Semver comparison: equal, behind, ahead, dev, pre-release |
| `TestCheckLatestRelease` | Unit | GitHub API response parsing (mock HTTP) |
| `TestParseHelmfileVersions` | Unit | Extract chart+version pairs from a helmfile YAML |
| `TestCheckChartVersions` | Unit | Chart status determination from helm search output |
| `TestUpdateCommand` | Integration | Full `obol update` with mocked externals |
| `TestUpgradeCommand` | Integration | Full `obol upgrade` with mocked externals |

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| No internet connection | Helm repo update fails gracefully, GitHub check fails gracefully, print warning |
| Development build (`version = "dev"`) | Skip CLI version comparison, print "Development build" |
| Cluster not running (no kubeconfig) | Skip helm checks, only check CLI version |
| GitHub API rate limited (403) | Print "GitHub rate limit reached, try again later" |
| No installed networks | Skip network upgrade step, print "No networks installed" |
| Helmfile missing from deployment dir | Skip that deployment with warning |
| New CLI binary but old on-disk defaults | `obol upgrade` re-copies embedded defaults before sync |
| Chart version is unpinned in helmfile | Report as "Unpinned — uses latest" |

## Example UX

### `obol update`
```
$ obol update
Updating helm repositories...
  ✓ Helm repositories updated

Checking chart versions...
  Chart                                      Pinned     Latest     Status
  prometheus-community/kube-prometheus-stack  79.5.0     80.1.0     Update available
  traefik/traefik                             38.0.2     38.0.2     Up to date
  stakater/reloader                           2.2.7      2.3.0      Update available
  obol/obol-app                               0.1.1      0.1.2      Update available
  ethereum/erpc                               -          0.3.0      Unpinned
  ./base                                      -          -          Local chart
  ./cloudflared                               -          -          Local chart

Checking CLI version...
  Obol CLI                                    v0.1.0     v0.2.0     Update available

Summary:
  3 chart update(s) available. Run 'obol upgrade' to apply.
  CLI update available (v0.1.0 → v0.2.0). Run:
    bash <(curl -s https://stack.obol.org)
```

### `obol upgrade`
```
$ obol upgrade
Updating helm repositories...
  ✓ Helm repositories updated

Refreshing default infrastructure templates...
  ✓ Defaults updated from embedded assets

Upgrading default infrastructure...
  Deploying default infrastructure with helmfile
  ...helmfile sync output...
  ✓ Default infrastructure upgraded

Upgrading installed networks...
  Syncing ethereum/knowing-wahoo...
  ...helmfile sync output...
  ✓ ethereum/knowing-wahoo upgraded

Upgrading installed apps...
  No apps installed.

✓ All helm chart upgrades applied.

Note: A newer version of the obol CLI is available (v0.1.0 → v0.2.0).
To update the CLI binary and dependencies, run:

  bash <(curl -s https://stack.obol.org)
```

### `obol update` (no cluster running)
```
$ obol update
Helm check skipped (cluster not running)

Checking CLI version...
  Obol CLI                                    v0.2.0     v0.2.0     Up to date

Everything is up to date.
```
