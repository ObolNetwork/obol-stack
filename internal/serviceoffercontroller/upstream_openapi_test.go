package serviceoffercontroller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

// TestGetJSONMap_SizeCap pins the BLOCKER fix: an upstream body over the
// cap must be rejected outright, not read in full and re-marshaled into the
// shared "obol-skill-md" ConfigMap where it would blow the ~1MiB k8s limit
// for every offer's bundle, not just its own.
func TestGetJSONMap_SizeCap(t *testing.T) {
	big := `{"paths":{"/x":{}},"pad":"` + strings.Repeat("a", maxUpstreamOpenAPIBytes) + `"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	}))
	defer srv.Close()

	if _, err := getJSONMap(srv.URL); err == nil {
		t.Fatal("getJSONMap accepted an oversized body, want a size-cap error")
	}

	small := `{"paths":{"/x":{}}}`
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(small))
	}))
	defer srv2.Close()
	if _, err := getJSONMap(srv2.URL); err != nil {
		t.Fatalf("getJSONMap rejected a small body: %v", err)
	}
}

// TestRewriteUpstreamOpenAPI_SizeCapFallsBack pins the re-marshal side of
// the same cap: even a document that arrived under budget must not be
// republished if rewriting (adding servers/info/x-discovery) pushes it over.
func TestRewriteUpstreamOpenAPI_SizeCapFallsBack(t *testing.T) {
	offer := hostnameOffer()
	oversized := map[string]any{
		"paths": map[string]any{"/x": map[string]any{}},
		"pad":   strings.Repeat("a", maxUpstreamOpenAPIBytes),
	}
	if _, ok := rewriteUpstreamOpenAPI(oversized, offer, schemas.StorefrontProfile{}); ok {
		t.Fatal("rewriteUpstreamOpenAPI accepted a document that re-marshals over the size cap")
	}

	// End-to-end: buildOfferBundles must fall back to the offer-scoped
	// openapi.json (buildOfferScopedOpenAPI) instead of failing the whole
	// static site.
	fallback := buildOfferScopedOpenAPI(offer, schemas.StorefrontProfile{})
	upstream := func(*monetizeapi.ServiceOffer) map[string]any { return oversized }
	bundles := buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, schemas.StorefrontProfile{}, upstream)
	var openapiContent string
	for _, f := range bundles {
		if f.Path == "offers/sec/audit/openapi.json" {
			openapiContent = f.Content
		}
	}
	if openapiContent != fallback {
		t.Fatalf("oversized upstream doc leaked into the bundle instead of falling back to buildOfferScopedOpenAPI")
	}
}

// TestUpstreamOpenAPICache_DeterministicAcrossFlappingFetch pins the second
// BLOCKER fix: buildOfferBundles must never re-fetch live, and the cache
// that feeds it must fetch at most once per offer generation — a flapping
// upstream (different content on every call) must not change what the
// cache serves between reconciles of unrelated offers.
func TestUpstreamOpenAPICache_DeterministicAcrossFlappingFetch(t *testing.T) {
	offer := hostnameOffer()
	offer.UID = "offer-uid-1"
	offer.Generation = 1

	fetchCount := 0
	flapping := func(*monetizeapi.ServiceOffer) map[string]any {
		fetchCount++
		return map[string]any{"paths": map[string]any{"/x": map[string]any{}}, "call": fetchCount}
	}

	var cache upstreamOpenAPICache
	cache.refresh(offer, flapping)
	first := cache.get(offer)
	if fetchCount != 1 {
		t.Fatalf("fetchCount after first refresh = %d, want 1", fetchCount)
	}

	// Same generation: refresh again (simulating a requeue with no spec
	// change while the upstream is flapping) must not fetch again, and the
	// bundle-facing read must be byte-identical to the first fetch.
	cache.refresh(offer, flapping)
	if fetchCount != 1 {
		t.Fatalf("fetchCount after same-generation refresh = %d, want 1 (no re-fetch)", fetchCount)
	}
	second := cache.get(offer)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("cached doc changed across a same-generation refresh: %s vs %s", firstJSON, secondJSON)
	}

	// A real generation bump (spec change) does fetch again.
	offer.Generation = 2
	cache.refresh(offer, flapping)
	if fetchCount != 2 {
		t.Fatalf("fetchCount after generation bump = %d, want 2", fetchCount)
	}

	// buildOfferBundles only ever reads the cache — never triggers a fetch
	// itself, however many times it's called (the static-site rebuild that
	// happens on every offer's reconcile).
	for i := 0; i < 3; i++ {
		buildOfferBundles([]*monetizeapi.ServiceOffer{offer}, schemas.StorefrontProfile{}, cache.get)
	}
	if fetchCount != 2 {
		t.Fatalf("fetchCount after 3 bundle rebuilds = %d, want 2 (buildOfferBundles must not fetch)", fetchCount)
	}
}

// TestBuildOfferWellKnownX402FromOpenAPI_PerRoutePriceOverride pins the
// price-override fix: a paid upstream operation whose path matches a
// spec.routes[] entry with a price override must be priced at that
// override, not the offer's default price for every op.
func TestBuildOfferWellKnownX402FromOpenAPI_PerRoutePriceOverride(t *testing.T) {
	offer := hostnameOffer() // routeTableOffer: /submit POST overridden to 0.5, offer default 0.1
	doc := map[string]any{
		"paths": map[string]any{
			"/submit": map[string]any{
				"post": map[string]any{
					"security":       []any{map[string]any{"x402": []any{}}},
					"x-payment-info": map[string]any{},
					"responses":      map[string]any{"402": map[string]any{}},
				},
			},
			"/v1/other": map[string]any{
				"get": map[string]any{
					"security":       []any{map[string]any{"x402": []any{}}},
					"x-payment-info": map[string]any{},
					"responses":      map[string]any{"402": map[string]any{}},
				},
			},
		},
	}
	var wk struct {
		Resources []struct {
			Resource string           `json:"resource"`
			Method   string           `json:"method"`
			Accepts  []map[string]any `json:"accepts"`
		} `json:"resources"`
	}
	if err := json.Unmarshal([]byte(buildOfferWellKnownX402FromOpenAPI(offer, doc)), &wk); err != nil {
		t.Fatal(err)
	}
	byResource := map[string]map[string]any{}
	for _, r := range wk.Resources {
		byResource[r.Resource] = r.Accepts[0]
	}
	submit := byResource["https://audit.v1337.example/submit"]
	if submit["amount"] != "500000" {
		t.Errorf("/submit accepts = %v, want the 0.5 route override (500000 atomic units)", submit)
	}
	other := byResource["https://audit.v1337.example/v1/other"]
	if other["amount"] != "100000" {
		t.Errorf("/v1/other accepts = %v, want the offer default (0.1 => 100000 atomic units)", other)
	}
}

// TestBuildOfferAgentRegistration_RegistrationsField pins the ERC-8004
// registrations[] fix: the field must be populated once the offer carries
// an on-chain agentId (status.AgentID), and absent otherwise — buyers using
// --expected-agent-id fail closed on an empty registrations[]
// (internal/buy/discover.go verifyAgentID).
func TestBuildOfferAgentRegistration_RegistrationsField(t *testing.T) {
	offer := hostnameOffer()
	offer.Spec.Registration.Enabled = true

	var unregistered map[string]any
	if err := json.Unmarshal([]byte(buildOfferAgentRegistration(offer, schemas.StorefrontProfile{})), &unregistered); err != nil {
		t.Fatal(err)
	}
	if _, has := unregistered["registrations"]; has {
		t.Errorf("unregistered offer already carries registrations[]: %v", unregistered["registrations"])
	}

	offer.Status.AgentID = "42"
	var reg map[string]any
	if err := json.Unmarshal([]byte(buildOfferAgentRegistration(offer, schemas.StorefrontProfile{})), &reg); err != nil {
		t.Fatal(err)
	}
	regs, ok := reg["registrations"].([]any)
	if !ok || len(regs) != 1 {
		t.Fatalf("registrations = %v, want one entry", reg["registrations"])
	}
	entry := regs[0].(map[string]any)
	if entry["agentId"] != float64(42) {
		t.Errorf("agentId = %v, want 42", entry["agentId"])
	}
	if entry["agentRegistry"] != erc8004.BaseSepolia.CAIP10Registry() {
		t.Errorf("agentRegistry = %v, want %s (offer's base-sepolia payment network)", entry["agentRegistry"], erc8004.BaseSepolia.CAIP10Registry())
	}
}

// TestBuildOfferAgentRegistration_OriginScope pins the origin-scope fix:
// an https(s) service endpoint must equal the offer's own origin, or be a
// sub-path of it — a same-prefix different-host origin like
// "https://audit.v1337.example.evil.tld" must not pass HasPrefix.
func TestBuildOfferAgentRegistration_OriginScope(t *testing.T) {
	offer := hostnameOffer()
	offer.Spec.Registration.Services = []monetizeapi.ServiceOfferService{
		{Name: "web", Endpoint: "https://audit.v1337.example.evil.tld/steal"},
		{Name: "a2a", Endpoint: "https://audit.v1337.example/a2a"},
		{Name: "root", Endpoint: "https://audit.v1337.example"},
	}
	var reg map[string]any
	if err := json.Unmarshal([]byte(buildOfferAgentRegistration(offer, schemas.StorefrontProfile{})), &reg); err != nil {
		t.Fatal(err)
	}
	services, _ := reg["services"].([]any)
	var endpoints []string
	for _, s := range services {
		endpoints = append(endpoints, s.(map[string]any)["endpoint"].(string))
	}
	for _, want := range endpoints {
		if strings.Contains(want, "evil.tld") {
			t.Fatalf("services leaked the evil-suffix origin: %v", endpoints)
		}
	}
	joined := strings.Join(endpoints, ",")
	if !strings.Contains(joined, "https://audit.v1337.example/a2a") || !strings.Contains(joined, "https://audit.v1337.example") {
		t.Errorf("services missing legitimately-scoped endpoints: %v", endpoints)
	}
}

// TestUpstreamOpenAPIBase_NamespaceConstraint pins the SSRF fix's namespace
// constraint: the openapi probe must target the offer's OWN namespace, not
// an offer-author-controlled spec.upstream.namespace override — unlike the
// Gateway API data path, this controller-side fetch has no ReferenceGrant
// check gating a cross-namespace target.
func TestUpstreamOpenAPIBase_NamespaceConstraint(t *testing.T) {
	offer := hostnameOffer()
	offer.Namespace = "tenant-a"
	offer.Spec.Upstream.Namespace = "tenant-b-secrets"

	base := upstreamOpenAPIBase(offer)
	if !strings.Contains(base, "tenant-a.svc.cluster.local") {
		t.Errorf("base = %q, want the offer's own namespace (tenant-a)", base)
	}
	if strings.Contains(base, "tenant-b") {
		t.Errorf("base = %q, leaked spec.upstream.namespace override", base)
	}
}

// TestGetJSONMap_NoRedirectFollow pins the SSRF fix's redirect guard: a
// republished document must never come from wherever a 3xx points, since
// the offer author controls the fetch target and the result is served
// publicly.
func TestGetJSONMap_NoRedirectFollow(t *testing.T) {
	var hitTarget bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitTarget = true
		w.Write([]byte(`{"paths":{"/x":{}}}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	if _, err := getJSONMap(redirector.URL); err == nil {
		t.Fatal("getJSONMap followed a redirect, want it rejected as a non-2xx response")
	}
	if hitTarget {
		t.Fatal("getJSONMap followed the redirect to a different host")
	}
}

// TestIsSimpleUpstreamPath pins the path-validation half of the SSRF fix:
// spec.registration.metadata["openapiPath"] is offer-author-controlled and
// gets string-concatenated onto the fetch base, so it must stay a plain
// '/'-prefixed path.
func TestIsSimpleUpstreamPath(t *testing.T) {
	cases := map[string]bool{
		"/v1/openapi.json":     true,
		"/openapi.json":        true,
		"openapi.json":         false, // not '/'-prefixed
		"/../../etc/passwd":    false, // traversal
		"/v1/../../../secrets": false, // traversal
		"http://evil.tld/x":    false, // embedded scheme
		"/x?y=http://evil.tld": false, // embedded scheme, even mid-path
	}
	for path, want := range cases {
		if got := isSimpleUpstreamPath(path); got != want {
			t.Errorf("isSimpleUpstreamPath(%q) = %v, want %v", path, got, want)
		}
	}
}
