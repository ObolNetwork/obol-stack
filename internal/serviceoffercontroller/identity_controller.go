package serviceoffercontroller

import (
	"context"
	"log"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

type agentIdentityKey struct {
	Namespace string
	Name      string
}

func defaultAgentIdentityKey() agentIdentityKey {
	return agentIdentityKey{
		Namespace: monetizeapi.AgentIdentityDefaultNamespace,
		Name:      monetizeapi.AgentIdentityDefaultName,
	}
}

func (c *Controller) enqueueIdentity(obj any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("serviceoffer-controller: build AgentIdentity queue key: %v", err)
		return
	}
	c.enqueueIdentityKey(key)
}

func (c *Controller) enqueueIdentityKey(key string) {
	if c.identityQueue == nil {
		return
	}
	c.identityQueue.Add(key)
}

func (c *Controller) enqueueAgentIdentityKey(key agentIdentityKey) {
	c.enqueueIdentityKey(key.Namespace + "/" + key.Name)
}

func (c *Controller) enqueueIdentityFromOffer(obj any) {
	c.enqueueAgentIdentityKey(defaultAgentIdentityKey())
}

func (c *Controller) enqueueIdentityFromRegistration(obj any) {
	c.enqueueAgentIdentityKey(defaultAgentIdentityKey())
}

func (c *Controller) processNextIdentity(ctx context.Context) bool {
	key, shutdown := c.identityQueue.Get()
	if shutdown {
		return false
	}
	defer c.identityQueue.Done(key)

	if err := c.reconcileAgentIdentity(ctx, key); err != nil {
		log.Printf("serviceoffer-controller: reconcile AgentIdentity %s: %v", key, err)
		c.identityQueue.AddRateLimited(key)
		return true
	}

	c.identityQueue.Forget(key)
	return true
}

func (c *Controller) reconcileAgentIdentity(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	raw, err := c.agentIdentities.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	identity, err := decodeAgentIdentity(raw)
	if err != nil {
		return err
	}

	identityKey := agentIdentityKey{Namespace: identity.Namespace, Name: identity.Name}
	if !isDefaultIdentityKey(identityKey) || identity.DeletionTimestamp != nil {
		return nil
	}

	return c.reconcileAgentIdentityPublication(ctx, identity, nil)
}

func (c *Controller) reconcileAgentIdentityPublication(ctx context.Context, identity *monetizeapi.AgentIdentity, override *monetizeapi.ServiceOffer) error {
	key := agentIdentityKey{Namespace: identity.Namespace, Name: identity.Name}
	if !isDefaultIdentityKey(key) {
		return nil
	}
	baseURL, err := c.registrationBaseURL(ctx)
	if err != nil {
		return err
	}
	offers, err := c.registrationOffersForIdentity(key, "", "")
	if err != nil {
		return err
	}
	offers = mergeOfferOverride(offers, override)

	document := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: identity,
		Offers:   offers,
		BaseURL:  baseURL,
	})
	documentJSON, contentHash, err := marshalRegistrationDocument(document)
	if err != nil {
		return err
	}
	if err := c.publishAgentIdentityRegistrationResources(ctx, identity, documentJSON, contentHash); err != nil {
		return err
	}

	_, _, err = c.identityRegistrationResourcesReady(ctx, identity)
	return err
}

func (c *Controller) ensureDefaultAgentIdentity(ctx context.Context) error {
	key := defaultAgentIdentityKey()
	if _, _, err := c.ensureAgentIdentityForKey(ctx, key); err != nil {
		return err
	}
	c.enqueueAgentIdentityKey(key)
	return nil
}

func (c *Controller) ensureAgentIdentityForOffer(ctx context.Context, offer *monetizeapi.ServiceOffer) (*unstructured.Unstructured, *monetizeapi.AgentIdentity, error) {
	return c.ensureAgentIdentityForKey(ctx, defaultAgentIdentityKey())
}

func (c *Controller) ensureAgentIdentityForKey(ctx context.Context, key agentIdentityKey) (*unstructured.Unstructured, *monetizeapi.AgentIdentity, error) {
	key = normalizeIdentityKey(key)
	raw, err := c.agentIdentities.Namespace(key.Namespace).Get(ctx, key.Name, metav1.GetOptions{})
	if err == nil {
		identity, decodeErr := decodeAgentIdentity(raw)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		return raw, identity, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, nil, err
	}

	identity := &monetizeapi.AgentIdentity{}
	identity.APIVersion = monetizeapi.Group + "/" + monetizeapi.Version
	identity.Kind = monetizeapi.AgentIdentityKind
	identity.Namespace = key.Namespace
	identity.Name = key.Name
	if isDefaultIdentityKey(key) {
		mergeIdentitySeed(identity, c.seedDefaultAgentIdentity())
	}
	created, err := c.agentIdentities.Namespace(key.Namespace).Create(ctx, agentIdentityToUnstructured(identity), metav1.CreateOptions{
		FieldManager: controllerFieldManager,
	})
	if err != nil {
		return nil, nil, err
	}
	if monetizeapi.HasAgentIdentityRegistrations(identity.Status) {
		if err := c.updateAgentIdentityStatus(ctx, created, identity.Status); err != nil {
			return nil, nil, err
		}
		created, err = c.agentIdentities.Namespace(key.Namespace).Get(ctx, key.Name, metav1.GetOptions{})
		if err != nil {
			return nil, nil, err
		}
	}
	decoded, err := decodeAgentIdentity(created)
	return created, decoded, err
}

func (c *Controller) seedDefaultAgentIdentity() *monetizeapi.AgentIdentity {
	if c.offerInformer != nil {
		offers, err := c.registrationOffersForIdentity(defaultAgentIdentityKey(), "", "")
		if err == nil {
			if seed := SeedIdentityFromOffers(offers); seed != nil {
				return seed
			}
		}
	}
	if c.registrationInformer == nil {
		return nil
	}
	type requestEntry struct {
		request *monetizeapi.RegistrationRequest
		ts      metav1.Time
	}
	entries := []requestEntry{}
	for _, item := range c.registrationInformer.GetStore().List() {
		u := asUnstructured(item)
		if u == nil {
			continue
		}
		request, err := decodeRegistrationRequest(u)
		if err != nil || strings.TrimSpace(request.Spec.Chain) == "" || strings.TrimSpace(request.Status.AgentID) == "" {
			continue
		}
		entries = append(entries, requestEntry{request: request, ts: request.CreationTimestamp})
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		ti := entries[i].ts.Time
		tj := entries[j].ts.Time
		if ti.Equal(tj) {
			left := entries[i].request.Namespace + "/" + entries[i].request.Name
			right := entries[j].request.Namespace + "/" + entries[j].request.Name
			return left < right
		}
		return ti.Before(tj)
	})
	status := monetizeapi.AgentIdentityStatus{}
	for _, entry := range entries {
		request := entry.request
		if monetizeapi.AgentIdentityAgentIDForChain(status, request.Spec.Chain) == "" {
			status = monetizeapi.UpsertAgentIdentityRegistration(status, request.Spec.Chain, request.Status.AgentID)
		}
	}
	if !monetizeapi.HasAgentIdentityRegistrations(status) {
		return nil
	}
	return &monetizeapi.AgentIdentity{Status: status}
}

func mergeIdentitySeed(identity *monetizeapi.AgentIdentity, seed *monetizeapi.AgentIdentity) {
	if identity == nil || seed == nil {
		return
	}
	for _, registration := range seed.Status.Registrations {
		if monetizeapi.AgentIdentityAgentIDForChain(identity.Status, registration.Chain) == "" {
			identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, registration.Chain, registration.AgentID)
		}
	}
}

func (c *Controller) publishAgentIdentityRegistrationResources(ctx context.Context, identity *monetizeapi.AgentIdentity, documentJSON, contentHash string) error {
	if err := c.applyIdentityChildObject(ctx, c.configMaps.Namespace(identity.Namespace), buildAgentIdentityRegistrationConfigMap(identity, documentJSON)); err != nil {
		return err
	}
	if err := c.applyIdentityChildObject(ctx, c.deployments.Namespace(identity.Namespace), buildAgentIdentityRegistrationDeployment(identity, contentHash)); err != nil {
		return err
	}
	if err := c.applyIdentityChildObject(ctx, c.services.Namespace(identity.Namespace), buildAgentIdentityRegistrationService(identity)); err != nil {
		return err
	}
	if err := c.applyIdentityChildObject(ctx, c.httpRoutes.Namespace(identity.Namespace), buildAgentIdentityRegistrationHTTPRoute(identity)); err != nil {
		return err
	}
	log.Printf("serviceoffer-controller: AgentIdentity registration resources published for %s/%s", identity.Namespace, identity.Name)
	return nil
}

func (c *Controller) identityRegistrationResourcesReady(ctx context.Context, identity *monetizeapi.AgentIdentity) (bool, string, error) {
	name := agentIdentityRegistrationName(identity)

	if _, err := c.configMaps.Namespace(identity.Namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		return false, "Waiting for AgentIdentity registration ConfigMap", nil
	} else if err != nil {
		return false, "", err
	}

	deployment, err := c.deployments.Namespace(identity.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, "Waiting for AgentIdentity registration Deployment", nil
	}
	if err != nil {
		return false, "", err
	}
	availableReplicas, _, err := unstructured.NestedInt64(deployment.Object, "status", "availableReplicas")
	if err != nil {
		return false, "", err
	}
	if availableReplicas < 1 {
		return false, "Waiting for AgentIdentity registration Deployment availability", nil
	}

	if _, err := c.services.Namespace(identity.Namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		return false, "Waiting for AgentIdentity registration Service", nil
	} else if err != nil {
		return false, "", err
	}

	route, err := c.httpRoutes.Namespace(identity.Namespace).Get(ctx, agentIdentityRouteName(identity), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, "Waiting for AgentIdentity registration HTTPRoute", nil
	}
	if err != nil {
		return false, "", err
	}
	if !httpRouteAccepted(route) {
		return false, "Waiting for AgentIdentity registration HTTPRoute acceptance", nil
	}

	return true, "", nil
}

func (c *Controller) applyIdentityChildObject(ctx context.Context, resource dynamic.ResourceInterface, desired *unstructured.Unstructured) error {
	_, err := resource.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err := resource.Create(ctx, desired, metav1.CreateOptions{FieldManager: controllerFieldManager})
		return err
	}
	if err != nil {
		return err
	}
	return c.applyObject(ctx, resource, desired)
}

func (c *Controller) updateAgentIdentityStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.AgentIdentityStatus) error {
	patched := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	if existing, found := patched.Object["status"]; found && equality.Semantic.DeepEqual(existing, statusObject) {
		return nil
	}
	patched.Object["status"] = statusObject
	_, err = c.agentIdentities.Namespace(patched.GetNamespace()).UpdateStatus(ctx, patched, metav1.UpdateOptions{})
	return err
}

func decodeAgentIdentity(raw *unstructured.Unstructured) (*monetizeapi.AgentIdentity, error) {
	var identity monetizeapi.AgentIdentity
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

func agentIdentityToUnstructured(identity *monetizeapi.AgentIdentity) *unstructured.Unstructured {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(identity)
	if err != nil {
		return &unstructured.Unstructured{}
	}
	return &unstructured.Unstructured{Object: obj}
}

func normalizeIdentityKey(key agentIdentityKey) agentIdentityKey {
	if strings.TrimSpace(key.Namespace) == "" {
		key.Namespace = monetizeapi.AgentIdentityDefaultNamespace
	}
	if strings.TrimSpace(key.Name) == "" {
		key.Name = monetizeapi.AgentIdentityDefaultName
	}
	return key
}

func isDefaultIdentityKey(key agentIdentityKey) bool {
	key = normalizeIdentityKey(key)
	return key.Namespace == monetizeapi.AgentIdentityDefaultNamespace && key.Name == monetizeapi.AgentIdentityDefaultName
}

func (c *Controller) registrationOffersForIdentity(key agentIdentityKey, excludeNamespace, excludeName string) ([]*monetizeapi.ServiceOffer, error) {
	key = normalizeIdentityKey(key)
	if !isDefaultIdentityKey(key) || c.offerInformer == nil {
		return nil, nil
	}
	var candidates []*monetizeapi.ServiceOffer
	for _, item := range c.offerInformer.GetStore().List() {
		u := asUnstructured(item)
		if u == nil {
			continue
		}
		offer, err := decodeServiceOffer(u)
		if err != nil {
			return nil, err
		}
		if offer.Namespace == excludeNamespace && offer.Name == excludeName {
			continue
		}
		// Draining offers stay in the registration candidate list so
		// the registration document continues to advertise them with
		// available=false until the drain grace period expires.
		if offer.DeletionTimestamp != nil || !offer.Spec.Registration.Enabled {
			continue
		}
		if !isConditionTrue(offer.Status, "UpstreamHealthy") {
			log.Printf("serviceoffer-controller: registration candidate %s/%s has unhealthy upstream", offer.Namespace, offer.Name)
		}
		candidates = append(candidates, offer)
	}
	return candidates, nil
}

func (c *Controller) registrationOwnerForIdentity(key agentIdentityKey) (*monetizeapi.ServiceOffer, error) {
	candidates, err := c.registrationOffersForIdentity(key, "", "")
	if err != nil {
		return nil, err
	}
	return selectRegistrationOwner(candidates), nil
}

func mergeOfferOverride(offers []*monetizeapi.ServiceOffer, override *monetizeapi.ServiceOffer) []*monetizeapi.ServiceOffer {
	if override == nil {
		return offers
	}
	out := make([]*monetizeapi.ServiceOffer, 0, len(offers)+1)
	replaced := false
	for _, offer := range offers {
		if offer != nil && offer.Namespace == override.Namespace && offer.Name == override.Name {
			out = append(out, override)
			replaced = true
			continue
		}
		out = append(out, offer)
	}
	if !replaced {
		out = append(out, override)
	}
	return out
}

func agentIdentityStatusFromRegistration(identity *monetizeapi.AgentIdentity, chain, agentID string) monetizeapi.AgentIdentityStatus {
	status := identity.Status
	if strings.TrimSpace(agentID) != "" {
		status = monetizeapi.UpsertAgentIdentityRegistration(status, chain, agentID)
	}
	return status
}

