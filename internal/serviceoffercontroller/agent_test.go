package serviceoffercontroller

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func TestValidateAgentSpec_Happy(t *testing.T) {
	a := &monetizeapi.Agent{
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Skills:  []string{"addresses", "gas"},
		},
	}
	reason, message, ok := validateAgentSpec(a)
	if !ok {
		t.Errorf("expected ok=true, got reason=%q message=%q", reason, message)
	}
}

func TestValidateAgentSpec_DefaultRuntimeAccepted(t *testing.T) {
	// EffectiveRuntime() falls back to hermes when spec.runtime is empty.
	// The validator should accept that case rather than failing on a missing
	// runtime — CRD-level default handles it but the runtime backstop must
	// agree.
	a := &monetizeapi.Agent{Spec: monetizeapi.AgentSpec{}}
	if _, _, ok := validateAgentSpec(a); !ok {
		t.Error("validator rejected empty-runtime spec; should fall through to hermes default")
	}
}

func TestValidateAgentSpec_RejectsUnknownRuntime(t *testing.T) {
	a := &monetizeapi.Agent{Spec: monetizeapi.AgentSpec{Runtime: "openclaw"}}
	reason, _, ok := validateAgentSpec(a)
	if ok {
		t.Fatal("expected validator to reject unknown runtime")
	}
	if reason != "UnsupportedRuntime" {
		t.Errorf("reason = %q, want UnsupportedRuntime", reason)
	}
}

func TestValidateAgentSpec_RejectsBlankSkillEntry(t *testing.T) {
	a := &monetizeapi.Agent{
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Skills:  []string{"addresses", "  "},
		},
	}
	reason, _, ok := validateAgentSpec(a)
	if ok {
		t.Fatal("expected validator to reject blank skill entry")
	}
	if reason != "InvalidSkillEntry" {
		t.Errorf("reason = %q, want InvalidSkillEntry", reason)
	}
}

func TestSetAgentCondition_AddAndUpdate(t *testing.T) {
	var status monetizeapi.AgentStatus

	setAgentCondition(&status, agentConditionReady, "False", "Initial", "first")
	if len(status.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1", len(status.Conditions))
	}
	first := status.Conditions[0]
	if first.Status != "False" || first.Reason != "Initial" {
		t.Errorf("first set wrong: %+v", first)
	}
	if first.LastTransitionTime.IsZero() {
		t.Error("first set must stamp LastTransitionTime")
	}

	// In-place update keeps the slice length at 1 and overwrites Status/
	// Reason/Message. The timestamp's monotonic behaviour is exercised
	// indirectly through the existing setCondition tests in render_test.go;
	// nanosecond-resolution comparisons inside a single test are flaky.
	setAgentCondition(&status, agentConditionReady, "True", "Done", "ready")
	if len(status.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1 after update (in-place)", len(status.Conditions))
	}
	updated := status.Conditions[0]
	if updated.Status != "True" || updated.Reason != "Done" || updated.Message != "ready" {
		t.Errorf("update wrong: %+v", updated)
	}
}

// TestReconcileAgent_Skeleton covers the step-2c contract: validated spec
// reaches Phase=Provisioning with Validated=True and a Provisioned=False
// reason that explicitly names the missing reconciler. Once step 2d lands,
// this test should be updated (or replaced) to cover the full lifecycle.
func TestReconcileAgent_Skeleton_SetsProvisioningPhase(t *testing.T) {
	agent := &monetizeapi.Agent{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "obol.org/v1alpha1",
			Kind:       "Agent",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "quant",
			Namespace:  "agent-quant",
			Generation: 3,
		},
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Skills:  []string{"addresses", "gas"},
			Wallet:  monetizeapi.AgentWallet{Create: true},
		},
	}
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))

	// Two-pass: finalizer first, then provisioning + status.
	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (status): %v", err)
	}

	got := getAgent(t, c, "agent-quant", "quant")
	if got.Status.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want 3", got.Status.ObservedGeneration)
	}
	if got.Status.Phase != monetizeapi.AgentPhaseProvisioning {
		t.Errorf("phase = %q, want Provisioning", got.Status.Phase)
	}
	if c := agentCondition(t, got, agentConditionValidated); c.Status != "True" {
		t.Errorf("Validated condition = %q, want True", c.Status)
	}
	if c := agentCondition(t, got, agentConditionProvisioned); c.Status != "False" {
		t.Errorf("Provisioned condition = %q, want False (deployment not ready in fake client)", c.Status)
	}
	if c := agentCondition(t, got, agentConditionReady); c.Status != "False" {
		t.Errorf("Ready condition = %q, want False until deployment ready", c.Status)
	}
}

func TestReconcileAgent_HappyPath_ProvisionsAndPins(t *testing.T) {
	agent := &monetizeapi.Agent{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "obol.org/v1alpha1",
			Kind:       "Agent",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:       "quant",
			Namespace:  "agent-quant",
			Generation: 1,
		},
		Spec: monetizeapi.AgentSpec{
			Runtime: "hermes",
			Model:   "qwen3.5:9b",
			Skills:  []string{"addresses"},
		},
	}
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "test-master-key"))

	// First reconcile: pins the finalizer and re-queues. Mirrors the
	// existing serviceoffer reconciler's two-pass admission flow.
	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-quant/quant"); err != nil {
		t.Fatalf("reconcileAgent (provisioning): %v", err)
	}

	got := getAgent(t, c, "agent-quant", "quant")
	if got.Status.PinnedModel != "qwen3.5:9b" {
		t.Errorf("pinnedModel = %q, want qwen3.5:9b", got.Status.PinnedModel)
	}
	if got.Status.Endpoint == "" || !strings.Contains(got.Status.Endpoint, "agent-quant.svc.cluster.local") {
		t.Errorf("endpoint not derived: %q", got.Status.Endpoint)
	}

	// Deployment hasn't reported readyReplicas, so we expect Provisioning,
	// not Ready. The Provisioned=False/WaitingForDeployment combo is the
	// signal that the pod hasn't come up yet — which is a real, expected
	// state for fake-client tests where no kubelet runs.
	if got.Status.Phase != monetizeapi.AgentPhaseProvisioning {
		t.Errorf("phase = %q, want Provisioning (no kubelet in fake client)", got.Status.Phase)
	}
	if c := agentCondition(t, got, agentConditionProvisioned); c.Status != "False" || c.Reason != "WaitingForDeployment" {
		t.Errorf("Provisioned condition = %+v, want False/WaitingForDeployment", c)
	}

	// Verify the manifests landed in the fake client. We don't assert on
	// every field — agent_render_test.go covers shape — but each kind
	// must exist by the namespaced/global address agentManifests uses.
	for _, kind := range []struct{ gvr, ns, name string }{
		{"namespaces", "", "agent-quant"},
		{"serviceaccounts", "agent-quant", "hermes"},
		{"persistentvolumeclaims", "agent-quant", "hermes-data"},
		{"configmaps", "agent-quant", "hermes-config"},
		{"secrets", "agent-quant", "hermes-api-server"},
		{"deployments", "agent-quant", "hermes"},
		{"services", "agent-quant", "hermes"},
	} {
		if !resourceExists(t, c, kind.gvr, kind.ns, kind.name) {
			t.Errorf("%s/%s/%s not applied", kind.gvr, kind.ns, kind.name)
		}
	}
}

func TestReconcileAgent_NoModel_ParksAtProvisioning(t *testing.T) {
	// EffectiveModel returns "" when neither spec nor status carries one.
	// The reconciler must surface that via a clear ModelUnpinned condition
	// rather than try to apply manifests with an empty model — which
	// would either render an invalid Hermes config or pick something
	// arbitrary.
	agent := &monetizeapi.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: "obol.org/v1alpha1", Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: "no-model", Namespace: "agent-no-model"},
		Spec:       monetizeapi.AgentSpec{Runtime: "hermes"},
	}
	c := newProvisioningTestController(t, agent)

	// Two-pass: finalizer first, then status path.
	if err := c.reconcileAgent(context.Background(), "agent-no-model/no-model"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-no-model/no-model"); err != nil {
		t.Fatalf("reconcileAgent (status): %v", err)
	}
	got := getAgent(t, c, "agent-no-model", "no-model")
	if got.Status.Phase != monetizeapi.AgentPhaseProvisioning {
		t.Errorf("phase = %q, want Provisioning", got.Status.Phase)
	}
	if c := agentCondition(t, got, agentConditionProvisioned); c.Reason != "ModelUnpinned" {
		t.Errorf("Provisioned reason = %q, want ModelUnpinned", c.Reason)
	}
}

func TestReconcileAgent_InvalidSpec_FailsAndExplains(t *testing.T) {
	agent := &monetizeapi.Agent{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "obol.org/v1alpha1",
			Kind:       "Agent",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: "agent-broken"},
		Spec:       monetizeapi.AgentSpec{Runtime: "claude-cli"},
	}
	c := newAgentTestController(t, agent)

	// Even validation failures need the finalizer to land first so a
	// later spec correction takes the deletion path through teardown
	// rather than orphaning resources we provisioned mid-recovery.
	if err := c.reconcileAgent(context.Background(), "agent-broken/broken"); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(context.Background(), "agent-broken/broken"); err != nil {
		t.Fatalf("reconcileAgent (status): %v", err)
	}

	got := getAgent(t, c, "agent-broken", "broken")
	if got.Status.Phase != monetizeapi.AgentPhaseFailed {
		t.Errorf("phase = %q, want Failed", got.Status.Phase)
	}
	if c := agentCondition(t, got, agentConditionValidated); c.Status != "False" || c.Reason != "UnsupportedRuntime" {
		t.Errorf("Validated condition = %+v, want False/UnsupportedRuntime", c)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func TestReconcileAgent_DeletionTriggersTeardown(t *testing.T) {
	// Provision an agent first so there's something to tear down.
	agent := agentWithWallet(t, "doomed", "agent-doomed", true)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))

	for i := 0; i < 2; i++ {
		if err := c.reconcileAgent(context.Background(), "agent-doomed/doomed"); err != nil {
			t.Fatalf("reconcileAgent pass %d: %v", i, err)
		}
	}

	// Confirm the resources we'll later assert are gone are present now.
	if !resourceExists(t, c, "deployments", "agent-doomed", hermesServiceName) {
		t.Fatal("Hermes deployment missing pre-delete — provisioning regression")
	}
	if !resourceExists(t, c, "deployments", "agent-doomed", remoteSignerName) {
		t.Fatal("remote-signer deployment missing pre-delete")
	}

	// Stamp a deletionTimestamp on the CR (simulating `kubectl delete agent`).
	raw, err := c.agents.Namespace("agent-doomed").Get(context.Background(), "doomed", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	now := metav1.NewTime(metav1.Now().Time)
	raw.SetDeletionTimestamp(&now)
	if _, err := c.agents.Namespace("agent-doomed").Update(context.Background(), raw, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("stamp deletion: %v", err)
	}

	if err := c.reconcileAgent(context.Background(), "agent-doomed/doomed"); err != nil {
		t.Fatalf("reconcileAgent (deletion): %v", err)
	}

	// Per-agent resources should be gone. Re-running teardown is a no-op
	// thanks to NotFound tolerance, so a second reconcile mustn't error.
	for _, kind := range []struct{ resource, name string }{
		{"deployments", hermesServiceName},
		{"deployments", remoteSignerName},
		{"services", hermesServiceName},
		{"services", remoteSignerName},
		{"persistentvolumeclaims", hermesDataPVC},
		{"secrets", hermesAPISecret},
		{"secrets", remoteSignerSecretName},
	} {
		if resourceExists(t, c, kind.resource, "agent-doomed", kind.name) {
			t.Errorf("%s/%s should be torn down", kind.resource, kind.name)
		}
	}

	// Idempotent re-run — fake client returns NotFound, teardown should
	// shrug and remove the finalizer cleanly.
	if err := c.reconcileAgent(context.Background(), "agent-doomed/doomed"); err != nil {
		t.Errorf("re-run after teardown errored: %v", err)
	}
}

func newAgentTestController(t *testing.T, agents ...*monetizeapi.Agent) *Controller {
	t.Helper()

	objects := make([]runtime.Object, 0, len(agents))
	for _, a := range agents {
		objects = append(objects, mustAgentObject(t, a))
	}

	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			monetizeapi.AgentGVR: "AgentList",
		},
		objects...,
	)

	return &Controller{
		dynClient: dynClient,
		client:    dynClient,
		agents:    dynClient.Resource(monetizeapi.AgentGVR),
	}
}

// newProvisioningTestController builds a Controller with all the GVRs
// that reconcileAgent's provisioning path touches. seedObjects beyond
// the Agents themselves can include the litellm-secrets Secret so the
// LiteLLM master-key read path doesn't fail.
func newProvisioningTestController(t *testing.T, agent *monetizeapi.Agent, seedObjects ...*unstructured.Unstructured) *Controller {
	t.Helper()

	objects := []runtime.Object{mustAgentObject(t, agent)}
	for _, o := range seedObjects {
		objects = append(objects, o)
	}

	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			monetizeapi.AgentGVR:          "AgentList",
			monetizeapi.NamespaceGVR:      "NamespaceList",
			monetizeapi.ServiceAccountGVR: "ServiceAccountList",
			monetizeapi.PVCGVR:            "PersistentVolumeClaimList",
			monetizeapi.ConfigMapGVR:      "ConfigMapList",
			monetizeapi.SecretGVR:         "SecretList",
			monetizeapi.DeploymentGVR:     "DeploymentList",
			monetizeapi.ServiceGVR:        "ServiceList",
		},
		objects...,
	)

	return &Controller{
		dynClient:   dynClient,
		client:      dynClient,
		agents:      dynClient.Resource(monetizeapi.AgentGVR),
		services:    dynClient.Resource(monetizeapi.ServiceGVR),
		configMaps:  dynClient.Resource(monetizeapi.ConfigMapGVR),
		deployments: dynClient.Resource(monetizeapi.DeploymentGVR),
	}
}

// litellmSecretObject builds the cluster's litellm-secrets Secret so
// reconcileAgent's LiteLLM-master-key read path returns a usable value.
// The fake dynamic client requires base64-encoded data exactly like the
// real apiserver returns, so we encode here.
func litellmSecretObject(t *testing.T, masterKey string) *unstructured.Unstructured {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString([]byte(masterKey))
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      "litellm-secrets",
			"namespace": "llm",
		},
		"data": map[string]any{
			"LITELLM_MASTER_KEY": encoded,
		},
	})
	return u
}

// resourceExists checks whether the named object landed in the fake
// dynamic client. Used by the happy-path test to confirm provisioning
// applied each kind.
func resourceExists(t *testing.T, c *Controller, resource, namespace, name string) bool {
	t.Helper()
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: resource}
	if resource == "deployments" {
		gvr = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: resource}
	}
	dyn := c.dynClient.Resource(gvr)
	if namespace != "" {
		_, err := dyn.Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		return err == nil
	}
	_, err := dyn.Get(context.Background(), name, metav1.GetOptions{})
	return err == nil
}

func mustAgentObject(t *testing.T, a *monetizeapi.Agent) *unstructured.Unstructured {
	t.Helper()
	if a.APIVersion == "" {
		a.APIVersion = "obol.org/v1alpha1"
	}
	if a.Kind == "" {
		a.Kind = "Agent"
	}
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(a)
	if err != nil {
		t.Fatalf("to unstructured: %v", err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func getAgent(t *testing.T, c *Controller, namespace, name string) *monetizeapi.Agent {
	t.Helper()
	raw, err := c.dynClient.Resource(monetizeapi.AgentGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get agent %s/%s: %v", namespace, name, err)
	}
	var a monetizeapi.Agent
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &a); err != nil {
		t.Fatalf("decode agent: %v", err)
	}
	return &a
}

func agentCondition(t *testing.T, a *monetizeapi.Agent, condType string) monetizeapi.Condition {
	t.Helper()
	for _, c := range a.Status.Conditions {
		if c.Type == condType {
			return c
		}
	}
	t.Fatalf("missing agent condition %q", condType)
	return monetizeapi.Condition{}
}
