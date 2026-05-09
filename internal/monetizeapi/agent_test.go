package monetizeapi

import "testing"

func TestAgentEffectiveRuntime_DefaultsToHermes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty defaults to hermes", "", AgentRuntimeHermes},
		{"explicit hermes", AgentRuntimeHermes, AgentRuntimeHermes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{Spec: AgentSpec{Runtime: tc.in}}
			if got := a.EffectiveRuntime(); got != tc.want {
				t.Errorf("EffectiveRuntime() = %q, want %q", got, tc.want)
			}
		})
	}
}

// EffectiveModel encodes the controller's pinning sequence:
// spec.model wins; if unset, fall back to status.pinnedModel; else "" so the
// reconciler knows it needs to do the first-time top-of-rank pick.
func TestAgentEffectiveModel_PinningSequence(t *testing.T) {
	cases := []struct {
		name      string
		spec      string
		statusPin string
		want      string
	}{
		{"both empty signals first reconcile", "", "", ""},
		{"status-only is the post-pin steady state", "", "qwen3.5:9b", "qwen3.5:9b"},
		{"spec wins over status", "qwen3.5:35b", "qwen3.5:9b", "qwen3.5:35b"},
		{"spec set, status empty (just before first reconcile finishes)", "qwen3.5:35b", "", "qwen3.5:35b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{
				Spec:   AgentSpec{Model: tc.spec},
				Status: AgentStatus{PinnedModel: tc.statusPin},
			}
			if got := a.EffectiveModel(); got != tc.want {
				t.Errorf("EffectiveModel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgentIsReady(t *testing.T) {
	cases := []struct {
		name  string
		phase string
		want  bool
	}{
		{"ready", AgentPhaseReady, true},
		{"pending", AgentPhasePending, false},
		{"provisioning", AgentPhaseProvisioning, false},
		{"failed", AgentPhaseFailed, false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Agent{Status: AgentStatus{Phase: tc.phase}}
			if got := a.IsReady(); got != tc.want {
				t.Errorf("IsReady() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAgentGVR_MatchesCRD(t *testing.T) {
	if AgentGVR.Group != "obol.org" {
		t.Errorf("AgentGVR.Group = %q, want obol.org", AgentGVR.Group)
	}
	if AgentGVR.Version != "v1alpha1" {
		t.Errorf("AgentGVR.Version = %q, want v1alpha1", AgentGVR.Version)
	}
	if AgentGVR.Resource != "agents" {
		t.Errorf("AgentGVR.Resource = %q, want agents", AgentGVR.Resource)
	}
}
