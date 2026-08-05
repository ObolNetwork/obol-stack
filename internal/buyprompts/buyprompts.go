// Package buyprompts is the single authoring point for buyer-facing "how to
// buy" instructions. Three surfaces show a buyer how to pay for a service —
// the 402 paywall page (internal/x402/paymentrequired.go), the public
// storefront (web/public-storefront), and the machine-readable catalog
// (/api/services.json via internal/serviceoffercontroller) — and when each
// composed its own copy they drifted: the 402 page taught agent buyers a
// call path that 404'd while every other surface taught the right one.
//
// The controller publishes the output of this package in each catalog
// entry's `buy` block; the storefront renders that block verbatim; the 402
// page builds its prompt cards from the same functions. Adding support for a
// new kind of buying software means adding one prompt key here — not forking
// a fourth copy of the instructions.
package buyprompts

import (
	"fmt"
	"strings"
)

// DefaultTaskExample is the placeholder task used in copy-paste prompts and
// wire examples wherever the buyer hasn't supplied a real one.
const DefaultTaskExample = "Summarise the README and list the top 3 risks."

// Prompt keys published in the catalog `buy.prompts` map. Stable API for
// storefront and downstream consumers.
const (
	// PromptObolAgent is pasted into an Obol Stack agent (Hermes/OpenClaw)
	// that has the buy-x402 skill.
	PromptObolAgent = "obol-agent"
	// PromptGenericLLM is pasted into any other AI agent (Claude, ChatGPT,
	// Gemini, ...) with tool access but no Obol tooling.
	PromptGenericLLM = "generic-llm"
	// PromptCLI is the shell command a human runs from an obol-stack host.
	PromptCLI = "cli"
	// PromptAgentCash is pasted into an AgentCash-connected agent/wallet.
	// AgentCash (Merit Systems) shares its discovery convention — OpenAPI
	// `x-payment-info` plus a `/.well-known/x402` fallback — with x402scan
	// and Poncho, so this same prompt covers all three.
	PromptAgentCash = "agentcash"
	// PromptBankr is pasted into Bankr chat or a Bankr-CLI agent. Guidance is
	// type-specific (observed 2026-08-05 against third-party Obol sellers):
	//   - http: prefer Bankr chat auto-pay — fast endpoints usually finish
	//     inside Bankr's ~30s client window; do NOT ask chat to run
	//     `bankr wallet sign` (chat cannot).
	//   - agent/inference: forbid chat/Apps auto-pay (`rpc timeout` / voucher
	//     quirks); teach `bankr wallet sign` + long curl in a Bankr-CLI agent
	//     (Cursor). Bankr's own docs also scope chat/CLI auto-pay to endpoints
	//     deployed via `bankr x402 deploy` + their discovery index, and Apps
	//     need an `allowedHosts` allowlist — still teach the proven CLI path
	//     for slow offers.
	PromptBankr = "bankr"
)

// Input describes one purchasable service. All fields are display-ready
// strings; zero values degrade gracefully (placeholders, omitted clauses).
type Input struct {
	// Type is the ServiceOffer type: inference, agent, http, fine-tuning.
	// Unknown/empty values get http (single-shot pay) semantics.
	Type string
	// URL is the service base URL (e.g. https://host/services/<name>).
	URL string
	// SiteURL is the storefront origin used for discovery references
	// (skill.md / openapi.json links). Empty falls back to x402.org.
	SiteURL string
	// Model is the model id for inference/agent offers ("" when unknown).
	Model string
	// PriceDisplay is the formatted price (e.g. "0.001 USDC per request").
	PriceDisplay string
	// NetworkLabel is the human-friendly chain name (e.g. "Base Sepolia").
	NetworkLabel string
	// TaskExample overrides DefaultTaskExample in prompts and examples.
	TaskExample string
}

// CallShape is the machine-readable request recipe for a service. Buying
// software uses it instead of guessing the path/method/streaming mode.
type CallShape struct {
	Method string `json:"method"`
	// Path is relative to the service base URL ("" = the base itself).
	Path string `json:"path,omitempty"`
	// BodyKind: "openai-chat" (chat-completions JSON), "json" (operator-
	// defined JSON), "multipart" (fine-tuning), or "none".
	BodyKind string `json:"bodyKind"`
	// Streaming reports whether the endpoint supports (and slow calls
	// should use) `"stream": true`.
	Streaming bool `json:"streaming"`
}

// Block is the full buyer-instruction block for one service, published as
// the catalog entry's `buy` field.
type Block struct {
	CallShape CallShape         `json:"callShape"`
	Prompts   map[string]string `json:"prompts"`
	// Example is a copy-pasteable wire example of one paid request.
	Example string `json:"example,omitempty"`
}

// Build assembles the canonical Block for a service.
func Build(in Input) Block {
	switch normalizeType(in.Type) {
	case "agent":
		return agentBlock(in)
	case "inference":
		return inferenceBlock(in)
	case "fine-tuning":
		return fineTuningBlock(in)
	default:
		return httpBlock(in)
	}
}

// GuideRef is the discovery reference woven into generic-LLM prompts: it
// tells a buyer with no Obol tooling where the full payment recipe lives.
func GuideRef(siteURL string) string {
	siteURL = strings.TrimRight(siteURL, "/")
	if siteURL == "" {
		return "x402 micropayments (see https://www.x402.org)"
	}
	return fmt.Sprintf(
		"x402 micropayments — read %s/skill.md for the full payment flow and %s/openapi.json for the exact request shapes",
		siteURL, siteURL,
	)
}

// ChatCompletionsURL is the canonical paid-call URL for chat-shaped offers
// (inference and agent). Must stay in lockstep with the gateway's tolerant
// path rewrite (internal/x402/verifier.go normalizeChatCompletionsPath) and
// buy.py's target construction.
func ChatCompletionsURL(baseURL string) string {
	return strings.TrimSuffix(baseURL, "/") + "/v1/chat/completions"
}

// ChatExample renders the wire example of one paid chat-completions call.
func ChatExample(url, model, task string) string {
	if task == "" {
		task = DefaultTaskExample
	}
	modelClause := ""
	if model != "" {
		modelClause = fmt.Sprintf("\n  \"model\": %q,", model)
	}
	return fmt.Sprintf(`POST %s
Content-Type: application/json
X-PAYMENT: <pre-signed-EIP-3009-or-Permit2-voucher>

{%s
  "messages": [
    {"role": "user", "content": %q}
  ]
}`, ChatCompletionsURL(url), modelClause, task)
}

func normalizeType(t string) string {
	switch t {
	case "inference", "agent", "fine-tuning":
		return t
	default:
		return "http"
	}
}

func task(in Input) string {
	if in.TaskExample != "" {
		return in.TaskExample
	}
	return DefaultTaskExample
}

func modelOr(in Input, placeholder string) string {
	if in.Model != "" {
		return in.Model
	}
	return placeholder
}

func agentBlock(in Input) Block {
	modelFlag := modelOr(in, "<model-id>")
	return Block{
		CallShape: CallShape{
			Method:    "POST",
			Path:      "/v1/chat/completions",
			BodyKind:  "openai-chat",
			Streaming: true,
		},
		Prompts: map[string]string{
			PromptObolAgent: fmt.Sprintf(
				"Use the buy-x402 skill's `pay-agent` command to buy one round of work from this "+
					"Obol Agent — it has its own skills, tools, and memory, not just a model. Edit the "+
					"message, then run:\n\n"+
					"pay-agent %s --model %q --message %q",
				in.URL, modelFlag, task(in),
			),
			PromptGenericLLM: fmt.Sprintf(
				"Help me call the Obol Agent at %s — it's an autonomous agent (tools + skills + memory), "+
					"not a raw LLM. It's gated by %s. POST OpenAI-style chat-completions JSON to "+
					"%s with this user message in `messages`: {\"role\":\"user\",\"content\":%q}. "+
					"Attach a signed EIP-3009/Permit2 authorization as `X-PAYMENT`, use `\"stream\": true`, "+
					"and report what the agent does.",
				in.URL, GuideRef(in.SiteURL), ChatCompletionsURL(in.URL), task(in),
			),
			PromptCLI: fmt.Sprintf(
				"python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py go %s --message %q",
				in.URL, task(in),
			),
			PromptAgentCash: fmt.Sprintf(
				"Call the Obol Agent at %s EXACTLY as specified — do not drop fields. "+
					"It's an autonomous agent (tools + skills + memory), gated by %s "+
					"(OpenAPI `x-payment-info` + `/.well-known/x402`). "+
					"REQUIRED: POST %s with Content-Type application/json and this exact body "+
					"(keep stream true; client HTTP timeout must be ≥180s — agents often take "+
					"30–120s before the first SSE byte; shorter timeouts cause false 'payment failed' retries):\n"+
					"```json\n"+
					"{\"model\":%q,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":%q}]}\n"+
					"```\n"+
					"Attach a signed EIP-3009/Permit2 authorization as `X-PAYMENT`. "+
					"If the response has `paymentSettled:false`, you were NOT charged — do not spam retries.",
				in.URL, GuideRef(in.SiteURL), ChatCompletionsURL(in.URL), modelOr(in, "hermes-agent"), task(in),
			),
			PromptBankr: fmt.Sprintf(
				"You have my Bankr wallet via the Bankr CLI (`bankr`). Buy one turn from the Obol Agent at %s "+
					"using MANUAL `bankr wallet sign` + a long curl (timeout ≥180s, prefer 300s). "+
					"Paste this into Cursor (or any Bankr-CLI agent) — not Bankr chat, not a Bankr App.\n\n"+
					"Do NOT use Bankr chat auto-pay, `bankr x402 call`, `/wallet/x402-pay`, or a Bankr App "+
					"`bankr.x402.fetch`. Against this agent offer those paths fail for three reasons:\n"+
					"(A) Scope: per Bankr's own docs, chat/CLI auto-pay is scoped to endpoints deployed via "+
					"`bankr x402 deploy` and approved into Bankr's own discovery index — an arbitrary "+
					"third-party URL like this one is out of scope by design.\n"+
					"(B) Voucher: when they do attempt a call, they often sign EIP-3009 with `validAfter=now` → "+
					"seller returns JSON `reason:facilitator_error` / `detail:unexpected_error` (verify "+
					"rejected; usually no charge).\n"+
					"(C) Timeout: Bankr chat/Apps abort at ~30s with `rpc timeout` while this agent is still "+
					"running (often 30–120s to first SSE byte) → UI failure; never auto-retry; treat BaseScan "+
					"as canonical before any retry.\n\n"+
					"Steps (exact):\n"+
					"1. Probe once without payment: POST %s with the JSON body below → read 402 `accepts[]`. "+
					"Copy network/amount/asset/payTo VERBATIM (CAIP-2 `eip155:8453`, not the alias `base`).\n"+
					"2. Sign EIP-3009 TransferWithAuthorization with `bankr wallet sign` (typed data). "+
					"REQUIRED: set `validAfter` to `0` or `now-600` (past buffer). NEVER `validAfter=now`. "+
					"Set `validBefore` to `now+900`. Match `from`=my Bankr EVM address, `to`/`value`/`asset` from accepts[].\n"+
					"3. Build the x402 payment payload (x402Version 2, accepted = that accepts[] entry, "+
					"payload.authorization + payload.signature), base64-encode it, and curl POST the paid request with:\n"+
					"   - Header `X-PAYMENT: <base64>` (also ok: `PAYMENT-SIGNATURE`)\n"+
					"   - Header `Content-Type: application/json`\n"+
					"   - `curl --max-time 300` (or equivalent) — agents often take 30–120s before first SSE byte\n"+
					"4. Exact body (keep stream true; do not omit fields):\n"+
					"```json\n"+
					"{\"model\":%q,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":%q}]}\n"+
					"```\n"+
					"5. Stream the SSE to me. On `facilitator_error`, re-sign once with a past `validAfter` (do not spam). "+
					"On timeout/504/`rpc timeout`, do NOT retry until you check BaseScan for a Transfer to payTo. "+
					"Seller docs: %s.",
				in.URL, ChatCompletionsURL(in.URL), modelOr(in, "hermes-agent"), task(in), GuideRef(in.SiteURL),
			),
		},
		Example: ChatExample(in.URL, in.Model, in.TaskExample),
	}
}

func inferenceBlock(in Input) Block {
	model := modelOr(in, "<model-id>")
	return Block{
		CallShape: CallShape{
			Method:    "POST",
			Path:      "/v1/chat/completions",
			BodyKind:  "openai-chat",
			Streaming: true,
		},
		Prompts: map[string]string{
			PromptObolAgent: fmt.Sprintf(
				"There's an Obol paid-inference service at %s offering the %s model. "+
					"Explain to me how it works, then — if I'm interested — run "+
					"`obol buy inference %s` from this host to pre-authorize it and wire "+
					"`paid/%s` into our local LiteLLM gateway. After it lands, switch "+
					"yourself over to the new model and confirm.",
				in.URL, model, in.URL, model,
			),
			PromptGenericLLM: fmt.Sprintf(
				"I want to use the remote LLM at %s (model %s) as a paid OpenAI-compatible "+
					"chat-completions endpoint at %s, paid with %s. Pre-sign a budget of EIP-3009/Permit2 "+
					"authorizations and POST chat-completions bodies with the X-PAYMENT header attached.",
				in.URL, model, ChatCompletionsURL(in.URL), GuideRef(in.SiteURL),
			),
			PromptCLI: fmt.Sprintf("obol buy inference %s", in.URL),
			PromptAgentCash: fmt.Sprintf(
				"I want to use the remote LLM at %s (model %s) as a paid OpenAI-compatible "+
					"chat-completions endpoint at %s. This origin already publishes AgentCash's "+
					"discovery convention (OpenAPI `x-payment-info`, `/.well-known/x402` "+
					"fallback), paid with %s — pre-sign a budget of EIP-3009/Permit2 "+
					"authorizations and POST chat-completions bodies with the X-PAYMENT header "+
					"attached.",
				in.URL, model, ChatCompletionsURL(in.URL), GuideRef(in.SiteURL),
			),
			PromptBankr: fmt.Sprintf(
				"You have my Bankr wallet via the Bankr CLI (`bankr`). Buy the remote LLM at %s "+
					"(model %s) at %s using MANUAL `bankr wallet sign` + curl — do NOT use Bankr chat "+
					"auto-pay or `bankr x402 call`. Bankr's own docs scope chat/CLI auto-pay to endpoints "+
					"deployed via `bankr x402 deploy` and approved into their own discovery index — an "+
					"arbitrary third-party URL like this one is out of scope by design. Even where it does "+
					"attempt a call: (A) `validAfter=now` → `facilitator_error`/`unexpected_error`; "+
					"(B) short client timeout while upstream still runs → UI failure (confirm BaseScan "+
					"before retry). Sign EIP-3009 with `validAfter=0` or `now-600`, match accepts[] verbatim "+
					"(CAIP-2), attach `X-PAYMENT`, timeout ≥180s, `stream:true`. Paid with %s.",
				in.URL, model, ChatCompletionsURL(in.URL), GuideRef(in.SiteURL),
			),
		},
		// The model field is required by chat-completions upstreams for
		// inference offers, so the example keeps a placeholder when the
		// real id is unknown (agents, by contrast, ignore it).
		Example: ChatExample(in.URL, model, "<your prompt>"),
	}
}

func httpBlock(in Input) Block {
	priceClause := ""
	if in.PriceDisplay != "" {
		priceClause = " Pay " + in.PriceDisplay + "."
	}
	netClause := ""
	if in.NetworkLabel != "" {
		netClause = " Network: " + in.NetworkLabel + "."
	}
	return Block{
		CallShape: CallShape{
			Method:   "GET",
			BodyKind: "none",
		},
		Prompts: map[string]string{
			PromptObolAgent: fmt.Sprintf(
				"Use the buy-x402 skill's `pay` command to call %s once.%s%s "+
					"Use the method and payload the seller documents, and report what it returns.",
				in.URL, priceClause, netClause,
			),
			PromptGenericLLM: fmt.Sprintf(
				"Call the paid HTTP endpoint at %s once. It's gated by %s.%s%s "+
					"Fetch it with no payment to read the 402 `accepts[]` pricing, sign a matching "+
					"EIP-3009/Permit2 authorization, retry the identical request with the payload "+
					"base64-encoded in the `X-PAYMENT` header, and report the response.",
				in.URL, GuideRef(in.SiteURL), priceClause, netClause,
			),
			PromptCLI: fmt.Sprintf(
				"python3 ${OBOL_SKILLS_DIR:-/data/.openclaw/skills}/buy-x402/scripts/buy.py go %s",
				in.URL,
			),
			PromptAgentCash: fmt.Sprintf(
				"Call the paid HTTP endpoint at %s once. It's gated by %s, and this origin "+
					"already speaks AgentCash's own discovery convention (OpenAPI "+
					"`x-payment-info` plus a `/.well-known/x402` fallback) — your "+
					"AgentCash-connected agent can auto-discover the price instead of parsing "+
					"the 402 by hand.%s%s Sign a matching EIP-3009/Permit2 authorization and "+
					"retry with the payload in the `X-PAYMENT` header.",
				in.URL, GuideRef(in.SiteURL), priceClause, netClause,
			),
			PromptBankr: fmt.Sprintf(
				"Paste this into Bankr chat (or Max Mode). Buy the HTTP endpoint at %s "+
					"using Bankr chat auto-pay — call the URL and let Bankr attach payment.%s%s\n\n"+
					"Prefer chat auto-pay for this fast HTTP offer. Do NOT ask Bankr chat to run "+
					"`bankr wallet sign` or build a manual X-PAYMENT header — chat cannot do that, "+
					"and it will fall back to auto-pay anyway.\n\n"+
					"If chat auto-pay returns `facilitator_error` / `unexpected_error`, that is usually "+
					"a `validAfter=now` voucher quirk (auth not consumed) — retry once; do not spam. "+
					"If it keeps failing, switch to a Bankr-CLI agent (Cursor) and use "+
					"`bankr wallet sign` with `validAfter=0` or `now-600`, then curl with `X-PAYMENT`.\n"+
					"Seller docs: %s.",
				in.URL, priceClause, netClause, GuideRef(in.SiteURL),
			),
		},
	}
}

func fineTuningBlock(in Input) Block {
	block := httpBlock(in)
	block.CallShape = CallShape{
		Method:   "POST",
		BodyKind: "multipart",
	}
	return block
}
