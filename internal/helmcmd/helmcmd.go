// Package helmcmd contains small helpers for invoking the pinned helm binary.
//
// The main job here is keeping `helmfile sync` working across helm major
// versions. Helm 4 turned server-side apply on by default; SSA introduces
// field-ownership conflicts (the apiserver synthesises a "before-first-apply"
// manager for any field that pre-existed the first SSA call) that helm 4
// only takes over when --force-conflicts is passed. Helm 3 used client-side
// apply, has no SSA and rejects --force-conflicts as an unknown flag, so the
// flag must only be appended on helm 4+.
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

// SyncFlagsForVersion returns the extra `helmfile sync` flags needed for the
// detected helm version. On helm 4+ this is --sync-args=--force-conflicts so
// helm's SSA upgrade can take ownership of fields previously written by other
// managers (e.g. `kubectl apply`, the `obol model setup` patch on
// litellm-config.data.config.yaml, or fields recorded under the synthetic
// "before-first-apply" manager). On helm 3 this returns nil — helm 3 uses
// client-side apply and rejects --force-conflicts.
//
// Detection failures degrade silently to nil so a missing/old helm binary
// doesn't block the user; the helmfile sync will still surface the real error.
func SyncFlagsForVersion(helmBinary string) []string {
	major, err := MajorVersion(helmBinary)
	if err != nil || major < 4 {
		return nil
	}
	return []string{"--sync-args=--force-conflicts"}
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

// ManagedRepoNames returns just the repo names from a helmfile.yaml. These are
// the only repos this stack is responsible for keeping up to date; everything
// else in the user's global `helm repo list` belongs to other tools.
func ManagedRepoNames(helmfilePath string) ([]string, error) {
	repos, err := ParseHelmfileRepos(helmfilePath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	return names, nil
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
