package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// TestDefaultPaidMethod_RootPricedIsPOST is the regression test for the
// advertised-method/routing contract.
//
// renderStaticSite publishes the offer root as exactTo("/", "index.html") — a
// GET-scoped Exact match — so at the offer root GET is served by the static
// landing page and only POST falls through to the payment gate. A discovery
// document that advertises GET at the root therefore points buyers at a 200
// HTML page that can never return 402.
//
// Sub-path paid routes have no such collision, so they keep the GET default
// that stops OpenAPI/AgentCash clients POSTing into a GET-only upstream.
func TestDefaultPaidMethod_RootPricedIsPOST(t *testing.T) {
	httpOffer := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{Type: "http"}}

	for _, tc := range []struct {
		name      string
		routePath string
		want      string
	}{
		{"implicit catch-all", "/*", "POST"},
		{"explicit root", "/", "POST"},
		{"empty path", "", "POST"},
		{"sub-path", "/submit", "GET"},
		{"sub-path wildcard", "/jobs/*", "GET"},
	} {
		if got := defaultPaidMethod(httpOffer, tc.routePath); got != tc.want {
			t.Errorf("%s: defaultPaidMethod(http, %q) = %s, want %s", tc.name, tc.routePath, got, tc.want)
		}
	}

	// Write-shaped types are POST everywhere, root or not.
	for _, typ := range []string{"inference", "agent", "fine-tuning"} {
		offer := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{Type: typ}}
		if got := defaultPaidMethod(offer, "/v1/chat/completions"); got != "POST" {
			t.Errorf("type %s: defaultPaidMethod = %s, want POST", typ, got)
		}
	}
}

// TestAdvertisedMethodReachesPaymentGate is the assertion the route-surface
// suite was missing: for every paid route, the method the discovery documents
// advertise must NOT be one the static site claims first.
//
// The static site claims GET on the offer root (index.html) and on the
// discovery documents themselves. Any paid route whose advertised method is
// GET at the root is unreachable-as-payable — that is the defect this guards.
func TestAdvertisedMethodReachesPaymentGate(t *testing.T) {
	offer := routeTableOffer()

	for _, rt := range offer.EffectiveRoutes() {
		if rt.EffectiveGate() != monetizeapi.GatePaid {
			continue
		}
		method := defaultPaidMethod(offer, rt.Path)
		if len(rt.Methods) > 0 {
			method = strings.ToUpper(rt.Methods[0])
		}
		if openAPIRelPathForRoute(rt.Path) == "" && method == "GET" {
			t.Errorf("paid route %q advertises GET at the offer root, which the static "+
				"index.html claims — buyers would get 200 HTML instead of a 402", rt.Path)
		}
	}
}

// TestPrimaryPaidMethod_RootPricedIsPOST covers the primaryPaidMethod wrapper,
// which feeds the openapi docs deep-link and the skill.md call line, for both
// the no-route-table and declared-root-route cases.
func TestPrimaryPaidMethod_RootPricedIsPOST(t *testing.T) {
	noRoutes := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{Type: "http"}}
	if got := primaryPaidMethod(noRoutes); got != "POST" {
		t.Errorf("no route table: primaryPaidMethod = %s, want POST", got)
	}

	rootRoute := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
		Type:   "http",
		Routes: []monetizeapi.ServiceOfferRoute{{Path: "/*", Gate: monetizeapi.GatePaid}},
	}}
	if got := primaryPaidMethod(rootRoute); got != "POST" {
		t.Errorf("declared root route: primaryPaidMethod = %s, want POST", got)
	}

	subPath := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
		Type:   "http",
		Routes: []monetizeapi.ServiceOfferRoute{{Path: "/fetch", Gate: monetizeapi.GatePaid}},
	}}
	if got := primaryPaidMethod(subPath); got != "GET" {
		t.Errorf("sub-path route: primaryPaidMethod = %s, want GET", got)
	}

	// An explicit declaration always wins over the default.
	explicit := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
		Type:   "http",
		Routes: []monetizeapi.ServiceOfferRoute{{Path: "/*", Gate: monetizeapi.GatePaid, Methods: []string{"get"}}},
	}}
	if got := primaryPaidMethod(explicit); got != "GET" {
		t.Errorf("explicit methods: primaryPaidMethod = %s, want GET", got)
	}
}
