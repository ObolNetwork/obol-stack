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
// source-check style follows internal/x402/setup_structure_test.go.)
func TestBountyReconcile_NeverCreatesIngressOrSecrets(t *testing.T) {
	src, err := os.ReadFile("bounty.go")
	if err != nil {
		t.Fatalf("read bounty.go: %v", err)
	}

	forbidden := regexp.MustCompile(`HTTPRouteGVR|MiddlewareGVR|ReferenceGrantGVR|SecretGVR|c\.httpRoutes|c\.middlewares|c\.referenceGrants`)
	if match := forbidden.Find(src); match != nil {
		t.Fatalf("bounty.go references %q — the bounty reconcile must never create routes, middlewares, reference grants, or secrets (a bounty must never become ingress)", match)
	}
}
