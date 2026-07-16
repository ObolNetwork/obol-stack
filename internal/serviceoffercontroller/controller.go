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
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
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
	agentIdentities      dynamic.NamespaceableResourceInterface
	agents               dynamic.NamespaceableResourceInterface
	services             dynamic.NamespaceableResourceInterface
	configMaps           dynamic.NamespaceableResourceInterface
	deployments          dynamic.NamespaceableResourceInterface
	middlewares          dynamic.NamespaceableResourceInterface
	httpRoutes           dynamic.NamespaceableResourceInterface
	referenceGrants      dynamic.NamespaceableResourceInterface

	offerInformer             cache.SharedIndexInformer
	registrationInformer      cache.SharedIndexInformer
	identityInformer          cache.SharedIndexInformer
	purchaseInformer          cache.SharedIndexInformer
	agentInformer             cache.SharedIndexInformer
	configMapInformer         cache.SharedIndexInformer
	storefrontProfileInformer cache.SharedIndexInformer
	offerQueue                workqueue.TypedRateLimitingInterface[string]
	registrationQueue         workqueue.TypedRateLimitingInterface[string]
	identityQueue             workqueue.TypedRateLimitingInterface[string]
	purchaseQueue             workqueue.TypedRateLimitingInterface[string]
	agentQueue                workqueue.TypedRateLimitingInterface[string]
	staticSiteMu              sync.Mutex

	// upstreamOpenAPICache is populated from each offer's own reconcile
	// (refresh, outside staticSiteMu) and only ever read by
	// reconcileStaticSite's buildOfferBundles call (under staticSiteMu) —
	// see upstream_openapi.go.
	upstreamOpenAPICache upstreamOpenAPICache

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
	identityInformer := factory.ForResource(monetizeapi.AgentIdentityGVR).Informer()
	purchaseInformer := factory.ForResource(monetizeapi.PurchaseRequestGVR).Informer()
	agentInformer := factory.ForResource(monetizeapi.AgentGVR).Informer()
	configMapFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, "obol-frontend", func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "obol-stack-config").String()
	})
	configMapInformer := configMapFactory.ForResource(monetizeapi.ConfigMapGVR).Informer()
	storefrontProfileFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, storefront.ProfileNamespace, func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", storefront.ProfileConfigMap).String()
	})
	storefrontProfileInformer := storefrontProfileFactory.ForResource(monetizeapi.ConfigMapGVR).Informer()

	controller := &Controller{
		kubeClient:                kubeClient,
		dynClient:                 client,
		client:                    client,
		offers:                    client.Resource(monetizeapi.ServiceOfferGVR),
		registrationRequests:      client.Resource(monetizeapi.RegistrationRequestGVR),
		agentIdentities:           client.Resource(monetizeapi.AgentIdentityGVR),
		agents:                    client.Resource(monetizeapi.AgentGVR),
		services:                  client.Resource(monetizeapi.ServiceGVR),
		configMaps:                client.Resource(monetizeapi.ConfigMapGVR),
		deployments:               client.Resource(monetizeapi.DeploymentGVR),
		middlewares:               client.Resource(monetizeapi.MiddlewareGVR),
		httpRoutes:                client.Resource(monetizeapi.HTTPRouteGVR),
		referenceGrants:           client.Resource(monetizeapi.ReferenceGrantGVR),
		offerInformer:             offerInformer,
		registrationInformer:      registrationInformer,
		identityInformer:          identityInformer,
		purchaseInformer:          purchaseInformer,
		agentInformer:             agentInformer,
		configMapInformer:         configMapInformer,
		storefrontProfileInformer: storefrontProfileInformer,
		offerQueue:                workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		registrationQueue:         workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		identityQueue:             workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		purchaseQueue:             workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		agentQueue:                workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		httpClient:                &http.Client{Timeout: 3 * time.Second},
		registrationRPCBase:       getenvDefault("ERC8004_RPC_BASE", erc8004.DefaultRPCBase),
		baseURLOverride:           strings.TrimRight(os.Getenv("AGENT_BASE_URL"), "/"),
		defaultBaseURL:            "http://obol.stack:8080",
	}

	offerInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			controller.enqueueOffer(obj)
			controller.enqueueIdentityFromOffer(obj)
		},
		UpdateFunc: func(_, newObj any) {
			controller.enqueueOffer(newObj)
			controller.enqueueIdentityFromOffer(newObj)
		},
		DeleteFunc: func(obj any) {
			controller.enqueueOffer(obj)
			controller.enqueueIdentityFromOffer(obj)
		},
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
	registrationInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueIdentityFromRegistration,
		UpdateFunc: func(_, newObj any) { controller.enqueueIdentityFromRegistration(newObj) },
		DeleteFunc: controller.enqueueIdentityFromRegistration,
	})
	identityInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueIdentity,
		UpdateFunc: func(_, newObj any) { controller.enqueueIdentity(newObj) },
		DeleteFunc: controller.enqueueIdentity,
	})
	purchaseInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueuePurchase,
		UpdateFunc: func(_, newObj any) { controller.enqueuePurchase(newObj) },
		DeleteFunc: controller.enqueuePurchase,
	})
	agentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			controller.enqueueAgent(obj)
			controller.enqueueOffersFromAgent(obj)
		},
		UpdateFunc: func(_, newObj any) {
			controller.enqueueAgent(newObj)
			controller.enqueueOffersFromAgent(newObj)
		},
		DeleteFunc: func(obj any) {
			controller.enqueueAgent(obj)
			controller.enqueueOffersFromAgent(obj)
		},
	})
	configMapInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueDiscoveryRefresh,
		UpdateFunc: func(_, newObj any) { controller.enqueueDiscoveryRefresh(newObj) },
		DeleteFunc: controller.enqueueDiscoveryRefresh,
	})
	storefrontProfileInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    controller.enqueueStorefrontProfileRefresh,
		UpdateFunc: func(_, newObj any) { controller.enqueueStorefrontProfileRefresh(newObj) },
		DeleteFunc: controller.enqueueStorefrontProfileRefresh,
	})

	return controller, nil
}

func (c *Controller) Run(ctx context.Context, workers int) error {
	defer c.offerQueue.ShutDown()
	defer c.registrationQueue.ShutDown()
	defer c.identityQueue.ShutDown()
	defer c.purchaseQueue.ShutDown()
	defer c.agentQueue.ShutDown()

	go c.offerInformer.Run(ctx.Done())
	go c.registrationInformer.Run(ctx.Done())
	go c.identityInformer.Run(ctx.Done())
	go c.purchaseInformer.Run(ctx.Done())
	go c.agentInformer.Run(ctx.Done())
	go c.configMapInformer.Run(ctx.Done())
	go c.storefrontProfileInformer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(),
		c.offerInformer.HasSynced,
		c.registrationInformer.HasSynced,
		c.identityInformer.HasSynced,
		c.purchaseInformer.HasSynced,
		c.agentInformer.HasSynced,
		c.configMapInformer.HasSynced,
		c.storefrontProfileInformer.HasSynced,
	) {
		return fmt.Errorf("wait for informer sync")
	}

	if err := c.ensureDefaultAgentIdentity(ctx); err != nil {
		log.Printf("serviceoffer-controller: ensure default AgentIdentity: %v", err)
	}

	// Heal paid-route entries written before the x402-buyer split (they
	// point at the removed litellm-pod sidecar address). One-shot,
	// idempotent, no-op on fresh clusters.
	c.migrateLegacyBuyerAPIBases(ctx, "llm")

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
			for c.processNextIdentity(ctx) {
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
		if offer.DeletionTimestamp != nil || !offer.Spec.Registration.Enabled {
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
	ns, name := u.GetNamespace(), u.GetName()
	if ns == "obol-frontend" && name == "obol-stack-config" {
		log.Printf("serviceoffer-controller: base URL change detected, refreshing offers and registration requests")
		for _, item := range c.offerInformer.GetStore().List() {
			c.enqueueOffer(item)
		}
		for _, item := range c.registrationInformer.GetStore().List() {
			c.enqueueRegistration(item)
		}
		for _, item := range c.identityInformer.GetStore().List() {
			c.enqueueIdentity(item)
		}
	}
}

func (c *Controller) enqueueStorefrontProfileRefresh(obj any) {
	u := asUnstructured(obj)
	if u == nil {
		return
	}
	if u.GetNamespace() != storefront.ProfileNamespace || u.GetName() != storefront.ProfileConfigMap {
		return
	}
	log.Printf("serviceoffer-controller: storefront profile change detected, refreshing static site")
	c.enqueueStaticSiteRefresh()
}

func (c *Controller) enqueueStaticSiteRefresh() {
	items := c.offerInformer.GetStore().List()
	if len(items) > 0 {
		// Any single offer reconcile rebuilds the full catalog.
		c.enqueueOffer(items[0])
		return
	}
	go c.refreshStaticSiteAsync()
}

func (c *Controller) refreshStaticSiteAsync() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.reconcileStaticSite(ctx, nil); err != nil {
		log.Printf("serviceoffer-controller: refresh static site: %v", err)
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
		if err := c.reconcileStaticSite(ctx, &tombstone); err != nil {
			return err
		}
		c.upstreamOpenAPICache.forget(offer.UID)
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
			if offer.DrainExpired(time.Now()) {
				if err := c.deleteRouteChildren(ctx, offer); err != nil {
					return err
				}
				setCondition(&status, "Draining", "False", "Drained", fmt.Sprintf("Drain ended at %s; route torn down", offer.DrainEndsAt().UTC().Format(time.RFC3339)))
				setCondition(&status, "PaymentGateReady", "False", "Drained", "Offer drained; payment gate removed")
				setCondition(&status, "RoutePublished", "False", "Drained", "Offer drained; route removed")
			} else {
				setCondition(&status, "PaymentGateReady", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
				setCondition(&status, "RoutePublished", "False", "WaitingForAgent", "Referenced Agent is not yet Ready")
			}
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

	if root := monetizeapi.ReservedPathConflict(offer.EffectivePath()); root != "" {
		// Reserved shared-origin surface (discovery docs, /rpc, /.well-known,
		// the storefront root): publishing would shadow platform routes.
		// Same teardown + no-route treatment as an offer-vs-offer conflict.
		msg := fmt.Sprintf("path %s collides with the reserved platform path %s — set a different spec.path", offer.EffectivePath(), root)
		log.Printf("serviceoffer-controller: %s/%s reserved path: %s", offer.Namespace, offer.Name, msg)
		if err := c.deleteRouteChildren(ctx, offer); err != nil {
			return err
		}
		setCondition(&status, "Draining", "False", "Active", "Offer is active")
		setCondition(&status, "PaymentGateReady", "False", "ReservedPath", msg)
		setCondition(&status, "RoutePublished", "False", "ReservedPath", msg)
	} else if routePath, root := reservedRouteConflict(offer); root != "" {
		// Same reserved-surface treatment as above, but for an individual
		// spec.routes[].path entry (F8) — e.g. a route declared at "/auth"
		// would shadow the verifier's own SIWX sign-in endpoints for
		// gate:auth offers.
		msg := fmt.Sprintf("route path %s collides with the reserved platform path %s — set a different spec.routes[].path", routePath, root)
		log.Printf("serviceoffer-controller: %s/%s reserved route path: %s", offer.Namespace, offer.Name, msg)
		if err := c.deleteRouteChildren(ctx, offer); err != nil {
			return err
		}
		setCondition(&status, "Draining", "False", "Active", "Offer is active")
		setCondition(&status, "PaymentGateReady", "False", "ReservedPath", msg)
		setCondition(&status, "RoutePublished", "False", "ReservedPath", msg)
	} else if conflict := c.findHostnameConflict(offer); conflict != "" {
		// One offer per public origin — same first-claimant-wins treatment
		// as a path conflict.
		msg := fmt.Sprintf("hostname %s is already claimed by older offer %s — set a different spec.hostname", offer.Spec.Hostname, conflict)
		log.Printf("serviceoffer-controller: %s/%s hostname conflict: %s", offer.Namespace, offer.Name, msg)
		if err := c.deleteRouteChildren(ctx, offer); err != nil {
			return err
		}
		setCondition(&status, "Draining", "False", "Active", "Offer is active")
		setCondition(&status, "PaymentGateReady", "False", "HostnameConflict", msg)
		setCondition(&status, "RoutePublished", "False", "HostnameConflict", msg)
		c.offerQueue.AddAfter(offer.Namespace+"/"+offer.Name, 30*time.Second)
	} else if conflict := c.findPathConflict(offer); conflict != "" {
		// First-claimant-wins: an older offer holds this path. Publishing
		// anyway would silently shadow one of the two offers in the
		// verifier's first-match route table. Tear down any children we
		// previously published (covers collisions that predate this check
		// and an older offer moving onto our path) and poll for the path
		// freeing up — there is no event edge when the older offer goes
		// away that re-enqueues this one.
		msg := fmt.Sprintf("path %s is already claimed by older offer %s — set a different spec.path", offer.EffectivePath(), conflict)
		log.Printf("serviceoffer-controller: %s/%s path conflict: %s", offer.Namespace, offer.Name, msg)
		if err := c.deleteRouteChildren(ctx, offer); err != nil {
			return err
		}
		setCondition(&status, "Draining", "False", "Active", "Offer is active")
		setCondition(&status, "PaymentGateReady", "False", "PathConflict", msg)
		setCondition(&status, "RoutePublished", "False", "PathConflict", msg)
		c.offerQueue.AddAfter(offer.Namespace+"/"+offer.Name, 30*time.Second)
	} else if offer.IsDraining() {
		now := time.Now()
		drainEndsAt := offer.DrainEndsAt()
		if offer.DrainExpired(now) {
			// Drain grace period elapsed: tear down the HTTPRoute +
			// payment gate. The CR itself stays (delete is the canonical
			// removal path) so external observers continue to see the
			// offer in the catalog with available=false.
			if err := c.deleteRouteChildren(ctx, offer); err != nil {
				return err
			}
			setCondition(&status, "Draining", "False", "Drained", fmt.Sprintf("Drain ended at %s; route torn down", drainEndsAt.UTC().Format(time.RFC3339)))
			setCondition(&status, "PaymentGateReady", "False", "Drained", "Offer drained; payment gate removed")
			setCondition(&status, "RoutePublished", "False", "Drained", "Offer drained; route removed")
		} else {
			// Still in the drain window: keep the route + payment gate
			// up so in-flight buyers can finish, but mark Draining=True
			// so discovery surfaces can advertise available=false.
			if upstreamHealthy && isConditionTrue(status, "ModelReady") {
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
			setCondition(&status, "Draining", "True", "Draining", fmt.Sprintf("Drain ends at %s", drainEndsAt.UTC().Format(time.RFC3339)))
			// Requeue at the drain expiry so the route is torn down on
			// time even without any spec change in the interim. Add a
			// small slack so the comparison in DrainExpired clears.
			if delay := time.Until(drainEndsAt) + time.Second; delay > 0 {
				c.offerQueue.AddAfter(offer.Namespace+"/"+offer.Name, delay)
			} else {
				c.offerQueue.Add(offer.Namespace + "/" + offer.Name)
			}
		}
	} else if upstreamHealthy && isConditionTrue(status, "ModelReady") {
		setCondition(&status, "Draining", "False", "Active", "Offer is active")
		if err := c.reconcilePaymentGate(ctx, &status, offer); err != nil {
			return err
		}
		if isConditionTrue(status, "PaymentGateReady") {
			if err := c.reconcileRoute(ctx, &status, offer); err != nil {
				return err
			}
		}
	} else {
		setCondition(&status, "Draining", "False", "Active", "Offer is active")
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
		identityKey := defaultAgentIdentityKey()
		c.enqueueAgentIdentityKey(identityKey)
		owner, err := c.registrationOwnerForIdentity(identityKey)
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
	// Refresh this offer's upstream OpenAPI cache from its own reconcile,
	// outside staticSiteMu, at most once per generation — see
	// upstreamOpenAPICache and buildOfferBundles for why the rebuild below
	// must never fetch live.
	if offer.Spec.Hostname != "" {
		c.upstreamOpenAPICache.refresh(offer, tryUpstreamOpenAPI)
	}
	// Rebuild the static site on every reconcile so tunnel URL changes and
	// offer status updates propagate immediately. reconcileStaticSite skips
	// ConfigMap/Deployment writes when the rendered hash is unchanged.
	return c.reconcileStaticSite(ctx, nil)
}

func (c *Controller) reconcileDeletingOffer(ctx context.Context, offer *monetizeapi.ServiceOffer) error {
	if err := c.deleteRouteChildren(ctx, offer); err != nil {
		return err
	}

	if offer.Spec.Registration.Enabled {
		identityKey := defaultAgentIdentityKey()
		c.enqueueAgentIdentityKey(identityKey)
		nextOwner, err := c.registrationOwnerForIdentity(identityKey)
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

	// Require a 2xx health response. Treating any <500 (including 404) as
	// healthy left agents with a wrong healthPath (or a never-started API)
	// stuck in UpstreamHealthy=True and then Ready=True while paid traffic
	// 404'd end-to-end.
	if !upstreamHealthStatusOK(response.StatusCode) {
		setCondition(status, "UpstreamHealthy", "False", "Unhealthy", fmt.Sprintf("HTTP %d from upstream health path %s", response.StatusCode, offer.EffectiveHealthPath()))
		return false, nil
	}

	setCondition(status, "UpstreamHealthy", "True", "Healthy", fmt.Sprintf("Upstream responded with HTTP %d", response.StatusCode))
	return true, nil
}

// upstreamHealthStatusOK reports whether an HTTP status from the offer's
// healthPath should count as UpstreamHealthy=True.
func upstreamHealthStatusOK(code int) bool {
	return code >= 200 && code < 300
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
	// Protection middleware must exist before the routes that reference it
	// (Traefik drops routes with a dangling ExtensionRef). inFlightReq and
	// rateLimit are separate Middleware CRs — a combined CR is rejected by
	// Traefik while the ServiceOffer still looked Ready.
	for _, mw := range buildLimitsMiddlewares(offer) {
		if err := c.applyObject(ctx, c.middlewares.Namespace(offer.Namespace), mw); err != nil {
			setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
			return err
		}
	}
	// Tear down unused limit CRs (including the legacy combined -limits name).
	for _, name := range []string{
		limitsInFlightMiddlewareName(offer.Name),
		limitsRPSMiddlewareName(offer.Name),
		legacyLimitsMiddlewareName(offer.Name),
	} {
		wanted := false
		for _, mw := range buildLimitsMiddlewares(offer) {
			if mw.GetName() == name {
				wanted = true
				break
			}
		}
		if wanted {
			continue
		}
		err := c.middlewares.Namespace(offer.Namespace).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	// Delete both superseded ReferenceGrant names every reconcile so grants
	// orphaned by a rename don't linger and collide with another offer: the
	// pre-4726dcfe non-namespaced name, and the 4726dcfe dash-joined name that
	// the injective hash suffix replaced.
	for _, staleGrant := range []string{
		legacyBackendReferenceGrantName(offer.Name),
		intermediateBackendReferenceGrantName(offer.Namespace, offer.Name),
	} {
		if err := c.referenceGrants.Namespace("x402").Delete(ctx, staleGrant, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(offer.Namespace), buildHTTPRoute(offer)); err != nil {
		setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
		return err
	}
	route, err := c.httpRoutes.Namespace(offer.Namespace).Get(ctx, childName(offer.Name), metav1.GetOptions{})
	if err != nil {
		setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
		return err
	}
	if !httpRouteAccepted(route) {
		setCondition(status, "RoutePublished", "False", "WaitingForTraefikAcceptance", "HTTPRoute applied but not yet accepted by Traefik")
		return nil
	}
	if offer.Spec.Hostname != "" {
		if err := c.applyObject(ctx, c.httpRoutes.Namespace(offer.Namespace), buildHostHTTPRoute(offer)); err != nil {
			setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
			return err
		}
		hostRoute, err := c.httpRoutes.Namespace(offer.Namespace).Get(ctx, hostChildName(offer.Name), metav1.GetOptions{})
		if err != nil {
			setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
			return err
		}
		if !httpRouteAccepted(hostRoute) {
			setCondition(status, "RoutePublished", "False", "WaitingForTraefikAcceptance", "Host HTTPRoute applied but not yet accepted by Traefik")
			return nil
		}
	} else {
		// Hostname removed from the spec: tear the host route down so the
		// origin frees up (for the storefront catch-all or another offer).
		err := c.httpRoutes.Namespace(offer.Namespace).Delete(ctx, hostChildName(offer.Name), metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	published := offer.EffectivePath()
	if origin := offer.EffectiveOrigin(); origin != "" {
		published = origin + " (+ shared-origin alias " + offer.EffectivePath() + ")"
	}
	log.Printf("serviceoffer-controller: route published for %s/%s at %s", offer.Namespace, offer.Name, published)
	setCondition(status, "RoutePublished", "True", "Reconciled", fmt.Sprintf("HTTPRoute published at %s", published))
	return nil
}

func (c *Controller) reconcileRegistrationStatus(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if !offer.Spec.Registration.Enabled {
		if err := c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name); err != nil {
			return err
		}
		// Disabling registration must stop reporting whatever agentId was
		// last recorded — otherwise a disabled offer keeps showing a
		// (possibly stale, wrong-chain) id in status/CLI output.
		status.AgentID = ""
		status.RegistrationTxHash = ""
		setCondition(status, "Registered", "True", "Disabled", "Registration disabled")
		return nil
	}
	_, identity, err := c.ensureAgentIdentityForOffer(ctx, offer)
	if err != nil {
		setCondition(status, "Registered", "False", "IdentityError", err.Error())
		return err
	}
	status.AgentID = monetizeapi.AgentIdentityAgentIDForChain(identity.Status, offer.Spec.Payment.Network)

	identityKey := defaultAgentIdentityKey()
	owner, err := c.registrationOwnerForIdentity(identityKey)
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
		applySharedRegistrationStatus(status, offer, owner, identity, request)
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

	applySharedRegistrationStatus(status, offer, owner, identity, request)
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
		identityKey := defaultAgentIdentityKey()
		identityRaw, identity, identityErr := c.ensureAgentIdentityForKey(ctx, identityKey)
		if identityErr != nil {
			return identityErr
		}
		owner, ownerErr := c.registrationOwnerForIdentity(identityKey)
		if ownerErr != nil {
			return ownerErr
		}
		if owner != nil {
			if err := c.deleteRegistrationRequest(ctx, namespace, request.Spec.ServiceOfferName); err != nil {
				return err
			}
			c.enqueueAgentIdentityKey(identityKey)
			c.offerQueue.Add(owner.Namespace + "/" + owner.Name)
			c.registrationQueue.Add(owner.Namespace + "/" + registrationRequestName(owner.Name))
			return nil
		}

		registrationChain := request.Spec.Chain
		agentID := firstNonEmpty(monetizeapi.AgentIdentityAgentIDForChain(identity.Status, registrationChain), request.Status.AgentID)
		if agentID != "" && monetizeapi.AgentIdentityAgentIDForChain(identity.Status, registrationChain) == "" {
			identity.Status = agentIdentityStatusFromRegistration(identity, registrationChain, agentID)
			if err := c.updateAgentIdentityStatus(ctx, identityRaw, identity.Status); err != nil {
				return err
			}
			identityRaw, err = c.agentIdentities.Namespace(identity.Namespace).Get(ctx, identity.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			identity, err = decodeAgentIdentity(identityRaw)
			if err != nil {
				return err
			}
		}

		baseURL, baseErr := c.registrationBaseURL(ctx)
		if baseErr != nil {
			return baseErr
		}
		document := BuildIdentityRegistrationDocument(IdentityRegistrationView{
			Identity: identity,
			Offers:   nil,
			BaseURL:  baseURL,
		})
		documentJSON, contentHash, marshalErr := marshalRegistrationDocument(document)
		if marshalErr != nil {
			return marshalErr
		}
		if err := c.publishAgentIdentityRegistrationResources(ctx, identity, documentJSON, contentHash); err != nil {
			return err
		}
		if err := c.deleteRegistrationResources(ctx, request); err != nil {
			return err
		}
		newStatus := request.Status
		newStatus.Phase = registrationPhaseOffChainOnly
		newStatus.Message = "Last ServiceOffer deleted; published tombstone registration document"
		newStatus.AgentID = agentID
		if newStatus.PublishedURL == "" {
			newStatus.PublishedURL = strings.TrimRight(baseURL, "/") + "/.well-known/agent-registration.json"
		}
		return c.updateRegistrationStatus(ctx, raw, newStatus)
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
	identityRaw, identity, err := c.ensureAgentIdentityForOffer(ctx, offer)
	if err != nil {
		status.Phase = registrationPhaseAwaitingExternal
		status.Message = truncateMessage(fmt.Sprintf("Waiting for AgentIdentity: %v", err))
		return c.updateRegistrationStatus(ctx, raw, status)
	}
	registrationChain := firstNonEmpty(request.Spec.Chain, offer.Spec.Payment.Network)
	// Chain-scoped only: status.AgentID/offer.Status.AgentID are not
	// chain-tagged, so falling back to them here would reuse a
	// wrong-chain id after a network switch and skip the on-chain
	// ownership verification below. AgentIdentityAgentIDForChain
	// correctly returns "" until this chain has a verified registration.
	agentID := monetizeapi.AgentIdentityAgentIDForChain(identity.Status, registrationChain)
	txHash := firstNonEmpty(status.RegistrationTxHash, offer.Status.RegistrationTxHash)

	offers, err := c.registrationOffersForIdentity(defaultAgentIdentityKey(), "", "")
	if err != nil {
		return err
	}
	identity.Status = agentIdentityStatusFromRegistration(identity, registrationChain, agentID)
	document := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: identity,
		Offers:   mergeOfferOverride(offers, offer),
		BaseURL:  baseURL,
	})
	documentJSON, contentHash, err := marshalRegistrationDocument(document)
	if err != nil {
		return err
	}
	if err := c.publishAgentIdentityRegistrationResources(ctx, identity, documentJSON, contentHash); err != nil {
		return err
	}
	if err := c.deleteRegistrationResources(ctx, request); err != nil {
		return err
	}

	status.PublishedURL = strings.TrimRight(baseURL, "/") + "/.well-known/agent-registration.json"
	resourcesReady, message, err := c.identityRegistrationResourcesReady(ctx, identity)
	if err != nil {
		return err
	}
	if !resourcesReady {
		status.Phase = registrationPhasePublishing
		status.Message = message
		return c.updateRegistrationStatus(ctx, raw, status)
	}

	// On-chain registration is performed by the CLI (`obol sell register` /
	// `obol sell http`) via the agent's remote-signer; never by the
	// controller. The controller only publishes the registration document
	// and watches for the registration tx to land on-chain so it can mark
	// the request Ready=True. RegistrationRequest.spec.chain selects which
	// chain to watch and AgentIdentity.status.registrations records the
	// resulting per-chain tokenId. The client dials <eRPC base>/<network alias>.
	var client *erc8004.Client
	if agentID == "" {
		network, lookupErr := erc8004.ResolveNetwork(registrationChain)
		if lookupErr != nil {
			status.Phase = registrationPhaseAwaitingExternal
			status.Message = truncateMessage(fmt.Sprintf("Unsupported registration chain %q: %v", registrationChain, lookupErr))
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
			status.Message = awaitingExternalRegistrationMessage(registrationChain)
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
			status.Message = awaitingExternalRegistrationMessage(registrationChain)
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
		identityStatus := agentIdentityStatusFromRegistration(identity, registrationChain, agentID)
		if err := c.updateAgentIdentityStatus(ctx, identityRaw, identityStatus); err != nil {
			return err
		}
		c.enqueueAgentIdentityKey(defaultAgentIdentityKey())
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

func (c *Controller) reconcileRegistrationTombstone(ctx context.Context, raw *unstructured.Unstructured, request *monetizeapi.RegistrationRequest, offer *monetizeapi.ServiceOffer, baseURL string) error {
	status := request.Status
	_, identity, err := c.ensureAgentIdentityForOffer(ctx, offer)
	if err != nil {
		status.Phase = registrationPhaseAwaitingExternal
		status.Message = truncateMessage(fmt.Sprintf("Waiting for AgentIdentity tombstone: %v", err))
		return c.updateRegistrationStatus(ctx, raw, status)
	}
	// Chain-scoped only — see reconcileRegistrationActive for why the
	// status.AgentID/offer.Status.AgentID fallbacks are dropped. Since
	// agentID is now always exactly what AgentIdentityAgentIDForChain
	// already returns, there is nothing left to backfill into identity.
	registrationChain := firstNonEmpty(request.Spec.Chain, offer.Spec.Payment.Network)
	agentID := monetizeapi.AgentIdentityAgentIDForChain(identity.Status, registrationChain)

	document := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: identity,
		Offers:   nil,
		BaseURL:  baseURL,
	})
	documentJSON, contentHash, err := marshalRegistrationDocument(document)
	if err != nil {
		return err
	}
	if err := c.publishAgentIdentityRegistrationResources(ctx, identity, documentJSON, contentHash); err != nil {
		return err
	}
	if err := c.deleteRegistrationResources(ctx, request); err != nil {
		return err
	}

	status.Phase = registrationPhaseOffChainOnly
	status.Message = "Published tombstone registration document; on-chain NFT preserved"
	status.AgentID = agentID
	if status.PublishedURL == "" {
		status.PublishedURL = strings.TrimRight(baseURL, "/") + "/.well-known/agent-registration.json"
	}
	return c.updateRegistrationStatus(ctx, raw, status)
}

func (c *Controller) loadStorefrontProfile(ctx context.Context) (*schemas.StorefrontProfile, error) {
	cm, err := c.configMaps.Namespace(storefront.ProfileNamespace).Get(ctx, storefront.ProfileConfigMap, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	raw, found, err := unstructured.NestedString(cm.Object, "data", storefront.ProfileDataKey)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return storefront.ParseProfile(raw)
}

// reconcileStaticSite rebuilds the /skill.md ConfigMap/Deployment/Service/
// HTTPRoute from the current set of operationally-ready ServiceOffers. Offers
// are listed from the API server so every reconcile sees a consistent snapshot;
// override replaces or appends a just-created offer the list has not observed yet.
// ConfigMap and Deployment are only applied when the rendered catalog hash differs
// from the live obol-skill-md Deployment annotation, so idle reconciles do not
// roll the static-site pod.
func (c *Controller) reconcileStaticSite(ctx context.Context, override *monetizeapi.ServiceOffer) error {
	c.staticSiteMu.Lock()
	defer c.staticSiteMu.Unlock()

	baseURL, err := c.registrationBaseURL(ctx)
	if err != nil {
		return err
	}

	offers, err := c.listServiceOffersForCatalog(ctx, override)
	if err != nil {
		return err
	}

	storefrontProfile, err := c.loadStorefrontProfile(ctx)
	if err != nil {
		return err
	}
	content := buildSkillMarkdown(offers, baseURL, storefrontProfile)
	servicesJSON := buildServiceCatalogJSON(offers, baseURL, storefrontProfile)
	resolvedProfile := storefront.ResolvePublished(storefrontProfile, baseURL)
	// buildOpenAPIDocument prefers the tunnel URL for the public `servers[0]`
	// entry; baseURL is sourced from obol-stack-config.tunnelURL via
	// registrationBaseURL, which is also what /skill.md and services.json
	// use as their public-facing prefix, so the three surfaces stay in sync
	// on tunnel restarts (the configMap informer re-enqueues every offer
	// when tunnelURL changes — see enqueueDiscoveryRefresh).
	openAPIJSON := buildOpenAPIDocument(offers, baseURL, resolvedProfile)
	apiDocsHTML := scalarHTML(resolvedProfile)
	bundles := buildOfferBundles(offers, resolvedProfile, c.upstreamOpenAPICache.get)
	contentHash := computeStaticSiteContentHash(content, servicesJSON, openAPIJSON, apiDocsHTML, bundles)

	unchanged, err := c.staticSiteContentUnchanged(ctx, content, servicesJSON, openAPIJSON, apiDocsHTML, bundles)
	if err != nil {
		return err
	}
	if unchanged {
		readyOffers := countReadyServiceOffers(offers)
		log.Printf("serviceoffer-controller: /skill.md unchanged (hash=%s, %d ready offer(s))", contentHash, readyOffers)
		return nil
	}

	if err := c.applyObject(ctx, c.configMaps.Namespace(staticSiteNamespace), buildStaticSiteConfigMap(content, servicesJSON, openAPIJSON, apiDocsHTML, bundles)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.deployments.Namespace(staticSiteNamespace), buildStaticSiteDeployment(contentHash, bundles)); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.services.Namespace(staticSiteNamespace), buildStaticSiteService()); err != nil {
		return err
	}
	// Headers Middleware must exist before the routes that reference it, or
	// Traefik drops the routes for a dangling ExtensionRef.
	if err := c.applyObject(ctx, c.middlewares.Namespace(staticSiteNamespace), buildCatalogHeadersMiddleware()); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(staticSiteNamespace), buildStaticSiteHTTPRoute()); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(staticSiteNamespace), buildServicesJSONHTTPRoute()); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(staticSiteNamespace), buildOpenAPIHTTPRoute()); err != nil {
		return err
	}
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(staticSiteNamespace), buildAPIDocsHTTPRoute()); err != nil {
		return err
	}
	readyOffers := countReadyServiceOffers(offers)
	log.Printf("serviceoffer-controller: /skill.md published with %d ready offer(s) (hash=%s)", readyOffers, contentHash)
	return nil
}

func countReadyServiceOffers(offers []*monetizeapi.ServiceOffer) int {
	readyOffers := 0
	for _, offer := range offers {
		if offer != nil && offer.DeletionTimestamp == nil && isConditionTrue(offer.Status, "Ready") {
			readyOffers++
		}
	}
	return readyOffers
}

func (c *Controller) deleteRouteChildren(ctx context.Context, offer *monetizeapi.ServiceOffer) error {
	for _, deletion := range []struct {
		resource dynamic.ResourceInterface
		name     string
	}{
		{resource: c.referenceGrants.Namespace("x402"), name: backendReferenceGrantName(offer.Namespace, offer.Name)},
		{resource: c.referenceGrants.Namespace("x402"), name: legacyBackendReferenceGrantName(offer.Name)},
		{resource: c.referenceGrants.Namespace("x402"), name: intermediateBackendReferenceGrantName(offer.Namespace, offer.Name)},
		{resource: c.httpRoutes.Namespace(offer.Namespace), name: childName(offer.Name)},
		{resource: c.httpRoutes.Namespace(offer.Namespace), name: hostChildName(offer.Name)},
		{resource: c.middlewares.Namespace(offer.Namespace), name: limitsInFlightMiddlewareName(offer.Name)},
		{resource: c.middlewares.Namespace(offer.Namespace), name: limitsRPSMiddlewareName(offer.Name)},
		{resource: c.middlewares.Namespace(offer.Namespace), name: legacyLimitsMiddlewareName(offer.Name)},
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

// applySharedRegistrationStatus copies a shared RegistrationRequest's phase
// onto offer's status, but the agentId itself is never trusted straight off
// request.Status: that subresource belongs to the registration owner and is
// never chain-tagged, so it can't tell whether it was recorded under
// offer's own Spec.Payment.Network or a different chain the owner (or offer
// itself, before a network switch) used previously. The agentId is instead
// always re-derived from the chain-scoped AgentIdentity record — if the
// identity has no registration for offer's chain, agentId stays empty and
// Registered does not flip True on the strength of a foreign chain's id.
func applySharedRegistrationStatus(status *monetizeapi.ServiceOfferStatus, offer, owner *monetizeapi.ServiceOffer, identity *monetizeapi.AgentIdentity, request *monetizeapi.RegistrationRequest) {
	agentID := monetizeapi.AgentIdentityAgentIDForChain(identity.Status, offer.Spec.Payment.Network)
	status.AgentID = agentID
	// The tx hash lives on the same chain-blind request.Status subresource
	// the agentId was retired from, so it is only mirrored once the offer's
	// own chain has a recorded registration for it to describe.
	status.RegistrationTxHash = ""
	if agentID != "" {
		status.RegistrationTxHash = request.Status.RegistrationTxHash
	}

	if !isConditionTrue(*status, "RoutePublished") {
		setCondition(status, "Registered", "False", "WaitingForRoute", "Waiting for route publication before shared registration")
		return
	}

	// Registered only ever flips True on the strength of a chain-scoped
	// agentId. requestPhaseReady(request.Status.Phase) alone is NOT a
	// sufficient condition: Phase is set by whoever last reconciled this
	// RegistrationRequest for whatever chain it targeted at that time, and
	// (like the AgentID field) is never chain-tagged — it can read stale
	// "Registered" from a chain the offer (or, when shared, the owner) has
	// since moved off. Every legitimate Phase=Registered transition sets
	// agentID for that same chain in the same reconcile pass, so requiring
	// agentID != "" here costs no real case while closing the stale-phase
	// gap (this is also what tempers the request.Status.AgentID-based
	// widening 84dcbf83 added — that source is gone, but a bare Phase
	// check has the identical staleness problem).
	if agentID != "" {
		message := defaultString(request.Status.Message, fmt.Sprintf("Recorded agent %s", agentID))
		if owner != nil && (owner.Namespace != offer.Namespace || owner.Name != offer.Name) {
			message = fmt.Sprintf("Shared registration via %s/%s recorded agent %s", owner.Namespace, owner.Name, agentID)
		}
		reason := defaultString(request.Status.Phase, "Active")
		if !requestPhaseReady(request.Status.Phase) {
			reason = "Active"
		}
		setCondition(status, "Registered", "True", reason, message)
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

// awaitingExternalRegistrationMessage builds the operator-facing message shown
// while a ServiceOffer's registration is enabled but the on-chain ERC-8004
// registration tx has not yet landed. The on-chain tx is submitted by the
// operator (via `obol sell register`), never by the controller, so the message
// names the exact command and reassures that the offer already serves paid
// traffic — the offer is operationally usable even though Ready stays False
// until the registration is recorded on-chain (see offerOperationallyReady).
func awaitingExternalRegistrationMessage(chain string) string {
	cmd := "obol sell register"
	if chain = strings.TrimSpace(chain); chain != "" {
		cmd += " --network " + chain
	}
	return truncateMessage("Awaiting external ERC-8004 registration tx — submit with `" + cmd + "`; offer already serves paid traffic")
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
