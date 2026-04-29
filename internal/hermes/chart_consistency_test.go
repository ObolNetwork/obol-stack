package hermes

import (
	"os"
	"regexp"
	"testing"
)

// TestRemoteSignerChartVersionConsistency catches drift between the two
// independent pins of the shared `remote-signer` Helm chart.
//
// The same chart is referenced from internal/hermes/hermes.go and
// internal/openclaw/openclaw.go — once each, in private package constants
// — and Renovate has historically updated only one of the two when a new
// chart version landed. That left Hermes pinned to chart 0.3.0 (image
// v0.1.0, legacy u64 signer contract) while OpenClaw moved to 0.3.1
// (image v0.2.0, canonical-string contract), which silently broke
// `obol sell register` for any flow that exercised the Hermes path
// against the post-PR-#359 client.
//
// This test is intentionally a string match against the source files so
// it works without exporting the constants or restructuring the package
// graph. If you bump one pin, bump the other in the same commit.
func TestRemoteSignerChartVersionConsistency(t *testing.T) {
	hermesV := readChartPin(t, "hermes.go")
	openclawV := readChartPin(t, "../openclaw/openclaw.go")
	if hermesV != openclawV {
		t.Fatalf("remote-signer chart pin drift:\n  internal/hermes/hermes.go      = %q\n  internal/openclaw/openclaw.go = %q\nbump both pins in the same commit",
			hermesV, openclawV)
	}
}

func readChartPin(t *testing.T, relPath string) string {
	t.Helper()
	raw, err := os.ReadFile(relPath)
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	re := regexp.MustCompile(`(?m)^\s*remoteSignerChartVersion\s*=\s*"([^"]+)"`)
	m := re.FindStringSubmatch(string(raw))
	if len(m) < 2 {
		t.Fatalf("could not find remoteSignerChartVersion in %s", relPath)
	}
	return m[1]
}
