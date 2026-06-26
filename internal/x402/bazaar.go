package x402

// The x402 v2 `bazaar` discovery extension (specs/extensions/bazaar.md in
// x402-foundation/x402). Every paid route advertises {info, schema} in the
// 402 response's `extensions.bazaar` so facilitators and indexers
// (CDP Bazaar, x402scan) can catalog the endpoint without out-of-band
// registration:
//
//   - `info` carries a concrete invocation example (HTTP method, bodyType,
//     request body, response example).
//   - `schema` is a JSON Schema (Draft 2020-12) that validates `info`;
//     indexers also mine `schema.properties.input.properties.body` as the
//     operation's request schema, so the body subschema must be the real
//     request contract, not a placeholder.
//
// Facilitators MUST validate info against schema before cataloging — keep
// the two halves in lockstep when editing. TestBuildBazaarExtension pins
// that invariant structurally.

// Service metadata advertised on every 402 `resource` block (bazaar spec,
// "Service Metadata on `resource`"). Facilitators apply soft-drop rules:
// ServiceName must be printable ASCII ≤32 chars; IconURL must be an absolute
// https URL (no IP literals, no localhost, ≤2048 chars).
const (
	// ResourceServiceName names the hosting service in catalog UIs.
	ResourceServiceName = "Obol Stack"

	// ResourceIconURL is the catalog icon — the Obol infinity-loop brand
	// mark (982px, dark circle) served from obol.org (mirrored from the
	// ObolNetwork/obol-org-site repo root). Piggybacked on the main site
	// because tunnel hostnames rotate and facilitators reject non-https/IP
	// icon hosts. Stack-specific icon drafts live in assets/stack/ if a
	// dedicated sub-brand icon is wanted later.
	ResourceIconURL = "https://obol.org/ObolIcon.png"
)

// WithBazaar merges the bazaar discovery extension for a route into
// extensions, allocating the map when nil. offerType is the originating
// ServiceOffer.spec.type ("" for static config routes, which get the
// generic HTTP shape); model seeds the chat-completions example for
// inference/agent offers.
func WithBazaar(extensions map[string]any, offerType, model string) map[string]any {
	if extensions == nil {
		extensions = map[string]any{}
	}
	extensions["bazaar"] = BuildBazaarExtension(offerType, model)
	return extensions
}

// BuildBazaarExtension returns the `{info, schema}` value for
// `extensions.bazaar` appropriate to the offer type. inference and agent
// offers share the OpenAI chat-completions wire format (same rationale as
// openAPIPathsForOffer in internal/serviceoffercontroller); everything else
// gets the generic operator-defined JSON shape.
func BuildBazaarExtension(offerType, model string) map[string]any {
	switch normalizeOfferType(offerType) {
	case "inference":
		// The buyer selects the model (paid/<remote-model>), so the real id
		// is buyer-facing and correct to advertise in the example.
		return bazaarChatCompletions(model)
	case "agent":
		// An agent runs its own model and ignores the request `model` field,
		// so the model id is an internal detail, not buyer-facing. Seed the
		// chat example with the neutral placeholder rather than the real id.
		return bazaarChatCompletions("")
	default:
		return bazaarGenericJSON()
	}
}

func bazaarChatCompletions(model string) map[string]any {
	if model == "" {
		// The example must still be a plausible request; the buyer reads
		// the real model id from accepts[].extra or /api/services.json.
		model = "your-model-id"
	}
	return map[string]any{
		"info": map[string]any{
			"input": map[string]any{
				"type":     "http",
				"method":   "POST",
				"bodyType": "json",
				"body": map[string]any{
					"model": model,
					"messages": []any{
						map[string]any{"role": "user", "content": "Hello"},
					},
				},
			},
			"output": map[string]any{
				"type": "json",
				"example": map[string]any{
					"id":     "chatcmpl-example",
					"object": "chat.completion",
					"model":  model,
					"choices": []any{
						map[string]any{
							"index": 0,
							"message": map[string]any{
								"role":    "assistant",
								"content": "Hello! How can I help you today?",
							},
							"finish_reason": "stop",
						},
					},
				},
			},
		},
		"schema": bazaarBodySchema(chatCompletionsBodySchema()),
	}
}

func bazaarGenericJSON() map[string]any {
	return map[string]any{
		"info": map[string]any{
			"input": map[string]any{
				"type":     "http",
				"method":   "POST",
				"bodyType": "json",
				"body":     map[string]any{},
			},
			"output": map[string]any{
				"type":    "json",
				"example": map[string]any{},
			},
		},
		"schema": bazaarBodySchema(map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"description": "Operator-defined JSON payload. Probe the upstream " +
				"or consult the operator's /openapi.json for the concrete shape.",
		}),
	}
}

// bazaarBodySchema wraps a request-body JSON Schema in the bazaar envelope
// schema for an HTTP body-method (POST) endpoint. The envelope mirrors the
// POST example in specs/extensions/bazaar.md: input requires
// type/method/bodyType/body, output requires type.
func bazaarBodySchema(body map[string]any) map[string]any {
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":     map[string]any{"type": "string", "const": "http"},
					"method":   map[string]any{"type": "string", "enum": []any{"POST", "PUT", "PATCH"}},
					"bodyType": map[string]any{"type": "string", "enum": []any{"json", "form-data", "text"}},
					"body":     body,
					"queryParams": map[string]any{
						"type":                 "object",
						"additionalProperties": map[string]any{"type": "string"},
					},
					"headers": map[string]any{
						"type":                 "object",
						"additionalProperties": map[string]any{"type": "string"},
					},
				},
				"required":             []any{"type", "method", "bodyType", "body"},
				"additionalProperties": false,
			},
			"output": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":    map[string]any{"type": "string"},
					"example": map[string]any{"type": "object"},
				},
				"required": []any{"type"},
			},
		},
		"required": []any{"input"},
	}
}

// chatCompletionsBodySchema is the self-contained (no $ref) OpenAI
// chat-completions request schema embedded in the bazaar envelope.
// Deliberately permissive — only model and messages are required, and
// additionalProperties stays open so providers that extend the schema
// (vLLM, Ollama, paid/* via litellm) still validate. Mirrors the OpenAPI
// component in internal/serviceoffercontroller/openapi_components.go; the
// two serve different documents and stay hand-synced.
func chatCompletionsBodySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             []any{"model", "messages"},
		"additionalProperties": true,
		"properties": map[string]any{
			"model": map[string]any{
				"type":        "string",
				"description": "Model identifier advertised in the 402 accepts[].extra or /api/services.json.",
			},
			"messages": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []any{"role", "content"},
					"properties": map[string]any{
						"role": map[string]any{
							"type": "string",
							"enum": []any{"system", "user", "assistant", "tool"},
						},
						"content": map[string]any{
							"oneOf": []any{
								map[string]any{"type": "string"},
								map[string]any{"type": "array"},
								map[string]any{"type": "null"},
							},
						},
					},
					"additionalProperties": true,
				},
			},
			"stream":      map[string]any{"type": "boolean"},
			"max_tokens":  map[string]any{"type": "integer", "minimum": 1},
			"temperature": map[string]any{"type": "number", "minimum": 0, "maximum": 2},
		},
	}
}
