package serviceoffercontroller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestHermesConfigDecision(t *testing.T) {
	desiredYAML := "model: qwen\n"
	desiredHash := fmt.Sprintf("%x", sha256.Sum256([]byte(desiredYAML)))
	otherYAML := "model: other\n"
	otherHash := fmt.Sprintf("%x", sha256.Sum256([]byte(otherYAML)))

	cases := []struct {
		name      string
		live      *unstructured.Unstructured
		wantSkip  bool
		wantDrift bool
	}{
		{
			name:      "live nil (not found) -> first create",
			live:      nil,
			wantSkip:  false,
			wantDrift: false,
		},
		{
			name:      "annotation matches and data hashes to desired -> skip, no drift",
			live:      hermesConfigCM(t, "ns", desiredYAML, desiredHash, "1"),
			wantSkip:  true,
			wantDrift: false,
		},
		{
			name:      "annotation matches but data hand-edited -> skip with drift",
			live:      hermesConfigCM(t, "ns", otherYAML, desiredHash, "1"),
			wantSkip:  true,
			wantDrift: true,
		},
		{
			name:      "annotation does not match desired -> apply",
			live:      hermesConfigCM(t, "ns", desiredYAML, otherHash, "1"),
			wantSkip:  false,
			wantDrift: false,
		},
		{
			name:      "annotation absent (pre-feature migration) -> apply",
			live:      hermesConfigCM(t, "ns", desiredYAML, "", "1"),
			wantSkip:  false,
			wantDrift: false,
		},
		{
			name:      "annotation matches but data key missing -> skip with drift",
			live:      hermesConfigCMNoData(t, "ns", desiredHash, "1"),
			wantSkip:  true,
			wantDrift: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skip, drift := hermesConfigDecision(tc.live, desiredHash)
			if skip != tc.wantSkip || drift != tc.wantDrift {
				t.Fatalf("hermesConfigDecision = (%v, %v), want (%v, %v)",
					skip, drift, tc.wantSkip, tc.wantDrift)
			}
		})
	}
}

func TestReconcileAgent_HermesConfig_SkipWhenHashMatches(t *testing.T) {
	agent := testAgentForConfigHash("quant", "agent-quant")
	litellmKey := "test-master-key"
	configYAML := renderHermesConfig(agent, litellmKey)
	desiredHash := fmt.Sprintf("%x", sha256.Sum256([]byte(configYAML)))

	const seedRV = "42"
	seeded := hermesConfigCM(t, agent.Namespace, configYAML, desiredHash, seedRV)
	// Pre-label so ownership checks (if any) see controller management.
	seeded.SetLabels(agentLabels(agent.Name))

	c := newProvisioningTestController(t, agent, litellmSecretObject(t, litellmKey), seeded)

	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (provision): %v", err)
	}

	live, err := c.configMaps.Namespace(agent.Namespace).Get(context.Background(), hermesConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get hermes-config: %v", err)
	}
	if got := live.GetResourceVersion(); got != seedRV {
		t.Errorf("resourceVersion = %q, want %q (ConfigMap must not be rewritten)", got, seedRV)
	}
	liveYAML, _, _ := unstructured.NestedString(live.Object, "data", "config.yaml")
	if liveYAML != configYAML {
		t.Errorf("config.yaml rewritten; got %q", liveYAML)
	}

	got := getAgent(t, c, agent.Namespace, agent.Name)
	cond := agentCondition(t, got, agentConditionConfigDrift)
	if cond.Status != "False" || cond.Reason != "InSync" {
		t.Errorf("ConfigDrift = %+v, want False/InSync", cond)
	}
}

func TestReconcileAgent_HermesConfig_AppliesWhenAnnotationStaleOrAbsent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		annotation string // empty = absent
	}{
		{name: "stale annotation", annotation: "deadbeef"},
		{name: "annotation absent", annotation: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := testAgentForConfigHash("quant", "agent-quant")
			litellmKey := "test-master-key"
			configYAML := renderHermesConfig(agent, litellmKey)
			desiredHash := fmt.Sprintf("%x", sha256.Sum256([]byte(configYAML)))

			// Seed outdated content so a successful apply is observable.
			staleContent := "model: stale-placeholder\n"
			seeded := hermesConfigCM(t, agent.Namespace, staleContent, tc.annotation, "7")
			seeded.SetLabels(agentLabels(agent.Name))

			c := newProvisioningTestController(t, agent, litellmSecretObject(t, litellmKey), seeded)

			if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
				t.Fatalf("reconcileAgent (finalizer): %v", err)
			}
			if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
				t.Fatalf("reconcileAgent (provision): %v", err)
			}

			live, err := c.configMaps.Namespace(agent.Namespace).Get(context.Background(), hermesConfigMap, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get hermes-config: %v", err)
			}
			gotHash, _, _ := unstructured.NestedString(live.Object, "metadata", "annotations", hermesConfigHashAnnotation)
			if gotHash != desiredHash {
				t.Errorf("stamped hash = %q, want %q", gotHash, desiredHash)
			}
			liveYAML, _, _ := unstructured.NestedString(live.Object, "data", "config.yaml")
			if liveYAML != configYAML {
				t.Errorf("config.yaml not updated to desired render")
			}

			got := getAgent(t, c, agent.Namespace, agent.Name)
			cond := agentCondition(t, got, agentConditionConfigDrift)
			if cond.Status != "False" || cond.Reason != "InSync" {
				t.Errorf("ConfigDrift = %+v, want False/InSync after apply", cond)
			}
		})
	}
}

func TestReconcileAgent_HermesConfig_DriftDoesNotClobber(t *testing.T) {
	agent := testAgentForConfigHash("quant", "agent-quant")
	litellmKey := "test-master-key"
	configYAML := renderHermesConfig(agent, litellmKey)
	desiredHash := fmt.Sprintf("%x", sha256.Sum256([]byte(configYAML)))

	handEdited := "# operator edit\n" + configYAML + "\nextra: true\n"
	const seedRV = "99"
	seeded := hermesConfigCM(t, agent.Namespace, handEdited, desiredHash, seedRV)
	seeded.SetLabels(agentLabels(agent.Name))

	c := newProvisioningTestController(t, agent, litellmSecretObject(t, litellmKey), seeded)

	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (provision): %v", err)
	}

	live, err := c.configMaps.Namespace(agent.Namespace).Get(context.Background(), hermesConfigMap, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get hermes-config: %v", err)
	}
	if got := live.GetResourceVersion(); got != seedRV {
		t.Errorf("resourceVersion = %q, want %q (must not overwrite out-of-band edit)", got, seedRV)
	}
	liveYAML, _, _ := unstructured.NestedString(live.Object, "data", "config.yaml")
	if liveYAML != handEdited {
		t.Errorf("config.yaml was overwritten; want hand-edited content preserved")
	}

	got := getAgent(t, c, agent.Namespace, agent.Name)
	cond := agentCondition(t, got, agentConditionConfigDrift)
	if cond.Status != "True" || cond.Reason != "OutOfBandEdit" {
		t.Errorf("ConfigDrift = %+v, want True/OutOfBandEdit", cond)
	}
}

func TestBuildAgentConfigMap_StampsHashAnnotation(t *testing.T) {
	agent := testAgentForConfigHash("quant", "agent-quant")
	configYAML := "model: test\n"
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(configYAML)))

	cm := buildAgentConfigMap(agent, configYAML)
	got, _, _ := unstructured.NestedString(cm.Object, "metadata", "annotations", hermesConfigHashAnnotation)
	if got != wantHash {
		t.Errorf("hash annotation = %q, want %q", got, wantHash)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func testAgentForConfigHash(name, namespace string) *monetizeapi.Agent {
	return &monetizeapi.Agent{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "obol.org/v1alpha1",
			Kind:       "Agent",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: 1,
		},
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Model:   "qwen3.5:9b",
			Skills:  []string{"addresses"},
		},
	}
}

func hermesConfigCM(t *testing.T, namespace, configYAML, hashAnnotation, resourceVersion string) *unstructured.Unstructured {
	t.Helper()
	meta := map[string]any{
		"name":            hermesConfigMap,
		"namespace":       namespace,
		"resourceVersion": resourceVersion,
	}
	if hashAnnotation != "" {
		meta["annotations"] = map[string]any{
			hermesConfigHashAnnotation: hashAnnotation,
		}
	}
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   meta,
		"data": map[string]any{
			"config.yaml": configYAML,
		},
	})
	return u
}

func hermesConfigCMNoData(t *testing.T, namespace, hashAnnotation, resourceVersion string) *unstructured.Unstructured {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":            hermesConfigMap,
			"namespace":       namespace,
			"resourceVersion": resourceVersion,
			"annotations": map[string]any{
				hermesConfigHashAnnotation: hashAnnotation,
			},
		},
		"data": map[string]any{},
	})
	return u
}
