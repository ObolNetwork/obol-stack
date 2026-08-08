package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

// upstreamDocWithPaidRoutes is a minimal upstream OpenAPI document with two
// paid operations, so the expanded /.well-known/x402 has more than the single
// root entry the route-table fallback produces.
func upstreamDocWithPaidRoutes() map[string]any {
	paidOp := func(summary string) map[string]any {
		return map[string]any{
			"summary":        summary,
			"security":       []any{map[string]any{"x402": []any{}}},
			"x-payment-info": map[string]any{"price": map[string]any{"amount": "0.001"}},
			"responses":      map[string]any{"200": map[string]any{}, "402": map[string]any{}},
		}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "upstream", "version": "1.0.0"},
		"paths": map[string]any{
			"/v1/alpha": map[string]any{"get": paidOp("Alpha")},
			"/v1/beta":  map[string]any{"get": paidOp("Beta")},
		},
	}
}

func bundleContent(bundles []offerBundleFile, key string) string {
	for _, b := range bundles {
		if b.Key == key {
			return b.Content
		}
	}
	return ""
}

// TestBuildOfferBundles_UnsettledPreservesPublished is the regression test for
// the restart window.
//
// The upstream-OpenAPI cache is process-local, so every controller restart
// begins with no entry for any offer. reconcileStaticSite rebuilds the SHARED
// bundle on EVERY offer's reconcile — including reconciles belonging to other
// offers — so an offer that has not reconciled yet would be re-rendered from
// the route-table fallback and visibly lose its advertised routes until its
// own reconcile lands. Measured at 30–90s on a nine-offer stack.
//
// While a probe is unsettled, the already-published document must be kept.
func TestBuildOfferBundles_UnsettledPreservesPublished(t *testing.T) {
	offer := hostnameOffer()
	profile := schemas.StorefrontProfile{}

	settled := func(*monetizeapi.ServiceOffer) (map[string]any, bool) {
		return upstreamDocWithPaidRoutes(), true
	}
	unsettled := func(*monetizeapi.ServiceOffer) (map[string]any, bool) { return nil, false }

	x402Key := offerBundleKey(offer, "x402.json")
	openapiKey := offerBundleKey(offer, "openapi.json")

	// What a healthy controller publishes once the probe has settled.
	good := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, settled, nil)
	goodX402 := bundleContent(good, x402Key)
	goodOpenAPI := bundleContent(good, openapiKey)
	if !strings.Contains(goodX402, "/v1/alpha") || !strings.Contains(goodX402, "/v1/beta") {
		t.Fatalf("precondition: settled x402 should enumerate upstream paid routes, got %s", goodX402)
	}

	published := map[string]string{x402Key: goodX402, openapiKey: goodOpenAPI}

	// Controller restarts: cache empty, this offer has not reconciled yet.
	after := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, unsettled, published)
	if got := bundleContent(after, x402Key); got != goodX402 {
		t.Errorf("unsettled probe replaced the published x402 document.\n got: %s\nwant: %s", got, goodX402)
	}
	if got := bundleContent(after, openapiKey); got != goodOpenAPI {
		t.Error("unsettled probe replaced the published openapi.json")
	}
}

// TestBuildOfferBundles_SettledEmptyStillFallsBack guards the other direction:
// once we HAVE probed and there is genuinely no upstream document, the
// route-table fallback is the correct final answer and must not be blocked by
// stale published content. Otherwise an offer that legitimately drops its
// upstream OpenAPI would serve the old document forever.
func TestBuildOfferBundles_SettledEmptyStillFallsBack(t *testing.T) {
	offer := hostnameOffer()
	profile := schemas.StorefrontProfile{}
	x402Key := offerBundleKey(offer, "x402.json")

	fallback := bundleContent(
		buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, noUpstreamOpenAPI, nil), x402Key)

	stale := map[string]string{x402Key: `{"x402Version":2,"resources":[{"method":"GET","resource":"https://stale/v1/gone"}]}`}
	got := bundleContent(
		buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, profile, noUpstreamOpenAPI, stale), x402Key)

	if got != fallback {
		t.Errorf("settled-with-no-document must re-render the fallback, not keep stale content.\n got: %s\nwant: %s", got, fallback)
	}
	if strings.Contains(got, "stale") {
		t.Error("stale published content leaked into a settled rebuild")
	}
}

// TestBuildOfferBundles_UnsettledWithNoPublishedRendersFallback covers a first
// ever reconcile: nothing published yet, nothing to preserve, so the fallback
// is correct rather than an empty document.
func TestBuildOfferBundles_UnsettledWithNoPublishedRendersFallback(t *testing.T) {
	offer := hostnameOffer()
	unsettled := func(*monetizeapi.ServiceOffer) (map[string]any, bool) { return nil, false }

	bundles := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, schemas.StorefrontProfile{}, unsettled, nil)
	got := bundleContent(bundles, offerBundleKey(offer, "x402.json"))
	if got == "" {
		t.Fatal("first reconcile produced no x402 document at all")
	}
	if !strings.Contains(got, "resources") {
		t.Errorf("first reconcile should render the route-table fallback, got %s", got)
	}
}
