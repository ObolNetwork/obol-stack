package serviceoffercontroller

import (
	"context"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// resolveAgentOffer fills in the controller's view of an agent-type
// ServiceOffer. It looks up the referenced Agent CR and, when the agent is
// Ready, populates:
//
//   - status.AgentResolution: the resolved metadata that the route source
//     reads when building RouteRules. Surfaced in the 402 response's
//     accepts[].extra so buyers see model + skills + runtime.
//   - offer.Spec.Upstream (in the in-memory copy only — never written
//     back): synthesised so the existing reconcileUpstream/reconcileModel
//     paths Just Work without further branching.
//   - offer.Spec.Model.Name (in-memory only): mirrors
//     status.AgentResolution.Model so reconcileModel sets ModelReady.
//
// Returns:
//   - ok=true when the agent is ready and the offer is fully resolved.
//   - ok=false when the agent is missing or not yet ready; the caller
//     should set a "WaitingForAgent" condition and stop further
//     reconciliation work for this pass. resolveAgentOffer leaves status
//     untouched in the not-ready case (apart from clearing a stale
//     AgentResolution) so transient agent flaps don't churn the offer's
//     conditions.
func (c *Controller) resolveAgentOffer(ctx context.Context, offer *monetizeapi.ServiceOffer, status *monetizeapi.ServiceOfferStatus) (ok bool, err error) {
	ref := offer.Spec.Agent.Ref
	if ref.Name == "" || ref.Namespace == "" {
		return false, fmt.Errorf("type=agent offer %s/%s missing spec.agent.ref", offer.Namespace, offer.Name)
	}
	if ref.Namespace != offer.Namespace {
		// Confused-deputy guard: the verifier route source injects the
		// hermes-api-server API_SERVER_KEY from ref.Namespace into the
		// outbound Authorization header. Allowing a cross-namespace ref
		// would let any principal with serviceoffers write in namespace A
		// expose Hermes /api in namespace B as an x402-gated route under
		// attacker-controlled path and payTo, granting paying buyers
		// authenticated proxy access to the victim agent.
		return false, fmt.Errorf("type=agent offer %s/%s: spec.agent.ref.namespace %q must equal offer namespace", offer.Namespace, offer.Name, ref.Namespace)
	}

	raw, err := c.agents.Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		status.AgentResolution = nil
		return false, nil
	}
	if err != nil {
		return false, err
	}

	agent, err := decodeAgent(raw)
	if err != nil {
		return false, err
	}

	if !agent.IsReady() || agent.Status.Endpoint == "" {
		status.AgentResolution = nil
		return false, nil
	}

	model := agent.EffectiveModel()
	resolution := &monetizeapi.ServiceOfferAgentResolution{
		Model:    model,
		Skills:   append([]string(nil), agent.Spec.Skills...),
		Runtime:  agent.EffectiveRuntime(),
		Endpoint: agent.Status.Endpoint,
	}
	status.AgentResolution = resolution

	// Synthesise the upstream + model into the in-memory offer so the rest
	// of the reconcile pipeline (reconcileUpstream, reconcileModel) works
	// without an agent-specific branch. We never write these back to the
	// CRD's spec — that's user input.
	offer.Spec.Upstream = monetizeapi.ServiceOfferUpstream{
		Service:    "hermes",
		Namespace:  ref.Namespace,
		Port:       hermesPort,
		// Hermes API (port hermesPort) serves unauthenticated /health.
		// /api/status is the dashboard probe path on a different port and
		// returns 404 on the API — that used to slip past as "healthy"
		// under the pre-2xx UpstreamHealthy gate.
		HealthPath: hermesAPIPath,
	}
	offer.Spec.Model = monetizeapi.ServiceOfferModel{
		Name:    model,
		Runtime: agent.EffectiveRuntime(),
	}

	return true, nil
}
