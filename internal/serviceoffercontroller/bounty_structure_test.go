package serviceoffercontroller

import (
	"os"
	"regexp"
	"testing"
)

// TestBountyReconcile_NeverCreatesIngressOrSecrets pins the review invariant
// that a ServiceBounty must never become public ingress and the bounty pass
// must never broker credentials: the reconcile source must not touch
// HTTPRoute, Middleware, ReferenceGrant, or Secret resources. (The structural
// source-check style follows internal/x402/setup_structure_test.go.) The scan
// covers every file the bounty reconcile spans — escrow, eval market, panel
// selection, escalation, grounding, and seed sourcing all carry the same
// invariant.
func TestBountyReconcile_NeverCreatesIngressOrSecrets(t *testing.T) {
	files := []string{
		"bounty.go",
		"bounty_eval.go",
		"bounty_panel.go",
		"bounty_escalation.go",
		"bounty_grounding.go",
		"seed.go",
	}
	forbidden := regexp.MustCompile(`HTTPRouteGVR|MiddlewareGVR|ReferenceGrantGVR|SecretGVR|c\.httpRoutes|c\.middlewares|c\.referenceGrants`)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if match := forbidden.Find(src); match != nil {
			t.Fatalf("%s references %q — the bounty reconcile must never create routes, middlewares, reference grants, or secrets (a bounty must never become ingress)", file, match)
		}
	}
}
