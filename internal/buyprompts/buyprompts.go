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
	// PromptAgentCash is pasted into an AgentCash-connected agent/wallet
	// (Merit Systems MCP/CLI). Discovery: OpenAPI `x-payment-info` + `/.well-known/x402`.
	PromptAgentCash = "agentcash"
	// PromptPoncho is pasted into Poncho chat (https://tryponcho.com) — same
	// Merit/AgentCash discovery + wallet-pay path.
	PromptPoncho = "poncho"
	// PromptBankr is pasted into Bankr chat or a Bankr-CLI agent.
	// http: chat auto-pay. agent/inference: `bankr wallet sign` + long curl.
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
	// Deliberately no model anywhere: an Obol Agent runs its own pinned
	// model, skills, and memory — the buyer never picks one, pay-agent takes
	// no --model, and the agent ignores the chat-completions `model` field,
	// so the wire example omits it too.
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
					"pay-agent %s --message %q",
				in.URL, task(in),
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
				"Call the Obol Agent at %s — gated by %s (OpenAPI `x-payment-info` + `/.well-known/x402`). "+
					"POST %s with Content-Type application/json, client timeout ≥180s, and this body:\n"+
					"```json\n{\"model\":%q,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":%q}]}\n```\n"+
					"Use AgentCash auto-pay (x402) — do not craft X-PAYMENT by hand. "+
					"Treat as paid ONLY if the response includes a non-null payment receipt / tx hash "+
					"(or X-PAYMENT-RESPONSE). If `payment` is null, report unpaid/failed — do not claim success.",
				in.URL, GuideRef(in.SiteURL), ChatCompletionsURL(in.URL), modelOr(in, "hermes-agent"), task(in),
			),
			PromptPoncho: fmt.Sprintf(
				"Call the Obol Agent at %s — gated by %s (OpenAPI `x-payment-info` + `/.well-known/x402`). "+
					"POST %s with timeout ≥180s and body:\n"+
					"```json\n{\"model\":%q,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":%q}]}\n```\n"+
					"Confirm the quoted payment. Treat as paid ONLY if a payment receipt / tx hash is shown; "+
					"if payment is missing, report unpaid/failed.",
				in.URL, GuideRef(in.SiteURL), ChatCompletionsURL(in.URL), modelOr(in, "hermes-agent"), task(in),
			),
			PromptBankr: fmt.Sprintf(
				"Buy one turn from the Obol Agent at %s via Bankr CLI — not Bankr chat/Apps. "+
					"1) Probe POST %s (body below) without payment; take ONE `accepts[]` entry and reuse it "+
					"ENTIRE as the payment payload's `accepted` object — every field "+
					"(scheme, network, maxAmountRequired, asset, payTo, maxTimeoutSeconds, extra). "+
					"Do not rebuild or drop fields (`unsupported_scheme` means you stripped the entry). "+
					"2) `bankr wallet sign` EIP-3009 with `validAfter=0` or `now-600` (never `now`), `validBefore=now+900`. "+
					"3) Base64-encode the x402 v2 payload; curl POST with `X-PAYMENT`, `Content-Type: application/json`, "+
					"`curl --max-time 300`, body:\n"+
					"```json\n{\"model\":%q,\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":%q}]}\n```\n"+
					"Treat as paid ONLY if `X-PAYMENT-RESPONSE` / a tx hash appears; on timeout check the chain explorer before retry.",
				in.URL, ChatCompletionsURL(in.URL), modelOr(in, "hermes-agent"), task(in),
			),
		},
		Example: ChatExample(in.URL, "", in.TaskExample),
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
				"Call the remote LLM at %s (model %s) via POST %s, paid with %s "+
					"(OpenAPI `x-payment-info` + `/.well-known/x402`). POST with `stream:true`, "+
					"timeout ≥180s, body `{\"model\":%q,\"stream\":true,\"messages\":[...]}`. "+
					"Use AgentCash auto-pay. Treat as paid ONLY if a non-null payment receipt / tx hash is returned.",
				in.URL, model, ChatCompletionsURL(in.URL), GuideRef(in.SiteURL), model,
			),
			PromptPoncho: fmt.Sprintf(
				"Call the remote LLM at %s (model %s) via POST %s, paid with %s "+
					"(OpenAPI `x-payment-info` + `/.well-known/x402`). Body "+
					"`{\"model\":%q,\"stream\":true,\"messages\":[...]}`, timeout ≥180s. "+
					"Confirm payment; treat as paid ONLY if a receipt / tx hash is shown.",
				in.URL, model, ChatCompletionsURL(in.URL), GuideRef(in.SiteURL), model,
			),
			PromptBankr: fmt.Sprintf(
				"Buy the remote LLM at %s (model %s) at %s via Bankr CLI — not Bankr chat/Apps. "+
					"Probe without payment; copy `accepts[]` network/amount/asset/payTo VERBATIM; "+
					"`bankr wallet sign` EIP-3009 with `validAfter=0` or `now-600` (never `now`); "+
					"curl with `X-PAYMENT`, `--max-time 300`, `stream:true`, body model=%q. Paid with %s.",
				in.URL, model, ChatCompletionsURL(in.URL), model, GuideRef(in.SiteURL),
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
				"Call the paid HTTP endpoint at %s once. It's gated by %s.%s%s "+
					"This origin publishes AgentCash discovery (OpenAPI `x-payment-info` + `/.well-known/x402`). "+
					"Use AgentCash auto-pay. Treat as paid ONLY if a non-null payment receipt / tx hash is returned.",
				in.URL, GuideRef(in.SiteURL), priceClause, netClause,
			),
			PromptPoncho: fmt.Sprintf(
				"Call the paid HTTP endpoint at %s once.%s%s "+
					"Discovery: OpenAPI `x-payment-info` + `/.well-known/x402`. "+
					"Confirm payment; treat as paid ONLY if a receipt / tx hash is shown.",
				in.URL, priceClause, netClause,
			),
			PromptBankr: fmt.Sprintf(
				"Buy the HTTP endpoint at %s with Bankr chat auto-pay.%s%s "+
					"If chat returns `facilitator_error`, retry once; if it keeps failing, use "+
					"`bankr wallet sign` with `validAfter=0` or `now-600`, then curl with `X-PAYMENT`.",
				in.URL, priceClause, netClause,
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
