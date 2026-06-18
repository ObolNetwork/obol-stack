package serviceoffercontroller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// reconcileSkillBundle validates a type=skill offer's bundle ConfigMap and,
// when the artifact checks out, renders the bundle-server children
// (meta ConfigMap + Deployment + Service) in the offer's namespace.
//
// Returns ok=true when the children were applied and the rest of the
// condition ladder (reconcileUpstream → PaymentGateReady → RoutePublished
// → Registered → Ready) should proceed unchanged. Returns ok=false with a
// nil error when the offer is not yet publishable; in that case status
// already carries UpstreamHealthy=False with one of the specific reasons:
//
//   - InvalidSkillSpec      — required spec.skill fields missing (defense
//     in depth behind the CRD's CEL rule)
//   - InvalidSkillUpstream  — spec.upstream does not point at the
//     controller-rendered bundle server. Anti-spoof: a skill offer may
//     only ever advertise its own bundle server, so the sha256 surfaced
//     in the 402 extra can never describe a different upstream.
//   - BundleMissing         — bundle ConfigMap or its binaryData key absent
//   - BundleInvalid         — binaryData is not decodable base64
//   - BundleTooLarge        — compressed bytes exceed MaxSkillBundleBytes
//   - BundleHashMismatch    — sha256 of the bytes != spec.skill.sha256
//
// Errors are only returned for transient API failures (the caller's
// rate-limited requeue handles those).
func (c *Controller) reconcileSkillBundle(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) (bool, error) {
	skill := offer.Spec.Skill
	if skill.Name == "" || skill.Version == "" || skill.SHA256 == "" || skill.BundleConfigMap == "" {
		setCondition(status, "UpstreamHealthy", "False", "InvalidSkillSpec",
			"type=skill offer requires spec.skill.name, .version, .sha256 and .bundleConfigMap")
		return false, nil
	}

	workload := monetizeapi.SkillBundleWorkloadName(offer.Name)
	if offer.Spec.Upstream.Service != workload ||
		offer.EffectiveNamespace() != offer.Namespace ||
		offer.EffectivePort() != skillBundlePort {
		setCondition(status, "UpstreamHealthy", "False", "InvalidSkillUpstream",
			fmt.Sprintf("type=skill offers must use the controller-rendered bundle server %s.%s:%d as upstream", workload, offer.Namespace, skillBundlePort))
		return false, nil
	}

	raw, err := c.configMaps.Namespace(offer.Namespace).Get(ctx, skill.BundleConfigMap, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		setCondition(status, "UpstreamHealthy", "False", "BundleMissing",
			fmt.Sprintf("bundle ConfigMap %s/%s not found", offer.Namespace, skill.BundleConfigMap))
		return false, nil
	}
	if err != nil {
		return false, err
	}

	encoded, found, err := unstructured.NestedString(raw.Object, "binaryData", monetizeapi.SkillBundleKey)
	if err != nil || !found || encoded == "" {
		setCondition(status, "UpstreamHealthy", "False", "BundleMissing",
			fmt.Sprintf("bundle ConfigMap %s/%s has no binaryData[%q]", offer.Namespace, skill.BundleConfigMap, monetizeapi.SkillBundleKey))
		return false, nil
	}

	bundle, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		setCondition(status, "UpstreamHealthy", "False", "BundleInvalid",
			fmt.Sprintf("bundle ConfigMap %s/%s binaryData[%q] is not valid base64: %v", offer.Namespace, skill.BundleConfigMap, monetizeapi.SkillBundleKey, err))
		return false, nil
	}

	if len(bundle) > monetizeapi.MaxSkillBundleBytes {
		setCondition(status, "UpstreamHealthy", "False", "BundleTooLarge",
			fmt.Sprintf("bundle is %d bytes; the cap is %d bytes of compressed artifact", len(bundle), monetizeapi.MaxSkillBundleBytes))
		return false, nil
	}

	sum := sha256.Sum256(bundle)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, skill.SHA256) {
		setCondition(status, "UpstreamHealthy", "False", "BundleHashMismatch",
			fmt.Sprintf("bundle sha256 %s does not match spec.skill.sha256 %s", got, strings.ToLower(skill.SHA256)))
		return false, nil
	}

	meta, err := buildSkillBundleMetaConfigMap(offer)
	if err != nil {
		return false, err
	}
	for _, child := range []*unstructured.Unstructured{
		meta,
		buildSkillBundleDeployment(offer),
		buildSkillBundleService(offer),
	} {
		// applyAgentObject (get-or-create-or-update) rather than the SSA
		// applyObject so the same code path is exercised by the fake
		// dynamic client in unit tests — see the rationale on
		// applyAgentObject. All three kinds are mutable (not in
		// isCreateOnlyKind), so re-reconciles pick up rendered changes.
		if err := c.applyAgentObject(ctx, c.resourceFor(child), child); err != nil {
			setCondition(status, "UpstreamHealthy", "False", "ApplyFailed", err.Error())
			return false, err
		}
	}

	// Children applied. The actual UpstreamHealthy verdict is owned by the
	// shared reconcileUpstream, which health-checks the bundle Service at
	// spec.upstream (http://so-<name>-bundle.<ns>.svc:8080/skill.json), so
	// the gate only opens once the httpd pod really serves the artifact.
	return true, nil
}
