package x402

import (
	"path"
	"slices"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// liteLLMBuyerPaths are the LiteLLM endpoints a buyer may reach through a
// paid offer. The verifier injects the LiteLLM master key on these offers,
// so anything else (/model/new, /key/generate, /config/update, /model/info,
// stored /v1/responses, ...) would hand a paying buyer admin access.
var liteLLMBuyerPaths = []string{
	"/v1/chat/completions",
	"/chat/completions",
	"/v1/completions",
	"/completions",
	"/v1/embeddings",
	"/embeddings",
}

// upstreamPathsForOffer returns the exhaustive upstream path list for offers
// whose injected credential is privileged, or nil when any path may pass.
func upstreamPathsForOffer(offer *monetizeapi.ServiceOffer) []string {
	if isLiteLLMUpstream(offer) {
		return liteLLMBuyerPaths
	}

	return nil
}

// upstreamPathAllowed reports whether a request for escapedPath may reach the
// rule's upstream. Matching is exact on the path the upstream will route:
// percent-encoding, backslashes, dot segments and repeated slashes are all
// refused rather than normalised, so the path compared here is the path the
// upstream sees.
func upstreamPathAllowed(rule *RouteRule, method, escapedPath string) bool {
	if len(rule.UpstreamPaths) == 0 {
		return true
	}

	if strings.ContainsAny(escapedPath, `%\`) {
		return false
	}

	sub := stripRoutePrefix(rule.StripPrefix, escapedPath)
	sub = normalizeChatCompletionsPath(rule.OfferType, method, sub)
	sub = strings.TrimSuffix(sub, "/")

	if sub == "" || path.Clean(sub) != sub {
		return false
	}

	return slices.Contains(rule.UpstreamPaths, sub)
}
