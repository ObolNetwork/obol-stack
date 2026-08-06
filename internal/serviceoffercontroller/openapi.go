package serviceoffercontroller

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

// openAPISpecVersion is the OpenAPI specification version we emit. 3.1.0
// lines up JSON Schema 2020-12 with the dialect used in our component
// schemas (additionalProperties, examples, oneOf). Scalar and modern Redoc
// both render 3.1 natively.
const openAPISpecVersion = "3.1.0"

// localBaseURL is the well-known local-cluster origin published as a
// secondary entry in the OpenAPI `servers` block. The tunnel URL (when
// known) is listed first because the spec is most useful for external
// buyers; the local entry keeps the doc usable on `obol stack up` without
// a tunnel.
const localBaseURL = "http://obol.stack:8080"

// buildOpenAPIDocument produces the aggregate OpenAPI 3.1 JSON for every
// operationally-ready ServiceOffer. tunnelURL is the public origin sourced
// from the obol-frontend/obol-stack-config ConfigMap; when empty (no
// tunnel) only the local-cluster server entry is emitted.
//
// The output is JSON, deterministically ordered, indented with two spaces
// so manual `curl /openapi.json` is readable.
func buildOpenAPIDocument(offers []*monetizeapi.ServiceOffer, tunnelURL string, profile schemas.StorefrontProfile) string {
	tunnelURL = strings.TrimRight(tunnelURL, "/")

	now := time.Now()
	var ready []*monetizeapi.ServiceOffer
	for _, offer := range offers {
		if offer == nil || offer.DeletionTimestamp != nil {
			continue
		}
		if offer.DrainExpired(now) {
			continue
		}
		if offerOperationallyReady(offer) {
			ready = append(ready, offer)
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Namespace == ready[j].Namespace {
			return ready[i].Name < ready[j].Name
		}
		return ready[i].Namespace < ready[j].Namespace
	})

	components := map[string]any{
		"schemas":   openAPIComponentSchemas(),
		"responses": openAPIComponentResponses(),
	}
	// The siwx securityScheme is emitted ONLY when some offer declares a
	// gate:auth route — those genuinely are HTTP-auth-gated (wallet
	// sign-in). An unconditional scheme would re-introduce the indexer
	// misclassification the "no securitySchemes" rule below exists to
	// prevent (schemes present ⇒ x402scan reads the API as auth-gated).
	if anyOfferHasAuthRoute(ready) {
		components["securitySchemes"] = map[string]any{
			"siwx": map[string]any{
				"type":        "http",
				"scheme":      "bearer",
				"description": "Sign-In With X (EIP-4361). Either send `Authorization: SIWX <base64 message>.<base64 signature>` — an EIP-4361 message (domain = this host, Version 1, fresh Nonce, recent Issued At) signed with EIP-191 personal_sign — or POST {message, signature} to the offer's `/auth/verify` endpoint and reuse the returned session token as `Authorization: Bearer <token>`. Operations gated this way carry `x-auth-info` with the exact URLs.",
			},
		}
	}

	doc := map[string]any{
		"openapi":    openAPISpecVersion,
		"info":       buildOpenAPIInfo(profile, len(ready)),
		"servers":    buildOpenAPIServers(tunnelURL),
		"tags":       buildOpenAPITags(ready),
		"paths":      buildOpenAPIPaths(ready),
		"components": components,
		// No global security block: payment is enforced by the x402 paywall
		// (runtime 402 + X-PAYMENT retry), not an HTTP auth scheme. Modeling
		// X-PAYMENT as an apiKey securityScheme made discovery indexers
		// (x402scan) classify every route as API-key-gated instead of paid;
		// per their convention, paid operations carry `x-payment-info` and
		// an explicitly empty per-operation `security: []`.
	}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		// json.MarshalIndent on a hand-built map[string]any tree only fails
		// on unsupported types (chans, funcs). Everything above is plain
		// scalars/maps/slices, so this branch is unreachable in practice —
		// fall back to a minimal valid doc rather than an empty body.
		return `{"openapi":"` + openAPISpecVersion + `","info":{"title":"Obol Stack — paid services","version":"1"},"paths":{}}`
	}
	return string(encoded)
}

func buildOpenAPIInfo(profile schemas.StorefrontProfile, readyCount int) map[string]any {
	description := "x402 payment-gated services advertised by this Obol Stack operator. " +
		"Every operation expects an `X-PAYMENT` header carrying a signed x402 v2 payment payload; " +
		"unpaid requests receive a 402 with `accepts[]` describing the prices the operator will honour. " +
		"See https://www.x402.org for the wire protocol and " +
		"https://github.com/ObolNetwork/obol-stack for the source.\n\n" +
		"Note: schemas for individual operations are derived heuristically from the underlying " +
		"ServiceOffer's `type` field. Buyers using generated clients should treat request and " +
		"response shapes as advisory until operators tighten them with explicit OpenAPI fragments."
	if readyCount == 0 {
		description = "This operator currently advertises no live services. " + description
	}
	contactName := strings.TrimSpace(profile.DisplayName)
	if contactName == "" || contactName == "Obol Stack" {
		contactName = "Obol Stack"
	}
	contact := map[string]any{
		"name": contactName,
		"url":  "https://github.com/ObolNetwork/obol-stack",
	}
	if email := strings.TrimSpace(profile.ContactEmail); email != "" {
		contact["email"] = email
	}
	return map[string]any{
		"title":       "Obol Stack — paid services",
		"version":     "1",
		"description": description,
		"contact":     contact,
		// x-guidance is the agent-facing usage overview read by discovery
		// indexers (x402scan L4 audit). Keep it answer-shaped: what an agent
		// must do to call any operation in this document.
		"x-guidance": "All operations are x402 payment-gated; no API key or signup is needed — " +
			"any wallet holding the listed settlement asset can pay. To call one: " +
			"(1) send the request without payment and read the 402 JSON response — `accepts[]` " +
			"lists the price in atomic units, the CAIP-2 chain id, and the settlement token contract; " +
			"(2) sign a payment authorization matching one accepts entry and retry the identical " +
			"request with the payload base64-encoded in the `X-PAYMENT` header; " +
			"(3) on success the settlement metadata arrives in the `X-PAYMENT-RESPONSE` header. " +
			"Each 402 also carries machine-readable invocation schemas in `extensions.bazaar` " +
			"and the same challenge base64-encoded in the `PAYMENT-REQUIRED` header. " +
			"For chat-completions operations, take the model identifier from the operation summary " +
			"or the catalog at /api/services.json. Streaming (`stream: true`) is supported and " +
			"recommended for long-running agent calls.",
	}
}

func buildOpenAPIServers(tunnelURL string) []any {
	servers := []any{}
	if tunnelURL != "" {
		servers = append(servers, map[string]any{
			"url":         tunnelURL,
			"description": "Public tunnel",
		})
	}
	servers = append(servers, map[string]any{
		"url":         localBaseURL,
		"description": "Local cluster (obol stack up host)",
	})
	return servers
}

// buildOpenAPITags returns the deduplicated union of every ready offer's
// registration.skills and registration.domains. Used to populate the
// top-level `tags` block so renderers can show a categorised sidebar.
func buildOpenAPITags(offers []*monetizeapi.ServiceOffer) []any {
	seen := map[string]struct{}{}
	for _, offer := range offers {
		if offer == nil {
			continue
		}
		for _, skill := range offer.Spec.Registration.Skills {
			if skill = strings.TrimSpace(skill); skill != "" {
				seen[skill] = struct{}{}
			}
		}
		for _, domain := range offer.Spec.Registration.Domains {
			if domain = strings.TrimSpace(domain); domain != "" {
				seen[domain] = struct{}{}
			}
		}
	}
	if len(seen) == 0 {
		return []any{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	tags := make([]any, 0, len(names))
	for _, name := range names {
		tags = append(tags, map[string]any{"name": name})
	}
	return tags
}

// buildOpenAPIPaths emits one or more PathItem entries per offer. The
// keys are namespaced under the offer's `EffectivePath()`, so two offers
// with the same shape never collide.
func buildOpenAPIPaths(offers []*monetizeapi.ServiceOffer) map[string]any {
	paths := map[string]any{}
	for _, offer := range offers {
		for relPath, item := range openAPIPathsForOffer(offer) {
			full := joinOpenAPIPath(offer.EffectivePath(), relPath)
			paths[full] = item
		}
	}
	return paths
}

// openAPIPrimaryPathForOffer returns the offer's key in the OpenAPI document's
// `paths` object (e.g. "/services/foo/v1/chat/completions"). Published on the
// catalog entry as `openapiPath` so consumers can jump from /api/services.json
// straight to the offer's request/response schema.
func openAPIPrimaryPathForOffer(offer *monetizeapi.ServiceOffer) string {
	if offer == nil {
		return ""
	}
	if offer.IsInference() || offer.IsAgent() {
		return joinOpenAPIPath(offer.EffectivePath(), "/v1/chat/completions")
	}
	if rt, ok := primaryPaidRoute(offer); ok {
		return joinOpenAPIPath(offer.EffectivePath(), openAPIRelPathForRoute(rt.Path))
	}
	return joinOpenAPIPath(offer.EffectivePath(), "")
}

// anyOfferHasAuthRoute reports whether any offer's declared route table
// contains a gate:auth entry (the condition for emitting the siwx
// securityScheme).
func anyOfferHasAuthRoute(offers []*monetizeapi.ServiceOffer) bool {
	for _, offer := range offers {
		for _, rt := range offer.Spec.Routes {
			if rt.EffectiveGate() == monetizeapi.GateAuth {
				return true
			}
		}
	}
	return false
}

// primaryPaidRoute returns the first paid entry of a DECLARED route table.
// ok=false for offers without spec.routes — callers keep the legacy
// offer-root behavior — and for (misconfigured) all-free tables.
func primaryPaidRoute(offer *monetizeapi.ServiceOffer) (monetizeapi.ServiceOfferRoute, bool) {
	if len(offer.Spec.Routes) == 0 {
		return monetizeapi.ServiceOfferRoute{}, false
	}
	for _, rt := range offer.EffectiveRoutes() {
		if rt.EffectiveGate() == monetizeapi.GatePaid {
			return rt, true
		}
	}
	return monetizeapi.ServiceOfferRoute{}, false
}

// defaultPaidMethod is the method advertised when a paid route does not
// declare Methods. Inference/agent/fine-tuning are write-shaped (POST);
// plain http defaults to GET — that matches demo/hello and most "pay to
// fetch" endpoints, and stops OpenAPI/AgentCash clients POSTing into a
// GET-only upstream (405).
func defaultPaidMethod(offer *monetizeapi.ServiceOffer) string {
	if offer != nil && (offer.IsInference() || offer.IsAgent() || strings.EqualFold(offer.Spec.Type, "fine-tuning")) {
		return "POST"
	}
	return "GET"
}

// primaryPaidMethod is the HTTP method advertised for the offer's primary
// paid operation: the first declared method on the primary paid route, or
// defaultPaidMethod when Methods is empty.
func primaryPaidMethod(offer *monetizeapi.ServiceOffer) string {
	if rt, ok := primaryPaidRoute(offer); ok && len(rt.Methods) > 0 {
		return strings.ToUpper(rt.Methods[0])
	}
	return defaultPaidMethod(offer)
}

// openAPIDocsAnchorForOffer returns the site-relative Scalar deep link for
// this offer's operation, e.g.
// "/api#tag/agent/POST/services/foo/v1/chat/completions". Scalar's default
// hash routing is "#tag/<first-tag>/<METHOD><path>"; centralising the format
// here (published as the catalog entry's `docsPath`) means consumers link
// docs without hardcoding a renderer-version-specific anchor scheme.
func openAPIDocsAnchorForOffer(offer *monetizeapi.ServiceOffer) string {
	path := openAPIPrimaryPathForOffer(offer)
	if path == "" {
		return ""
	}
	return "/api#tag/" + fallbackOfferType(offer) + "/" + primaryPaidMethod(offer) + path
}

// openAPIPathsForOffer returns the set of {path → pathItem} entries this
// offer contributes. Phase 1 uses pure type-based heuristics:
//
//   - inference, agent → POST /v1/chat/completions with the OpenAI shapes.
//     User feedback: agent uses the same OpenAI chat completions wire
//     format as inference, so they share an emission path.
//   - fine-tuning    → POST <path> with multipart upload, generic 200.
//   - http (default) → GET <path>, generic 200 (no request body).
//
// Every operation references the shared `PaymentRequired` 402 response and
// carries an `x-payment-info` extension marking it as x402-paid for
// discovery indexers. Phase 2 will let operators override these emissions
// with explicit per-offer fragments.
func openAPIPathsForOffer(offer *monetizeapi.ServiceOffer) map[string]map[string]any {
	if offer == nil {
		return nil
	}
	// Declared route tables drive the emission for http/fine-tuning offers:
	// one operation per route, with per-route gate + price. Inference/agent
	// offers keep the chat-completions synthesis (their wire shape is fixed
	// by the OpenAI contract regardless of route carve-outs).
	if len(offer.Spec.Routes) > 0 && !offer.IsInference() && !offer.IsAgent() {
		return annotateAsyncPaths(offer, openAPIPathsForRouteTable(offer))
	}
	switch {
	case offer.IsInference(), offer.IsAgent():
		return map[string]map[string]any{
			"/v1/chat/completions": {
				"post": openAPIOperation(offer, openAPIOperationOptions{
					summary:     openAIChatCompletionsSummary(offer),
					description: offerDescription(offer, "OpenAI-compatible chat completions endpoint."),
					requestBody: openAPIJSONRequestBody(
						"#/components/schemas/OpenAIChatCompletionsRequest",
						"OpenAI Chat Completions request payload.",
					),
					successResponse: openAPIJSONSuccessResponse(
						"#/components/schemas/OpenAIChatCompletionsResponse",
						"Chat completion (OpenAI-compatible).",
					),
				}),
			},
		}
	case strings.EqualFold(offer.Spec.Type, "fine-tuning"):
		return map[string]map[string]any{
			"": {
				"post": openAPIOperation(offer, openAPIOperationOptions{
					summary:     "Submit fine-tuning job — " + offer.Name,
					description: offerDescription(offer, "Submit a fine-tuning job. Payload shape is operator-defined."),
					requestBody: map[string]any{
						"required": true,
						"content": map[string]any{
							"multipart/form-data": map[string]any{
								"schema": map[string]any{
									"type":                 "object",
									"description":          "Training payload. Concrete fields are upstream-specific until the operator publishes an explicit OpenAPI fragment.",
									"additionalProperties": true,
								},
							},
						},
					},
					successResponse: openAPIGenericSuccessResponse("Fine-tuning job accepted."),
				}),
			},
		}
	default:
		return annotateAsyncPaths(offer, map[string]map[string]any{
			"": {
				"get": openAPIOperation(offer, openAPIOperationOptions{
					summary:         "Invoke " + offer.Name,
					description:     offerDescription(offer, "x402 payment-gated HTTP service."),
					successResponse: openAPIGenericSuccessResponse("Upstream response (shape is operator-defined)."),
				}),
			},
		})
	}
}

// annotateAsyncPaths marks the paid operations of an async offer and adds
// the /jobs surface, so buyers learn the 202→poll→result dance from the
// document alone.
func annotateAsyncPaths(offer *monetizeapi.ServiceOffer, paths map[string]map[string]any) map[string]map[string]any {
	if !offer.Spec.Async.Enabled {
		return paths
	}
	for _, item := range paths {
		for _, rawOp := range item {
			op, ok := rawOp.(map[string]any)
			if !ok {
				continue
			}
			if _, paid := op["x-payment-info"]; !paid {
				continue
			}
			op["x-async"] = true
			if responses, ok := op["responses"].(map[string]any); ok {
				responses["202"] = map[string]any{
					"description": "Job accepted; payment settled. The body carries {jobId, statusUrl, resultUrl, jobToken}; Location points at the free status page. Poll statusUrl until state=complete, then GET resultUrl authenticated as the paying wallet (SIWX) or with `Authorization: Bearer <jobToken>`. Optional: include {\"callbackUrl\": \"...\"} in a JSON submit body for a completion webhook.",
				}
				delete(responses, "200")
			}
		}
	}
	paths["/jobs/{jobId}"] = map[string]any{
		"get": map[string]any{
			"summary":     offer.Name + " — job status (free)",
			"description": "Free async job status: JSON for pollers, an auto-refreshing HTML page for browsers. `Prefer: redirect` returns 303 to the result once complete.",
			"operationId": openAPIOperationID(offer) + "-job-status",
			"tags":        operationTagsForOffer(offer),
			"security":    []any{},
			"x-gate":      "free",
			"responses": map[string]any{
				"200": openAPIGenericSuccessResponse("Job state: {jobId, state, createdAt, expiresAt, resultUrl?, error?}."),
				"410": map[string]any{"description": "Retention window elapsed; the job record and result were deleted."},
			},
		},
	}
	access := "Access: the paying wallet (SIWX) or the jobToken from the 202 body."
	if offer.Spec.Async.EffectiveResultVisibility() == monetizeapi.ResultVisibilityPublic {
		access = "This offer publishes results publicly — the unguessable job id is the capability."
	}
	paths["/jobs/{jobId}/result"] = map[string]any{
		"get": map[string]any{
			"summary":     offer.Name + " — job result",
			"description": "The stored upstream response, served verbatim once state=complete. " + access,
			"operationId": openAPIOperationID(offer) + "-job-result",
			"tags":        operationTagsForOffer(offer),
			"security":    []any{},
			"responses": map[string]any{
				"200": openAPIGenericSuccessResponse("The stored upstream response (original content type)."),
				"401": map[string]any{"description": "Not the paying wallet and no valid jobToken."},
				"409": map[string]any{"description": "Job still running; poll the status URL."},
				"502": map[string]any{"description": "The job failed. Payment settled at acceptance — report via info.contact."},
			},
		},
	}
	return paths
}

// openAPIPathsForRouteTable renders one operation per declared route.
// Paid routes carry the shared 402 reference and an x-payment-info built
// from the route's effective price (per-route override or the offer's
// payments). Free routes carry neither — they are advertised as plainly
// callable, marked with x-gate: free so indexers don't misread the absence
// of payment metadata as an omission.
//
// openAPIRelPathForRoute collapses an exact path and its own "/*" wildcard
// sibling onto the same {key, method} slot (e.g. "/jobs" and "/jobs/*" both
// key as "/jobs"). The verifier resolves that overlap by specificity, not
// declaration order (sortRoutesBySpecificity in internal/x402/matcher.go) —
// exact beats wildcard. We sort a copy of the route table the same way
// before rendering, and skip a method that a more specific route already
// wrote, so the collapsed operation always reflects the route the verifier
// will actually select instead of whichever was declared last.
func openAPIPathsForRouteTable(offer *monetizeapi.ServiceOffer) map[string]map[string]any {
	routes := append([]monetizeapi.ServiceOfferRoute(nil), offer.EffectiveRoutes()...)
	sortRouteTableBySpecificity(routes)

	paths := map[string]map[string]any{}
	for _, rt := range routes {
		rel := openAPIRelPathForRoute(rt.Path)
		item := paths[rel]
		if item == nil {
			item = map[string]any{}
			paths[rel] = item
		}
		gate := rt.EffectiveGate()

		summary := rt.Summary
		if summary == "" {
			switch gate {
			case monetizeapi.GateFree:
				summary = fmt.Sprintf("%s — %s (free)", offer.Name, rt.Path)
			case monetizeapi.GateAuth:
				summary = fmt.Sprintf("%s — %s (wallet sign-in)", offer.Name, rt.Path)
			default:
				summary = fmt.Sprintf("Invoke %s — %s", offer.Name, rt.Path)
			}
		}
		description := offerDescription(offer, "x402 payment-gated HTTP service.")
		if strings.HasSuffix(rt.Path, "/*") {
			description += " This operation covers every sub-path under " + rt.Path + "."
		}

		methods := rt.Methods
		if len(methods) == 0 {
			if gate == monetizeapi.GatePaid {
				methods = []string{defaultPaidMethod(offer)}
			} else {
				methods = []string{"GET"}
			}
		}

		for _, method := range methods {
			// routes is sorted most-specific-first: if a more specific
			// route already claimed this method at this collapsed key,
			// it wins — matching the verifier's resolution.
			if _, claimed := item[strings.ToLower(method)]; claimed {
				continue
			}
			op := map[string]any{
				"summary":     summary,
				"description": description,
				"operationId": openAPIRouteOperationID(offer, method, rt.Path),
				"tags":        operationTagsForOffer(offer),
				"security":    []any{},
			}
			switch gate {
			case monetizeapi.GateFree:
				op["responses"] = map[string]any{
					"200": openAPIGenericSuccessResponse("Upstream response (shape is operator-defined)."),
				}
				op["x-gate"] = "free"
			case monetizeapi.GateAuth:
				op["responses"] = map[string]any{
					"200": openAPIGenericSuccessResponse("Upstream response (shape is operator-defined)."),
					"401": map[string]any{
						"description": "Authentication required. The body and WWW-Authenticate header describe the SIWX challenge; browsers get a sign-in page.",
					},
				}
				op["security"] = []any{map[string]any{"siwx": []any{}}}
				op["x-gate"] = "auth"
				op["x-auth-info"] = map[string]any{
					"scheme":    "siwx",
					"version":   "eip4361",
					"signInUrl": joinOpenAPIPath(offer.EffectivePath(), "/auth"),
					"verifyUrl": joinOpenAPIPath(offer.EffectivePath(), "/auth/verify"),
				}
			default:
				op["responses"] = map[string]any{
					"200": openAPIGenericSuccessResponse("Upstream response (shape is operator-defined)."),
					"402": map[string]any{"$ref": "#/components/responses/PaymentRequired"},
				}
				op["x-payment-info"] = routePaymentInfoExtension(offer, rt)
				if strings.EqualFold(method, "POST") || strings.EqualFold(method, "PUT") || strings.EqualFold(method, "PATCH") {
					op["requestBody"] = openAPIJSONRequestBody(
						"",
						"Operator-defined JSON payload. Shape is not specified in phase 1.",
					)
				}
			}
			item[strings.ToLower(method)] = op
		}
	}
	return paths
}

// sortRouteTableBySpecificity orders a copy of the route table
// most-specific-first: exact patterns before wildcards, then longer literal
// prefix, then deeper (more path segments) — the same rule
// sortRoutesBySpecificity (internal/x402/matcher.go) applies to the
// verifier's RouteRules. All routes in a table share the offer's prefix, so
// comparing the relative rt.Path is equivalent to comparing the full
// verifier pattern. sort.SliceStable so equally-specific routes keep their
// declared order.
func sortRouteTableBySpecificity(routes []monetizeapi.ServiceOfferRoute) {
	sort.SliceStable(routes, func(i, j int) bool {
		ei, li := routePatternSpecificity(routes[i].Path)
		ej, lj := routePatternSpecificity(routes[j].Path)
		if ei != ej {
			return ei // exact before wildcard
		}
		if li != lj {
			return li > lj // longer literal prefix first
		}
		si := strings.Count(routes[i].Path, "/")
		sj := strings.Count(routes[j].Path, "/")
		return si > sj // deeper pattern first
	})
}

// routePatternSpecificity mirrors patternSpecificity in
// internal/x402/matcher.go: whether the path is an exact match (no
// wildcards) and the length of its literal prefix before the first "*".
func routePatternSpecificity(routePath string) (exact bool, literalLen int) {
	i := strings.IndexByte(routePath, '*')
	if i < 0 {
		return true, len(routePath)
	}
	return false, i
}

// openAPIRelPathForRoute converts a route-table path into an OpenAPI paths
// key relative to the offer root: the catch-all "/*" is the offer root
// itself (""), a trailing wildcard collapses to its literal prefix (the
// operation description notes the sub-path coverage), exact paths pass
// through verbatim.
func openAPIRelPathForRoute(routePath string) string {
	if routePath == "" || routePath == "/" || routePath == "/*" {
		return ""
	}
	return strings.TrimSuffix(routePath, "/*")
}

// openAPIRouteOperationID derives a unique, stable operation id for one
// route-table operation: the offer's base id plus method and sanitized path.
func openAPIRouteOperationID(offer *monetizeapi.ServiceOffer, method, routePath string) string {
	suffix := strings.Trim(strings.TrimSuffix(routePath, "/*"), "/")
	suffix = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, suffix)
	suffix = strings.Trim(suffix, "-")
	if suffix == "" {
		suffix = "root"
	}
	return openAPIOperationID(offer) + "-" + strings.ToLower(method) + "-" + suffix
}

// routePaymentInfoExtension is offerPaymentInfoExtension with the
// route-table price semantics applied: a per-route price override replaces
// the primary option's price and collapses the route to single-payment
// (mirrors routeRulesFromOffer in the verifier's route source — the two
// MUST agree or discovery advertises a price the gate doesn't charge).
func routePaymentInfoExtension(offer *monetizeapi.ServiceOffer, rt monetizeapi.ServiceOfferRoute) map[string]any {
	if !rt.HasPriceOverride() {
		return offerPaymentInfoExtension(offer)
	}
	p := offer.EffectivePayments()[0]
	p.Price = rt.Price
	return map[string]any{
		"price":     paymentInfoPrice(p),
		"protocols": []any{map[string]any{"x402": map[string]any{}}},
		"accepts":   []any{paymentInfoAccept(p)},
	}
}

type openAPIOperationOptions struct {
	summary         string
	description     string
	requestBody     map[string]any
	successResponse map[string]any
}

// openAPIOperation builds one OperationObject. It is responsible for tags,
// the operation ID, the request body, the 200 response, the shared 402
// response reference, the x-payment-info extension, and the explicitly
// empty per-operation security block.
func openAPIOperation(offer *monetizeapi.ServiceOffer, opts openAPIOperationOptions) map[string]any {
	op := map[string]any{
		"summary":     opts.summary,
		"description": opts.description,
		"operationId": openAPIOperationID(offer),
		"tags":        operationTagsForOffer(offer),
		"responses": map[string]any{
			"200": opts.successResponse,
			"402": map[string]any{"$ref": "#/components/responses/PaymentRequired"},
		},
		// Explicitly empty: there is no HTTP auth scheme. Payment runs at
		// the x402 paywall, declared via x-payment-info. Discovery indexers
		// read [x-payment-info present + security: []] as a paid route;
		// runtime 402 behaviour stays authoritative over this metadata.
		"security":       []any{},
		"x-payment-info": offerPaymentInfoExtension(offer),
	}
	if opts.requestBody != nil {
		op["requestBody"] = opts.requestBody
	}
	return op
}

func openAPIJSONRequestBody(schemaRef, description string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": true,
	}
	if schemaRef != "" {
		schema = map[string]any{"$ref": schemaRef}
	}
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema":      schema,
				"description": description,
			},
		},
	}
}

func openAPIJSONSuccessResponse(schemaRef, description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": schemaRef},
			},
		},
	}
}

func openAPIGenericSuccessResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
		},
	}
}

// offerPaymentInfoExtension emits the `x-payment-info` OpenAPI extension —
// the de-facto convention discovery indexers (x402scan/agentcash) use to
// classify an operation as paid and display its price. There is no official
// x402↔OpenAPI binding; the shape follows https://www.x402scan.com/discovery/spec:
//
//	{ "price": {"mode": "fixed", "currency": "USD", "amount": "0.001"},
//	  "protocols": [{"x402": {}}] }
//
// USDC-settled offers advertise ISO-4217 "USD" (1:1). Other assets advertise
// their token symbol; indexers that require ISO-4217 fall back to
// presence-only classification (still "paid"), which is the honest outcome
// for tokens with no USD quote. perMTok prices are converted to the same
// per-request approximation the verifier enforces on the 402 wire, so this
// metadata never promises a cheaper call than the runtime charges.
func offerPaymentInfoExtension(offer *monetizeapi.ServiceOffer) map[string]any {
	payments := offer.EffectivePayments()

	info := map[string]any{
		// `price` stays the PRIMARY option (payments[0]) for single-price
		// indexers that read only this field.
		"price":     paymentInfoPrice(payments[0]),
		"protocols": []any{map[string]any{"x402": map[string]any{}}},
	}

	// Advertise every accepted option (single-payment offers included) with
	// full signing metadata: an OpenAPI-only client should be able to
	// construct a valid X-PAYMENT from this document alone, without a second
	// fetch of /api/services.json or a probe round-trip.
	accepts := make([]any, 0, len(payments))
	for i := range payments {
		accepts = append(accepts, paymentInfoAccept(payments[i]))
	}
	info["accepts"] = accepts

	return info
}

// paymentInfoAccept renders one payment option with everything a signer
// needs: {mode,currency,amount} (x402scan price shape) plus the CAIP-2
// network, recipient, atomic amount, and asset metadata including the
// EIP-712 signing domain. Mirrors the /api/services.json payments[] entries
// so OpenAPI-only consumers aren't second-class.
func paymentInfoAccept(p monetizeapi.ServiceOfferPayment) map[string]any {
	entry := paymentInfoPrice(p)
	if net := strings.TrimSpace(p.Network); net != "" {
		if caip, _ := caip2ForNetwork(net); caip != "" {
			entry["network"] = caip
		} else {
			entry["network"] = net
		}
	}
	if payTo := strings.TrimSpace(p.PayTo); payTo != "" {
		entry["payTo"] = payTo
	}
	asset := paymentAssetJSON(p)
	if asset == nil {
		return entry
	}
	assetMap := map[string]any{}
	if asset.Address != "" {
		assetMap["address"] = asset.Address
	}
	if asset.Symbol != "" {
		assetMap["symbol"] = asset.Symbol
	}
	if asset.Decimals != 0 {
		assetMap["decimals"] = asset.Decimals
	}
	if asset.TransferMethod != "" {
		assetMap["transferMethod"] = asset.TransferMethod
	}
	if asset.EIP712Domain != nil {
		assetMap["eip712Domain"] = map[string]any{
			"name":    asset.EIP712Domain.Name,
			"version": asset.EIP712Domain.Version,
		}
	}
	if len(assetMap) > 0 {
		entry["asset"] = assetMap
	}
	if raw, _ := paymentPriceRawAndUnit(p); raw != "" && catalogAssetHasKnownDecimals(asset) {
		entry["amountAtomicUnits"] = decimalToAtomicString(raw, int(asset.Decimals))
	}
	return entry
}

// paymentInfoPrice renders one payment option as an x402scan-style price
// object: {mode:"fixed", currency, amount}. USDC-settled options advertise
// ISO-4217 "USD" (1:1); other assets advertise their token symbol. perMTok
// prices collapse to the same per-request approximation the verifier enforces
// on the wire, so this metadata never undercuts the runtime charge.
func paymentInfoPrice(p monetizeapi.ServiceOfferPayment) map[string]any {
	price := map[string]any{"mode": "fixed"}
	if asset := paymentAssetJSON(p); asset != nil && asset.Symbol != "" {
		if strings.EqualFold(asset.Symbol, "USDC") {
			price["currency"] = "USD"
		} else {
			price["currency"] = asset.Symbol
		}
	}
	if amount, unit := paymentPriceRawAndUnit(p); amount != "" {
		if unit == "perMTok" {
			if approx, err := schemas.ApproximateRequestPriceFromPerMTok(amount); err == nil {
				amount = approx
			}
		}
		price["amount"] = amount
	}
	return price
}

// operationTagsForOffer combines the offer's coarse type tag with any
// registration.skills / registration.domains, so renderers can group
// related endpoints under a category.
func operationTagsForOffer(offer *monetizeapi.ServiceOffer) []any {
	seen := map[string]struct{}{}
	out := []any{}
	add := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return
		}
		if _, ok := seen[tag]; ok {
			return
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	add(fallbackOfferType(offer))
	for _, skill := range offer.Spec.Registration.Skills {
		add(skill)
	}
	for _, domain := range offer.Spec.Registration.Domains {
		add(domain)
	}
	return out
}

func openAPIOperationID(offer *monetizeapi.ServiceOffer) string {
	if offer == nil {
		return ""
	}
	if offer.Namespace == "" {
		return offer.Name
	}
	return offer.Namespace + "_" + offer.Name
}

func openAIChatCompletionsSummary(offer *monetizeapi.ServiceOffer) string {
	model := offer.Spec.Model.Name
	if model == "" && offer.Status.AgentResolution != nil {
		model = offer.Status.AgentResolution.Model
	}
	if model == "" {
		return "Chat completions — " + offer.Name
	}
	return "Chat completions — " + offer.Name + " (" + model + ")"
}

func offerDescription(offer *monetizeapi.ServiceOffer, fallback string) string {
	if desc := strings.TrimSpace(offer.Spec.Registration.Description); desc != "" {
		return desc
	}
	return fallback
}

// joinOpenAPIPath composes an offer's base path with a relative sub-path,
// collapsing duplicate slashes and ensuring exactly one leading slash. An
// empty `rel` returns the base unchanged.
func joinOpenAPIPath(base, rel string) string {
	base = "/" + strings.Trim(base, "/")
	if rel == "" || rel == "/" {
		return base
	}
	rel = "/" + strings.Trim(rel, "/")
	if base == "/" {
		return rel
	}
	return base + rel
}
