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

// RankWithPreference is Rank, but a non-empty preferred model wins over the
// capability ordering as long as it appears in the candidate list. Fallbacks
// are the remaining models in capability order.
//
// Resolution order is therefore: explicit user preference → capability rank
// → input order. This is the contract `obol model prefer` relies on: an
// explicit pick must remain primary across stack restarts and defaults
// refreshes, otherwise rank silently overrides the user every time.
//
// A preferred model that is not in the candidate list is treated as absent
// (stale preference) and the function falls back to plain Rank — empty
// preference is normal and produces the same result.
func RankWithPreference(models []string, preferred string) (primary string, fallbacks []string) {
	if len(models) == 0 {
		return "", nil
	}
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		return Rank(models)
	}
	matchIdx := -1
	for i, m := range models {
		if modelMatchesPreference(m, preferred) {
			matchIdx = i
			break
		}
	}
	if matchIdx < 0 {
		return Rank(models)
	}
	rest := make([]string, 0, len(models)-1)
	rest = append(rest, models[:matchIdx]...)
	rest = append(rest, models[matchIdx+1:]...)
	_, restFallbacks := Rank(rest)
	// Rank returns a non-empty primary for non-empty input; flatten primary +
	// fallbacks back into the fallback list, capability-ordered.
	if rp, _ := Rank(rest); rp != "" {
		out := append([]string{rp}, restFallbacks...)
		return models[matchIdx], out
	}
	return models[matchIdx], restFallbacks
}

// modelMatchesPreference returns true when candidate corresponds to the
// preferred model name, ignoring provider prefix differences. The user can
// type `claude-sonnet-4-6` and match `anthropic/claude-sonnet-4-6` or
// `openai/claude-sonnet-4-6`; equally `qwen3.5:9b` matches itself or any
// `ollama/qwen3.5:9b` variant.
func modelMatchesPreference(candidate, preferred string) bool {
	candidate = strings.TrimSpace(candidate)
	preferred = strings.TrimSpace(preferred)
	if candidate == "" || preferred == "" {
		return false
	}
	if candidate == preferred {
		return true
	}
	cb := stripProviderPrefix(candidate)
	pb := stripProviderPrefix(preferred)
	return cb == pb
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

// stripProviderPrefix is an internal helper for ranking only. It is NOT
// exported and MUST NOT be used to mutate model identifiers that the agent
// will pass back to LiteLLM on chat-completion calls.
//
// LiteLLM `model_name` is bare (no provider prefix) — see the contract
// documented on AddCustomEndpoint and buildModelEntries. The only place a
// `provider/model` shape can sneak in is wildcard entries like `anthropic/*`,
// or legacy entries from older releases that namespaced custom endpoints as
// `custom/<name>/<model>`. We strip those here so size/family parsing in
// IsCloudModel/cloudRank/localRank still works on tagged tokens, but the
// caller in Rank() returns the ORIGINAL string — never the stripped form.
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

// paramSizeRe matches the parameter-count tag in model names. Decimals are
// allowed so a `:0.6b` Ollama tag doesn't fall through to the family default
// and accidentally outrank a `:9b` peer. The captured groups are:
//
//	llama3.2:1b           → "1"           → 10  (deci-billions)
//	qwen3.5:9b            → "9"           → 90
//	qwen3:0.6b            → "0.6"         → 6
//	deepseek-r1:32b       → "32"          → 320
//	mixtral:8x7b          → "8x7"         → 560 (8 * 70)
//	qwen3-vl:235b-cloud   → "235"         → 2350
//
// localRank returns deci-billions (parameter count × 10) so 0.5b / 0.6b
// fractional sizes still produce distinct integer ranks without complicating
// the comparator.
var paramSizeRe = regexp.MustCompile(`(?i)(?::|-)(\d+(?:\.\d+)?(?:x\d+(?:\.\d+)?)?)b\b`)

// localRank returns the parameter count (in deci-billions, i.e. params × 10)
// for a local model name. Decimal sizes (`0.5b`, `0.6b`) survive the int
// conversion intact. Untagged Ollama models fall back to a family-average
// lookup; truly unknown models return 0 (worst). Larger → higher rank.
func localRank(name string) int {
	n := strings.ToLower(stripProviderPrefix(name))
	n = strings.TrimSuffix(n, ":latest")

	if m := paramSizeRe.FindStringSubmatch(n); m != nil {
		raw := m[1]
		if x := strings.Index(raw, "x"); x >= 0 {
			a, errA := strconv.ParseFloat(raw[:x], 64)
			b, errB := strconv.ParseFloat(raw[x+1:], 64)
			if errA == nil && errB == nil {
				return int(a * b * 10)
			}
		}
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return int(v * 10)
		}
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
// count expressed in deci-billions (params × 10), so the table shares units
// with localRank's tagged-parsing branch. The numbers don't have to be exact
// — the goal is "is this roughly bigger than that other model", not a
// precise sort. Untagged-model selection is rare; most Ollama users carry
// a size in the tag.
var untaggedFamilyDefaults = map[string]int{
	"qwen3.5":        90,
	"qwen3":          140,
	"qwen2.5":        70,
	"llama3.3":       700,
	"llama3.2":       30,
	"llama3.1":       80,
	"llama3":         80,
	"deepseek-r1":    140,
	"deepseek-coder": 60,
	"deepseek-ocr":   70,
	"mistral":        70,
	"mixtral":        560,
	"phi4":           140,
	"phi3":           30,
	"gemma3":         70,
	"gemma2":         90,
	"command-r":      350,
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
