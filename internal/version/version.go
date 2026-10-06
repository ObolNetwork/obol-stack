package version

import (
	"fmt"
	"runtime/debug"
	"time"
)

const unknownValue = "unknown" // default for unset ldflags fields

var (
	// These variables are set via ldflags during build
	Version   = "dev"        // Semantic version (e.g., "0.1.0")
	GitCommit = unknownValue // Git commit hash (e.g., "a751d4c")
	BuildTime = unknownValue // Build timestamp (e.g., "20251015123705")
	GitDirty  = "false"      // Whether repo had uncommitted changes

	// CommitFromVCS is true when GitCommit came from the toolchain's VCS
	// stamp (debug.ReadBuildInfo) rather than release ldflags. Such builds
	// have no published per-commit images, so image pinning must keep
	// treating them as untagged dev builds (see internal/images).
	CommitFromVCS bool
)

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		applyBuildInfo(bi)
	}
}

// applyBuildInfo fills fields that ldflags left unset from the VCS metadata
// the Go toolchain stamps into every binary built inside a git checkout
// (`go build`, `go install`), so plain builds still report their commit.
// Values injected via ldflags (justfile, goreleaser) always win.
func applyBuildInfo(bi *debug.BuildInfo) {
	if Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		Version = bi.Main.Version // e.g. `go install …@v0.15.0`
	}

	if GitCommit != unknownValue {
		return // ldflags build: trust the injected metadata as a set
	}

	var revision, vcsTime, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}

	if revision == "" {
		return
	}

	if len(revision) > 7 {
		revision = revision[:7]
	}

	GitCommit = revision
	CommitFromVCS = true

	if BuildTime == unknownValue && vcsTime != "" {
		if t, err := time.Parse(time.RFC3339, vcsTime); err == nil {
			BuildTime = t.UTC().Format("20060102150405")
		}
	}

	if modified == "true" {
		GitDirty = "true"
	}
}

// Full returns the full version string including all metadata
// Format: version+commit.timestamp[-dirty]

func Full() string {
	version := Version

	// Add build metadata if available
	if GitCommit != unknownValue && BuildTime != unknownValue {
		version = fmt.Sprintf("%s+%s.%s", version, GitCommit, BuildTime)
	} else if GitCommit != unknownValue {
		version = fmt.Sprintf("%s+%s", version, GitCommit)
	}

	// Add dirty flag if repo had uncommitted changes
	if GitDirty == "true" {
		version += "-dirty"
	}

	return version
}

// Short returns just the semantic version
func Short() string {
	return Version
}

// BuildInfo returns detailed build information
func BuildInfo() string {
	info := fmt.Sprintf("Version:    %s\n", Version)
	info += fmt.Sprintf("Git Commit: %s\n", GitCommit)
	info += fmt.Sprintf("Build Time: %s\n", BuildTime)
	info += fmt.Sprintf("Dirty Repo: %s\n", GitDirty)

	// Add Go version and build info
	if bi, ok := debug.ReadBuildInfo(); ok {
		info += fmt.Sprintf("Go Version: %s\n", bi.GoVersion)
	}

	return info
}
