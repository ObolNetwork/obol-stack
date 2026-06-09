package serviceoffercontroller

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/x402"
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
func buildOpenAPIDocument(offers []*monetizeapi.ServiceOffer, tunnelURL string) string {
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

	doc := map[string]any{
		"openapi": openAPISpecVersion,
		"info":    buildOpenAPIInfo(len(ready)),
		"servers": buildOpenAPIServers(tunnelURL),
		"tags":    buildOpenAPITags(ready),
		"paths":   buildOpenAPIPaths(ready),
		"components": map[string]any{
			"schemas":         openAPIComponentSchemas(),
			"responses":       openAPIComponentResponses(),
			"securitySchemes": openAPISecuritySchemes(),
		},
		"security": []any{
			map[string]any{"x402Payment": []any{}},
		},
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

func buildOpenAPIInfo(readyCount int) map[string]any {
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
	return map[string]any{
		"title":       "Obol Stack — paid services",
		"version":     "1",
		"description": description,
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

// openAPIPathsForOffer returns the set of {path → pathItem} entries this
// offer contributes. Phase 1 uses pure type-based heuristics:
//
//   - inference, agent → POST /v1/chat/completions with the OpenAI shapes.
//     User feedback: agent uses the same OpenAI chat completions wire
//     format as inference, so they share an emission path.
//   - fine-tuning    → POST <path> with multipart upload, generic 200.
//   - http (default) → POST <path> with application/json body, generic 200.
//
// Every operation references the shared `PaymentRequired` 402 response and
// carries an `x-x402-payment` extension that snapshots the per-operation
// payment block. Phase 2 will let operators override these emissions with
// explicit per-offer fragments.
func openAPIPathsForOffer(offer *monetizeapi.ServiceOffer) map[string]map[string]any {
	if offer == nil {
		return nil
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
		return map[string]map[string]any{
			"": {
				"post": openAPIOperation(offer, openAPIOperationOptions{
					summary:     "Invoke " + offer.Name,
					description: offerDescription(offer, "x402 payment-gated HTTP service."),
					requestBody: openAPIJSONRequestBody(
						"",
						"Operator-defined JSON payload. Shape is not specified in phase 1.",
					),
					successResponse: openAPIGenericSuccessResponse("Upstream response (shape is operator-defined)."),
				}),
			},
		}
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
// response reference, the x-x402-payment extension, and the per-operation
// security requirement.
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
		"security": []any{
			map[string]any{"x402Payment": []any{}},
		},
		"x-x402-payment": offerPaymentExtension(offer),
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

// offerPaymentExtension serializes the per-operation x402 payment block.
// Mirrors PaymentRequirements (v2) so a buyer can locate the same fields
// they would receive in a live 402 response, including the resolved CAIP-2
// network and the price in both decimal and atomic units when known.
func offerPaymentExtension(offer *monetizeapi.ServiceOffer) map[string]any {
	scheme := offer.Spec.Payment.Scheme
	if scheme == "" {
		scheme = "exact"
	}
	caip2, _ := caip2ForNetwork(offer.Spec.Payment.Network)
	network := caip2
	if network == "" {
		network = offer.Spec.Payment.Network
	}

	ext := map[string]any{
		"scheme":  scheme,
		"network": network,
		"payTo":   offer.Spec.Payment.PayTo,
	}
	if offer.Spec.Payment.MaxTimeoutSeconds > 0 {
		// Advertise the value the 402 wire will enforce — the verifier clamps
		// over-cap spec values (x402.ClampMaxTimeoutSeconds), so the catalog
		// must not promise a larger settle window than buyers actually get.
		ext["maxTimeoutSeconds"] = x402.ClampMaxTimeoutSeconds(offer.Spec.Payment.MaxTimeoutSeconds)
	}

	if asset := offerAssetJSON(offer); asset != nil {
		assetBlock := map[string]any{}
		if asset.Address != "" {
			assetBlock["address"] = asset.Address
		}
		if asset.Symbol != "" {
			assetBlock["symbol"] = asset.Symbol
		}
		if asset.Decimals > 0 {
			assetBlock["decimals"] = asset.Decimals
		}
		if asset.TransferMethod != "" {
			assetBlock["transferMethod"] = asset.TransferMethod
		}
		if len(assetBlock) > 0 {
			ext["asset"] = assetBlock
		}
	}

	price := map[string]any{}
	raw, unit := offerPriceRawAndUnit(offer)
	if raw != "" && unit != "" {
		price[unit] = raw
		if asset := offerAssetJSON(offer); asset != nil && asset.Decimals > 0 {
			if atomic := decimalToAtomicString(raw, int(asset.Decimals)); atomic != "" {
				price[unit+"Atomic"] = atomic
			}
		}
	}
	if len(price) > 0 {
		ext["price"] = price
	}

	return ext
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
