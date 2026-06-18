package x402

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	x402types "github.com/x402-foundation/x402/go/types"
)

func TestMergeSkillExtras_Noop_NonSkillRule(t *testing.T) {
	req := x402types.PaymentRequirements{Extra: map[string]any{"name": "USDC"}}
	rule := &RouteRule{}

	mergeSkillExtras(&req, rule)

	if _, ok := req.Extra["skill"]; ok {
		t.Error("non-skill rule must not add extra.skill")
	}
	if got := req.Extra["name"]; got != "USDC" {
		t.Errorf("non-skill merge clobbered existing extra.name: %v", got)
	}
}

func TestMergeSkillExtras_AddsSkillBlock(t *testing.T) {
	req := x402types.PaymentRequirements{Extra: map[string]any{}}
	rule := &RouteRule{
		SkillName:    "buy-x402",
		SkillVersion: "0.1.0",
		SkillSHA256:  strings.Repeat("ab", 32),
	}

	mergeSkillExtras(&req, rule)

	skill, ok := req.Extra["skill"].(map[string]any)
	if !ok {
		t.Fatalf("extra.skill wrong type: %T", req.Extra["skill"])
	}
	if skill["name"] != "buy-x402" {
		t.Errorf("skill.name = %v, want buy-x402", skill["name"])
	}
	if skill["version"] != "0.1.0" {
		t.Errorf("skill.version = %v, want 0.1.0", skill["version"])
	}
	if skill["sha256"] != strings.Repeat("ab", 32) {
		t.Errorf("skill.sha256 = %v", skill["sha256"])
	}
}

func TestMergeSkillExtras_InitialisesNilExtra(t *testing.T) {
	req := x402types.PaymentRequirements{}
	rule := &RouteRule{SkillName: "buy-x402"}

	mergeSkillExtras(&req, rule)

	if req.Extra == nil {
		t.Fatal("Extra not initialised")
	}
	skill, ok := req.Extra["skill"].(map[string]any)
	if !ok || skill["name"] != "buy-x402" {
		t.Errorf("extra.skill missing or malformed: %+v", req.Extra)
	}
	if _, ok := skill["version"]; ok {
		t.Error("empty version must be omitted from extra.skill")
	}
	if _, ok := skill["sha256"]; ok {
		t.Error("empty sha256 must be omitted from extra.skill")
	}
}

// TestVerifier_402_SkillExtra exercises the full 402 path for a type=skill
// route: a paymentless probe must surface accepts[].extra.skill =
// {name, version, sha256} in the JSON body (the wire contract buyers use
// to verify the artifact before paying), while a non-skill route must not
// gain the key. Modeled on the agent-extras coverage.
func TestVerifier_402_SkillExtra(t *testing.T) {
	fac := newMockFacilitator(t, mockFacilitatorOpts{})
	sha := strings.Repeat("0a", 32)
	v := newTestVerifier(t, fac.URL, []RouteRule{
		{
			Pattern:      "/services/buy-x402/*",
			Price:        "0.01",
			OfferType:    "skill",
			SkillName:    "buy-x402",
			SkillVersion: "0.1.0",
			SkillSHA256:  sha,
		},
		{
			Pattern: "/services/plain-http/*",
			Price:   "0.01",
		},
	})

	probe402 := func(t *testing.T, uri string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/verify", nil)
		req.Header.Set("X-Forwarded-Uri", uri)
		req.Header.Set("X-Forwarded-Host", "obol.stack")
		w := httptest.NewRecorder()
		v.HandleVerify(w, req)
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("status = %d, want 402", w.Code)
		}
		body, _ := io.ReadAll(w.Body)
		var parsed struct {
			Accepts []map[string]any `json:"accepts"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("402 body is not JSON: %v\n%s", err, body)
		}
		if len(parsed.Accepts) != 1 {
			t.Fatalf("accepts = %d entries, want 1", len(parsed.Accepts))
		}
		extra, _ := parsed.Accepts[0]["extra"].(map[string]any)
		return extra
	}

	t.Run("skill route advertises extra.skill", func(t *testing.T) {
		extra := probe402(t, "/services/buy-x402/bundle.tar.gz")
		skill, ok := extra["skill"].(map[string]any)
		if !ok {
			t.Fatalf("extra.skill missing or wrong shape: %+v", extra)
		}
		if skill["name"] != "buy-x402" || skill["version"] != "0.1.0" || skill["sha256"] != sha {
			t.Errorf("extra.skill = %+v, want name/version/sha256 populated", skill)
		}
	})

	t.Run("non-skill route emits no extra.skill", func(t *testing.T) {
		extra := probe402(t, "/services/plain-http/anything")
		if _, ok := extra["skill"]; ok {
			t.Errorf("non-skill route must not advertise extra.skill: %+v", extra)
		}
	})
}
