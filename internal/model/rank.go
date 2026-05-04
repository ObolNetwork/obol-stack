package model

import "strings"

// Rank selects the primary model from the configured model list and demotes the
// rest to fallbacks.
//
// Model ordering is configuration, not hidden product policy. LiteLLM's
// model_list order is the source of truth, so Rank preserves that order instead
// of guessing quality from provider names, parameter-count tags, or model-family
// aliases. The only exception is known embedding-only entries: they are kept in
// the fallback list but moved behind chat-capable models so an embedding model
// does not become the default chat model when another option exists.
//
// The returned strings are the original inputs. Do not strip provider prefixes
// or normalize names here; Hermes/OpenClaw round-trip the returned primary back
// to LiteLLM as the chat-completions model field.
func Rank(models []string) (primary string, fallbacks []string) {
	if len(models) == 0 {
		return "", nil
	}

	ordered := make([]string, 0, len(models))
	embeddingOnly := make([]string, 0)
	for _, m := range models {
		if isEmbeddingOnlyModel(m) {
			embeddingOnly = append(embeddingOnly, m)
			continue
		}
		ordered = append(ordered, m)
	}
	ordered = append(ordered, embeddingOnly...)

	primary = ordered[0]
	fallbacks = ordered[1:]
	return primary, fallbacks
}

func isEmbeddingOnlyModel(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "embed")
}
