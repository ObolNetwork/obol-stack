// Package helmcmd contains small helpers for invoking the pinned helm binary.
//
// The main job here is keeping `helmfile sync` working across helm major
// versions. obol pins Helm 4, which server-side applies fresh installs but
// keeps upgrades on the previous release's method (--server-side=auto), and
// SSA introduces field-ownership conflicts (the apiserver synthesises a
// "before-first-apply" manager for any field that pre-existed the first SSA
// call) that helm only takes over when --force-conflicts is passed. Helm 3
// (an older obol-managed copy, or OBOL_HELM) used client-side apply and rejects both
// flags, so they are only appended on helm 4+.
package helmcmd

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// versionRE matches the major number in `helm version --short` output, e.g.
//
//	v4.1.3+gc94d381
//	v3.20.1+g4d04eef
var versionRE = regexp.MustCompile(`^v(\d+)\.`)

// MajorVersion runs `<helmBinary> version --short` and returns the major
// version integer (3, 4, ...). Returns an error if helm cannot be invoked
// or the output is unparseable.
func MajorVersion(helmBinary string) (int, error) {
	out, err := exec.Command(helmBinary, "version", "--short").Output()
	if err != nil {
		return 0, fmt.Errorf("invoke %s version: %w", helmBinary, err)
	}
	return parseMajor(string(out))
}

func parseMajor(short string) (int, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(short))
	if len(m) != 2 {
		return 0, fmt.Errorf("parse helm version %q: no leading vN. found", short)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("parse helm major %q: %w", m[1], err)
	}
	return major, nil
}

// UpgradeFlagsForVersion returns the extra `helm upgrade --install` flags
// for the detected helm version: on helm 4+ `--server-side=true
// --force-conflicts`, on helm 3 (or when detection fails) nil.
//
// Why both flags:
//   - --server-side=true: helm 4 upgrades default to --server-side=auto, i.e.
//     whatever method the previous release revision used. Releases first
//     installed by helm 3 were client-side applied, so under auto they would
//     stay client-side forever while fresh installs are SSA. Forcing true
//     converges every existing release on SSA (one-time switch; afterwards
//     auto would pick SSA anyway, so passing it on every sync is a no-op).
//   - --force-conflicts: SSA refuses to overwrite fields owned by another
//     field manager. After the CSA→SSA switch the apiserver attributes
//     pre-existing fields to managers like `kubectl-client-side-apply`,
//     `before-first-apply`, or obol's own `kubectl apply --server-side`
//     paths (e.g. the `obol model` patch on litellm-config, the hermes
//     remote-signer password Secret). helm is the source of truth for what it
//     renders, so it takes ownership — the same last-writer-wins outcome as
//     helm 3's three-way merge.
//
// Helm 3 has no SSA and rejects both flags. Detection failures degrade
// silently to nil so a missing/odd helm binary doesn't block the user; the
// helm call itself surfaces the real error.
func UpgradeFlagsForVersion(helmBinary string) []string {
	major, err := MajorVersion(helmBinary)
	if err != nil {
		return nil
	}

	return upgradeFlagsForMajor(major)
}

func upgradeFlagsForMajor(major int) []string {
	if major < 4 {
		return nil
	}

	return []string{"--server-side=true", "--force-conflicts"}
}

// SyncFlagsForVersion returns the extra `helmfile sync` flags for the
// detected helm version: UpgradeFlagsForVersion's flags wrapped in a single
// `--sync-args=...` (helmfile splits it on whitespace and appends each word
// to its `helm upgrade --install` call). nil on helm 3.
func SyncFlagsForVersion(helmBinary string) []string {
	major, err := MajorVersion(helmBinary)
	if err != nil {
		return nil
	}

	return syncFlagsForMajor(major)
}

func syncFlagsForMajor(major int) []string {
	flags := upgradeFlagsForMajor(major)
	if len(flags) == 0 {
		return nil
	}

	return []string{"--sync-args=" + strings.Join(flags, " ")}
}

// helmfileRepo mirrors the shape of each entry under the top-level
// `repositories:` key in a helmfile.yaml. Only the fields we need are decoded;
// extra keys (oci, username, passwordRef, ...) are ignored.
type helmfileRepo struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// helmfileDoc is the minimal shape of helmfile.yaml we care about for the
// repo-update preflight: just the `repositories:` block.
type helmfileDoc struct {
	Repositories []helmfileRepo `yaml:"repositories"`
}

// ParseHelmfileRepos extracts (name, url) entries from a helmfile.yaml file.
// Repos without both a name and a URL (e.g. OCI-only refs) are skipped — they
// are not added via `helm repo add` so they don't participate in the
// `helm repo update` path that this package guards against.
func ParseHelmfileRepos(helmfilePath string) ([]helmfileRepo, error) {
	data, err := os.ReadFile(helmfilePath)
	if err != nil {
		return nil, fmt.Errorf("read helmfile %s: %w", helmfilePath, err)
	}
	return parseHelmfileReposBytes(data)
}

func parseHelmfileReposBytes(data []byte) ([]helmfileRepo, error) {
	var doc helmfileDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse helmfile yaml: %w", err)
	}

	out := make([]helmfileRepo, 0, len(doc.Repositories))
	for _, r := range doc.Repositories {
		if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// EnsureRepos registers each (name, url) pair via `helm repo add --force-update`
// so that a fresh host without `helm repo add` for our managed repos still gets
// them registered before we ask helm to update them by name. Best-effort:
// failures are returned for visibility but should not be treated as fatal by
// callers (the subsequent `helm repo update` will surface real problems).
func EnsureRepos(helmBinary string, repos []helmfileRepo) error {
	var firstErr error
	for _, r := range repos {
		cmd := exec.Command(helmBinary, "repo", "add", "--force-update", r.Name, r.URL)
		if out, err := cmd.CombinedOutput(); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("helm repo add %s %s: %w (%s)", r.Name, r.URL, err, strings.TrimSpace(string(out)))
			}
		}
	}
	return firstErr
}

// RepoUpdateSupportsFailOnRepoUpdateFail reports whether the current helm
// binary accepts `helm repo update --fail-on-repo-update-fail=false`.
//
// Do not infer this from the major version. Some Helm 4 builds dropped the flag
// even though Helm 3.14+ had it, and passing an unknown flag prevents the
// targeted repo update from running at all.
func RepoUpdateSupportsFailOnRepoUpdateFail(helmBinary string) bool {
	cmd := exec.Command(helmBinary, "repo", "update", "--help")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "--fail-on-repo-update-fail")
}

// UpdateRepos runs `helm repo update <names...>` and, when the helm binary
// advertises support, passes --fail-on-repo-update-fail=false so that a single
// dead repo doesn't abort the whole update.
//
// Behaviour:
//   - helm versions that advertise --fail-on-repo-update-fail: the flag is
//     passed and the returned error is nil even if individual repos in `names`
//     fail.
//   - other helm versions: the flag is omitted and the error surfaces normally.
//
// The targeted form (`helm repo update <names...>`) is important: it limits the
// update to repos this stack actually needs, so unrelated dead repos in the
// user's global helm config can't break us even on helm versions that lack the
// tolerant flag.
func UpdateRepos(helmBinary string, names []string) ([]byte, error) {
	if len(names) == 0 {
		return nil, nil
	}
	args := []string{"repo", "update"}
	if RepoUpdateSupportsFailOnRepoUpdateFail(helmBinary) {
		args = append(args, "--fail-on-repo-update-fail=false")
	}
	args = append(args, names...)

	cmd := exec.Command(helmBinary, args...)
	out, err := cmd.CombinedOutput()
	return out, err
}

// Helmfile builds a helmfile command that is pinned to helmBinary via
// --helm-binary. Without it helmfile runs whatever `helm` is first on PATH,
// while SyncFlagsForVersion probes the obol-resolved binary: with a Helm 4
// on PATH and a Helm 3 in the bin dir, sync ran Helm 4's
// server-side apply WITHOUT --force-conflicts and failed on field-manager
// conflicts (e.g. a Secret previously written by `kubectl apply`). Use this
// for every helmfile invocation so the probed and executed helm match.
func Helmfile(helmfileBinary, helmBinary string, args ...string) *exec.Cmd {
	return exec.Command(helmfileBinary, append([]string{"--helm-binary", helmBinary}, args...)...)
}
