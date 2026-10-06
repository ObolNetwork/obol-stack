package version

import (
	"runtime/debug"
	"testing"
)

func TestApplyBuildInfo_VCSFallback(t *testing.T) {
	saved := [...]string{Version, GitCommit, BuildTime, GitDirty}
	savedVCS := CommitFromVCS
	t.Cleanup(func() {
		Version, GitCommit, BuildTime, GitDirty = saved[0], saved[1], saved[2], saved[3]
		CommitFromVCS = savedVCS
	})

	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.time", Value: "2026-10-05T12:34:56Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}

	Version, GitCommit, BuildTime, GitDirty, CommitFromVCS = "dev", unknownValue, unknownValue, "false", false
	applyBuildInfo(bi)

	if GitCommit != "0123456" || BuildTime != "20261005123456" || GitDirty != "true" || !CommitFromVCS || Version != "dev" {
		t.Fatalf("fallback not applied: commit=%q time=%q dirty=%q vcs=%v version=%q", GitCommit, BuildTime, GitDirty, CommitFromVCS, Version)
	}

	// ldflags-injected values win.
	Version, GitCommit, BuildTime, GitDirty, CommitFromVCS = "1.2.3", "abcdef0", "20250101000000", "false", false
	applyBuildInfo(bi)

	if GitCommit != "abcdef0" || BuildTime != "20250101000000" || GitDirty != "false" || CommitFromVCS || Version != "1.2.3" {
		t.Fatalf("ldflags values overridden: commit=%q time=%q dirty=%q vcs=%v version=%q", GitCommit, BuildTime, GitDirty, CommitFromVCS, Version)
	}
}
