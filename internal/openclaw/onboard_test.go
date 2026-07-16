package openclaw

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// TestOnboardRejectsUnsafeID guards against the Canary402 full-surface audit
// finding: an unsanitized --id becomes a DNS label (hostname, namespace) and
// was written verbatim into /etc/hosts, so a newline in --id could inject
// arbitrary /etc/hosts lines via "sudo tee". Onboard must reject any id that
// isn't a safe DNS label before it reaches deployment/hostname construction.
func TestOnboardRejectsUnsafeID(t *testing.T) {
	invalid := []string{
		"evil\n127.0.0.1 attacker.com", // /etc/hosts line injection
		"has space",
		"has/slash",
		"-leading-dash",
	}
	for _, id := range invalid {
		cfg := testConfig(t)
		err := Onboard(cfg, OnboardOptions{ID: id}, ui.New(false))
		if err == nil {
			t.Errorf("Onboard(ID=%q) = nil, want error", id)
		}
	}
}

// TestOnboardRejectsIDTooLongForDNSLabel guards against the "openclaw-<id>"
// DNS label overflowing 63 characters: validate.Name alone allows a 63-char
// id, but Onboard prepends "openclaw-" to it, so an id at (or near) that
// limit must be rejected here with a clear error instead of failing later
// with an opaque Kubernetes error.
func TestOnboardRejectsIDTooLongForDNSLabel(t *testing.T) {
	max := agentruntime.MaxIDLength(agentruntime.OpenClaw)

	tooLong := "a" + strings.Repeat("b", max) // max+1 chars, still a valid DNS label on its own
	cfg := testConfig(t)
	err := Onboard(cfg, OnboardOptions{ID: tooLong}, ui.New(false))
	if err == nil {
		t.Fatalf("Onboard(ID=%d chars) = nil, want error", len(tooLong))
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should explain the id is too long, got: %v", err)
	}
}
