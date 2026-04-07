package openclaw

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestOpenClawVersionConsistency ensures the three OpenClaw version sources
// stay in sync:
//
//  1. internal/openclaw/OPENCLAW_VERSION  — single source of truth (Renovate watches this)
//  2. openclawImageTag() in Go           — read via go:embed from the same file
//  3. obolup.sh OPENCLAW_VERSION         — shell constant for standalone installs
//
// If this test fails, update all three in the same commit.
func TestOpenClawVersionConsistency(t *testing.T) {
	// 1. Read the canonical version file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	versionFile := filepath.Join(filepath.Dir(thisFile), "OPENCLAW_VERSION")

	raw, err := os.ReadFile(versionFile)
	if err != nil {
		t.Fatalf("cannot read OPENCLAW_VERSION: %v", err)
	}

	var fileVersion string

	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fileVersion = strings.TrimPrefix(line, "v")

		break
	}

	if fileVersion == "" {
		t.Fatal("OPENCLAW_VERSION file has no version line")
	}

	// 2. Check the Go embedded version matches.
	goTag := openclawImageTag()
	if goTag != fileVersion {
		t.Errorf("openclawImageTag() = %q, want %q (from OPENCLAW_VERSION)", goTag, fileVersion)
	}

	// 3. Check obolup.sh constant matches.
	obolupPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "obolup.sh")

	obolupRaw, err := os.ReadFile(obolupPath)
	if err != nil {
		t.Fatalf("cannot read obolup.sh: %v", err)
	}

	re := regexp.MustCompile(`(?m)^readonly OPENCLAW_VERSION="([^"]+)"`)

	matches := re.FindSubmatch(obolupRaw)
	if matches == nil {
		t.Fatal("obolup.sh does not contain OPENCLAW_VERSION constant")
	}

	shellVersion := string(matches[1])
	if shellVersion != fileVersion {
		t.Errorf("obolup.sh OPENCLAW_VERSION = %q, want %q (from OPENCLAW_VERSION file)", shellVersion, fileVersion)
	}
}
