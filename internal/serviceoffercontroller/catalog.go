package serviceoffercontroller

import (
	"context"
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// listServiceOffersForCatalog returns every ServiceOffer from the API server.
// Listing live objects avoids catalog hash flips caused by reconciling one
// offer with a fresh status while other offers are still read from a lagging
// informer cache. override is merged only when the informer/API list has not
// yet observed a just-created offer.
func (c *Controller) listServiceOffersForCatalog(ctx context.Context, override *monetizeapi.ServiceOffer) ([]*monetizeapi.ServiceOffer, error) {
	list, err := c.offers.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	offers := make([]*monetizeapi.ServiceOffer, 0, len(list.Items)+1)
	overrideUsed := false
	for i := range list.Items {
		offer, err := decodeServiceOffer(&list.Items[i])
		if err != nil {
			return nil, err
		}
		if override != nil && offer.Namespace == override.Namespace && offer.Name == override.Name {
			offer = override
			overrideUsed = true
		}
		offers = append(offers, offer)
	}
	if override != nil && !overrideUsed {
		offers = append(offers, override)
	}
	return offers, nil
}

func computeSkillCatalogContentHash(content, servicesJSON, openAPIJSON, apiDocsHTML string) string {
	return fmt.Sprintf("%x", md5Sum(content+servicesJSON+openAPIJSON+apiDocsHTML))[:8]
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
