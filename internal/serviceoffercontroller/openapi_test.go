package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// readyOfferWithSpec returns a ServiceOffer fixture with Ready=True so it
// passes offerOperationallyReady. identity_render_test.go owns a different
// `readyOffer` helper with a narrower signature — we keep this separate
// instead of forcing a refactor of those tests.
func readyOfferWithSpec(name, namespace string, spec monetizeapi.ServiceOfferSpec) *monetizeapi.ServiceOffer {
	return &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       spec,
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}
}

// parseOpenAPI decodes the rendered spec into a generic map. We avoid a real
// OpenAPI validator dependency in unit tests — structural assertions are
// enough to lock in the contract phase 1 ships.
func parseOpenAPI(t *testing.T, payload string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		t.Fatalf("openapi document is not valid JSON: %v\n%s", err, payload)
	}
	return out
}

// dig descends through nested maps using dotted keys, returning the leaf
// value or nil if any segment is missing.
func dig(t *testing.T, m map[string]any, keys ...string) any {
	t.Helper()
	var cur any = m
	for i, key := range keys {
		curMap, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig: expected map at segment %d (%q), got %T", i, key, cur)
		}
		next, ok := curMap[key]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func TestBuildOpenAPIDocument_EmptyCluster(t *testing.T) {
	out := buildOpenAPIDocument(nil, "https://tunnel.example", schemas.StorefrontProfile{})
	doc := parseOpenAPI(t, out)

	if got := doc["openapi"]; got != openAPISpecVersion {
		t.Errorf("openapi version = %v, want %s", got, openAPISpecVersion)
	}

	paths, _ := doc["paths"].(map[string]any)
	if len(paths) != 0 {
		t.Errorf("expected empty paths block, got %d entries: %v", len(paths), paths)
	}

	servers, _ := doc["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers = %d entries, want tunnel + local", len(servers))
	}
	first := servers[0].(map[string]any)
	if first["url"] != "https://tunnel.example" {
		t.Errorf("servers[0].url = %v, want tunnel URL first", first["url"])
	}

	// Components are always emitted even when paths is empty — clients can
	// still discover the x402 wire format from /openapi.json on a quiet operator.
	if schemas := dig(t, doc, "components", "schemas"); schemas == nil {
		t.Error("components.schemas missing on empty-cluster doc")
	}
	// No securitySchemes: payment is not an HTTP auth scheme. Discovery
	// indexers (x402scan) classified the apiKey-typed X-PAYMENT scheme as
	// "API-key-gated", masking the paid classification.
	if comps, _ := dig(t, doc, "components").(map[string]any); comps != nil {
		if _, ok := comps["securitySchemes"]; ok {
			t.Error("components.securitySchemes should be omitted (X-PAYMENT is not an auth scheme)")
		}
	}
	if _, ok := doc["security"]; ok {
		t.Error("global security block should be omitted")
	}
	// Discovery indexers audit info.x-guidance and info.contact.
	if g, _ := dig(t, doc, "info", "x-guidance").(string); g == "" {
		t.Error("info.x-guidance missing")
	}
	if c := dig(t, doc, "info", "contact", "url"); c != "https://github.com/ObolNetwork/obol-stack" {
		t.Errorf("info.contact.url = %v, want obol-stack repo", c)
	}
	if dig(t, doc, "info", "contact", "email") != nil {
		t.Error("info.contact.email should be omitted when unset")
	}
}

func TestBuildOpenAPIDocument_ContactEmail(t *testing.T) {
	profile := schemas.StorefrontProfile{
		DisplayName:  "Acme Labs",
		ContactEmail: "ops@acme.example",
	}
	doc := parseOpenAPI(t, buildOpenAPIDocument(nil, "https://tunnel.example", profile))
	if e := dig(t, doc, "info", "contact", "email"); e != "ops@acme.example" {
		t.Errorf("info.contact.email = %v, want ops@acme.example", e)
	}
	if n := dig(t, doc, "info", "contact", "name"); n != "Acme Labs" {
		t.Errorf("info.contact.name = %v, want Acme Labs", n)
	}
}

func TestBuildOpenAPIDocument_NoTunnelOmitsTunnelServer(t *testing.T) {
	doc := parseOpenAPI(t, buildOpenAPIDocument(nil, "", schemas.StorefrontProfile{}))
	servers, _ := doc["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("servers = %d entries, want only local fallback", len(servers))
	}
	first := servers[0].(map[string]any)
	if first["url"] != localBaseURL {
		t.Errorf("servers[0].url = %v, want %s", first["url"], localBaseURL)
	}
}

func TestBuildOpenAPIDocument_InferenceOffer(t *testing.T) {
	offer := readyOfferWithSpec("llama-3", "llm", monetizeapi.ServiceOfferSpec{
		Type:     "inference",
		Model:    monetizeapi.ServiceOfferModel{Name: "llama-3-70b", Runtime: "vllm"},
		Upstream: monetizeapi.ServiceOfferUpstream{Service: "vllm", Port: 8000},
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base",
			PayTo:   "0x1111111111111111111111111111111111111111",
			Price:   monetizeapi.ServiceOfferPriceTable{PerMTok: "1.50"},
		},
		Registration: monetizeapi.ServiceOfferRegistration{
			Description: "Llama-3 70B with vLLM",
			Skills:      []string{"text-generation"},
			Domains:     []string{"ai/llm"},
		},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "https://tunnel.example", schemas.StorefrontProfile{}))

	want := "/services/llama-3/v1/chat/completions"
	op := dig(t, doc, "paths", want, "post")
	if op == nil {
		t.Fatalf("expected POST at %s, paths = %v", want, doc["paths"])
	}
	opMap := op.(map[string]any)

	// Description carries operator copy.
	if d := opMap["description"]; d != "Llama-3 70B with vLLM" {
		t.Errorf("description = %v, want operator-supplied", d)
	}
	// Tags include the type + skill + domain (deduplicated).
	tags, _ := opMap["tags"].([]any)
	if !containsAny(tags, "inference", "text-generation", "ai/llm") {
		t.Errorf("tags missing expected entries: %v", tags)
	}
	// Request body uses the OpenAI chat completions $ref.
	body := dig(t, opMap, "requestBody", "content", "application/json", "schema")
	if r, _ := body.(map[string]any)["$ref"].(string); !strings.HasSuffix(r, "OpenAIChatCompletionsRequest") {
		t.Errorf("request body schema $ref = %v, want OpenAIChatCompletionsRequest", body)
	}
	// 402 is a $ref to the shared response.
	ref402 := dig(t, opMap, "responses", "402")
	if r, _ := ref402.(map[string]any)["$ref"].(string); r != "#/components/responses/PaymentRequired" {
		t.Errorf("402 $ref = %v, want PaymentRequired component", ref402)
	}
	// 200 uses the chat-completions response schema.
	successRef := dig(t, opMap, "responses", "200", "content", "application/json", "schema", "$ref")
	if s, _ := successRef.(string); !strings.HasSuffix(s, "OpenAIChatCompletionsResponse") {
		t.Errorf("200 schema $ref = %v, want OpenAIChatCompletionsResponse", successRef)
	}
	// x-payment-info marks the operation as paid for discovery indexers.
	// The offer has no explicit asset, so it settles in the chain's default
	// USDC → currency USD; the perMTok price is converted to the same
	// per-request approximation the verifier enforces (1.50/MTok → 0.0015).
	xpay, _ := opMap["x-payment-info"].(map[string]any)
	if xpay == nil {
		t.Fatalf("x-payment-info extension missing")
	}
	price, _ := xpay["price"].(map[string]any)
	if price["mode"] != "fixed" {
		t.Errorf("price.mode = %v, want fixed", price["mode"])
	}
	if price["currency"] != "USD" {
		t.Errorf("price.currency = %v, want USD for USDC-settled offer", price["currency"])
	}
	if price["amount"] != "0.0015" {
		t.Errorf("price.amount = %v, want per-request approximation 0.0015", price["amount"])
	}
	protocols, _ := xpay["protocols"].([]any)
	if len(protocols) != 1 {
		t.Fatalf("protocols = %v, want exactly the x402 entry", xpay["protocols"])
	}
	if _, ok := protocols[0].(map[string]any)["x402"]; !ok {
		t.Errorf("protocols[0] = %v, want {\"x402\": {}}", protocols[0])
	}
	// Security is explicitly empty: x402 is enforced by the paywall, not an
	// HTTP auth scheme, and [x-payment-info + security: []] is what indexers
	// read as a paid route.
	sec, ok := opMap["security"].([]any)
	if !ok || len(sec) != 0 {
		t.Fatalf("security = %v, want explicitly empty array", opMap["security"])
	}
}

// TestBuildOpenAPIDocument_MultiPaymentAdvertisesAllOptions locks in the
// multi-currency x-payment-info contract: `price` stays the primary option
// (for single-price indexers), and `accepts[]` lists every option with its
// currency and CAIP-2 network so indexers can surface the cheapest.
// TestBuildOpenAPIDocument_AcceptsCarrySigningMetadata pins that every
// x-payment-info advertises accepts[] (single-payment offers included) with
// the full signing recipe — payTo, CAIP-2 network, atomic amount, and the
// asset's EIP-712 domain — so an OpenAPI-only client can construct a valid
// X-PAYMENT without a second fetch of /api/services.json.
func TestBuildOpenAPIDocument_AcceptsCarrySigningMetadata(t *testing.T) {
	offer := readyOfferWithSpec("solo", "svc", monetizeapi.ServiceOfferSpec{
		Type:     "inference",
		Model:    monetizeapi.ServiceOfferModel{Name: "m1"},
		Upstream: monetizeapi.ServiceOfferUpstream{Service: "up", Port: 8000},
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base-sepolia",
			PayTo:   "0x2222222222222222222222222222222222222222",
			Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
		},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "https://tunnel.example", schemas.StorefrontProfile{}))
	op := dig(t, doc, "paths", "/services/solo/v1/chat/completions", "post")
	info, _ := op.(map[string]any)["x-payment-info"].(map[string]any)
	if info == nil {
		t.Fatalf("x-payment-info missing")
	}

	accepts, ok := info["accepts"].([]any)
	if !ok || len(accepts) != 1 {
		t.Fatalf("accepts = %#v, want exactly 1 entry for a single-payment offer", info["accepts"])
	}
	entry := accepts[0].(map[string]any)
	if entry["payTo"] != "0x2222222222222222222222222222222222222222" {
		t.Errorf("payTo = %v", entry["payTo"])
	}
	if entry["network"] != "eip155:84532" {
		t.Errorf("network = %v, want CAIP-2 eip155:84532", entry["network"])
	}
	if entry["amountAtomicUnits"] != "1000" {
		t.Errorf("amountAtomicUnits = %v, want 1000 (0.001 USDC)", entry["amountAtomicUnits"])
	}
	asset, ok := entry["asset"].(map[string]any)
	if !ok {
		t.Fatalf("asset missing: %#v", entry)
	}
	domain, ok := asset["eip712Domain"].(map[string]any)
	if !ok {
		t.Fatalf("asset.eip712Domain missing: %#v (wrong-domain signing is the top silent buyer killer)", asset)
	}
	if domain["name"] == "" || domain["version"] == "" {
		t.Errorf("eip712Domain incomplete: %#v", domain)
	}
}

func TestBuildOpenAPIDocument_MultiPaymentAdvertisesAllOptions(t *testing.T) {
	offer := readyOfferWithSpec("dual", "llm", monetizeapi.ServiceOfferSpec{
		Type: "inference",
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base", PayTo: "0x1111111111111111111111111111111111111111",
			Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"},
		},
		Payments: []monetizeapi.ServiceOfferPayment{
			{
				Network: "base", PayTo: "0x1111111111111111111111111111111111111111",
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"},
			},
			{
				Network: "ethereum", PayTo: "0x2222222222222222222222222222222222222222",
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "10"},
				Asset: monetizeapi.ServiceOfferAsset{Symbol: "OBOL", Address: "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7", Decimals: 18, TransferMethod: "permit2", EIP712Name: "Obol Network", EIP712Version: "1"},
			},
		},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "", schemas.StorefrontProfile{}))
	op := dig(t, doc, "paths", "/services/dual/v1/chat/completions", "post")
	xpay, _ := op.(map[string]any)["x-payment-info"].(map[string]any)
	if xpay == nil {
		t.Fatalf("x-payment-info missing")
	}
	// Primary price = first option (USDC on base → USD).
	if price, _ := xpay["price"].(map[string]any); price["currency"] != "USD" || price["amount"] != "1" {
		t.Errorf("primary price = %v, want USD/1", xpay["price"])
	}
	accepts, _ := xpay["accepts"].([]any)
	if len(accepts) != 2 {
		t.Fatalf("accepts = %v, want 2 options", xpay["accepts"])
	}
	a0 := accepts[0].(map[string]any)
	if a0["currency"] != "USD" || a0["network"] != "eip155:8453" {
		t.Errorf("accepts[0] = %v, want USD on eip155:8453", a0)
	}
	a1 := accepts[1].(map[string]any)
	if a1["currency"] != "OBOL" || a1["amount"] != "10" || a1["network"] != "eip155:1" {
		t.Errorf("accepts[1] = %v, want OBOL/10 on eip155:1", a1)
	}
}

// TestBuildOpenAPIDocument_AgentOfferSameShapeAsInference locks in the
// user-confirmed decision: agent-type offers ship the OpenAI chat
// completions endpoint, identical to inference. Renderers don't need
// special-case agent handling.
func TestBuildOpenAPIDocument_AgentOfferSameShapeAsInference(t *testing.T) {
	offer := readyOfferWithSpec("hermes-agent", "hermes-obol-agent", monetizeapi.ServiceOfferSpec{
		Type: "agent",
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base-sepolia",
			PayTo:   "0x2222222222222222222222222222222222222222",
			Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
		},
	})
	offer.Status.AgentResolution = &monetizeapi.ServiceOfferAgentResolution{Model: "qwen3.5:9b"}

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "", schemas.StorefrontProfile{}))

	if op := dig(t, doc, "paths", "/services/hermes-agent/v1/chat/completions", "post"); op == nil {
		t.Fatalf("agent offer missing /v1/chat/completions endpoint, paths = %v", doc["paths"])
	}
	// Summary surfaces the resolved agent model (via status.agentResolution).
	summary := dig(t, doc, "paths", "/services/hermes-agent/v1/chat/completions", "post", "summary")
	if s, _ := summary.(string); !strings.Contains(s, "qwen3.5:9b") {
		t.Errorf("summary = %v, want resolved agent model surfaced", summary)
	}
}

func TestBuildOpenAPIDocument_HTTPOffer(t *testing.T) {
	offer := readyOfferWithSpec("echo", "demo", monetizeapi.ServiceOfferSpec{
		Type: "http",
		Path: "/services/echo",
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base",
			PayTo:   "0x3333333333333333333333333333333333333333",
			Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.00001"},
		},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "", schemas.StorefrontProfile{}))

	op := dig(t, doc, "paths", "/services/echo", "get")
	if op == nil {
		t.Fatalf("http offer missing GET /services/echo, paths = %v", doc["paths"])
	}
	// Default http emission is GET with no request body (demo/hello-shaped).
	if body := dig(t, op.(map[string]any), "requestBody"); body != nil {
		t.Errorf("http GET offer must not advertise a requestBody, got %v", body)
	}
	if post := dig(t, doc, "paths", "/services/echo", "post"); post != nil {
		t.Errorf("default http offer must not advertise POST when Methods is empty")
	}
}

func TestBuildOpenAPIDocument_FineTuningOffer(t *testing.T) {
	offer := readyOfferWithSpec("train", "demo", monetizeapi.ServiceOfferSpec{
		Type: "fine-tuning",
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base",
			PayTo:   "0x4444444444444444444444444444444444444444",
			Price:   monetizeapi.ServiceOfferPriceTable{PerHour: "0.10"},
		},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{offer}, "", schemas.StorefrontProfile{}))

	op := dig(t, doc, "paths", "/services/train", "post")
	if op == nil {
		t.Fatalf("fine-tuning offer missing POST /services/train, paths = %v", doc["paths"])
	}
	// Fine-tuning uses multipart/form-data, not JSON.
	if mp := dig(t, op.(map[string]any), "requestBody", "content", "multipart/form-data"); mp == nil {
		t.Errorf("fine-tuning offer missing multipart/form-data body")
	}
}

func TestBuildOpenAPIDocument_ExcludesNotReadyAndDrained(t *testing.T) {
	ready := readyOfferWithSpec("alpha", "demo", monetizeapi.ServiceOfferSpec{
		Type:    "inference",
		Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xaa", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
	})
	notReady := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "demo"},
		Status:     monetizeapi.ServiceOfferStatus{Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "False"}}},
	}

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{ready, notReady}, "", schemas.StorefrontProfile{}))
	paths, _ := doc["paths"].(map[string]any)
	if len(paths) != 1 {
		t.Fatalf("paths = %d entries, want only the ready offer: %v", len(paths), paths)
	}
	if _, ok := paths["/services/alpha/v1/chat/completions"]; !ok {
		t.Errorf("ready inference offer missing from paths: %v", paths)
	}
}

// TestBuildOpenAPIDocument_TunnelURLTrailingSlash mirrors the same
// safety check buildServiceCatalogJSON has — a stray trailing slash on
// the configmap value should not produce a `//` in the spec servers[].
func TestBuildOpenAPIDocument_TunnelURLTrailingSlash(t *testing.T) {
	doc := parseOpenAPI(t, buildOpenAPIDocument(nil, "https://tunnel.example/", schemas.StorefrontProfile{}))
	servers, _ := doc["servers"].([]any)
	first := servers[0].(map[string]any)
	if first["url"] != "https://tunnel.example" {
		t.Errorf("servers[0].url = %v, want trailing-slash-stripped", first["url"])
	}
}

// TestBuildOpenAPIDocument_MultipleOffersPathsDistinct ensures two offers
// rendering at the same shape get distinct paths (the offer's
// EffectivePath is the namespacing key).
func TestBuildOpenAPIDocument_MultipleOffersPathsDistinct(t *testing.T) {
	a := readyOfferWithSpec("a", "llm", monetizeapi.ServiceOfferSpec{
		Type:    "inference",
		Model:   monetizeapi.ServiceOfferModel{Name: "m1"},
		Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xaa", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
	})
	b := readyOfferWithSpec("b", "llm", monetizeapi.ServiceOfferSpec{
		Type:    "inference",
		Model:   monetizeapi.ServiceOfferModel{Name: "m2"},
		Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xbb", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{a, b}, "", schemas.StorefrontProfile{}))
	paths, _ := doc["paths"].(map[string]any)
	if _, ok := paths["/services/a/v1/chat/completions"]; !ok {
		t.Errorf("offer a missing")
	}
	if _, ok := paths["/services/b/v1/chat/completions"]; !ok {
		t.Errorf("offer b missing")
	}
}

// TestBuildOpenAPIDocument_AggregateTags pins the union behavior — every
// skill/domain across every offer becomes a top-level tag entry.
func TestBuildOpenAPIDocument_AggregateTags(t *testing.T) {
	a := readyOfferWithSpec("a", "llm", monetizeapi.ServiceOfferSpec{
		Type:    "inference",
		Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xaa", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
		Registration: monetizeapi.ServiceOfferRegistration{
			Skills:  []string{"text-generation"},
			Domains: []string{"ai/llm"},
		},
	})
	b := readyOfferWithSpec("b", "llm", monetizeapi.ServiceOfferSpec{
		Type:    "inference",
		Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0xbb", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"}},
		Registration: monetizeapi.ServiceOfferRegistration{
			Skills:  []string{"text-generation", "embeddings"},
			Domains: []string{"ai/llm"},
		},
	})

	doc := parseOpenAPI(t, buildOpenAPIDocument([]*monetizeapi.ServiceOffer{a, b}, "", schemas.StorefrontProfile{}))
	tags, _ := doc["tags"].([]any)
	names := map[string]struct{}{}
	for _, t := range tags {
		tm, _ := t.(map[string]any)
		if n, ok := tm["name"].(string); ok {
			names[n] = struct{}{}
		}
	}
	for _, want := range []string{"text-generation", "embeddings", "ai/llm"} {
		if _, ok := names[want]; !ok {
			t.Errorf("aggregate tags missing %q: %v", want, names)
		}
	}
}

// TestBuildOpenAPIHTTPRoute and TestBuildAPIDocsHTTPRoute pin the path
// matchers — they are the contract Traefik resolves on, and the routes
// must remain unrestricted (no hostnames filter) so they reach the public
// tunnel as well as obol.stack:8080.
func TestBuildOpenAPIHTTPRoute(t *testing.T) {
	route := buildOpenAPIHTTPRoute()
	if route.GetName() != openAPIRouteName {
		t.Fatalf("name = %q, want %q", route.GetName(), openAPIRouteName)
	}
	spec, _ := route.Object["spec"].(map[string]any)
	if _, hasHostnames := spec["hostnames"]; hasHostnames {
		t.Error("openapi route must not have hostnames filter (tunnel-reachable by design)")
	}
	rules, _ := spec["rules"].([]any)
	rule := rules[0].(map[string]any)
	matches, _ := rule["matches"].([]any)
	if got := matches[0].(map[string]any)["path"].(map[string]any)["value"]; got != "/openapi.json" {
		t.Errorf("match path = %v, want /openapi.json", got)
	}
}

func TestBuildAPIDocsHTTPRoute(t *testing.T) {
	route := buildAPIDocsHTTPRoute()
	if route.GetName() != apiDocsRouteName {
		t.Fatalf("name = %q, want %q", route.GetName(), apiDocsRouteName)
	}
	spec, _ := route.Object["spec"].(map[string]any)
	if _, hasHostnames := spec["hostnames"]; hasHostnames {
		t.Error("api docs route must not have hostnames filter")
	}
	rules, _ := spec["rules"].([]any)
	rule := rules[0].(map[string]any)
	matches, _ := rule["matches"].([]any)
	gotPaths := map[string]struct{}{}
	for _, m := range matches {
		path := m.(map[string]any)["path"].(map[string]any)
		gotPaths[path["value"].(string)] = struct{}{}
	}
	for _, want := range []string{"/api", "/api/"} {
		if _, ok := gotPaths[want]; !ok {
			t.Errorf("api docs route missing %q match, got %v", want, gotPaths)
		}
	}
}

// TestScalarHTMLShell asserts the OG/theme contract: theme tokens, OG
// metadata, and that the spec URL is pointed at the sibling /openapi.json.
func TestScalarHTMLShell(t *testing.T) {
	html := scalarHTML(storefront.ResolvePublished(nil, "https://seller.example.com"))

	lightTheme := storefront.ResolveTheme(storefront.DefaultTheme, "")
	for _, want := range []string{
		`<script id="api-reference" data-url="/openapi.json">`,
		lightTheme.Vars["green"],
		lightTheme.Vars["bg01"],
		`property="og:title"`,
		`property="og:image"`,
		`name="twitter:card"`,
		`name="theme-color"`,
		"Obol Stack — API reference",
		"@scalar/api-reference@" + scalarBundleVersion,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("scalar HTML missing %q", want)
		}
	}
}

// TestScalarHTMLShell_BrandedProfile asserts the operator profile drives
// title, theme, favicon, and og image.
func TestScalarHTMLShell_BrandedProfile(t *testing.T) {
	html := scalarHTML(storefront.ResolvePublished(&schemas.StorefrontProfile{
		DisplayName: "Acme Labs",
		Theme:       storefront.ThemeDark,
		AccentColor: "#a1b2c3",
		FaviconURL:  "https://cdn.example.com/fav.png",
		OGImageURL:  "https://cdn.example.com/og.png",
	}, "https://seller.example.com"))

	for _, want := range []string{
		"Acme Labs — API reference",
		`--scalar-color-accent: #a1b2c3;`,
		`--scalar-background-1: #091011;`,
		`<link rel="icon" href="https://cdn.example.com/fav.png" />`,
		`content="https://cdn.example.com/og.png"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("branded scalar HTML missing %q", want)
		}
	}
}

func containsAny(slice []any, wants ...string) bool {
	got := map[string]struct{}{}
	for _, item := range slice {
		if s, ok := item.(string); ok {
			got[s] = struct{}{}
		}
	}
	for _, w := range wants {
		if _, ok := got[w]; !ok {
			return false
		}
	}
	return true
}
