package serviceoffercontroller

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ethereum/go-ethereum/common"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	serviceOfferFinalizer  = "monetize.obol.org/finalizer"
	controllerFieldManager = "serviceoffer-controller"

	registrationDesiredActive     = "Active"
	registrationDesiredTombstoned = "Tombstoned"

	registrationPhasePublishing       = "Publishing"
	registrationPhaseRegistering      = "Registering"
	registrationPhaseAwaitingExternal = "AwaitingExternalRegistration"
	registrationPhaseRegistered       = "Registered"
	registrationPhaseOffChainOnly     = "OffChainOnly"
	registrationPhaseTombstoned       = "Tombstoned"
)

type Controller struct {
	kubeClient           kubernetes.Interface
	dynClient            dynamic.Interface
	client               dynamic.Interface
	offers               dynamic.NamespaceableResourceInterface
	registrationRequests dynamic.NamespaceableResourceInterface
	agents               dynamic.NamespaceableResourceInterface
	services             dynamic.NamespaceableResourceInterface
	configMaps           dynamic.NamespaceableResourceInterface
	deployments          dynamic.NamespaceableResourceInterface
	middlewares          dynamic.NamespaceableResourceInterface
	httpRoutes           dynamic.NamespaceableResourceInterface
	referenceGrants      dynamic.NamespaceableResourceInterface

	offerInformer        cache.SharedIndexInformer
	registrationInformer cache.SharedIndexInformer
	purchaseInformer     cache.SharedIndexInformer
	agentInformer        cache.SharedIndexInformer
	configMapInformer    cache.SharedIndexInformer
	offerQueue           workqueue.TypedRateLimitingInterface[string]
	registrationQueue    workqueue.TypedRateLimitingInterface[string]
	purchaseQueue        workqueue.TypedRateLimitingInterface[string]
	agentQueue           workqueue.TypedRateLimitingInterface[string]
	catalogMu            sync.Mutex

	pendingAuths sync.Map // key: "ns/name" → []map[string]string

	httpClient *http.Client

	// litellmURLOverride is used in tests to point at a local httptest server
	// instead of the in-cluster litellm Service DNS. Empty in production.
	litellmURLOverride string

	// registrationRPCBase is the eRPC base URL; per-chain clients dial
	// <base>/<NetworkConfig.ERPCNetwork>. Override with ERC8004_RPC_BASE.
	registrationRPCBase string
	baseURLOverride     string
	defaultBaseURL      string
}

func New(cfg *rest.Config) (*Controller, error) {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kube client: %w", err)
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, nil)
	offerInformer := factory.ForResource(monetizeapi.ServiceOfferGVR).Informer()
	registrationInformer := factory.ForResource(monetizeapi.RegistrationRequestGVR).Informer()
	purchaseInformer := factory.ForResource(monetizeapi.PurchaseRequestGVR).Informer()
	agentInformer := factory.ForResource(monetizeapi.AgentGVR).Informer()
	configMapFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, "obol-frontend", func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "obol-stack-config").String()
	})
	configMapInformer := configMapFactory.ForResource(monetizeapi.ConfigMapGVR).Informer()

	controller := &Controller{
		kubeClient:           kubeClient,
		dynClient:            client,
		client:               client,
		offers:               client.Resource(monetizeapi.ServiceOfferGVR),
		registrationRequests: client.Resource(monetizeapi.RegistrationRequestGVR),
		agents:               client.Resource(monetizeapi.AgentGVR),
		services:             client.Resource(monetizeapi.ServiceGVR),
		configMaps:           client.Resource(monetizeapi.ConfigMapGVR),
		deployments:          client.Resource(monetizeapi.DeploymentGVR),
		middlewares:          client.Resource(monetizeapi.MiddlewareGVR),
		httpRoutes:           client.Resource(monetizeapi.HTTPRouteGVR),
		referenceGrants:      client.Resource(monetizeapi.ReferenceGrantGVR),
		offerInformer:        offerInformer,
		registrationInformer: registrationInformer,
		purchaseInformer:     purchaseInformer,
		agentInformer:        agentInformer,
		configMapInformer:    configMapInformer,
		offerQueue:           workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		registrationQueue:    workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		purchaseQueue:        workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		agentQueue:           workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		httpClient:           &http.Client{Timeout: 3 * time.Second},
		registrationRPCBase:  getenvDefault("ERC8004_RPC_BASE", erc8004.DefaultRPCBase),
		baseURLOverride:      strings.TrimRight(os.Getenv("AGENT_BASE_URL"), "/"),
		defaultBaseURL:       "http://obol.stack:8080",
	}

	offerInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueOffer,
		UpdateFunc: func(_, newObj any) { controller.enqueueOffer(newObj) },
		DeleteFunc: controller.enqueueOffer,
	})
	registrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueRegistration,
		UpdateFunc: func(_, newObj any) { controller.enqueueRegistration(newObj) },
		DeleteFunc: controller.enqueueRegistration,
	})
	registrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueOfferFromRegistration,
		UpdateFunc: func(_, newObj any) { controller.enqueueOfferFromRegistration(newObj) },
		DeleteFunc: controller.enqueueOfferFromRegistration,
	})
	purchaseInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueuePurchase,
		UpdateFunc: func(_, newObj any) { controller.enqueuePurchase(newObj) },
		DeleteFunc: controller.enqueuePurchase,
	})
	agentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueAgent,
		UpdateFunc: func(_, newObj any) { controller.enqueueAgent(newObj) },
		DeleteFunc: controller.enqueueAgent,
	})
	configMapInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueDiscoveryRefresh,
		UpdateFunc: func(_, newObj any) { controller.enqueueDiscoveryRefresh(newObj) },
		DeleteFunc: controller.enqueueDiscoveryRefresh,
	})

	return controller, nil
}

func (c *Controller) Run(ctx context.Context, workers int) error {
	defer c.offerQueue.ShutDown()
	defer c.registrationQueue.ShutDown()
	defer c.purchaseQueue.ShutDown()
	defer c.agentQueue.ShutDown()

	go c.offerInformer.Run(ctx.Done())
	go c.registrationInformer.Run(ctx.Done())
	go c.purchaseInformer.Run(ctx.Done())
	go c.agentInformer.Run(ctx.Done())
	go c.configMapInformer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(),
		c.offerInformer.HasSynced,
		c.registrationInformer.HasSynced,
		c.purchaseInformer.HasSynced,
		c.agentInformer.HasSynced,
		c.configMapInformer.HasSynced,
	) {
		return fmt.Errorf("wait for informer sync")
	}

	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		go func() {
			for c.processNextOffer(ctx) {
			}
		}()
		go func() {
			for c.processNextRegistration(ctx) {
			}
		}()
		go func() {
			for c.processNextPurchase(ctx) {
			}
		}()
		go func() {
			for c.processNextAgent(ctx) {
			}
		}()
	}

	<-ctx.Done()
	return nil
}

func (c *Controller) enqueueOffer(obj any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("serviceoffer-controller: build offer queue key: %v", err)
		return
	}
	c.offerQueue.Add(key)
}

func (c *Controller) enqueueRegistration(obj any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("serviceoffer-controller: build registration queue key: %v", err)
		return
	}
	c.registrationQueue.Add(key)
}

func (c *Controller) enqueueOfferFromRegistration(obj any) {
	u := asUnstructured(obj)
	if u == nil {
		return
	}
	for _, item := range c.offerInformer.GetStore().List() {
		u := asUnstructured(item)
		if u == nil {
			continue
		}
		offer, err := decodeServiceOffer(u)
		if err != nil {
			log.Printf("serviceoffer-controller: decode offer for registration fan-out: %v", err)
			continue
		}
		if offer.DeletionTimestamp != nil || offer.IsPaused() || !offer.Spec.Registration.Enabled {
			continue
		}
		c.offerQueue.Add(offer.Namespace + "/" + offer.Name)
	}
}

func (c *Controller) enqueueDiscoveryRefresh(obj any) {
	u := asUnstructured(obj)
	if u == nil {
		return
	}
	if u.GetNamespace() != "obol-frontend" || u.GetName() != "obol-stack-config" {
		return
	}
	log.Printf("serviceoffer-controller: base URL change detected, refreshing offers and registration requests")
	for _, item := range c.offerInformer.GetStore().List() {
		c.enqueueOffer(item)
	}
	for _, item := range c.registrationInformer.GetStore().List() {
		c.enqueueRegistration(item)
	}
}

func (c *Controller) processNextOffer(ctx context.Context) bool {
	key, shutdown := c.offerQueue.Get()
	if shutdown {
		return false
	}
	defer c.offerQueue.Done(key)

	if err := c.reconcileOffer(ctx, key); err != nil {
		log.Printf("serviceoffer-controller: reconcile offer %s: %v", key, err)
		c.offerQueue.AddRateLimited(key)
		return true
	}

	c.offerQueue.Forget(key)
	return true
}

func (c *Controller) processNextRegistration(ctx context.Context) bool {
	key, shutdown := c.registrationQueue.Get()
	if shutdown {
		return false
	}
	defer c.registrationQueue.Done(key)

	if err := c.reconcileRegistrationRequest(ctx, key); err != nil {
		log.Printf("serviceoffer-controller: reconcile registration %s: %v", key, err)
		c.registrationQueue.AddRateLimited(key)
		return true
	}

	c.registrationQueue.Forget(key)
	return true
}

func (c *Controller) enqueuePurchase(obj any) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		log.Printf("serviceoffer-controller: build purchase queue key: %v", err)
		return
	}
	c.purchaseQueue.Add(key)
}

func (c *Controller) processNextPurchase(ctx context.Context) bool {
	key, shutdown := c.purchaseQueue.Get()
	if shutdown {
		return false
	}
	defer c.purchaseQueue.Done(key)

	if err := c.reconcilePurchase(ctx, key); err != nil {
		log.Printf("serviceoffer-controller: reconcile purchase %s: %v", key, err)
		c.purchaseQueue.AddRateLimited(key)
		return true
	}

	c.purchaseQueue.Forget(key)
	return true
}

func (c *Controller) reconcileOffer(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	raw, err := c.offers.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	offer, err := decodeServiceOffer(raw)
	if err != nil {
		return err
	}

	if offer.DeletionTimestamp != nil {
		if !slices.Contains(raw.GetFinalizers(), serviceOfferFinalizer) {
			return nil
		}
		if err := c.reconcileDeletingOffer(ctx, offer); err != nil {
			return err
		}
		// Deletion in progress: omit the offer from the catalog. The informer
		// store may still have it (deletion event is async), so pass a tombstone
		// override to suppress it explicitly rather than rely on cache eviction.
		tombstone := *offer
		if tombstone.DeletionTimestamp == nil {
			now := metav1.Now()
			tombstone.DeletionTimestamp = &now
		}
		if err := c.reconcileSkillCatalog(ctx, &tombstone); err != nil {
			return err
		}
		return c.removeFinalizer(ctx, raw, serviceOfferFinalizer)
	}

	if !slices.Contains(raw.GetFinalizers(), serviceOfferFinalizer) {
		return c.addFinalizer(ctx, raw, serviceOfferFinalizer)
	}

	status := offer.Status
	status.ObservedGeneration = offer.Generation
	status.Endpoint = offer.EffectivePath()

	if offer.IsAgent() {
		ready, resolveErr := c.resolveAgentOffer(ctx, offer, &status)
		if resolveErr != nil {
			return resolveErr
		}
		if !ready {
			setCondition(&status, "ModelReady", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
			setCondition(&status, "UpstreamHealthy", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
			setCondition(&status, "PaymentGateReady", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
			setCondition(&status, "RoutePublished", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
			setCondition(&status, "Ready", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
			return c.updateOfferStatus(ctx, raw, status)
		}
	}

	if err := c.reconcileModel(&status, offer); err != nil {
		return err
	}

	upstreamHealthy, err := c.reconcileUpstream(ctx, &status, offer)
	if err != nil {
		return err
	}

	if offer.IsPaused() {
		if err := c.deleteRouteChildren(ctx, offer); err != nil {
			return err
		}
		setCondition(&status, "PaymentGateReady", "False", "Paused", "Offer is paused")
		setCondition(&status, "RoutePublished", "False", "Paused", "Offer is paused")
	} else if upstreamHealthy && isConditionTrue(status, "ModelReady") {
		if err := c.reconcilePaymentGate(ctx, &status, offer); err != nil {
			return err
		}
		if isConditionTrue(status, "PaymentGateReady") {
			if err := c.reconcileRoute(ctx, &status, offer); err != nil {
				return err
			}
		}
	} else {
		setCondition(&status, "PaymentGateReady", "False", "WaitingForUpstream", "Waiting for upstream health before publishing payment gate")
		setCondition(&status, "RoutePublished", "False", "WaitingForPaymentGate", "Waiting for payment gate before publishing route")
	}

	if err := c.reconcileRegistrationStatus(ctx, &status, offer); err != nil {
		return err
	}

	ready := isConditionTrue(status, "ModelReady") &&
		isConditionTrue(status, "UpstreamHealthy") &&
		isConditionTrue(status, "PaymentGateReady") &&
		isConditionTrue(status, "RoutePublished") &&
		isConditionTrue(status, "Registered")
	if ready {
		setCondition(&status, "Ready", "True", "Reconciled", "Offer reconciled successfully")
	} else {
		setCondition(&status, "Ready", "False", "Reconciling", "Offer is not fully reconciled yet")
	}

	if err := c.updateOfferStatus(ctx, raw, status); err != nil {
		return err
	}
	if offer.Spec.Registration.Enabled {
		owner, err := c.registrationOwner()
		if err != nil {
			return err
		}
		if owner != nil {
			c.registrationQueue.Add(owner.Namespace + "/" + registrationRequestName(owner.Name))
		}
	}
	if !ready {
		// Dependent resources like the upstream Deployment, Middleware, HTTPRoute,
		// and RegistrationRequest can become ready after this reconcile completes.
		// Requeue offers that are still converging so status can advance without
		// requiring a spec mutation or unrelated ConfigMap update.
		c.offerQueue.AddAfter(offer.Namespace+"/"+offer.Name, 5*time.Second)
	}
	// Rebuild the skill catalog on every reconcile so the just-updated status
	// (not yet reflected in the informer store) and tunnel URL changes both
	// propagate immediately. The catalog's ConfigMap/Deployment only rotate
	// when the rendered markdown actually differs, so idle reconciles are
	// no-ops at the API-server level. Pass `offer`+`status` as an override so
	// the just-committed status is used instead of the stale informer copy.
	freshOffer := *offer
	freshOffer.Status = status
	return c.reconcileSkillCatalog(ctx, &freshOffer)
}

func (c *Controller) reconcileDeletingOffer(ctx context.Context, offer *monetizeapi.ServiceOffer) error {
	if err := c.deleteRouteChildren(ctx, offer); err != nil {
		return err
	}

	if offer.Spec.Registration.Enabled {
		nextOwner, err := c.registrationOwner()
		if err != nil {
			return err
		}
		if nextOwner != nil {
			if err := c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name); err != nil {
				return err
			}
			c.offerQueue.Add(nextOwner.Namespace + "/" + nextOwner.Name)
			c.registrationQueue.Add(nextOwner.Namespace + "/" + registrationRequestName(nextOwner.Name))
			return nil
		}
	}

	if !offer.Spec.Registration.Enabled && strings.TrimSpace(offer.Status.AgentID) == "" {
		return c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name)
	}

	ready, err := c.ensureRegistrationCleanup(ctx, offer)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("registration cleanup pending for %s/%s", offer.Namespace, offer.Name)
	}

	return c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name)
}

func (c *Controller) reconcileModel(status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if !offer.IsInference() {
		setCondition(status, "ModelReady", "True", "Skipped", "HTTP offer does not require model preparation")
		return nil
	}
	if offer.Spec.Model.Name == "" {
		setCondition(status, "ModelReady", "False", "MissingModel", "Inference offer is missing spec.model.name")
		return nil
	}
	setCondition(status, "ModelReady", "True", "Declared", fmt.Sprintf("Model %s declared", offer.Spec.Model.Name))
	return nil
}

func (c *Controller) reconcileUpstream(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) (bool, error) {
	_, err := c.services.Namespace(offer.EffectiveNamespace()).Get(ctx, offer.Spec.Upstream.Service, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		setCondition(status, "UpstreamHealthy", "False", "MissingService", "Upstream Service does not exist")
		return false, nil
	}
	if err != nil {
		return false, err
	}

	healthURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s",
		offer.Spec.Upstream.Service,
		offer.EffectiveNamespace(),
		offer.EffectivePort(),
		offer.EffectiveHealthPath(),
	)
	healthCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(healthCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return false, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		setCondition(status, "UpstreamHealthy", "False", "Unhealthy", err.Error())
		return false, nil
	}
	defer response.Body.Close()

	if response.StatusCode >= 500 {
		setCondition(status, "UpstreamHealthy", "False", "Unhealthy", fmt.Sprintf("HTTP %d from upstream", response.StatusCode))
		return false, nil
	}

	setCondition(status, "UpstreamHealthy", "True", "Healthy", fmt.Sprintf("Upstream responded with HTTP %d", response.StatusCode))
	return true, nil
}

func (c *Controller) reconcilePaymentGate(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if err := c.applyObject(ctx, c.referenceGrants.Namespace("x402"), buildReferenceGrant(offer)); err != nil {
		setCondition(status, "PaymentGateReady", "False", "ApplyFailed", err.Error())
		return err
	}

	if _, err := c.services.Namespace("x402").Get(ctx, "x402-verifier", metav1.GetOptions{}); apierrors.IsNotFound(err) {
		setCondition(status, "PaymentGateReady", "False", "WaitingForGateway", "Shared x402 gateway Service does not exist")
		return nil
	} else if err != nil {
		return err
	}

	deployment, err := c.deployments.Namespace("x402").Get(ctx, "x402-verifier", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		setCondition(status, "PaymentGateReady", "False", "WaitingForGateway", "Shared x402 gateway Deployment does not exist")
		return nil
	}
	if err != nil {
		return err
	}

	availableReplicas, _, err := unstructured.NestedInt64(deployment.Object, "status", "availableReplicas")
	if err != nil {
		return err
	}
	if availableReplicas < 1 {
		setCondition(status, "PaymentGateReady", "False", "WaitingForGateway", "Shared x402 gateway Deployment is not yet available")
		return nil
	}

	setCondition(status, "PaymentGateReady", "True", "Reconciled", "Shared x402 gateway is available")
	return nil
}

func (c *Controller) reconcileRoute(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(offer.Namespace), buildHTTPRoute(offer)); err != nil {
		setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
		return err
	}
	log.Printf("serviceoffer-controller: route published for %s/%s at %s", offer.Namespace, offer.Name, offer.EffectivePath())
	setCondition(status, "RoutePublished", "True", "Reconciled", fmt.Sprintf("HTTPRoute published at %s", offer.EffectivePath()))
	return nil
}

func (c *Controller) reconcileRegistrationStatus(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if !offer.Spec.Registration.Enabled {
		if err := c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name); err != nil {
			return err
		}
		setCondition(status, "Registered", "True", "Disabled", "Registration disabled")
		return nil
	}
	owner, err := c.registrationOwner()
	if err != nil {
		return err
	}
	if owner == nil {
		setCondition(status, "Registered", "False", "Pending", "Waiting for shared registration owner")
		return nil
	}
	if owner.Namespace != offer.Namespace || owner.Name != offer.Name {
		if err := c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name); err != nil {
			return err
		}
		raw, err := c.registrationRequests.Namespace(owner.Namespace).Get(ctx, registrationRequestName(owner.Name), metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			setCondition(
				status,
				"Registered",
				"False",
				"Pending",
				fmt.Sprintf("Waiting for shared registration owned by %s/%s", owner.Namespace, owner.Name),
			)
			return nil
		}
		if err != nil {
			return err
		}
		request, err := decodeRegistrationRequest(raw)
		if err != nil {
			return err
		}
		applySharedRegistrationStatus(status, offer, owner, request)
		return nil
	}

	requestName := registrationRequestName(offer.Name)
	if err := c.applyObject(ctx, c.registrationRequests.Namespace(offer.Namespace), buildRegistrationRequest(offer, registrationDesiredActive)); err != nil {
		setCondition(status, "Registered", "False", "ApplyFailed", err.Error())
		return err
	}

	raw, err := c.registrationRequests.Namespace(offer.Namespace).Get(ctx, requestName, metav1.GetOptions{})
	if err != nil {
		setCondition(status, "Registered", "False", "Pending", "Waiting for RegistrationRequest")
		return nil
	}
	request, err := decodeRegistrationRequest(raw)
	if err != nil {
		return err
	}

	status.AgentID = request.Status.AgentID
	status.RegistrationTxHash = request.Status.RegistrationTxHash

	applySharedRegistrationStatus(status, offer, owner, request)
	return nil
}

func (c *Controller) ensureRegistrationCleanup(ctx context.Context, offer *monetizeapi.ServiceOffer) (bool, error) {
	if err := c.applyObject(ctx, c.registrationRequests.Namespace(offer.Namespace), buildRegistrationRequest(offer, registrationDesiredTombstoned)); err != nil {
		return false, err
	}

	raw, err := c.registrationRequests.Namespace(offer.Namespace).Get(ctx, registrationRequestName(offer.Name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return strings.TrimSpace(offer.Status.AgentID) == "", nil
	}
	if err != nil {
		return false, err
	}
	request, err := decodeRegistrationRequest(raw)
	if err != nil {
		return false, err
	}
	return requestCleanupComplete(request.Status.Phase), nil
}

func (c *Controller) reconcileRegistrationRequest(ctx context.Context, key string) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	raw, err := c.registrationRequests.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	request, err := decodeRegistrationRequest(raw)
	if err != nil {
		return err
	}

	offerRaw, err := c.offers.Namespace(request.Spec.ServiceOfferNamespace).Get(ctx, request.Spec.ServiceOfferName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		owner, ownerErr := c.registrationOwner()
		if ownerErr != nil {
			return ownerErr
		}
		if owner != nil {
			if err := c.deleteRegistrationRequest(ctx, namespace, request.Spec.ServiceOfferName); err != nil {
				return err
			}
			c.offerQueue.Add(owner.Namespace + "/" + owner.Name)
			c.registrationQueue.Add(owner.Namespace + "/" + registrationRequestName(owner.Name))
			return nil
		}
		if err := c.deleteRegistrationResources(ctx, request); err != nil {
			return err
		}
		return c.updateRegistrationStatus(ctx, raw, monetizeapi.RegistrationRequestStatus{
			Phase:   registrationPhaseTombstoned,
			Message: "ServiceOffer no longer exists",
		})
	}
	if err != nil {
		return err
	}

	offer, err := decodeServiceOffer(offerRaw)
	if err != nil {
		return err
	}

	baseURL, err := c.registrationBaseURL(ctx)
	if err != nil {
		return err
	}

	switch request.Spec.DesiredState {
	case registrationDesiredTombstoned:
		return c.reconcileRegistrationTombstone(ctx, raw, request, offer, baseURL)
	default:
		return c.reconcileRegistrationActive(ctx, raw, request, offer, baseURL)
	}
}

func (c *Controller) reconcileRegistrationActive(ctx context.Context, raw *unstructured.Unstructured, request *monetizeapi.RegistrationRequest, offer *monetizeapi.ServiceOffer, baseURL string) error {
	status := request.Status
	agentID := firstNonEmpty(status.AgentID, offer.Status.AgentID)
	txHash := firstNonEmpty(status.RegistrationTxHash, offer.Status.RegistrationTxHash)

	offers, err := c.registrationOffers("", "")
	if err != nil {
		return err
	}
	document := buildActiveRegistrationDocument(offer, offers, baseURL, agentID)
	documentJSON, contentHash, err := marshalRegistrationDocument(document)
	if err != nil {
		return err
	}
	if err := c.publishRegistrationResources(ctx, request, documentJSON, contentHash); err != nil {
		return err
	}

	status.PublishedURL = strings.TrimRight(baseURL, "/") + "/.well-known/agent-registration.json"
	resourcesReady, message, err := c.registrationResourcesReady(ctx, request)
	if err != nil {
		return err
	}
	if !resourcesReady {
		status.Phase = registrationPhasePublishing
		status.Message = message
		return c.updateRegistrationStatus(ctx, raw, status)
	}

	// On-chain registration is performed by the CLI (`obol sell register` /
	// `obol sell http`) via the agent's remote-signer — never by the
	// controller. The controller only publishes the registration document
	// and watches for the registration tx to land on-chain so it can mark
	// the request Ready=True. Each offer's payment.network selects which
	// chain to watch; the client dials <eRPC base>/<network alias>.
	var client *erc8004.Client
	if agentID == "" {
		network, lookupErr := erc8004.ResolveNetwork(offer.Spec.Payment.Network)
		if lookupErr != nil {
			status.Phase = registrationPhaseAwaitingExternal
			status.Message = truncateMessage(fmt.Sprintf("Unsupported registration chain %q: %v", offer.Spec.Payment.Network, lookupErr))
			return c.updateRegistrationStatus(ctx, raw, status)
		}
		client, err = erc8004.NewClientForNetwork(ctx, c.registrationRPCBase, network)
		if err != nil {
			status.Phase = registrationPhaseAwaitingExternal
			status.Message = truncateMessage(fmt.Sprintf("Waiting for ERC-8004 RPC connectivity on %s: %v", network.Name, err))
			return c.updateRegistrationStatus(ctx, raw, status)
		}
		defer client.Close()

		if status.RegistrationURI != status.PublishedURL ||
			!strings.EqualFold(status.RegistrationOwner, offer.Spec.Payment.PayTo) ||
			status.RegistrationSearchFromBlock == 0 {
			height, err := client.CurrentBlockNumber(ctx)
			if err != nil {
				status.Phase = registrationPhaseAwaitingExternal
				status.Message = truncateMessage(fmt.Sprintf("Preparing external registration recovery: %v", err))
				return c.updateRegistrationStatus(ctx, raw, status)
			}

			status.Phase = registrationPhaseAwaitingExternal
			status.Message = "Waiting for external ERC-8004 registration"
			status.RegistrationOwner = offer.Spec.Payment.PayTo
			status.RegistrationURI = status.PublishedURL
			fromBlock := int64(height) - 1024
			if fromBlock < 0 {
				fromBlock = 0
			}
			status.RegistrationSearchFromBlock = fromBlock
			status.RegistrationTxHash = ""
			return c.updateRegistrationStatus(ctx, raw, status)
		}

		recoveredAgentID, recoveredTxHash, found, err := c.recoverRegistration(ctx, client, status)
		if err != nil {
			status.Phase = registrationPhaseAwaitingExternal
			status.Message = truncateMessage(fmt.Sprintf("Recovering external registration state: %v", err))
			if updateErr := c.updateRegistrationStatus(ctx, raw, status); updateErr != nil {
				return updateErr
			}
			return err
		}
		if !found {
			status.Phase = registrationPhaseAwaitingExternal
			status.Message = fmt.Sprintf("Waiting for external ERC-8004 registration for owner %s", status.RegistrationOwner)
			return c.updateRegistrationStatus(ctx, raw, status)
		}

		agentID = recoveredAgentID
		txHash = recoveredTxHash
	}

	status.AgentID = agentID
	status.RegistrationTxHash = txHash
	status.RegistrationURI = firstNonEmpty(status.RegistrationURI, status.PublishedURL)
	if agentID != "" {
		status.Phase = registrationPhaseRegistered
		status.Message = fmt.Sprintf("Published registration document and recorded agent %s", agentID)
	}

	return c.updateRegistrationStatus(ctx, raw, status)
}

func (c *Controller) recoverRegistration(ctx context.Context, client *erc8004.Client, status monetizeapi.RegistrationRequestStatus) (string, string, bool, error) {
	if txHash := strings.TrimSpace(status.RegistrationTxHash); txHash != "" {
		agentID, resolvedTxHash, found, err := client.FindRegistrationByTxHash(ctx, txHash)
		if err != nil || !found {
			return "", "", found, err
		}
		return agentID.String(), resolvedTxHash, true, nil
	}

	if strings.TrimSpace(status.RegistrationOwner) == "" || strings.TrimSpace(status.RegistrationURI) == "" || status.RegistrationSearchFromBlock < 0 {
		return "", "", false, nil
	}

	agentID, resolvedTxHash, found, err := client.FindRegistrationByOwnerAndURI(
		ctx,
		common.HexToAddress(status.RegistrationOwner),
		status.RegistrationURI,
		uint64(status.RegistrationSearchFromBlock),
	)
	if err != nil || !found {
		return "", "", found, err
	}
	return agentID.String(), resolvedTxHash, true, nil
}

func (c *Controller) reconcileRegistrationTombstone(ctx context.Context, raw *unstructured.Unstructured, request *monetizeapi.RegistrationRequest, offer *monetizeapi.ServiceOffer, _ string) error {
	status := request.Status
	agentID := firstNonEmpty(status.AgentID, offer.Status.AgentID)

	// On-chain tombstoning is the operator's responsibility via the CLI
	// (the controller has no signing key by design — registration is a
	// CLI/remote-signer flow). We only delete the published registration
	// resources here and mark the request OffChainOnly when an agent ID
	// was ever assigned, otherwise Tombstoned (nothing to tombstone).
	if agentID != "" {
		status.Phase = registrationPhaseOffChainOnly
		status.Message = "Deleted registration resources; on-chain tombstone is the operator's responsibility"
	} else {
		status.Phase = registrationPhaseTombstoned
		status.Message = "Deleted registration resources"
	}

	if err := c.deleteRegistrationResources(ctx, request); err != nil {
		return err
	}
	return c.updateRegistrationStatus(ctx, raw, status)
}

func (c *Controller) publishRegistrationResources(ctx context.Context, request *monetizeapi.RegistrationRequest, documentJSON, contentHash string) error {
	if err := c.applyObject(ctx, c.configMaps.Namespace(request.Namespace), buildRegistrationConfigMap(request, documentJSON)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.deployments.Namespace(request.Namespace), buildRegistrationDeployment(request, contentHash)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.services.Namespace(request.Namespace), buildRegistrationService(request)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(request.Namespace), buildRegistrationHTTPRoute(request)); err != nil {
		return err
	}
	log.Printf("serviceoffer-controller: registration resources published for %s/%s", request.Namespace, request.Name)
	return nil
}

// reconcileSkillCatalog rebuilds the /skill.md ConfigMap/Deployment/Service/
// HTTPRoute from the current set of Ready ServiceOffers. If `override` is
// non-nil, that offer replaces (or is appended to) the informer-cached copy
// with the same namespace/name — this is how reconcileOffer feeds its
// just-committed status into the catalog without waiting for the informer's
// watch event to update the local store.
func (c *Controller) reconcileSkillCatalog(ctx context.Context, override *monetizeapi.ServiceOffer) error {
	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()

	baseURL, err := c.registrationBaseURL(ctx)
	if err != nil {
		return err
	}

	items := c.offerInformer.GetStore().List()
	offers := make([]*monetizeapi.ServiceOffer, 0, len(items)+1)
	overrideUsed := false
	for _, item := range items {
		raw := asUnstructured(item)
		if raw == nil {
			continue
		}
		offer, err := decodeServiceOffer(raw)
		if err != nil {
			return err
		}
		if override != nil && offer.Namespace == override.Namespace && offer.Name == override.Name {
			offer = override
			overrideUsed = true
		}
		offers = append(offers, offer)
	}
	if override != nil && !overrideUsed {
		// Override refers to an offer the informer hasn't yet observed (e.g.
		// a just-created ServiceOffer whose Add event hasn't fired). Include
		// it so the catalog reflects reality.
		offers = append(offers, override)
	}

	content := buildSkillCatalogMarkdown(offers, baseURL)
	servicesJSON := buildServiceCatalogJSON(offers, baseURL)
	contentHash := fmt.Sprintf("%x", md5Sum(content+servicesJSON))[:8]

	if err := c.applyObject(ctx, c.configMaps.Namespace(skillCatalogNamespace), buildSkillCatalogConfigMap(content, servicesJSON)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.deployments.Namespace(skillCatalogNamespace), buildSkillCatalogDeployment(contentHash)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.services.Namespace(skillCatalogNamespace), buildSkillCatalogService()); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(skillCatalogNamespace), buildSkillCatalogHTTPRoute()); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(skillCatalogNamespace), buildServicesJSONHTTPRoute()); err != nil {
		return err
	}
	readyOffers := 0
	for _, offer := range offers {
		if offer != nil && offer.DeletionTimestamp == nil && !offer.IsPaused() && isConditionTrue(offer.Status, "Ready") {
			readyOffers++
		}
	}
	log.Printf("serviceoffer-controller: /skill.md published with %d ready offer(s)", readyOffers)
	return nil
}

func (c *Controller) registrationResourcesReady(ctx context.Context, request *monetizeapi.RegistrationRequest) (bool, string, error) {
	name := registrationWorkloadName(request.Name)

	if _, err := c.configMaps.Namespace(request.Namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		return false, "Waiting for registration ConfigMap", nil
	} else if err != nil {
		return false, "", err
	}

	deployment, err := c.deployments.Namespace(request.Namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, "Waiting for registration Deployment", nil
	}
	if err != nil {
		return false, "", err
	}
	availableReplicas, _, err := unstructured.NestedInt64(deployment.Object, "status", "availableReplicas")
	if err != nil {
		return false, "", err
	}
	if availableReplicas < 1 {
		return false, "Waiting for registration Deployment availability", nil
	}

	if _, err := c.services.Namespace(request.Namespace).Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
		return false, "Waiting for registration Service", nil
	} else if err != nil {
		return false, "", err
	}

	route, err := c.httpRoutes.Namespace(request.Namespace).Get(ctx, registrationRouteName(request.Spec.ServiceOfferName), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, "Waiting for registration HTTPRoute", nil
	}
	if err != nil {
		return false, "", err
	}
	if !httpRouteAccepted(route) {
		return false, "Waiting for registration HTTPRoute acceptance", nil
	}

	return true, "", nil
}

func (c *Controller) deleteRouteChildren(ctx context.Context, offer *monetizeapi.ServiceOffer) error {
	for _, deletion := range []struct {
		resource dynamic.ResourceInterface
		name     string
	}{
		{resource: c.referenceGrants.Namespace("x402"), name: backendReferenceGrantName(offer.Name)},
		{resource: c.httpRoutes.Namespace(offer.Namespace), name: childName(offer.Name)},
	} {
		err := deletion.resource.Delete(ctx, deletion.name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *Controller) deleteRegistrationResources(ctx context.Context, request *monetizeapi.RegistrationRequest) error {
	for _, deletion := range []struct {
		resource dynamic.ResourceInterface
		name     string
	}{
		{resource: c.httpRoutes.Namespace(request.Namespace), name: registrationRouteName(request.Spec.ServiceOfferName)},
		{resource: c.services.Namespace(request.Namespace), name: registrationWorkloadName(request.Name)},
		{resource: c.deployments.Namespace(request.Namespace), name: registrationWorkloadName(request.Name)},
		{resource: c.configMaps.Namespace(request.Namespace), name: registrationWorkloadName(request.Name)},
	} {
		err := deletion.resource.Delete(ctx, deletion.name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *Controller) deleteRegistrationRequest(ctx context.Context, namespace, offerName string) error {
	err := c.registrationRequests.Namespace(namespace).Delete(ctx, registrationRequestName(offerName), metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (c *Controller) registrationOffers(excludeNamespace, excludeName string) ([]*monetizeapi.ServiceOffer, error) {
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
		if offer.DeletionTimestamp != nil || offer.IsPaused() || !offer.Spec.Registration.Enabled {
			continue
		}
		if !isConditionTrue(offer.Status, "UpstreamHealthy") {
			log.Printf("serviceoffer-controller: registration candidate %s/%s has unhealthy upstream", offer.Namespace, offer.Name)
		}
		candidates = append(candidates, offer)
	}
	return candidates, nil
}

func (c *Controller) registrationOwner() (*monetizeapi.ServiceOffer, error) {
	candidates, err := c.registrationOffers("", "")
	if err != nil {
		return nil, err
	}
	return selectRegistrationOwner(candidates), nil
}

func selectRegistrationOwner(offers []*monetizeapi.ServiceOffer) *monetizeapi.ServiceOffer {
	if len(offers) == 0 {
		return nil
	}
	sort.Slice(offers, func(i, j int) bool {
		ti := offers[i].CreationTimestamp.Time
		tj := offers[j].CreationTimestamp.Time
		switch {
		case ti.Equal(tj):
			if offers[i].Namespace == offers[j].Namespace {
				return offers[i].Name < offers[j].Name
			}
			return offers[i].Namespace < offers[j].Namespace
		case ti.IsZero():
			return false
		case tj.IsZero():
			return true
		default:
			return ti.Before(tj)
		}
	})
	return offers[0]
}

func applySharedRegistrationStatus(status *monetizeapi.ServiceOfferStatus, offer, owner *monetizeapi.ServiceOffer, request *monetizeapi.RegistrationRequest) {
	status.AgentID = request.Status.AgentID
	status.RegistrationTxHash = request.Status.RegistrationTxHash

	if !isConditionTrue(*status, "RoutePublished") {
		setCondition(status, "Registered", "False", "WaitingForRoute", "Waiting for route publication before shared registration")
		return
	}

	if requestPhaseReady(request.Status.Phase) {
		message := defaultString(request.Status.Message, "Registration reconciled")
		if owner != nil && (owner.Namespace != offer.Namespace || owner.Name != offer.Name) {
			if request.Status.AgentID != "" {
				message = fmt.Sprintf("Shared registration via %s/%s recorded agent %s", owner.Namespace, owner.Name, request.Status.AgentID)
			} else {
				message = fmt.Sprintf("Shared registration via %s/%s is active", owner.Namespace, owner.Name)
			}
		}
		setCondition(status, "Registered", "True", request.Status.Phase, message)
		return
	}

	reason := defaultString(request.Status.Phase, "Pending")
	message := defaultString(request.Status.Message, "Waiting for RegistrationRequest to finish")
	if owner != nil && (owner.Namespace != offer.Namespace || owner.Name != offer.Name) {
		message = fmt.Sprintf("Waiting for shared registration owned by %s/%s: %s", owner.Namespace, owner.Name, message)
	}
	setCondition(status, "Registered", "False", reason, message)
}

func (c *Controller) applyObject(ctx context.Context, resource dynamic.ResourceInterface, desired *unstructured.Unstructured) error {
	payload, err := json.Marshal(desired.Object)
	if err != nil {
		return err
	}

	force := true
	_, err = resource.Patch(ctx, desired.GetName(), types.ApplyPatchType, payload, metav1.PatchOptions{
		FieldManager: controllerFieldManager,
		Force:        &force,
	})
	return err
}

func (c *Controller) updateOfferStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.ServiceOfferStatus) error {
	patched := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	if existing, found := patched.Object["status"]; found && equality.Semantic.DeepEqual(existing, statusObject) {
		return nil
	}
	patched.Object["status"] = statusObject
	_, err = c.offers.Namespace(patched.GetNamespace()).UpdateStatus(ctx, patched, metav1.UpdateOptions{})
	return err
}

func (c *Controller) updateRegistrationStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.RegistrationRequestStatus) error {
	patched := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	if existing, found := patched.Object["status"]; found && equality.Semantic.DeepEqual(existing, statusObject) {
		return nil
	}
	patched.Object["status"] = statusObject
	_, err = c.registrationRequests.Namespace(patched.GetNamespace()).UpdateStatus(ctx, patched, metav1.UpdateOptions{})
	return err
}

func (c *Controller) addFinalizer(ctx context.Context, raw *unstructured.Unstructured, finalizer string) error {
	patched := raw.DeepCopy()
	patched.SetFinalizers(append(patched.GetFinalizers(), finalizer))
	_, err := c.offers.Namespace(patched.GetNamespace()).Update(ctx, patched, metav1.UpdateOptions{})
	return err
}

func (c *Controller) removeFinalizer(ctx context.Context, raw *unstructured.Unstructured, finalizer string) error {
	patched := raw.DeepCopy()
	patched.SetFinalizers(slices.DeleteFunc(patched.GetFinalizers(), func(s string) bool { return s == finalizer }))
	_, err := c.offers.Namespace(patched.GetNamespace()).Update(ctx, patched, metav1.UpdateOptions{})
	return err
}

func (c *Controller) registrationBaseURL(ctx context.Context) (string, error) {
	if c.baseURLOverride != "" {
		return c.baseURLOverride, nil
	}
	configMap, err := c.configMaps.Namespace("obol-frontend").Get(ctx, "obol-stack-config", metav1.GetOptions{})
	if err == nil {
		if value, found, err := unstructured.NestedString(configMap.Object, "data", "tunnelURL"); err == nil && found && strings.TrimSpace(value) != "" {
			return strings.TrimRight(value, "/"), nil
		}
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	return c.defaultBaseURL, nil
}

func decodeServiceOffer(raw *unstructured.Unstructured) (*monetizeapi.ServiceOffer, error) {
	var offer monetizeapi.ServiceOffer
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &offer); err != nil {
		return nil, err
	}
	if offer.Spec.Upstream.Namespace == "" {
		offer.Spec.Upstream.Namespace = offer.Namespace
	}
	return &offer, nil
}

func decodeRegistrationRequest(raw *unstructured.Unstructured) (*monetizeapi.RegistrationRequest, error) {
	var request monetizeapi.RegistrationRequest
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &request); err != nil {
		return nil, err
	}
	return &request, nil
}

func asUnstructured(obj any) *unstructured.Unstructured {
	switch typed := obj.(type) {
	case *unstructured.Unstructured:
		return typed
	case cache.DeletedFinalStateUnknown:
		if u, ok := typed.Obj.(*unstructured.Unstructured); ok {
			return u
		}
	}
	return nil
}

func requestPhaseReady(phase string) bool {
	return phase == registrationPhaseRegistered
}

func requestCleanupComplete(phase string) bool {
	return phase == registrationPhaseTombstoned || phase == registrationPhaseOffChainOnly
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= 200 {
		return message
	}
	return message[:200]
}

func getenvDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func httpRouteAccepted(route *unstructured.Unstructured) bool {
	parents, found, err := unstructured.NestedSlice(route.Object, "status", "parents")
	if err != nil || !found {
		return false
	}
	for _, parent := range parents {
		parentMap, ok := parent.(map[string]any)
		if !ok {
			continue
		}
		conditions, ok := parentMap["conditions"].([]any)
		if !ok {
			continue
		}
		accepted := false
		resolvedRefs := true
		for _, condition := range conditions {
			condMap, ok := condition.(map[string]any)
			if !ok {
				continue
			}
			condType, _ := condMap["type"].(string)
			condStatus, _ := condMap["status"].(string)
			switch condType {
			case "Accepted":
				accepted = condStatus == "True"
			case "ResolvedRefs":
				resolvedRefs = condStatus == "True"
			}
		}
		if accepted && resolvedRefs {
			return true
		}
	}
	return false
}

func md5Sum(content string) [16]byte {
	return md5.Sum([]byte(content))
}
