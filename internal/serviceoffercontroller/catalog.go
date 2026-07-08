package serviceoffercontroller

import (
	"context"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// listServiceOffersForCatalog returns every ServiceOffer from the API server.
// A live list is the single source of truth for catalog rendering so parallel
// offer reconciles do not mix one freshly-updated status with stale informer
// copies. override is appended only when a just-created offer is not yet
// visible in the list response.
func (c *Controller) listServiceOffersForCatalog(ctx context.Context, override *monetizeapi.ServiceOffer) ([]*monetizeapi.ServiceOffer, error) {
	list, err := c.offers.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	offers := make([]*monetizeapi.ServiceOffer, 0, len(list.Items)+1)
	seen := make(map[string]struct{}, len(list.Items))
	for i := range list.Items {
		offer, err := decodeServiceOffer(&list.Items[i])
		if err != nil {
			return nil, err
		}
		seen[offer.Namespace+"/"+offer.Name] = struct{}{}
		offers = append(offers, offer)
	}
	if override != nil {
		if _, ok := seen[override.Namespace+"/"+override.Name]; !ok {
			offers = append(offers, override)
		}
	}
	return offers, nil
}

func skillCatalogContentMatches(cm *unstructured.Unstructured, content, servicesJSON, openAPIJSON, apiDocsHTML string, bundles []offerBundleFile) bool {
	if cm == nil {
		return false
	}
	data, _, _ := unstructured.NestedStringMap(cm.Object, "data")
	if data == nil {
		return false
	}
	if data["skill.md"] != content ||
		data["services.json"] != servicesJSON ||
		data["openapi.json"] != openAPIJSON ||
		data["api.html"] != apiDocsHTML {
		return false
	}
	// Per-offer bundles: every expected file present + identical, and no
	// stale bundle keys lingering from removed hostname offers.
	expected := make(map[string]string, len(bundles))
	for _, f := range bundles {
		expected[f.Key] = f.Content
	}
	for key, content := range expected {
		if data[key] != content {
			return false
		}
	}
	for key := range data {
		if strings.HasPrefix(key, "offer_") {
			if _, ok := expected[key]; !ok {
				return false
			}
		}
	}
	return true
}

func (c *Controller) skillCatalogContentUnchanged(ctx context.Context, content, servicesJSON, openAPIJSON, apiDocsHTML string, bundles []offerBundleFile) (bool, error) {
	cm, err := c.configMaps.Namespace(skillCatalogNamespace).Get(ctx, skillCatalogConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return skillCatalogContentMatches(cm, content, servicesJSON, openAPIJSON, apiDocsHTML, bundles), nil
}

func computeSkillCatalogContentHash(content, servicesJSON, openAPIJSON, apiDocsHTML string, bundles []offerBundleFile) string {
	return fmt.Sprintf("%x", md5Sum(content+servicesJSON+openAPIJSON+apiDocsHTML+bundleDigestInput(bundles)))[:8]
}

func skillCatalogDeployedContentHash(deployment *unstructured.Unstructured) string {
	if deployment == nil {
		return ""
	}
	hash, _, _ := unstructured.NestedString(deployment.Object, "spec", "template", "metadata", "annotations", "obol.org/content-hash")
	return hash
}

func (c *Controller) skillCatalogDeployedContentHash(ctx context.Context) (string, error) {
	deployment, err := c.deployments.Namespace(skillCatalogNamespace).Get(ctx, skillCatalogConfigMapName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return skillCatalogDeployedContentHash(deployment), nil
}
