package serviceoffercontroller

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

const (
	// agentConditionValidated reports whether spec passed admission-time
	// shape checks (runtime supported, skill names well-formed, objective
	// present when wallet is created so the SOUL.md seed is meaningful).
	agentConditionValidated = "Validated"

	// agentConditionProvisioned reports whether the controller has
	// materialised the namespace + signer + Hermes deployment for this
	// Agent.
	agentConditionProvisioned = "Provisioned"

	// agentConditionReady is the rollup the rest of the system reads.
	// True only when both Validated and Provisioned are True.
	agentConditionReady = "Ready"

	// agentFinalizer keeps the CR around until the controller has had a
	// chance to tear down per-agent resources (Deployments, PVCs,
	// Secrets, etc.) ahead of the namespace deletion that K8s' GC would
	// otherwise drive. Without this, deleting an Agent CR can leave
	// orphan resources running until the user notices.
	agentFinalizer = "agents.obol.org/finalizer"
)

var agentReadinessRequeueDelay = 5 * time.Second

func (c *Controller) enqueueAgent(obj any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("serviceoffer-controller: build agent queue key: %v", err)
		return
	}
	c.agentQueue.Add(key)
}

// enqueueOffersFromAgent re-queues every ServiceOffer whose
// spec.agent.ref points at the given Agent. Without this, an Agent
// status change (e.g. status.pinnedModel after the user edits the
// Agent's spec.model) would not propagate into the offer's
// status.agentResolution — the offer reconciler only runs when the
// offer itself changes. Mirrors enqueueOfferFromRegistration.
func (c *Controller) enqueueOffersFromAgent(obj any) {
	u := asUnstructured(obj)
	if u == nil {
		return
	}
	agentNS := u.GetNamespace()
	agentName := u.GetName()
	for _, item := range c.offerInformer.GetStore().List() {
		ou := asUnstructured(item)
		if ou == nil {
			continue
		}
		offer, err := decodeServiceOffer(ou)
		if err != nil {
			log.Printf("serviceoffer-controller: decode offer for agent fan-out: %v", err)
			continue
		}
		if !offer.IsAgent() {
			continue
		}
		ref := offer.Spec.Agent.Ref
		if ref.Name != agentName || ref.Namespace != agentNS {
			continue
		}
		c.offerQueue.Add(offer.Namespace + "/" + offer.Name)
	}
}

func (c *Controller) processNextAgent(ctx context.Context) bool {
	key, shutdown := c.agentQueue.Get()
	if shutdown {
		return false
	}
	defer c.agentQueue.Done(key)

	if err := c.reconcileAgent(ctx, key); err != nil {
		log.Printf("serviceoffer-controller: reconcile agent %s: %v", key, err)
		c.agentQueue.AddRateLimited(key)
		return true
	}

	c.agentQueue.Forget(key)
	return true
}

// reconcileAgent drives an Agent CR to Ready by validating spec shape,
// applying the K8s primitives that back the runtime (namespace, SA, PVC,
// ConfigMap, Secret, Deployment, Service) via server-side apply, and
// reflecting the Deployment's actual readiness back into status. Both
// status.PinnedModel (the actual model in use) and status.Endpoint (the
// in-cluster URL) are populated once we have enough information; they're
// what the ServiceOffer agent-resolver reads later.
//
// Optional remote-signer provisioning (when wallet.create=true) is not
// landed yet — `WalletPending` is surfaced as a condition until that
// follow-up arrives, but the rest of the pipeline runs.
func (c *Controller) reconcileAgent(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	raw, err := c.agents.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	agent, err := decodeAgent(raw)
	if err != nil {
		return err
	}

	if agent.DeletionTimestamp != nil {
		// Mid-deletion: tear down per-agent resources before letting GC
		// take the CR. Idempotent — re-running is safe.
		if !hasFinalizer(raw, agentFinalizer) {
			return nil
		}
		if err := c.tearDownAgent(ctx, agent); err != nil {
			return err
		}
		return c.removeAgentFinalizer(ctx, raw)
	}

	// Pin the finalizer on first sight so subsequent deletes go through
	// the cleanup path above. Re-runs are no-ops because addAgentFinalizer
	// short-circuits when the finalizer is already present.
	if !hasFinalizer(raw, agentFinalizer) {
		return c.addAgentFinalizer(ctx, raw)
	}

	status := agent.Status
	status.ObservedGeneration = agent.Generation

	if reason, message, ok := validateAgentSpec(agent); !ok {
		setAgentCondition(&status, agentConditionValidated, "False", reason, message)
		setAgentCondition(&status, agentConditionReady, "False", reason, message)
		status.Phase = monetizeapi.AgentPhaseFailed
		return c.updateAgentStatus(ctx, raw, status)
	}
	setAgentCondition(&status, agentConditionValidated, "True", "SpecValid", "Agent spec passed shape checks")

	// Pin the model on first reconcile. spec.model wins; otherwise we
	// hold whatever was previously pinned. A real "pick top-of-rank from
	// LiteLLM" path is a follow-up — for now an empty model surfaces a
	// clear condition rather than silently picking something arbitrary.
	model := agent.EffectiveModel()
	if model == "" {
		setAgentCondition(&status, agentConditionProvisioned, "False", "ModelUnpinned",
			"spec.model is empty and no previous status.pinnedModel exists; set --model on agent creation")
		setAgentCondition(&status, agentConditionReady, "False", "ModelUnpinned", "Pin a model to provision")
		status.Phase = monetizeapi.AgentPhaseProvisioning
		return c.updateAgentStatus(ctx, raw, status)
	}
	status.PinnedModel = model

	if err := c.provisionAgent(ctx, agent, &status); err != nil {
		setAgentCondition(&status, agentConditionProvisioned, "False", "ProvisionError", truncateMessage(err.Error()))
		setAgentCondition(&status, agentConditionReady, "False", "ProvisionError", truncateMessage(err.Error()))
		status.Phase = monetizeapi.AgentPhaseProvisioning
		_ = c.updateAgentStatus(ctx, raw, status)
		return err
	}

	// Optional: provision a per-namespace remote-signer when the operator
	// asked for it at agent-creation time. Failure here is recorded as a
	// WalletError condition but does not roll back Hermes provisioning —
	// the agent can still serve cached / deterministic answers without a
	// signing wallet. Operators flip wallet.create=false then re-run if
	// they need to abandon the keystore.
	if agent.Spec.Wallet.Create {
		address, walletErr := c.ensureAgentWallet(ctx, agent)
		if walletErr != nil {
			setAgentCondition(&status, agentConditionReady, "False", "WalletError", truncateMessage(walletErr.Error()))
			status.Phase = monetizeapi.AgentPhaseProvisioning
			_ = c.updateAgentStatus(ctx, raw, status)
			return walletErr
		}
		status.WalletAddress = address
	}

	// Provisioning succeeded; readiness mirrors the Deployment's actual
	// state. Endpoint is deterministic from the Service so callers can
	// rely on it before the pod becomes Ready.
	status.Endpoint = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", hermesServiceName, agent.Namespace, hermesPort)

	deploymentReady, err := c.isAgentDeploymentReady(ctx, agent.Namespace)
	if err != nil {
		return err
	}

	if deploymentReady {
		setAgentCondition(&status, agentConditionProvisioned, "True", "Provisioned", "Hermes deployment reports ready replicas")
		if agent.Spec.Wallet.Create && status.WalletAddress == "" {
			// ensureAgentWallet ran but didn't produce an address. Defer
			// to the next reconcile rather than flipping Ready=True with
			// a missing wallet — buyers will treat agentWallet=true as
			// load-bearing.
			setAgentCondition(&status, agentConditionReady, "False", "WaitingForWallet", "wallet.create=true but status.walletAddress is unset")
			status.Phase = monetizeapi.AgentPhaseProvisioning
		} else {
			setAgentCondition(&status, agentConditionReady, "True", "Reconciled", "Agent is serving on the cluster endpoint")
			status.Phase = monetizeapi.AgentPhaseReady
		}
	} else {
		setAgentCondition(&status, agentConditionProvisioned, "False", "WaitingForDeployment", "Hermes deployment has no ready replicas yet")
		setAgentCondition(&status, agentConditionReady, "False", "WaitingForDeployment", "Hermes deployment has no ready replicas yet")
		status.Phase = monetizeapi.AgentPhaseProvisioning
		if err := c.updateAgentStatus(ctx, raw, status); err != nil {
			return err
		}
		c.requeueAgentAfter(key, agentReadinessRequeueDelay)
		return nil
	}

	return c.updateAgentStatus(ctx, raw, status)
}

func (c *Controller) requeueAgentAfter(key string, delay time.Duration) {
	if c.agentQueue == nil {
		return
	}
	c.agentQueue.AddAfter(key, delay)
}

// validateAgentSpec returns ok=false with a short reason+message when the
// CR cannot be reconciled regardless of cluster state. CRD-level OpenAPI
// validation already rejects most malformed specs at admission; this is a
// runtime backstop for shape invariants OpenAPI can't express.
func validateAgentSpec(agent *monetizeapi.Agent) (reason, message string, ok bool) {
	runtime := agent.EffectiveRuntime()
	if runtime != monetizeapi.AgentRuntimeHermes {
		return "UnsupportedRuntime", fmt.Sprintf("runtime %q is not supported (only %q)", runtime, monetizeapi.AgentRuntimeHermes), false
	}

	for _, skill := range agent.Spec.Skills {
		if strings.TrimSpace(skill) == "" {
			return "InvalidSkillEntry", "spec.skills contains an empty entry", false
		}
	}

	// Objective is optional at the CRD level (defaults to a neutral SOUL.md
	// when empty), so we don't reject on its absence here.
	return "", "", true
}

func decodeAgent(raw *unstructured.Unstructured) (*monetizeapi.Agent, error) {
	var agent monetizeapi.Agent
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &agent); err != nil {
		return nil, err
	}
	return &agent, nil
}

func (c *Controller) updateAgentStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.AgentStatus) error {
	patched := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	if existing, found := patched.Object["status"]; found && equality.Semantic.DeepEqual(existing, statusObject) {
		return nil
	}
	patched.Object["status"] = statusObject
	_, err = c.agents.Namespace(patched.GetNamespace()).UpdateStatus(ctx, patched, metav1.UpdateOptions{})
	return err
}

// provisionAgent applies the K8s primitives that back an Agent. It
// preserves an existing API server token (so probe credentials don't
// rotate on every reconcile) and pulls the LiteLLM master key from the
// shared cluster Secret so sub-agents can route inference. Resources
// are server-side-applied with the controller's field manager so
// repeated reconciles converge instead of fighting each other.
func (c *Controller) provisionAgent(ctx context.Context, agent *monetizeapi.Agent, status *monetizeapi.AgentStatus) error {
	apiKey, err := c.ensureAgentAPIKey(ctx, agent)
	if err != nil {
		return fmt.Errorf("ensure API key: %w", err)
	}

	litellmKey, err := c.readLiteLLMMasterKey(ctx)
	if err != nil {
		// Surface the cause via status — typical reasons are litellm-secrets
		// not yet provisioned (early in `obol stack up`) or an unexpected
		// schema change.
		return fmt.Errorf("read litellm master key: %w", err)
	}

	manifests, err := agentManifests(agent, litellmKey, apiKey)
	if err != nil {
		return err
	}

	for _, m := range manifests {
		if err := c.applyAgentObject(ctx, c.resourceFor(m), m); err != nil {
			return fmt.Errorf("apply %s/%s %s: %w", m.GetKind(), m.GetName(), m.GetNamespace(), err)
		}
	}
	return nil
}

// hasFinalizer reports whether the given raw CR carries the named
// finalizer. Mirrors the slices.Contains pattern the offer reconciler
// uses, but in unstructured-land where each finalizer is an `any`.
func hasFinalizer(raw *unstructured.Unstructured, name string) bool {
	for _, f := range raw.GetFinalizers() {
		if f == name {
			return true
		}
	}
	return false
}

// addAgentFinalizer puts the agent finalizer on the CR. Used the first
// time we see a new Agent so the eventual delete goes through the
// teardown path. Returns nil rather than treating "already present" as
// an error because concurrent reconciles can race here.
func (c *Controller) addAgentFinalizer(ctx context.Context, raw *unstructured.Unstructured) error {
	patched := raw.DeepCopy()
	current := patched.GetFinalizers()
	for _, f := range current {
		if f == agentFinalizer {
			return nil
		}
	}
	patched.SetFinalizers(append(current, agentFinalizer))
	_, err := c.agents.Namespace(patched.GetNamespace()).Update(ctx, patched, metav1.UpdateOptions{})
	return err
}

// removeAgentFinalizer drops the controller's finalizer so K8s GC can
// reap the CR. Called only after teardown reports clean.
func (c *Controller) removeAgentFinalizer(ctx context.Context, raw *unstructured.Unstructured) error {
	patched := raw.DeepCopy()
	finalizers := patched.GetFinalizers()
	out := finalizers[:0]
	for _, f := range finalizers {
		if f != agentFinalizer {
			out = append(out, f)
		}
	}
	patched.SetFinalizers(out)
	_, err := c.agents.Namespace(patched.GetNamespace()).Update(ctx, patched, metav1.UpdateOptions{})
	return err
}

// tearDownAgent removes the per-agent resources the controller created.
// Order matters: Service before Deployment before PVC + Secrets, so
// in-flight requests don't keep hitting a dead pod and so PVC binding
// has time to release. The keystore Secret is last because deleting it
// while remote-signer is still alive would cause noisy mount errors.
//
// Idempotent: NotFound responses from the apiserver are treated as
// success, so re-runs after a partial sweep complete cleanly.
func (c *Controller) tearDownAgent(ctx context.Context, agent *monetizeapi.Agent) error {
	type victim struct {
		gvr      string
		resource dynamic.NamespaceableResourceInterface
		name     string
	}
	dynNs := func(r dynamic.NamespaceableResourceInterface) dynamic.ResourceInterface {
		return r.Namespace(agent.Namespace)
	}

	steps := []struct {
		label    string
		resource dynamic.ResourceInterface
		name     string
	}{
		{"Service/hermes", dynNs(c.services), hermesServiceName},
		{"Service/remote-signer", dynNs(c.services), remoteSignerName},
		{"Deployment/hermes", dynNs(c.deployments), hermesServiceName},
		{"Deployment/remote-signer", dynNs(c.deployments), remoteSignerName},
		{"ConfigMap/hermes-config", dynNs(c.configMaps), hermesConfigMap},
		{"PVC/hermes-data", dynNs(c.client.Resource(monetizeapi.PVCGVR)), hermesDataPVC},
		{"Secret/hermes-api-server", dynNs(c.client.Resource(monetizeapi.SecretGVR)), hermesAPISecret},
		{"Secret/remote-signer-keystore", dynNs(c.client.Resource(monetizeapi.SecretGVR)), remoteSignerSecretName},
		{"ServiceAccount/hermes", dynNs(c.client.Resource(monetizeapi.ServiceAccountGVR)), hermesServiceName},
	}
	for _, s := range steps {
		if err := c.deleteAgentChild(ctx, agent, s.label, s.resource, s.name); err != nil {
			return err
		}
	}

	// Namespace itself is left for the operator (or `obol agent delete`
	// host-side cleanup) to remove. Deleting the namespace from the
	// controller would race with any other Agent CRs that might share
	// it — unlikely under our current naming, but cheap to be careful.
	_ = victim{} // keep the local type referenced; future kinds slot in here
	return nil
}

func (c *Controller) deleteAgentChild(ctx context.Context, agent *monetizeapi.Agent, label string, resource dynamic.ResourceInterface, name string) error {
	child, err := resource.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get %s before delete: %w", label, err)
	}
	if !agentOwnsChild(agent, child) {
		log.Printf("serviceoffer-controller: skip deleting %s for Agent %s/%s: child lacks matching controller ownership labels", label, agent.Namespace, agent.Name)
		return nil
	}
	if err := resource.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %s: %w", label, err)
	}
	return nil
}

func agentOwnsChild(agent *monetizeapi.Agent, child *unstructured.Unstructured) bool {
	labels := child.GetLabels()
	if labels["app.kubernetes.io/managed-by"] != "serviceoffer-controller" {
		return false
	}
	return labels["obol.org/agent"] == agent.Name || labels["app.kubernetes.io/instance"] == agent.Name
}

// applyAgentObject is a get-or-create-or-update wrapper used for agent
// provisioning. The existing applyObject in controller.go relies on
// server-side apply (ApplyPatchType), which the fake dynamic client used
// in tests does not auto-create on. This explicit path is also more
// predictable — we know exactly what mutation runs against each
// resource — at the cost of slightly more code than a single patch call.
func (c *Controller) applyAgentObject(ctx context.Context, resource dynamic.ResourceInterface, desired *unstructured.Unstructured) error {
	existing, err := resource.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := resource.Create(ctx, desired, metav1.CreateOptions{
			FieldManager: controllerFieldManager,
		})
		return err
	}
	if err != nil {
		return err
	}

	// Some kinds we only ever create — never reshape after the fact.
	// Namespaces are the canonical example: the host CLI creates the
	// namespace before applying the Agent CR (since the CR is namespaced
	// and can't land otherwise), and the controller's RBAC intentionally
	// only grants `create`, not `update`, to keep the blast radius small.
	// Treat existence as success for these and move on.
	if isCreateOnlyKind(desired.GetKind()) {
		return nil
	}

	// Preserve resourceVersion + uid so Update doesn't 409. Spec/data is
	// rewritten in full from `desired`; that's the controller's contract.
	desired.SetResourceVersion(existing.GetResourceVersion())
	desired.SetUID(existing.GetUID())
	if existing.GetCreationTimestamp().Time.Unix() != 0 {
		desired.SetCreationTimestamp(existing.GetCreationTimestamp())
	}
	_, err = resource.Update(ctx, desired, metav1.UpdateOptions{
		FieldManager: controllerFieldManager,
	})
	return err
}

// isCreateOnlyKind returns true for kinds that the controller refuses to
// Update on subsequent reconciles. Reasons vary by kind:
//   - Namespace: Update would require broader RBAC than we want.
//   - PersistentVolumeClaim: immutable spec fields reject a wholesale Update.
//   - Secret: the controller never mutates an agent Secret in place — the API
//     token is preserved across reconciles, not rotated (see ensureAgentAPIKey),
//     and the wallet keystore is minted once and never reshaped (see
//     ensureSignerKeystore). Treating Secrets as create-only lets the
//     ClusterRole drop the `update`/`patch` verbs, shrinking the Secret write
//     surface to create + delete.
//
// Other mutable kinds (ConfigMap, Deployment, Service ports, ServiceAccount)
// keep going through the normal Update path so reconciles still pick up
// rendered changes.
func isCreateOnlyKind(kind string) bool {
	switch kind {
	case "Namespace", "PersistentVolumeClaim", "Secret":
		return true
	}
	return false
}

// resourceFor maps an unstructured.Unstructured to the dynamic resource
// client for its GVK. Centralising the mapping keeps provisionAgent's
// loop generic and means a future kind addition is a one-line case.
func (c *Controller) resourceFor(u *unstructured.Unstructured) dynamic.ResourceInterface {
	switch u.GetKind() {
	case "Namespace":
		return c.client.Resource(monetizeapi.NamespaceGVR)
	case "ServiceAccount":
		return c.client.Resource(monetizeapi.ServiceAccountGVR).Namespace(u.GetNamespace())
	case "Service":
		return c.services.Namespace(u.GetNamespace())
	case "ConfigMap":
		return c.configMaps.Namespace(u.GetNamespace())
	case "Secret":
		return c.client.Resource(monetizeapi.SecretGVR).Namespace(u.GetNamespace())
	case "PersistentVolumeClaim":
		return c.client.Resource(monetizeapi.PVCGVR).Namespace(u.GetNamespace())
	case "Deployment":
		return c.deployments.Namespace(u.GetNamespace())
	case "NetworkPolicy":
		return c.client.Resource(monetizeapi.NetworkPolicyGVR).Namespace(u.GetNamespace())
	default:
		// Unknown kinds shouldn't reach this path — agentManifests is
		// closed-set. Fall through to a sensible default so the eventual
		// apply call surfaces a clear error rather than panicking.
		return c.client.Resource(monetizeapi.ConfigMapGVR).Namespace(u.GetNamespace())
	}
}

// ensureAgentAPIKey reads the existing per-agent API server Secret if
// one is already in place; otherwise it generates a fresh token. The
// caller bakes the result into the rendered Secret manifest, so a re-
// applied Secret with the same key is a no-op rather than a rotation.
func (c *Controller) ensureAgentAPIKey(ctx context.Context, agent *monetizeapi.Agent) (string, error) {
	secret, err := c.client.Resource(monetizeapi.SecretGVR).Namespace(agent.Namespace).Get(ctx, hermesAPISecret, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return generateAPIKey()
	case err != nil:
		return "", err
	}

	encoded, found, err := unstructured.NestedString(secret.Object, "data", "API_SERVER_KEY")
	if err == nil && found && encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err == nil && len(decoded) > 0 {
			return string(decoded), nil
		}
	}
	// Secret existed but didn't carry a usable key — mint a fresh one
	// rather than re-applying garbage. Old key remains until apply
	// overwrites it.
	return generateAPIKey()
}

// readLiteLLMMasterKey pulls LITELLM_MASTER_KEY out of the cluster's
// shared litellm-secrets Secret. RBAC for this read is already granted
// (it's the same Secret used by the existing controller paths).
func (c *Controller) readLiteLLMMasterKey(ctx context.Context) (string, error) {
	secret, err := c.client.Resource(monetizeapi.SecretGVR).Namespace("llm").Get(ctx, "litellm-secrets", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	encoded, found, err := unstructured.NestedString(secret.Object, "data", "LITELLM_MASTER_KEY")
	if err != nil {
		return "", err
	}
	if !found || encoded == "" {
		return "", fmt.Errorf("litellm-secrets does not carry LITELLM_MASTER_KEY")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode LITELLM_MASTER_KEY: %w", err)
	}
	return string(decoded), nil
}

// isAgentDeploymentReady reports whether the Hermes Deployment in the
// agent's namespace has at least one ready replica. Used to gate the
// Ready condition; absence of the Deployment (e.g. mid-provision) is
// treated as not-ready, not as an error.
func (c *Controller) isAgentDeploymentReady(ctx context.Context, namespace string) (bool, error) {
	dep, err := c.deployments.Namespace(namespace).Get(ctx, hermesServiceName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ready, found, err := unstructured.NestedInt64(dep.Object, "status", "readyReplicas")
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return ready >= 1, nil
}

// applyObject is the shared server-side apply helper. The serviceoffer
// reconciler has its own near-identical method against ResourceInterface
// — kept separate here because that one lives on Controller via a value
// receiver and we want the same call shape on the agent path without
// touching it.
//
// Uses Patch with ApplyPatchType, the same pattern the existing offer
// reconciler uses (see applyObject in controller.go). Force=true so we
// take ownership of fields when reapplying after an operator-side edit.
//
// (Method name reused intentionally so this file's call sites read the
// same as the rest of the controller; Go resolves them by receiver.)

// setAgentCondition is the AgentStatus equivalent of setCondition in
// render.go. Kept separate from the ServiceOffer flavour because the
// existing helper is typed against ServiceOfferStatus; refactoring both
// onto a shared []Condition helper is a clean follow-up but out of scope
// for the agent reconciler skeleton.
func setAgentCondition(status *monetizeapi.AgentStatus, conditionType, conditionStatus, reason, message string) {
	now := metav1.NewTime(time.Now().UTC())
	for i := range status.Conditions {
		if status.Conditions[i].Type != conditionType {
			continue
		}
		if status.Conditions[i].Status != conditionStatus {
			status.Conditions[i].LastTransitionTime = now
		}
		status.Conditions[i].Status = conditionStatus
		status.Conditions[i].Reason = reason
		status.Conditions[i].Message = message
		if status.Conditions[i].LastTransitionTime.IsZero() {
			status.Conditions[i].LastTransitionTime = now
		}
		return
	}
	status.Conditions = append(status.Conditions, monetizeapi.Condition{
		Type:               conditionType,
		Status:             conditionStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}
