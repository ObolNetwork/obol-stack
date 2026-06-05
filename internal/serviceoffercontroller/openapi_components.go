package serviceoffercontroller

// JSON Schema fragments used by the aggregate OpenAPI document we publish at
// /openapi.json. Built as map[string]any so they compose naturally with the
// rest of the OpenAPI builder (which also speaks unstructured JSON).
//
// Two principles:
//
//  1. x402 components mirror the canonical Coinbase types/v2 wire format
//     (github.com/coinbase/x402/go/types). Field names, optionality, and
//     X402Version=2 must stay in sync with that upstream — internal/x402
//     re-exports the same structs and lockstep is enforced by 402 smoke
//     tests, not by a generator. Update both together when the spec moves.
//
//  2. OpenAI Chat Completions schemas are deliberately permissive — only
//     `model` and `messages` are marked required, and `additionalProperties`
//     is left open so providers that extend the schema (vLLM, Ollama,
//     paid/* via litellm) still validate.

// x402PaymentRequiredSchema describes the JSON body of a 402 response from
// any obol-stack-managed paid endpoint. Mirrors x402types.PaymentRequired.
func x402PaymentRequiredSchema() map[string]any {
	return map[string]any{
		"type":  "object",
		"title": "X402PaymentRequired",
		"description": "x402 v2 payment-required response body. The client must read `accepts[]`, " +
			"choose one PaymentRequirements entry, sign a payment payload, and retry with the " +
			"base64-encoded payload in the `X-PAYMENT` request header. See https://www.x402.org.",
		"required": []any{"x402Version", "accepts"},
		"properties": map[string]any{
			"x402Version": map[string]any{
				"type":     "integer",
				"const":    2,
				"x-x402-protocol-version": 2,
				"description": "x402 protocol version. Obol Stack currently emits v2 responses.",
			},
			"error": map[string]any{
				"type":        "string",
				"description": "Human-readable summary of why payment is required.",
			},
			"resource": map[string]any{
				"$ref": "#/components/schemas/X402Resource",
			},
			"accepts": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/X402PaymentRequirements"},
				"description": "One or more payment configurations the server will accept. Client " +
					"picks the entry matching its wallet's chain and asset.",
			},
			"extensions": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Optional per-asset hints (e.g. eip2612GasSponsoring metadata).",
			},
		},
	}
}

// x402PaymentRequirementsSchema describes one entry in PaymentRequired.accepts.
// Mirrors x402types.PaymentRequirements (v2).
func x402PaymentRequirementsSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"title":    "X402PaymentRequirements",
		"required": []any{"scheme", "network", "asset", "amount", "payTo", "maxTimeoutSeconds"},
		"properties": map[string]any{
			"scheme": map[string]any{
				"type":        "string",
				"enum":        []any{"exact"},
				"description": "x402 settlement scheme. Obol Stack uses `exact` (single signed authorization per request).",
			},
			"network": map[string]any{
				"type":        "string",
				"description": "CAIP-2 chain identifier (e.g. `eip155:8453` for Base mainnet).",
				"examples":    []any{"eip155:8453", "eip155:84532"},
			},
			"asset": map[string]any{
				"type":        "string",
				"description": "Settlement token contract address (e.g. USDC or OBOL).",
				"pattern":     "^0x[0-9a-fA-F]{40}$",
			},
			"amount": map[string]any{
				"type":        "string",
				"description": "Price in atomic units of `asset` (e.g. micro-USDC for a 6-decimal token).",
				"examples":    []any{"1000"},
			},
			"payTo": map[string]any{
				"type":        "string",
				"description": "Recipient wallet address.",
				"pattern":     "^0x[0-9a-fA-F]{40}$",
			},
			"maxTimeoutSeconds": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Maximum age (seconds) of the signed payment authorization the server will accept.",
			},
			"extra": map[string]any{
				"type":                 "object",
				"additionalProperties": true,
				"description":          "Per-asset metadata, e.g. EIP-712 domain name/version for ERC-3009 signing.",
			},
		},
	}
}

// x402ResourceSchema mirrors x402types.ResourceInfo.
func x402ResourceSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"title":    "X402Resource",
		"required": []any{"url"},
		"properties": map[string]any{
			"url":         map[string]any{"type": "string", "format": "uri"},
			"description": map[string]any{"type": "string"},
			"mimeType":    map[string]any{"type": "string"},
		},
	}
}

// openAIChatCompletionsRequestSchema is a deliberately permissive OpenAI Chat
// Completions request schema. Only `model` and `messages` are required.
func openAIChatCompletionsRequestSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"title":                "OpenAIChatCompletionsRequest",
		"required":             []any{"model", "messages"},
		"additionalProperties": true,
		"properties": map[string]any{
			"model": map[string]any{
				"type":        "string",
				"description": "Model identifier. Use the model name advertised in the operator's `/api/services.json` entry.",
			},
			"messages": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/OpenAIChatMessage"},
			},
			"temperature": map[string]any{"type": "number", "minimum": 0, "maximum": 2},
			"top_p":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"n":           map[string]any{"type": "integer", "minimum": 1},
			"stream":      map[string]any{"type": "boolean"},
			"max_tokens":  map[string]any{"type": "integer", "minimum": 1},
			"stop": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			"presence_penalty":  map[string]any{"type": "number", "minimum": -2, "maximum": 2},
			"frequency_penalty": map[string]any{"type": "number", "minimum": -2, "maximum": 2},
			"user":              map[string]any{"type": "string"},
		},
	}
}

func openAIChatMessageSchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"title":    "OpenAIChatMessage",
		"required": []any{"role", "content"},
		"properties": map[string]any{
			"role": map[string]any{
				"type": "string",
				"enum": []any{"system", "user", "assistant", "tool"},
			},
			"content": map[string]any{
				"description": "Message text. May be a string or a structured content array depending on the model.",
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{"type": "array"},
					map[string]any{"type": "null"},
				},
			},
			"name":         map[string]any{"type": "string"},
			"tool_call_id": map[string]any{"type": "string"},
		},
		"additionalProperties": true,
	}
}

func openAIChatCompletionsResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"title":                "OpenAIChatCompletionsResponse",
		"additionalProperties": true,
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"object":  map[string]any{"type": "string", "examples": []any{"chat.completion"}},
			"created": map[string]any{"type": "integer"},
			"model":   map[string]any{"type": "string"},
			"choices": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/components/schemas/OpenAIChatChoice"},
			},
			"usage": map[string]any{"$ref": "#/components/schemas/OpenAIChatUsage"},
		},
	}
}

func openAIChatChoiceSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"title":                "OpenAIChatChoice",
		"additionalProperties": true,
		"properties": map[string]any{
			"index":         map[string]any{"type": "integer"},
			"message":       map[string]any{"$ref": "#/components/schemas/OpenAIChatMessage"},
			"finish_reason": map[string]any{"type": "string"},
		},
	}
}

func openAIChatUsageSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"title":                "OpenAIChatUsage",
		"additionalProperties": true,
		"properties": map[string]any{
			"prompt_tokens":     map[string]any{"type": "integer"},
			"completion_tokens": map[string]any{"type": "integer"},
			"total_tokens":      map[string]any{"type": "integer"},
		},
	}
}

// openAPIComponentSchemas returns the components.schemas block for the
// aggregate OpenAPI document. Keep alphabetized so the rendered spec stays
// diff-stable across reconciles.
func openAPIComponentSchemas() map[string]any {
	return map[string]any{
		"OpenAIChatChoice":              openAIChatChoiceSchema(),
		"OpenAIChatCompletionsRequest":  openAIChatCompletionsRequestSchema(),
		"OpenAIChatCompletionsResponse": openAIChatCompletionsResponseSchema(),
		"OpenAIChatMessage":             openAIChatMessageSchema(),
		"OpenAIChatUsage":               openAIChatUsageSchema(),
		"X402PaymentRequired":           x402PaymentRequiredSchema(),
		"X402PaymentRequirements":       x402PaymentRequirementsSchema(),
		"X402Resource":                  x402ResourceSchema(),
	}
}

// openAPIComponentResponses returns the components.responses block — currently
// just the shared 402 PaymentRequired response that every paid operation
// references.
func openAPIComponentResponses() map[string]any {
	return map[string]any{
		"PaymentRequired": map[string]any{
			"description": "x402 v2 payment required. The response body matches `X402PaymentRequired`. " +
				"Retry the same request with a base64-encoded x402 payment payload in the `X-PAYMENT` " +
				"header. See https://www.x402.org for the wire format.",
			"headers": map[string]any{
				"X-PAYMENT-REQUIRED": map[string]any{
					"description": "Indicates the response is an x402 challenge. Body carries the requirements.",
					"schema":      map[string]any{"type": "string"},
				},
			},
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/X402PaymentRequired"},
				},
			},
		},
	}
}

// openAPISecuritySchemes returns the security scheme for x402 payment.
func openAPISecuritySchemes() map[string]any {
	return map[string]any{
		"x402Payment": map[string]any{
			"type": "apiKey",
			"in":   "header",
			"name": "X-PAYMENT",
			"description": "Base64-encoded x402 v2 payment payload. Sign a `PaymentPayload` matching one " +
				"of the entries in the prior 402 response's `accepts[]` array. See https://www.x402.org.",
		},
	}
}
