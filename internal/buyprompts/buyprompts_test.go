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
// the five standard prompt keys existing for every offer type.
func TestBuild_AllTypesCarryEveryPromptKey(t *testing.T) {
	for _, typ := range []string{"agent", "inference", "http", "fine-tuning", "", "bogus"} {
		block := Build(Input{Type: typ, URL: "https://s.example/services/x"})
		for _, key := range []string{PromptObolAgent, PromptGenericLLM, PromptCLI, PromptAgentCash, PromptBankr} {
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
