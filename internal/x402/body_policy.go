package x402

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// BodyPolicyLiteLLM marks rules whose upstream is the seller's LiteLLM
// gateway. The verifier injects the LiteLLM master key on these, so the
// seller — not the buyer — decides which model runs: the JSON body's
// "model" is overwritten with RouteRule.Model, request fields LiteLLM
// would treat as routing or credential overrides are dropped, and the
// query string and x-litellm-* headers never reach LiteLLM.
const BodyPolicyLiteLLM = "litellm"

// BodyPolicyAgent marks agent (Hermes) offers. Hermes lets a request pick
// its own provider/model ("provider" is always honored, "model" when
// direct_model_requests is on, "model_options" passes through), and the
// X-Hermes-Session-Id / X-Hermes-Session-Key headers resume another
// session's history or memory scope. All of that is stripped: the agent
// runs the model its operator pinned, and each buyer turn stands alone.
const BodyPolicyAgent = "agent"

// agentModelSelectionFields are the Hermes chat-request fields that choose
// the provider/model the agent runs.
var agentModelSelectionFields = []string{"model", "provider", "model_options"}

// upstreamBodyLimit caps how much of a request body the verifier buffers to
// apply a body policy. Larger bodies are refused (413) before payment.
const upstreamBodyLimit = 16 << 20

// liteLLMCommonFields are accepted on every LiteLLM buyer path.
var liteLLMCommonFields = []string{"model", "user"}

// liteLLMSamplingFields are the OpenAI sampling/output parameters shared by
// chat and legacy completions, plus the widely used open-model extras.
var liteLLMSamplingFields = []string{
	"frequency_penalty", "presence_penalty", "repetition_penalty",
	"logit_bias", "logprobs", "top_logprobs",
	"max_tokens", "max_completion_tokens", "n", "seed", "stop",
	"stream", "stream_options", "temperature", "top_p", "top_k", "min_p",
}

// liteLLMChatFields are the extra OpenAI chat-completions request fields.
var liteLLMChatFields = []string{
	"messages", "response_format", "tools", "tool_choice", "parallel_tool_calls",
	"functions", "function_call", "reasoning_effort", "modalities", "audio",
	"prediction", "web_search_options", "verbosity",
}

var liteLLMCompletionFields = []string{"prompt", "best_of", "echo", "suffix"}

var liteLLMEmbeddingFields = []string{"input", "encoding_format", "dimensions"}

// liteLLMAllowedFields maps each LiteLLM buyer path to the top-level body
// fields forwarded on it. It is an allowlist because LiteLLM merges request
// kwargs OVER the deployment's litellm_params: fields such as fallbacks /
// context_window_fallbacks (run another model group on failure),
// extra_body (merged into the provider request, so extra_body.model swaps
// the provider-side model), custom_llm_provider, api_version, extra_headers,
// metadata (tag routing) or service_tier would each let a buyer pick or
// upgrade the model the seller pays for. Unknown fields are dropped, not
// rejected, so ordinary OpenAI clients keep working.
var liteLLMAllowedFields = func() map[string]map[string]bool {
	set := func(groups ...[]string) map[string]bool {
		m := make(map[string]bool)
		for _, g := range groups {
			for _, f := range g {
				m[f] = true
			}
		}
		return m
	}
	chat := set(liteLLMCommonFields, liteLLMSamplingFields, liteLLMChatFields)
	completions := set(liteLLMCommonFields, liteLLMSamplingFields, liteLLMCompletionFields)
	embeddings := set(liteLLMCommonFields, liteLLMEmbeddingFields)
	return map[string]map[string]bool{
		"/v1/chat/completions": chat,
		"/chat/completions":    chat,
		"/v1/completions":      completions,
		"/completions":         completions,
		"/v1/embeddings":       embeddings,
		"/embeddings":          embeddings,
	}
}()

// isLiteLLMUpstream reports whether the offer's upstream is the cluster
// LiteLLM gateway — the offers the verifier injects the master key on.
func isLiteLLMUpstream(offer *monetizeapi.ServiceOffer) bool {
	return !offer.IsAgent() && strings.EqualFold(offer.Spec.Upstream.Service, "litellm")
}

// bodyPolicyForOffer returns the request-body policy the verifier applies
// before proxying to the offer's upstream ("" = forward unchanged).
func bodyPolicyForOffer(offer *monetizeapi.ServiceOffer) string {
	switch {
	case offer.IsAgent():
		return BodyPolicyAgent
	case isLiteLLMUpstream(offer):
		return BodyPolicyLiteLLM
	}
	return ""
}

// bodyPolicyError is a request refused by a body policy, before payment.
type bodyPolicyError struct {
	status int
	title  string
	detail string
}

func (e *bodyPolicyError) Error() string { return e.detail }

// applyBodyPolicy enforces the rule's body policy on r in place. It runs in
// HandleProxy before the payment middleware, so a request that can't be
// served is refused before any 402 challenge or verification — nobody pays
// for it — and the upstream (and the asynchronous job broker) only ever see
// the rewritten body.
func applyBodyPolicy(rule *RouteRule, r *http.Request) error {
	if err := checkBodyPolicyConfig(rule); err != nil {
		return err
	}
	switch rule.BodyPolicy {
	case BodyPolicyLiteLLM:
		return applyLiteLLMBodyPolicy(rule, r)
	case BodyPolicyAgent:
		return applyAgentBodyPolicy(r)
	}
	return nil
}

// checkBodyPolicyConfig refuses rules whose body policy can't be applied
// (a LiteLLM offer with no pinned model) — before the 402, for every request.
func checkBodyPolicyConfig(rule *RouteRule) error {
	if rule.BodyPolicy == BodyPolicyLiteLLM && strings.TrimSpace(rule.Model) == "" {
		return &bodyPolicyError{http.StatusServiceUnavailable, "Offer misconfigured",
			fmt.Sprintf("This LiteLLM-backed offer has no model configured, so it is not serving requests. "+
				"The seller must pin one: obol sell update %s -n %s --model <litellm-model>",
				rule.OfferName, rule.OfferNamespace)}
	}
	return nil
}

// applyAgentBodyPolicy strips buyer model/provider selection and Hermes
// session headers from an agent chat request.
func applyAgentBodyPolicy(r *http.Request) error {
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-hermes-") {
			r.Header.Del(name)
		}
	}
	// Hermes scopes its response cache by API key, which every buyer
	// shares; a per-request payment makes replaying it pointless anyway.
	r.Header.Del("Idempotency-Key")
	if r.Method != http.MethodPost {
		// upstreamPathAllowed only lets POST through; nothing to rewrite.
		return nil
	}
	doc, err := readJSONObjectBody(r)
	if err != nil {
		return err
	}
	for _, k := range agentModelSelectionFields {
		delete(doc, k)
	}
	if err := isolateAgentSession(doc); err != nil {
		return err
	}
	r.URL.RawQuery = ""
	r.URL.ForceQuery = false
	return setJSONBody(r, doc)
}

// isolateAgentSession gives every paid agent request its own Hermes session.
// Without X-Hermes-Session-Id (stripped above), Hermes' /v1/chat/completions
// derives the session as sha256(system prompt + "\n" + first user message)
// (_derive_chat_session_id) and keys the agent's task sandbox, background
// processes and memory scope on it — so two buyers sending the same opening
// turn would share one. A leading system message carrying a random request
// id makes the system prompt, and so the derived session, unique and
// unguessable per request. (A random X-Hermes-Session-Id is the wrong lever:
// Hermes then loads history from its DB for that id and discards the
// buyer's in-body history.) Bodies without a messages array are left for
// Hermes to reject.
func isolateAgentSession(doc map[string]json.RawMessage) error {
	raw, ok := doc["messages"]
	if !ok {
		return nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil //nolint:nilerr // not an array: Hermes rejects it itself
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return &bodyPolicyError{http.StatusInternalServerError, "Internal error", "Could not isolate the request."}
	}
	note, err := json.Marshal(map[string]string{
		"role":    "system",
		"content": "Request id " + hex.EncodeToString(nonce[:]) + " (set by the payment gateway; not part of the conversation).",
	})
	if err != nil {
		return err
	}
	merged, err := json.Marshal(append([]json.RawMessage{note}, messages...))
	if err != nil {
		return err
	}
	doc["messages"] = merged
	return nil
}

func applyLiteLLMBodyPolicy(rule *RouteRule, r *http.Request) error {
	if r.Method != http.MethodPost {
		return &bodyPolicyError{http.StatusMethodNotAllowed, "Method not allowed",
			"This endpoint only accepts POST with a JSON body."}
	}

	sub := strings.TrimSuffix(normalizeChatCompletionsPath(rule.OfferType, r.Method,
		stripRoutePrefix(rule.StripPrefix, r.URL.Path)), "/")
	allowed, ok := liteLLMAllowedFields[sub]
	if !ok {
		// upstreamPathAllowed already limits these rules to the paths
		// above; anything else is a wiring bug, so fail closed.
		return &bodyPolicyError{http.StatusNotFound, "No service here",
			"This path is not part of any published service."}
	}

	doc, err := readJSONObjectBody(r)
	if err != nil {
		return err
	}
	for k := range doc {
		if !allowed[k] {
			delete(doc, k)
		}
	}
	model, _ := json.Marshal(rule.Model)
	doc["model"] = model

	if err := setJSONBody(r, doc); err != nil {
		return err
	}
	// LiteLLM's chat route takes `model` as a query parameter too, and it
	// beats the body (common_request_processing: `model or data["model"]`),
	// so the query string must never reach it. No OpenAI endpoint we
	// allow takes query parameters.
	r.URL.RawQuery = ""
	r.URL.ForceQuery = false
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-litellm-") {
			r.Header.Del(name)
		}
	}
	return nil
}

// readJSONObjectBody buffers r's body (bounded) and decodes it as a JSON
// object. Duplicate keys collapse to the last value, and the re-encoded body
// carries each key once, so the upstream can't parse a different "model"
// than the one the verifier wrote.
func readJSONObjectBody(r *http.Request) (map[string]json.RawMessage, error) {
	if ce := r.Header.Get("Content-Encoding"); ce != "" && !strings.EqualFold(ce, "identity") {
		return nil, &bodyPolicyError{http.StatusBadRequest, "Unsupported request body",
			"Compressed request bodies are not accepted; send plain JSON."}
	}
	if r.Body == nil || r.Body == http.NoBody {
		return nil, &bodyPolicyError{http.StatusBadRequest, "Invalid request body",
			"Request body must be a JSON object."}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, upstreamBodyLimit+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, &bodyPolicyError{http.StatusBadRequest, "Invalid request body",
			"Could not read the request body."}
	}
	if len(body) > upstreamBodyLimit {
		return nil, &bodyPolicyError{http.StatusRequestEntityTooLarge, "Request too large",
			fmt.Sprintf("Request body exceeds %d bytes.", upstreamBodyLimit)}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil || doc == nil {
		return nil, &bodyPolicyError{http.StatusBadRequest, "Invalid request body",
			"Request body must be a JSON object."}
	}
	return doc, nil
}

// setJSONBody re-encodes doc as r's body and pins the framing headers to it.
func setJSONBody(r *http.Request, doc map[string]json.RawMessage) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return &bodyPolicyError{http.StatusBadRequest, "Invalid request body",
			"Request body must be a JSON object."}
	}
	body := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	r.ContentLength = int64(len(body))
	r.TransferEncoding = nil
	r.Header.Set("Content-Length", strconv.Itoa(len(body)))
	r.Header.Del("Content-Encoding")
	r.Header.Set("Content-Type", "application/json")
	return nil
}

// writeBodyPolicyError renders a body-policy refusal.
func writeBodyPolicyError(w http.ResponseWriter, r *http.Request, err error) {
	var bpe *bodyPolicyError
	if !errors.As(err, &bpe) {
		bpe = &bodyPolicyError{http.StatusBadRequest, "Invalid request", err.Error()}
	}
	if bpe.status == http.StatusMethodNotAllowed {
		w.Header().Set("Allow", http.MethodPost)
	}
	writeErrorResponse(w, r, bpe.status, bpe.title, bpe.detail)
}
