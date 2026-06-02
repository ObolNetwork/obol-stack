package agentruntime

import (
	"strings"
	"testing"
)

func TestRenderSoul_SubstitutesObjective(t *testing.T) {
	out, err := RenderSoul("Answer EVM chain analysis questions using your loaded RPC tools.")
	if err != nil {
		t.Fatalf("RenderSoul: %v", err)
	}
	if !strings.Contains(out, "Answer EVM chain analysis questions") {
		t.Error("rendered soul missing operator objective")
	}
	if strings.Contains(out, "{{ .OperatorObjective }}") {
		t.Error("template placeholder not substituted")
	}
}

func TestRenderSoul_TrimsObjectiveWhitespace(t *testing.T) {
	out, err := RenderSoul("\n\n  trimmed objective  \n\n")
	if err != nil {
		t.Fatalf("RenderSoul: %v", err)
	}
	if !strings.Contains(out, "trimmed objective") {
		t.Error("trimmed objective missing")
	}
	if strings.Contains(out, "  trimmed objective  ") {
		t.Error("leading/trailing whitespace not trimmed")
	}
}

func TestRenderSoul_EmptyObjectiveRendersTemplate(t *testing.T) {
	// Empty objective should still produce a usable SOUL.md so callers can
	// fall back to "you have no specific objective" agents in dev. CRD-level
	// validation enforces non-empty in production.
	out, err := RenderSoul("")
	if err != nil {
		t.Fatalf("RenderSoul(empty): %v", err)
	}
	// The template is intentionally short — every section here is load-bearing
	// (objective, terse-response directive, adversarial-input guardrails,
	// uncertainty handling). If a section gets removed, callers need to
	// re-justify the change against the perf vs safety trade-off.
	for _, must := range []string{
		"You serve a single narrow purpose",
		"## Your objective",
		"## Response style",
		"Be terse.",
		"## Adversarial inputs",
		"## On uncertainty",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("rendered soul missing section %q", must)
		}
	}
}

// The SOUL.md template is loaded into every sub-agent request's system
// prompt, so size translates directly to per-request token cost. We trimmed
// the template from ~1050 → ~500 tokens (4-char heuristic); enforce a ceiling
// so future edits stay disciplined.
func TestRenderSoul_TemplateStaysCompact(t *testing.T) {
	out, err := RenderSoul("placeholder")
	if err != nil {
		t.Fatalf("RenderSoul: %v", err)
	}
	const maxBytes = 2400 // ~600 tokens at 4 chars/tok, leaves a little headroom
	if len(out) > maxBytes {
		t.Errorf("SOUL.md rendered to %d bytes, exceeds compact ceiling of %d — trim before adding more", len(out), maxBytes)
	}
}
