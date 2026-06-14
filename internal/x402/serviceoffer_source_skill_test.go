package x402

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func skillSourceTestOffer() monetizeapi.ServiceOffer {
	return monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "buy-x402", Namespace: "hermes-obol-agent"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "skill",
			Skill: monetizeapi.ServiceOfferSkill{
				Name:            "buy-x402",
				Version:         "0.1.0",
				SHA256:          strings.Repeat("0a", 32),
				BundleConfigMap: "buy-x402-skill-bundle",
			},
			Upstream: monetizeapi.ServiceOfferUpstream{
				Service:    monetizeapi.SkillBundleWorkloadName("buy-x402"),
				Namespace:  "hermes-obol-agent",
				Port:       8080,
				HealthPath: "/skill.json",
			},
			Payment: monetizeapi.ServiceOfferPayment{
				PayTo:   "0x1111111111111111111111111111111111111111",
				Network: "base-sepolia",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.01"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
		},
	}
}

func TestRouteRuleFromOffer_SkillPopulatesSkillFields(t *testing.T) {
	offer := skillSourceTestOffer()

	rule, err := routeRuleFromOffer(&offer, "")
	if err != nil {
		t.Fatalf("routeRuleFromOffer: %v", err)
	}

	if rule.SkillName != "buy-x402" {
		t.Errorf("SkillName = %q, want buy-x402", rule.SkillName)
	}
	if rule.SkillVersion != "0.1.0" {
		t.Errorf("SkillVersion = %q, want 0.1.0", rule.SkillVersion)
	}
	if rule.SkillSHA256 != strings.Repeat("0a", 32) {
		t.Errorf("SkillSHA256 = %q", rule.SkillSHA256)
	}
	if rule.OfferType != "skill" {
		t.Errorf("OfferType = %q, want skill", rule.OfferType)
	}

	// The upstream URL must be the controller-rendered bundle server,
	// derived from spec.upstream with no skill-specific synthesis.
	wantURL := "http://so-buy-x402-bundle.hermes-obol-agent.svc.cluster.local:8080"
	if rule.UpstreamURL != wantURL {
		t.Errorf("UpstreamURL = %q, want %q", rule.UpstreamURL, wantURL)
	}
	if rule.Pattern != "/services/buy-x402/*" {
		t.Errorf("Pattern = %q, want /services/buy-x402/*", rule.Pattern)
	}
}

func TestRouteRuleFromOffer_SkillUppercaseHashNormalizedToLower(t *testing.T) {
	offer := skillSourceTestOffer()
	offer.Spec.Skill.SHA256 = strings.ToUpper(strings.Repeat("0a", 32))

	rule, err := routeRuleFromOffer(&offer, "")
	if err != nil {
		t.Fatalf("routeRuleFromOffer: %v", err)
	}
	if rule.SkillSHA256 != strings.Repeat("0a", 32) {
		t.Errorf("SkillSHA256 = %q, want lowercase", rule.SkillSHA256)
	}
}

func TestRouteRuleFromOffer_SkillUpstreamAuthStaysEmpty(t *testing.T) {
	// Even if a litellm master key exists for the namespace, the bundle
	// server is a static file host — no Authorization header may be
	// injected (effectiveUpstreamAuth only injects for litellm/agent).
	offer := skillSourceTestOffer()

	rule, err := routeRuleFromOffer(&offer, "Bearer should-not-leak")
	if err != nil {
		t.Fatalf("routeRuleFromOffer: %v", err)
	}
	if rule.UpstreamAuth != "" {
		t.Errorf("UpstreamAuth = %q, want empty for skill bundle upstream", rule.UpstreamAuth)
	}
}

func TestRouteRuleFromOffer_NonSkillOffersCarryNoSkillFields(t *testing.T) {
	offer := monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:     "http",
			Upstream: monetizeapi.ServiceOfferUpstream{Service: "httpbin", Namespace: "llm", Port: 8080},
			Payment: monetizeapi.ServiceOfferPayment{
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.01"},
			},
		},
	}

	rule, err := routeRuleFromOffer(&offer, "")
	if err != nil {
		t.Fatalf("routeRuleFromOffer: %v", err)
	}
	if rule.SkillName != "" || rule.SkillVersion != "" || rule.SkillSHA256 != "" {
		t.Errorf("non-skill rule gained skill fields: %q %q %q", rule.SkillName, rule.SkillVersion, rule.SkillSHA256)
	}
}
