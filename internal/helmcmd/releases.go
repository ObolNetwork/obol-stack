package helmcmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ReleaseHealth summarises the helm releases in one namespace.
type ReleaseHealth int

const (
	// ReleasesMissing: no release at all in the namespace (fresh cluster).
	ReleasesMissing ReleaseHealth = iota
	// ReleasesUnhealthy: at least one release is not in "deployed" state
	// (failed, pending-install, pending-upgrade, ...).
	ReleasesUnhealthy
	// ReleasesDeployed: every release in the namespace is deployed.
	ReleasesDeployed
)

func (h ReleaseHealth) String() string {
	switch h {
	case ReleasesDeployed:
		return "deployed"
	case ReleasesUnhealthy:
		return "unhealthy"
	default:
		return "missing"
	}
}

// NamespaceReleaseHealth runs `helm list -n <ns> -o json` (all statuses) and
// classifies the result. Cheap (one helm call, no chart rendering), so record
// replay can use it to decide whether an expensive `helmfile sync` is needed.
func NamespaceReleaseHealth(helmBinary, kubeconfig, namespace string) (ReleaseHealth, error) {
	major, verr := MajorVersion(helmBinary)
	if verr != nil {
		major = 4 // current pin; an unknown binary is far likelier to be new
	}
	cmd := exec.Command(helmBinary, listAllArgs(major, namespace)...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return ReleasesMissing, fmt.Errorf("helm list -n %s: %w %s", namespace, err, stderr)
	}
	return ParseReleaseHealth(out)
}

// ParseReleaseHealth classifies `helm list -o json` output.
func ParseReleaseHealth(out []byte) (ReleaseHealth, error) {
	var releases []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "null" {
		return ReleasesMissing, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &releases); err != nil {
		return ReleasesMissing, fmt.Errorf("parse helm list output: %w", err)
	}
	if len(releases) == 0 {
		return ReleasesMissing, nil
	}
	for _, r := range releases {
		if r.Status != "deployed" {
			return ReleasesUnhealthy, nil
		}
	}
	return ReleasesDeployed, nil
}

// listAllArgs builds `helm list` args covering every release status. Helm 4
// lists all statuses by default and removed -a/--all; Helm 3 needs -a to
// include pending/uninstalling releases.
func listAllArgs(major int, namespace string) []string {
	args := []string{"list", "-n", namespace}
	if major < 4 {
		args = append(args, "-a")
	}

	return append(args, "-o", "json")
}
