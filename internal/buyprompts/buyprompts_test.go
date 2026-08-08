package buyprompts

import (
	"strings"
	"testing"
)

// TestBuild_ChatOffersTeachCanonicalPath pins the invariant that caused the
// original cross-surface drift: every instruction for a chat-shaped offer
// (agent, inference) must name the canonical /v1/chat/completions call path.
// A prompt that teaches the bare service base sends a paying buyer to a 404.
func TestBuild_ChatOffersTeachCanonicalPath(t *testing.T) {
	for _, typ := range []string{"agent", "inference"} {
		block := Build(Input{
			Type:    typ,
			URL:     "https://seller.example.com/services/demo",
			SiteURL: "https://seller.example.com",
			Model:   "qwen3.5:9b",
		})

		if block.CallShape.Path != "/v1/chat/completions" {
			t.Errorf("%s: callShape.path = %q, want /v1/chat/completions", typ, block.CallShape.Path)
		}
		if block.CallShape.Method != "POST" || block.CallShape.BodyKind != "openai-chat" || !block.CallShape.Streaming {
			t.Errorf("%s: callShape = %+v, want POST/openai-chat/streaming", typ, block.CallShape)
		}
		if !strings.Contains(block.Prompts[PromptGenericLLM], "/v1/chat/completions") {
			t.Errorf("%s: generic-llm prompt must name the chat-completions path:\n%s", typ, block.Prompts[PromptGenericLLM])
		}
		if !strings.Contains(block.Example, "POST https://seller.example.com/services/demo/v1/chat/completions") {
			t.Errorf("%s: example must POST the canonical path:\n%s", typ, block.Example)
		}
		if !strings.Contains(block.Example, "X-PAYMENT") {
			t.Errorf("%s: example must show the X-PAYMENT header", typ)
		}
	}
}

// TestBuild_AllTypesCarryEveryPromptKey ensures every surface can rely on
// the standard prompt keys existing for every offer type.
func TestBuild_AllTypesCarryEveryPromptKey(t *testing.T) {
	for _, typ := range []string{"agent", "inference", "http", "fine-tuning", "", "bogus"} {
		block := Build(Input{Type: typ, URL: "https://s.example/services/x"})
		for _, key := range []string{PromptObolAgent, PromptGenericLLM, PromptCLI, PromptAgentCash, PromptPoncho, PromptBankr} {
			if strings.TrimSpace(block.Prompts[key]) == "" {
				t.Errorf("type %q: missing prompt %q", typ, key)
			}
		}
	}
}

// TestBuild_UnknownTypeGetsHTTPSemantics pins the safe default: unknown
// types must get single-shot pay instructions, never a pre-authorization
// flow.
func TestBuild_UnknownTypeGetsHTTPSemantics(t *testing.T) {
	block := Build(Input{Type: "mystery", URL: "https://s.example/services/x"})
	if block.CallShape.BodyKind != "none" || block.CallShape.Method != "GET" {
		t.Errorf("unknown type callShape = %+v, want GET/none", block.CallShape)
	}
	if !strings.Contains(block.Prompts[PromptObolAgent], "`pay`") {
		t.Errorf("unknown type obol-agent prompt should teach single-shot pay:\n%s", block.Prompts[PromptObolAgent])
	}
}

// TestBuild_BankrPromptTeachesManualWalletSign pins type-specific Bankr copy:
// agent/inference forbid chat/Apps auto-pay and teach `bankr wallet sign`;
// http prefers Bankr chat auto-pay. Network fields come from accepts[] — never
// a hardcoded chain id / explorer.
func TestBuild_BankrPromptTeachesManualWalletSign(t *testing.T) {
	for _, typ := range []string{"agent", "inference"} {
		p := Build(Input{
			Type:  typ,
			URL:   "https://seller.example.com/services/demo",
			Model: "claude-sonnet-4-6",
		}).Prompts[PromptBankr]
		for _, want := range []string{"bankr wallet sign", "validAfter", "accepts[]", "--max-time 300"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s bankr prompt missing %q:\n%s", typ, want, p)
			}
		}
		for _, forbid := range []string{"eip155:8453", "BaseScan", "alias `base`"} {
			if strings.Contains(p, forbid) {
				t.Errorf("%s bankr prompt must not hardcode network %q:\n%s", typ, forbid, p)
			}
		}
	}

	agent := Build(Input{
		Type:  "agent",
		URL:   "https://seller.example.com/services/demo",
		Model: "claude-sonnet-4-6",
	}).Prompts[PromptBankr]
	for _, want := range []string{"maxTimeoutSeconds", "extra", "unsupported_scheme", "ENTIRE"} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent bankr prompt missing full-accepts guidance %q:\n%s", want, agent)
		}
	}

	httpPrompt := Build(Input{
		Type:         "http",
		URL:          "https://seller.example.com/services/demo",
		PriceDisplay: "0.001 USDC per request",
		NetworkLabel: "Base Sepolia",
	}).Prompts[PromptBankr]
	for _, want := range []string{"Bankr chat", "auto-pay", "0.001 USDC", "Base Sepolia"} {
		if !strings.Contains(httpPrompt, want) {
			t.Errorf("http bankr prompt missing %q:\n%s", want, httpPrompt)
		}
	}
	if strings.Contains(httpPrompt, "not Bankr chat") {
		t.Errorf("http bankr prompt must prefer chat auto-pay:\n%s", httpPrompt)
	}
}

// TestBuild_PonchoPromptMirrorsMeritDiscovery pins Poncho as an AgentCash-
// family buyer (shared x-payment-info discovery). The storefront tab labels
// Poncho; the prompt itself is a direct order (no "paste into" framing).
func TestBuild_PonchoPromptMirrorsMeritDiscovery(t *testing.T) {
	httpPrompt := Build(Input{
		Type: "http",
		URL:  "https://seller.example.com/services/demo",
	}).Prompts[PromptPoncho]
	for _, want := range []string{"x-payment-info", "/.well-known/x402", "Call the paid HTTP endpoint"} {
		if !strings.Contains(httpPrompt, want) {
			t.Errorf("http poncho prompt missing %q:\n%s", want, httpPrompt)
		}
	}
	for _, forbid := range []string{"Paste into", "tryponcho.com", "Help me"} {
		if strings.Contains(httpPrompt, forbid) {
			t.Errorf("http poncho prompt must be a direct order; still has %q:\n%s", forbid, httpPrompt)
		}
	}
	agent := Build(Input{
		Type:  "agent",
		URL:   "https://seller.example.com/services/demo",
		Model: "claude-sonnet-4-6",
	}).Prompts[PromptPoncho]
	for _, want := range []string{"≥180s", "/v1/chat/completions", "Call the Obol Agent"} {
		if !strings.Contains(agent, want) {
			t.Errorf("agent poncho prompt missing %q:\n%s", want, agent)
		}
	}
	for _, forbid := range []string{"Paste into", "Help me"} {
		if strings.Contains(agent, forbid) {
			t.Errorf("agent poncho prompt must be a direct order; still has %q:\n%s", forbid, agent)
		}
	}
}

// TestGuideRef_PointsAtSellerDocs pins that generic-LLM buyers are pointed
// at the seller's own machine-readable docs, not a generic external page.
func TestGuideRef_PointsAtSellerDocs(t *testing.T) {
	ref := GuideRef("https://seller.example.com/")
	for _, want := range []string{"https://seller.example.com/skill.md", "https://seller.example.com/openapi.json"} {
		if !strings.Contains(ref, want) {
			t.Errorf("GuideRef = %q, want it to reference %s", ref, want)
		}
	}
	if fallback := GuideRef(""); !strings.Contains(fallback, "x402.org") {
		t.Errorf("empty-site GuideRef = %q, want x402.org fallback", fallback)
	}
}

// TestBuild_AgentPromptRunsAsIs pins that the agent prompt embeds a concrete
// example task and the pay-agent invocation, so a buyer can paste it
// unedited and have it work.
func TestBuild_AgentPromptRunsAsIs(t *testing.T) {
	block := Build(Input{Type: "agent", URL: "https://s.example/services/quant", Model: "m1"})
	obol := block.Prompts[PromptObolAgent]
	if !strings.Contains(obol, "pay-agent https://s.example/services/quant") {
		t.Errorf("obol-agent prompt missing pay-agent invocation:\n%s", obol)
	}
	if !strings.Contains(obol, DefaultTaskExample) {
		t.Errorf("obol-agent prompt missing the concrete example task:\n%s", obol)
	}
	if !strings.Contains(block.Prompts[PromptCLI], "buy.py go ") {
		t.Errorf("cli prompt should use the go front door:\n%s", block.Prompts[PromptCLI])
	}
}

// TestBuild_GenericLLMMatchesMainStyle pins that the generic-llm prompt stays
// the concise main-branch wording (no external-tool digressions).
func TestBuild_GenericLLMMatchesMainStyle(t *testing.T) {
	agent := Build(Input{
		Type: "agent", URL: "https://s.example/services/a", SiteURL: "https://s.example", Model: "m1",
	}).Prompts[PromptGenericLLM]
	if strings.Contains(agent, "AgentCash") || strings.Contains(agent, "Bankr") || strings.Contains(agent, "Poncho") {
		t.Errorf("generic-llm must stay tool-agnostic:\n%s", agent)
	}
	http := Build(Input{
		Type: "http", URL: "https://s.example/services/h", SiteURL: "https://s.example",
		PriceDisplay: "0.001 USDC per request", NetworkLabel: "Base",
	}).Prompts[PromptGenericLLM]
	want := "Fetch it with no payment to read the 402 `accepts[]` pricing"
	if !strings.Contains(http, want) {
		t.Errorf("http generic-llm missing main-style accepts[] flow:\n%s", http)
	}
}
