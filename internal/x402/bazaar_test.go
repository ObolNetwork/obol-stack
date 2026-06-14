package x402

import (
	"encoding/json"
	"testing"
)

// digAny descends through nested map[string]any by key, nil when absent.
func digAny(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

// TestBuildBazaarExtension pins the official bazaar {info, schema} shape
// (specs/extensions/bazaar.md) and the two consumer contracts layered on it:
//
//   - facilitators validate info against schema before cataloging, so the
//     concrete example in info must satisfy the schema's required fields;
//   - x402scan/agentcash mine schema.properties.input.properties.body as the
//     request schema and reject the whole 402 (SCHEMA_INPUT_MISSING) when
//     that path is absent.
func TestBuildBazaarExtension(t *testing.T) {
	for _, tc := range []struct {
		offerType string
		model     string
		wantModel string
	}{
		{"inference", "llama-3-70b", "llama-3-70b"},
		{"agent", "qwen3.5:9b", "qwen3.5:9b"},
		{"inference", "", "your-model-id"},
		{"http", "", ""},
		{"dataset", "training-v1", ""}, // dataset is a download → generic shape, no chat model
		{"", "", ""},                   // static config routes fall back to the generic shape
	} {
		ext := BuildBazaarExtension(tc.offerType, tc.model)

		// Round-trip through JSON so assertions run against the wire shape.
		raw, err := json.Marshal(ext)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.offerType, err)
		}
		var wire map[string]any
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.offerType, err)
		}

		input, _ := digAny(wire, "info", "input").(map[string]any)
		if input == nil {
			t.Fatalf("%s: info.input missing", tc.offerType)
		}
		// info.input must satisfy the body-method requireds the sibling
		// schema declares: type/method/bodyType/body.
		if input["type"] != "http" || input["method"] != "POST" || input["bodyType"] != "json" {
			t.Errorf("%s: info.input = %v, want http POST json", tc.offerType, input)
		}
		body, ok := input["body"].(map[string]any)
		if !ok {
			t.Fatalf("%s: info.input.body missing", tc.offerType)
		}
		if tc.wantModel != "" && body["model"] != tc.wantModel {
			t.Errorf("%s: body.model = %v, want %s", tc.offerType, body["model"], tc.wantModel)
		}

		if out, _ := digAny(wire, "info", "output").(map[string]any); out == nil || out["type"] != "json" {
			t.Errorf("%s: info.output = %v, want type json", tc.offerType, out)
		}

		// The x402scan-mined path: schema.properties.input.properties.body
		// must be a JSON Schema object.
		minedBody, _ := digAny(wire, "schema", "properties", "input", "properties", "body").(map[string]any)
		if minedBody == nil {
			t.Fatalf("%s: schema.properties.input.properties.body missing — x402scan rejects the 402 without it", tc.offerType)
		}
		if minedBody["type"] != "object" {
			t.Errorf("%s: mined body schema type = %v, want object", tc.offerType, minedBody["type"])
		}

		// Chat offers embed the real request contract, not a placeholder.
		if tc.offerType == "inference" || tc.offerType == "agent" {
			req, _ := minedBody["required"].([]any)
			if len(req) != 2 || req[0] != "model" || req[1] != "messages" {
				t.Errorf("%s: chat body schema required = %v, want [model messages]", tc.offerType, req)
			}
			// The concrete example must carry every schema-required field —
			// facilitators validate info against schema before cataloging.
			for _, field := range req {
				if _, ok := body[field.(string)]; !ok {
					t.Errorf("%s: info example body missing schema-required field %q", tc.offerType, field)
				}
			}
		}

		if schemaURI := digAny(wire, "schema", "$schema"); schemaURI != "https://json-schema.org/draft/2020-12/schema" {
			t.Errorf("%s: schema.$schema = %v, want Draft 2020-12 (spec requirement)", tc.offerType, schemaURI)
		}
	}
}

// TestWithBazaar pins that every paid route's 402 extensions gain the bazaar
// key without clobbering asset-level extensions (eip2612GasSponsoring).
func TestWithBazaar(t *testing.T) {
	got := WithBazaar(nil, "http", "")
	if _, ok := got["bazaar"]; !ok {
		t.Fatal("WithBazaar(nil, ...) must allocate and set bazaar")
	}

	base := map[string]any{"eip2612GasSponsoring": map[string]any{"info": map[string]any{}}}
	got = WithBazaar(base, "inference", "m")
	if _, ok := got["eip2612GasSponsoring"]; !ok {
		t.Error("WithBazaar dropped a sibling extension")
	}
	if _, ok := got["bazaar"]; !ok {
		t.Error("WithBazaar did not add bazaar alongside existing extensions")
	}
}
