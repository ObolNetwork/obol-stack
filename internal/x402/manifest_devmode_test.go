package x402

import (
	"strings"
	"testing"
)

func TestX402Manifest_DevModeRewritesPins(t *testing.T) {
	t.Setenv("OBOL_DEVELOPMENT", "true")
	out := string(x402ManifestForApply())

	for _, want := range []string{
		"ghcr.io/obolnetwork/x402-verifier:latest",
		"ghcr.io/obolnetwork/serviceoffer-controller:latest",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dev mode did not rewrite to %q", want)
		}
	}
	for _, bad := range []string{
		"ghcr.io/obolnetwork/x402-verifier:b13254e",
		"ghcr.io/obolnetwork/serviceoffer-controller:b13254e",
	} {
		if strings.Contains(out, bad) && !strings.Contains(out, ":latest@sha256:") {
			// b13254e in a *comment* would be acceptable, but the regex doesn't
			// match comments preceded by '#' — flag any unrewritten image: line.
			for _, line := range strings.Split(out, "\n") {
				trim := strings.TrimSpace(line)
				if strings.HasPrefix(trim, "image:") && strings.Contains(trim, bad) {
					t.Errorf("dev mode left immutable pin on image line: %q", line)
				}
			}
		}
	}
}

func TestX402Manifest_ProductionPreservesPins(t *testing.T) {
	t.Setenv("OBOL_DEVELOPMENT", "")
	out := string(x402ManifestForApply())
	if !strings.Contains(out, "ghcr.io/obolnetwork/x402-verifier:b13254e") {
		t.Error("production manifest should preserve x402-verifier:b13254e pin")
	}
}
