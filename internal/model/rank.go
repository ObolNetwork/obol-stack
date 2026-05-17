package model

import (
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

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

// isChatCapableModelName returns true when the model name represents a
// chat-capable model rather than a wildcard catch-all or an embedding-only
// model. The function is the single source of truth for the filter logic used
// by ListChatCapableModels.
//
// Rules:
//  1. Wildcard entries (contain "*") are NOT chat-capable on their own.
//     The "paid/*" catch-all means "route any paid/<name> request to the
//     buyer sidecar" — it is useful only when at least one concrete
//     paid/<model> entry also exists.  Treating the wildcard alone as
//     chat-capable would mask the "no real model configured" case.
//  2. Embedding-only models (name contains "embed") are NOT chat-capable.
//  3. Everything else is considered chat-capable.
func isChatCapableModelName(name string) bool {
	if strings.Contains(name, "*") {
		return false
	}
	if isEmbeddingOnlyModel(name) {
		return false
	}
	return true
}

// ListChatCapableModels reads the current LiteLLM ConfigMap and returns the
// model names that are chat-capable (not wildcards, not embedding-only).
//
// It intentionally does NOT expand wildcard entries to live models — the
// goal is to detect whether at least one concrete, directly-routable chat
// model is present, not to enumerate every model the facilitator might
// expose. If the ConfigMap is absent or unreadable (cluster not yet up,
// first install) the function returns (nil, err) — callers should treat
// that as "no chat models" and emit the warning.
func ListChatCapableModels(cfg *config.Config) ([]string, error) {
	all, err := GetConfiguredModels(cfg)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, name := range all {
		if isChatCapableModelName(name) {
			out = append(out, name)
		}
	}
	return out, nil
}
