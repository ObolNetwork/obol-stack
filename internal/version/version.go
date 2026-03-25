package version

import (
	"fmt"
	"runtime/debug"
)

const unknownValue = "unknown" // default for unset ldflags fields

var (
	// These variables are set via ldflags during build
	Version   = "dev"        // Semantic version (e.g., "0.1.0")
	GitCommit = unknownValue // Git commit hash (e.g., "a751d4c")
	BuildTime = unknownValue // Build timestamp (e.g., "20251015123705")
	GitDirty  = "false"      // Whether repo had uncommitted changes
)

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
