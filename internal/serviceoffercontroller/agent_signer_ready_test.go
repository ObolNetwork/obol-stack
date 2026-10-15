package serviceoffercontroller

import (
	"context"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// preserveDeploymentStatusOnUpdate makes the fake dynamic client behave like
// the apiserver for Deployments, whose status is a subresource: a plain
// Update (as applyAgentObject issues on every reconcile) keeps the stored
// status instead of wiping it.
func preserveDeploymentStatusOnUpdate(t *testing.T, c *Controller) *dynamicfake.FakeDynamicClient {
	t.Helper()
	fakeDyn, ok := c.dynClient.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("dynClient is %T, want *fake.FakeDynamicClient", c.dynClient)
	}
	fakeDyn.PrependReactor("update", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		update, ok := action.(k8stesting.UpdateAction)
		if !ok || update.GetSubresource() != "" {
			return false, nil, nil
		}
		obj, ok := update.GetObject().(*unstructured.Unstructured)
		if !ok {
			return false, nil, nil
		}
		existing, err := fakeDyn.Tracker().Get(monetizeapi.DeploymentGVR, update.GetNamespace(), obj.GetName())
		if err != nil {
			return false, nil, nil //nolint:nilerr // not handled here: the default reactor reports it
		}
		if stored, ok := existing.(*unstructured.Unstructured); ok {
			if status, found := stored.Object["status"]; found {
				obj.Object["status"] = status
			} else {
				delete(obj.Object, "status")
			}
		}
		return false, nil, nil
	})
	return fakeDyn
}

// setDeploymentReadyReplicas stamps status.readyReplicas on a Deployment in
// the fake dynamic client, standing in for the kubelet/deployment controller.
func setDeploymentReadyReplicas(t *testing.T, c *Controller, namespace, name string, ready int64) {
	t.Helper()
	fakeDyn := c.dynClient.(*dynamicfake.FakeDynamicClient)
	obj, err := fakeDyn.Tracker().Get(monetizeapi.DeploymentGVR, namespace, name)
	if err != nil {
		t.Fatalf("get deployment %s/%s: %v", namespace, name, err)
	}
	dep := obj.(*unstructured.Unstructured).DeepCopy()
	if err := unstructured.SetNestedField(dep.Object, ready, "status", "readyReplicas"); err != nil {
		t.Fatal(err)
	}
	if err := fakeDyn.Tracker().Update(monetizeapi.DeploymentGVR, dep, namespace); err != nil {
		t.Fatalf("update deployment %s/%s: %v", namespace, name, err)
	}
}

// TestReconcileAgent_WithWallet_ReadyWaitsForSigner pins that an Agent with
// wallet.create=true is not Ready while only Hermes is up: the remote-signer
// Deployment must also report a ready replica. The wallet address stays
// published throughout (it identifies the existing keystore).
func TestReconcileAgent_WithWallet_ReadyWaitsForSigner(t *testing.T) {
	oldDelay := agentReadinessRequeueDelay
	agentReadinessRequeueDelay = 0
	defer func() { agentReadinessRequeueDelay = oldDelay }()

	const ns, name = "agent-signer", "signer"
	key := ns + "/" + name
	agent := agentWithWallet(t, name, ns, true)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))
	preserveDeploymentStatusOnUpdate(t, c)
	ctx := context.Background()

	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (provisioning): %v", err)
	}
	drainAgentQueue(c)

	// Hermes ready, signer not yet.
	setDeploymentReadyReplicas(t, c, ns, hermesServiceName, 1)
	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (hermes ready): %v", err)
	}
	got := getAgent(t, c, ns, name)
	if got.Status.WalletAddress == "" {
		t.Error("walletAddress should stay populated while waiting for the signer")
	}
	if cond := agentCondition(t, got, agentConditionReady); cond.Status != "False" || cond.Reason != "WaitingForSigner" {
		t.Fatalf("Ready = %s/%s, want False/WaitingForSigner", cond.Status, cond.Reason)
	}
	if cond := agentCondition(t, got, agentConditionProvisioned); cond.Status != "True" {
		t.Errorf("Provisioned = %s/%s, want True once Hermes is ready", cond.Status, cond.Reason)
	}
	if got.Status.Phase != monetizeapi.AgentPhaseProvisioning {
		t.Errorf("phase = %q, want Provisioning", got.Status.Phase)
	}
	if c.agentQueue.Len() == 0 {
		t.Fatal("agent was not requeued while waiting for the signer")
	}
	drainAgentQueue(c)

	// Signer becomes ready: Agent goes Ready.
	setDeploymentReadyReplicas(t, c, ns, remoteSignerName, 1)
	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (signer ready): %v", err)
	}
	got = getAgent(t, c, ns, name)
	if cond := agentCondition(t, got, agentConditionReady); cond.Status != "True" {
		t.Fatalf("Ready = %s/%s (%s), want True", cond.Status, cond.Reason, cond.Message)
	}
	if got.Status.Phase != monetizeapi.AgentPhaseReady {
		t.Errorf("phase = %q, want Ready", got.Status.Phase)
	}
}

// TestReconcileAgent_WithoutWallet_IgnoresSigner pins that agents without a
// wallet do not wait on a remote-signer Deployment that never exists.
func TestReconcileAgent_WithoutWallet_IgnoresSigner(t *testing.T) {
	const ns, name = "agent-nosigner", "nosigner"
	key := ns + "/" + name
	agent := agentWithWallet(t, name, ns, false)
	c := newProvisioningTestController(t, agent, litellmSecretObject(t, "key"))
	preserveDeploymentStatusOnUpdate(t, c)
	ctx := context.Background()

	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (finalizer): %v", err)
	}
	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (provisioning): %v", err)
	}
	setDeploymentReadyReplicas(t, c, ns, hermesServiceName, 1)
	if err := c.reconcileAgent(ctx, key); err != nil {
		t.Fatalf("reconcileAgent (hermes ready): %v", err)
	}
	got := getAgent(t, c, ns, name)
	if cond := agentCondition(t, got, agentConditionReady); cond.Status != "True" {
		t.Fatalf("Ready = %s/%s, want True", cond.Status, cond.Reason)
	}
}

func drainAgentQueue(c *Controller) {
	for c.agentQueue.Len() > 0 {
		key, _ := c.agentQueue.Get()
		c.agentQueue.Done(key)
		c.agentQueue.Forget(key)
	}
}
