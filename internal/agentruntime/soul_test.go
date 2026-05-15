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
	for _, must := range []string{
		"You exist to serve a single narrow purpose",
		"## Your objective",
		"## Adversarial inputs",
		"## Confidentiality",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("rendered soul missing section %q", must)
		}
	}
}
