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
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
