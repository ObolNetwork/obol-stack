package schemas

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEffectiveRequestPrice_PerRequest(t *testing.T) {
	p := PriceTable{PerRequest: "0.001"}
	if got := p.EffectiveRequestPrice(); got != "0.001" {
		t.Errorf("EffectiveRequestPrice() = %q, want %q", got, "0.001")
	}
}

func TestEffectiveRequestPrice_PerMTok(t *testing.T) {
	p := PriceTable{PerMTok: "0.50"}
	if got := p.EffectiveRequestPrice(); got != "0.0005" {
		t.Errorf("EffectiveRequestPrice() = %q, want %q", got, "0.0005")
	}
}

func TestEffectiveRequestPrice_PerHour(t *testing.T) {
	p := PriceTable{PerHour: "2.00"}
	if got := p.EffectiveRequestPrice(); got != "2.00" {
		t.Errorf("EffectiveRequestPrice() = %q, want %q", got, "2.00")
	}
}

func TestEffectiveRequestPrice_Empty(t *testing.T) {
	p := PriceTable{}
	if got := p.EffectiveRequestPrice(); got != "0" {
		t.Errorf("EffectiveRequestPrice() = %q, want %q", got, "0")
	}
}

func TestEffectiveRequestPrice_PerRequestPrecedence(t *testing.T) {
	p := PriceTable{PerRequest: "0.001", PerMTok: "0.50"}
	if got := p.EffectiveRequestPrice(); got != "0.001" {
		t.Errorf("EffectiveRequestPrice() = %q, want %q (PerRequest should take precedence)", got, "0.001")
	}
}

func TestApproximateRequestPriceFromPerMTok(t *testing.T) {
	got, err := ApproximateRequestPriceFromPerMTok("1.25")
	if err != nil {
		t.Fatalf("ApproximateRequestPriceFromPerMTok() error = %v", err)
	}
	if got != "0.00125" {
		t.Errorf("ApproximateRequestPriceFromPerMTok() = %q, want %q", got, "0.00125")
	}
}

func TestApproximateRequestPriceFromPerMTok_Invalid(t *testing.T) {
	if _, err := ApproximateRequestPriceFromPerMTok("bad"); err == nil {
		t.Fatal("ApproximateRequestPriceFromPerMTok() error = nil, want non-nil")
	}
}

func TestPaymentTerms_JSONRoundTrip(t *testing.T) {
	original := PaymentTerms{
		Network:           "base-sepolia",
		PayTo:             "0x1234567890abcdef1234567890abcdef12345678",
		Scheme:            "exact",
		MaxTimeoutSeconds: 300,
		Price: PriceTable{
			PerRequest: "0.001",
			PerMTok:    "0.50",
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded PaymentTerms
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if decoded.Network != original.Network {
		t.Errorf("Network = %q, want %q", decoded.Network, original.Network)
	}
	if decoded.PayTo != original.PayTo {
		t.Errorf("PayTo = %q, want %q", decoded.PayTo, original.PayTo)
	}
	if decoded.Scheme != original.Scheme {
		t.Errorf("Scheme = %q, want %q", decoded.Scheme, original.Scheme)
	}
	if decoded.MaxTimeoutSeconds != original.MaxTimeoutSeconds {
		t.Errorf("MaxTimeoutSeconds = %d, want %d", decoded.MaxTimeoutSeconds, original.MaxTimeoutSeconds)
	}
	if decoded.Price.PerRequest != original.Price.PerRequest {
		t.Errorf("Price.PerRequest = %q, want %q", decoded.Price.PerRequest, original.Price.PerRequest)
	}
	if decoded.Price.PerMTok != original.Price.PerMTok {
		t.Errorf("Price.PerMTok = %q, want %q", decoded.Price.PerMTok, original.Price.PerMTok)
	}
}

func TestPaymentTerms_YAMLRoundTrip(t *testing.T) {
	original := PaymentTerms{
		Network:           "base-sepolia",
		PayTo:             "0x1234567890abcdef1234567890abcdef12345678",
		Scheme:            "exact",
		MaxTimeoutSeconds: 300,
		Price: PriceTable{
			PerRequest: "0.001",
			PerMTok:    "0.50",
		},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	var decoded PaymentTerms
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}

	if decoded.Network != original.Network {
		t.Errorf("Network = %q, want %q", decoded.Network, original.Network)
	}
	if decoded.PayTo != original.PayTo {
		t.Errorf("PayTo = %q, want %q", decoded.PayTo, original.PayTo)
	}
	if decoded.Scheme != original.Scheme {
		t.Errorf("Scheme = %q, want %q", decoded.Scheme, original.Scheme)
	}
	if decoded.MaxTimeoutSeconds != original.MaxTimeoutSeconds {
		t.Errorf("MaxTimeoutSeconds = %d, want %d", decoded.MaxTimeoutSeconds, original.MaxTimeoutSeconds)
	}
	if decoded.Price.PerRequest != original.Price.PerRequest {
		t.Errorf("Price.PerRequest = %q, want %q", decoded.Price.PerRequest, original.Price.PerRequest)
	}
	if decoded.Price.PerMTok != original.Price.PerMTok {
		t.Errorf("Price.PerMTok = %q, want %q", decoded.Price.PerMTok, original.Price.PerMTok)
	}
}

func TestPaymentTerms_JSONFieldNames(t *testing.T) {
	pt := PaymentTerms{
		Network:           "base-sepolia",
		PayTo:             "0xABC",
		Scheme:            "exact",
		MaxTimeoutSeconds: 300,
		Price: PriceTable{
			PerRequest: "0.001",
			PerMTok:    "0.50",
		},
	}

	data, err := json.Marshal(pt)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal to map failed: %v", err)
	}

	// Check top-level camelCase field names
	for _, expected := range []string{"payTo", "maxTimeoutSeconds", "network", "scheme", "price"} {
		if _, ok := raw[expected]; !ok {
			t.Errorf("expected JSON field %q not found in output", expected)
		}
	}
	// Check snake_case variants are NOT present
	for _, unexpected := range []string{"pay_to", "max_timeout_seconds", "per_request", "per_mtok"} {
		if _, ok := raw[unexpected]; ok {
			t.Errorf("unexpected snake_case field %q found in JSON output", unexpected)
		}
	}

	// Check nested price fields
	var priceRaw map[string]json.RawMessage
	if err := json.Unmarshal(raw["price"], &priceRaw); err != nil {
		t.Fatalf("json.Unmarshal price to map failed: %v", err)
	}
	for _, expected := range []string{"perRequest", "perMTok"} {
		if _, ok := priceRaw[expected]; !ok {
			t.Errorf("expected price field %q not found in JSON output", expected)
		}
	}
	for _, unexpected := range []string{"per_request", "per_mtok"} {
		if _, ok := priceRaw[unexpected]; ok {
			t.Errorf("unexpected snake_case field %q found in price JSON output", unexpected)
		}
	}
}

func TestPriceTable_OmitEmpty(t *testing.T) {
	p := PriceTable{PerRequest: "0.001"}

	// JSON: only perRequest should be present
	jsonData, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var jsonMap map[string]json.RawMessage
	if err := json.Unmarshal(jsonData, &jsonMap); err != nil {
		t.Fatalf("json.Unmarshal to map failed: %v", err)
	}

	if _, ok := jsonMap["perRequest"]; !ok {
		t.Error("expected perRequest in JSON output")
	}
	for _, field := range []string{"perMTok", "perHour", "perEpoch"} {
		if _, ok := jsonMap[field]; ok {
			t.Errorf("field %q should be omitted from JSON when empty", field)
		}
	}

	// YAML: only perRequest should be present
	yamlData, err := yaml.Marshal(p)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}

	var yamlMap map[string]interface{}
	if err := yaml.Unmarshal(yamlData, &yamlMap); err != nil {
		t.Fatalf("yaml.Unmarshal to map failed: %v", err)
	}

	if _, ok := yamlMap["perRequest"]; !ok {
		t.Error("expected perRequest in YAML output")
	}
	for _, field := range []string{"perMTok", "perHour", "perEpoch"} {
		if _, ok := yamlMap[field]; ok {
			t.Errorf("field %q should be omitted from YAML when empty", field)
		}
	}
}
