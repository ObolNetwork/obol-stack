package offerkind

import (
	"os"
	"strings"
	"testing"
)

// TestResolve_CoversCRDEnum is the drift guard: every value in the
// ServiceOffer.spec.type CRD enum (monetizeapi/types.go) must have a Kind.
// Adding a 7th type to the enum without a table entry fails here, the same way
// TestOpenClawVersionConsistency catches version drift.
func TestResolve_CoversCRDEnum(t *testing.T) {
	src, err := os.ReadFile("../monetizeapi/types.go")
	if err != nil {
		t.Fatalf("read types.go: %v", err)
	}
	var enumLine string
	for _, ln := range strings.Split(string(src), "\n") {
		if strings.Contains(ln, "+kubebuilder:validation:Enum=") && strings.Contains(ln, "inference") {
			enumLine = ln
			break
		}
	}
	if enumLine == "" {
		t.Fatal("could not find the ServiceOfferSpec.Type enum in monetizeapi/types.go")
	}
	_, rhs, _ := strings.Cut(enumLine, "Enum=")
	values := strings.Split(strings.TrimSpace(rhs), ";")
	if len(values) < 6 {
		t.Fatalf("expected >=6 enum values, got %d from %q", len(values), rhs)
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		k := Resolve(v)
		if k.Type != v {
			t.Errorf("CRD enum value %q has no offerkind Kind (Resolve→Type %q); add it to kinds", v, k.Type)
		}
		if k.Integrity.Payment == "" {
			t.Errorf("type %q has an empty Payment class", v)
		}
	}
}

// TestResolve_LegacyCollapseValues locks the exact collapse values the legacy
// normalizeOfferType / openAPIPathsForOffer produced, so the rewire stays
// behavior-preserving — including the deliberate "" split (generic bazaar but
// chat openapi, because IsInference("")==true).
func TestResolve_LegacyCollapseValues(t *testing.T) {
	cases := []struct{ typ, paymentCopy, bazaar, openapi string }{
		{"", "http", "generic", "chat"},
		{"inference", "inference", "chat", "chat"},
		{"http", "http", "generic", "generic"},
		{"agent", "agent", "chat", "chat"},
		{"dataset", "http", "generic", "generic"},
		{"fine-tuning", "http", "generic", "multipart"},
		{"skill", "http", "generic", "generic"},
		{"totally-unknown", "http", "generic", "generic"},
	}
	for _, c := range cases {
		k := Resolve(c.typ)
		if k.PaymentCopy != c.paymentCopy {
			t.Errorf("Resolve(%q).PaymentCopy = %q, want %q", c.typ, k.PaymentCopy, c.paymentCopy)
		}
		if k.BazaarShape != c.bazaar {
			t.Errorf("Resolve(%q).BazaarShape = %q, want %q", c.typ, k.BazaarShape, c.bazaar)
		}
		if k.OpenAPIShape != c.openapi {
			t.Errorf("Resolve(%q).OpenAPIShape = %q, want %q", c.typ, k.OpenAPIShape, c.openapi)
		}
	}
}

func TestResolve_IntegrityProfiles(t *testing.T) {
	if got := Resolve("inference").Integrity; got != paymentOnly {
		t.Errorf("inference integrity = %+v, want payment-only", got)
	}
	if got := Resolve("http").Integrity; got != paymentOnly {
		t.Errorf("http integrity = %+v, want payment-only", got)
	}
	ds := Resolve("dataset").Integrity
	if ds.Content != ContentSignedVersionLog || ds.Scope != ScopeVersionEntitlement || ds.Identity != IdentityGroupAuth {
		t.Errorf("dataset integrity = %+v, want signed-log + version-entitlement + groupauth", ds)
	}
	if got := Resolve("skill").Integrity.Content; got != ContentBundleSHA256 {
		t.Errorf("skill content = %q, want bundle-sha256", got)
	}
	if got := Resolve("fine-tuning").Integrity.Content; got != ContentSignedVersionLog {
		t.Errorf("fine-tuning content = %q, want signed-version-log", got)
	}
}

func TestResolve_SemanticInference(t *testing.T) {
	for _, typ := range []string{"", "inference"} {
		if !Resolve(typ).SemanticInference {
			t.Errorf("Resolve(%q).SemanticInference = false, want true (matches IsInference)", typ)
		}
	}
	for _, typ := range []string{"http", "agent", "dataset", "fine-tuning", "skill"} {
		if Resolve(typ).SemanticInference {
			t.Errorf("Resolve(%q).SemanticInference = true, want false", typ)
		}
	}
}

func TestResolve_CapabilityFlags(t *testing.T) {
	if !Resolve("agent").ResolvesAgentRef {
		t.Error("agent should resolve an Agent ref")
	}
	if !Resolve("skill").RendersBundle {
		t.Error("skill should render a bundle")
	}
	if !Resolve("dataset").OneShotPurchase {
		t.Error("dataset is a one-shot purchase (perMB→total)")
	}
}
