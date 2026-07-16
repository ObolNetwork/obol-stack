package openclaw

import (
	"testing"

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
