package model

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Rank picks the strongest model from a list and demotes the rest to fallbacks.
//
// Selection order:
//
//  1. Cloud models (Anthropic Claude, OpenAI GPT/o-series) outrank local Ollama
//     models. The agent works far better against a frontier model when one is
//     wired up.
//  2. Within the cloud tier, models are ranked by a known-quality table —
//     newer/larger first (Opus > Sonnet > Haiku, gpt-5 > gpt-4.1, etc.). Names
//     not in the table fall to the bottom of the cloud tier alphabetically.
//  3. Within the local tier, models are ranked by parameter count parsed from
//     the model tag (e.g. `qwen3.5:9b` → 9, `mixtral:8x7b` → 56, `llama3.2:1b`
//     → 1). Larger first. Untagged or "latest" models are ranked using the
//     average size of their family if known, otherwise treated as unknown.
//
// The fallback list preserves cloud-then-local ordering so a controller using
// LiteLLM's fallback chain still tries cloud models before reaching for local
// ones if the primary fails.
//
// This used to be duplicated between internal/hermes and internal/openclaw,
// where each copy returned `local[0]` — i.e. whatever Ollama listed first.
// In practice that frequently picked llama3.2:1b on hosts that had recently
// pulled it, and a 1B model produces nonsense on the agent's tool-heavy
// system prompt (the typical symptom is the agent parroting its tool list
// back to the user instead of answering "hello"). Fixing the ranking here
// fixes both runtimes at once.
func Rank(models []string) (primary string, fallbacks []string) {
	if len(models) == 0 {
		return "", nil
	}

	var cloud, local []string
	for _, m := range models {
		if IsCloudModel(m) {
			cloud = append(cloud, m)
		} else {
			local = append(local, m)
		}
	}

	sort.Slice(cloud, func(i, j int) bool {
		return cloudRank(cloud[i]) < cloudRank(cloud[j])
	})
	sort.Slice(local, func(i, j int) bool {
		ip := localRank(local[i])
		jp := localRank(local[j])
		if ip != jp {
			return ip > jp
		}
		return local[i] < local[j]
	})

	if len(cloud) > 0 {
		primary = cloud[0]
		fallbacks = append(append([]string{}, cloud[1:]...), local...)
	} else {
		primary = local[0]
		fallbacks = local[1:]
	}
	return primary, fallbacks
}

// IsCloudModel reports whether a model name belongs to a frontier cloud
// provider (Anthropic Claude, OpenAI GPT or o-series). The check is by
// substring/prefix because LiteLLM model ids carry a provider prefix
// (`anthropic/claude-3-5-sonnet-latest`, `openai/gpt-4o`).
func IsCloudModel(name string) bool {
	n := strings.ToLower(stripProviderPrefix(name))
	if strings.Contains(n, "claude") {
		return true
	}
	if strings.HasPrefix(n, "gpt") ||
		strings.HasPrefix(n, "o1") ||
		strings.HasPrefix(n, "o3") ||
		strings.HasPrefix(n, "o4") ||
		strings.HasPrefix(n, "o5") {
		return true
	}
	return false
}

func stripProviderPrefix(name string) string {
	if idx := strings.Index(name, "/"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// cloudRank returns a sort key for cloud model names — lower is better. The
// table lists representative substrings; the first match wins.
func cloudRank(name string) int {
	n := strings.ToLower(stripProviderPrefix(name))
	for i, marker := range cloudPrecedence {
		if strings.Contains(n, marker) {
			return i
		}
	}
	return len(cloudPrecedence) + 1
}

var cloudPrecedence = []string{
	"opus-4-7", "opus-4-6", "opus-4-5", "opus-4", "opus",
	"sonnet-4-7", "sonnet-4-6", "sonnet-4-5", "sonnet-4", "sonnet-3-7", "sonnet",
	"haiku-4-5", "haiku-4", "haiku",
	"gpt-5", "gpt-4.1", "gpt-4o", "gpt-4", "gpt-3.5",
	"o5", "o4", "o3", "o1",
}

// paramSizeRe matches the parameter-count tag in model names. Examples:
//
//	llama3.2:1b           → 1
//	qwen3.5:9b            → 9
//	deepseek-r1:32b       → 32
//	mixtral:8x7b          → 56  (multiplied)
//	qwen3-vl:235b-cloud   → 235
var paramSizeRe = regexp.MustCompile(`(?i)(?::|-)(\d+(?:x\d+)?)b\b`)

// localRank returns the parameter count (in billions) for a local model name.
// Models with no parseable tag fall back to a family-average lookup; truly
// unknown models return 0 (worst). Larger parameter counts → higher rank.
func localRank(name string) int {
	n := strings.ToLower(stripProviderPrefix(name))
	n = strings.TrimSuffix(n, ":latest")

	if m := paramSizeRe.FindStringSubmatch(n); m != nil {
		raw := m[1]
		if x := strings.Index(raw, "x"); x >= 0 {
			a, _ := strconv.Atoi(raw[:x])
			b, _ := strconv.Atoi(raw[x+1:])
			return a * b
		}
		v, _ := strconv.Atoi(raw)
		return v
	}

	// No size in the tag — fall back to a family heuristic. These default
	// values track the family's flagship "latest" tag at the time of writing
	// and can be updated as new releases ship. We try longest prefixes first
	// so `llama3.3` doesn't get the `llama3` default.
	for _, prefix := range untaggedFamilyOrder {
		if strings.HasPrefix(n, prefix) {
			return untaggedFamilyDefaults[prefix]
		}
	}
	return 0
}

// untaggedFamilyDefaults maps a model-family prefix to a typical parameter
// count, used when an Ollama model tag doesn't carry a size. The numbers
// don't have to be exact — the goal is "is this roughly bigger than that
// other model", not a precise sort.
var untaggedFamilyDefaults = map[string]int{
	"qwen3.5":        9,
	"qwen3":          14,
	"qwen2.5":        7,
	"llama3.3":       70,
	"llama3.2":       3,
	"llama3.1":       8,
	"llama3":         8,
	"deepseek-r1":    14,
	"deepseek-coder": 6,
	"deepseek-ocr":   7,
	"mistral":        7,
	"mixtral":        56,
	"phi4":           14,
	"phi3":           3,
	"gemma3":         7,
	"gemma2":         9,
	"command-r":      35,
	"nomic-embed":    0, // embedding model, never pick as agent default
}

// untaggedFamilyOrder lists the keys of untaggedFamilyDefaults sorted by
// descending length so HasPrefix matches the most specific family first
// (e.g. `llama3.3` before `llama3`).
var untaggedFamilyOrder = func() []string {
	keys := make([]string, 0, len(untaggedFamilyDefaults))
	for k := range untaggedFamilyDefaults {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}()
