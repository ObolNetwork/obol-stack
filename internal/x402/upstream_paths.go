package x402

import (
	"path"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// Allowlist entries are "METHOD /path" (method-scoped) or "/path" (any
// method). Matching on the path is exact; see upstreamPathAllowed.

// liteLLMBuyerPaths are the LiteLLM endpoints a buyer may reach through a
// paid offer. The verifier injects the LiteLLM master key on these offers,
// so anything else (/model/new, /key/generate, /config/update, /model/info,
// stored /v1/responses, ...) would hand a paying buyer admin access.
var liteLLMBuyerPaths = []string{
	"POST /v1/chat/completions",
	"POST /chat/completions",
	"POST /v1/completions",
	"POST /completions",
	"POST /v1/embeddings",
	"POST /embeddings",
}

// hermesBuyerPaths are the Hermes API-server endpoints a buyer may reach
// through an agent offer. The verifier injects the agent's API_SERVER_KEY,
// which Hermes treats as full operator access: /api/sessions* and GET
// /v1/responses/{id} read every other buyer's conversations, /api/jobs*
// schedules recurring agent runs, /v1/runs* steers or approves runs,
// /v1/artifacts*, /v1/browser-control/*, /v1/room-members/* and the
// /p/{profile}/* mirrors expose the rest. A paying buyer needs exactly one
// turn of chat, which is also all the offer's 402, OpenAPI and chat widget
// advertise. (A bare-base or /chat/completions POST is normalized to this
// path first; see normalizeChatCompletionsPath.)
var hermesBuyerPaths = []string{
	"POST /v1/chat/completions",
}

// upstreamPathsForOffer returns the exhaustive upstream path list for offers
// whose injected credential is privileged, or nil when any path may pass.
func upstreamPathsForOffer(offer *monetizeapi.ServiceOffer) []string {
	switch {
	case offer.IsAgent():
		return hermesBuyerPaths
	case isLiteLLMUpstream(offer):
		return liteLLMBuyerPaths
	}

	return nil
}

// upstreamPathAllowed reports whether a request (method + escapedPath) may
// reach the rule's upstream. Matching is exact on the path the upstream will
// route: percent-encoding, backslashes, dot segments and repeated slashes
// are all refused rather than normalised, so the path compared here is the
// path the upstream sees. Method-scoped entries also require the method; an
// unknown (empty) method never matches them.
func upstreamPathAllowed(rule *RouteRule, method, escapedPath string) bool {
	if len(rule.UpstreamPaths) == 0 {
		return true
	}

	sub, ok := upstreamSubPath(rule, method, escapedPath)
	if !ok {
		return false
	}

	for _, entry := range rule.UpstreamPaths {
		wantMethod, wantPath, scoped := strings.Cut(entry, " ")
		if !scoped {
			wantPath, wantMethod = entry, ""
		}
		if wantPath == sub && (wantMethod == "" || wantMethod == method) {
			return true
		}
	}

	return false
}

// upstreamSubPath is the canonical upstream sub-path for escapedPath, or
// ok=false when the path is ambiguous (encoded, backslashed, dot segments,
// repeated slashes) and must be refused.
func upstreamSubPath(rule *RouteRule, method, escapedPath string) (string, bool) {
	if strings.ContainsAny(escapedPath, `%\`) {
		return "", false
	}

	sub := stripRoutePrefix(rule.StripPrefix, escapedPath)
	sub = normalizeChatCompletionsPath(rule.OfferType, method, sub)
	sub = strings.TrimSuffix(sub, "/")

	if sub == "" {
		return "", true
	}
	if path.Clean(sub) != sub {
		return "", false
	}

	return sub, true
}

// paywallPreview reports whether a refused request is a browser-style look
// at the offer root of a path-restricted rule (GET/HEAD /services/<name>,
// no payment attached). Those still get the 402 page — the catalog links
// buyers there — but never reach the upstream: HandleProxy answers them from
// the payment gate alone.
func paywallPreview(rule *RouteRule, method, escapedPath string, hasPayment bool) bool {
	if len(rule.UpstreamPaths) == 0 || hasPayment || rule.IsFree() || rule.IsAuth() {
		return false
	}
	if method != "GET" && method != "HEAD" {
		return false
	}
	sub, ok := upstreamSubPath(rule, method, escapedPath)
	return ok && sub == ""
}
