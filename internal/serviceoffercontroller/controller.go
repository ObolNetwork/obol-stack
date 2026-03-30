package serviceoffercontroller

import (
	"context"
	"crypto/ecdsa"
	"crypto/md5"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ethereum/go-ethereum/crypto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	serviceOfferFinalizer = "monetize.obol.org/finalizer"

	registrationDesiredActive     = "Active"
	registrationDesiredTombstoned = "Tombstoned"

	registrationPhasePublishing   = "Publishing"
	registrationPhaseRegistered   = "Registered"
	registrationPhaseOffChainOnly = "OffChainOnly"
	registrationPhaseTombstoned   = "Tombstoned"
)

type Controller struct {
	client               dynamic.Interface
	offers               dynamic.NamespaceableResourceInterface
	registrationRequests dynamic.NamespaceableResourceInterface
	services             dynamic.NamespaceableResourceInterface
	configMaps           dynamic.NamespaceableResourceInterface
	deployments          dynamic.NamespaceableResourceInterface
	middlewares          dynamic.NamespaceableResourceInterface
	httpRoutes           dynamic.NamespaceableResourceInterface

	offerInformer        cache.SharedIndexInformer
	registrationInformer cache.SharedIndexInformer
	offerQueue           workqueue.TypedRateLimitingInterface[string]
	registrationQueue    workqueue.TypedRateLimitingInterface[string]

	httpClient *http.Client

	registrationKey    *ecdsa.PrivateKey
	registrationRPCURL string
	baseURLOverride    string
	defaultBaseURL     string
}

func New(cfg *rest.Config) (*Controller, error) {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	registrationKey, err := loadRegistrationSigningKey()
	if err != nil {
		return nil, err
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 30*time.Second, metav1.NamespaceAll, nil)
	offerInformer := factory.ForResource(monetizeapi.ServiceOfferGVR).Informer()
	registrationInformer := factory.ForResource(monetizeapi.RegistrationRequestGVR).Informer()

	controller := &Controller{
		client:               client,
		offers:               client.Resource(monetizeapi.ServiceOfferGVR),
		registrationRequests: client.Resource(monetizeapi.RegistrationRequestGVR),
		services:             client.Resource(monetizeapi.ServiceGVR),
		configMaps:           client.Resource(monetizeapi.ConfigMapGVR),
		deployments:          client.Resource(monetizeapi.DeploymentGVR),
		middlewares:          client.Resource(monetizeapi.MiddlewareGVR),
		httpRoutes:           client.Resource(monetizeapi.HTTPRouteGVR),
		offerInformer:        offerInformer,
		registrationInformer: registrationInformer,
		offerQueue:           workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		registrationQueue:    workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		httpClient:           &http.Client{Timeout: 10 * time.Second},
		registrationKey:      registrationKey,
		registrationRPCURL:   getenvDefault("ERC8004_RPC_URL", erc8004.DefaultRPCURL),
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

	return controller, nil
}

func (c *Controller) Run(ctx context.Context, workers int) error {
	defer c.offerQueue.ShutDown()
	defer c.registrationQueue.ShutDown()

	go c.offerInformer.Run(ctx.Done())
	go c.registrationInformer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), c.offerInformer.HasSynced, c.registrationInformer.HasSynced) {
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
	var request monetizeapi.RegistrationRequest
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &request); err != nil {
		log.Printf("serviceoffer-controller: decode registrationrequest for parent enqueue: %v", err)
		return
	}
	if request.Spec.ServiceOfferNamespace == "" || request.Spec.ServiceOfferName == "" {
		return
	}
	c.offerQueue.Add(request.Spec.ServiceOfferNamespace + "/" + request.Spec.ServiceOfferName)
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
		if !containsFinalizer(raw, serviceOfferFinalizer) {
			return nil
		}
		if err := c.reconcileDeletingOffer(ctx, offer); err != nil {
			return err
		}
		if err := c.reconcileSkillCatalog(ctx); err != nil {
			return err
		}
		return c.removeFinalizer(ctx, raw, serviceOfferFinalizer)
	}

	if !containsFinalizer(raw, serviceOfferFinalizer) {
		return c.addFinalizer(ctx, raw, serviceOfferFinalizer)
	}

	status := offer.Status
	status.ObservedGeneration = offer.Generation
	status.Endpoint = offer.EffectivePath()

	if err := c.reconcileModel(statusFor(&status), offer); err != nil {
		return err
	}

	upstreamHealthy, err := c.reconcileUpstream(ctx, statusFor(&status), offer)
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
		if err := c.reconcilePaymentGate(ctx, statusFor(&status), offer); err != nil {
			return err
		}
		if isConditionTrue(status, "PaymentGateReady") {
			if err := c.reconcileRoute(ctx, statusFor(&status), offer); err != nil {
				return err
			}
		}
	} else {
		setCondition(&status, "PaymentGateReady", "False", "WaitingForUpstream", "Waiting for upstream health before publishing payment gate")
		setCondition(&status, "RoutePublished", "False", "WaitingForPaymentGate", "Waiting for payment gate before publishing route")
	}

	if err := c.reconcileRegistrationStatus(ctx, statusFor(&status), offer); err != nil {
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
	return c.reconcileSkillCatalog(ctx)
}

func (c *Controller) reconcileDeletingOffer(ctx context.Context, offer *monetizeapi.ServiceOffer) error {
	if err := c.deleteRouteChildren(ctx, offer); err != nil {
		return err
	}

	if !offer.Spec.Registration.Enabled && strings.TrimSpace(offer.Status.AgentID) == "" {
		return c.deleteRegistrationRequest(ctx, offer.Namespace, offer.Name)
	}

	ready, err := c.ensureRegistrationCleanup(ctx, offer)
	if err != nil {
		return err
	}
	if !ready {
		return nil
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
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
	if err := c.applyObject(ctx, c.middlewares.Namespace(offer.Namespace), buildMiddleware(offer)); err != nil {
		setCondition(status, "PaymentGateReady", "False", "ApplyFailed", err.Error())
		return err
	}
	setCondition(status, "PaymentGateReady", "True", "Reconciled", "Middleware is present")
	return nil
}

func (c *Controller) reconcileRoute(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if err := c.applyObject(ctx, c.httpRoutes.Namespace(offer.Namespace), buildHTTPRoute(offer)); err != nil {
		setCondition(status, "RoutePublished", "False", "ApplyFailed", err.Error())
		return err
	}
	setCondition(status, "RoutePublished", "True", "Reconciled", fmt.Sprintf("HTTPRoute published at %s", offer.EffectivePath()))
	return nil
}

func (c *Controller) reconcileRegistrationStatus(ctx context.Context, status *monetizeapi.ServiceOfferStatus, offer *monetizeapi.ServiceOffer) error {
	if !offer.Spec.Registration.Enabled {
		setCondition(status, "Registered", "True", "Disabled", "Registration disabled")
		return nil
	}
	if !isConditionTrue(*status, "RoutePublished") {
		setCondition(status, "Registered", "False", "WaitingForRoute", "Waiting for route publication before registration")
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

	if requestPhaseReady(request.Status.Phase) {
		setCondition(status, "Registered", "True", request.Status.Phase, defaultString(request.Status.Message, "Registration reconciled"))
		return nil
	}

	reason := defaultString(request.Status.Phase, "Pending")
	message := defaultString(request.Status.Message, "Waiting for RegistrationRequest to finish")
	setCondition(status, "Registered", "False", reason, message)
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

	document := buildActiveRegistrationDocument(offer, baseURL, agentID)
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

	if agentID == "" && c.registrationKey != nil {
		client, err := erc8004.NewClient(ctx, c.registrationRPCURL)
		if err != nil {
			status.Phase = registrationPhaseOffChainOnly
			status.Message = truncateMessage(fmt.Sprintf("Published off-chain registration only: %v", err))
			return c.updateRegistrationStatus(ctx, raw, status)
		}
		defer client.Close()

		registeredID, registeredTxHash, err := client.RegisterDetailed(ctx, c.registrationKey, status.PublishedURL)
		if err != nil {
			status.Phase = registrationPhaseOffChainOnly
			status.Message = truncateMessage(fmt.Sprintf("Published off-chain registration only: %v", err))
			return c.updateRegistrationStatus(ctx, raw, status)
		}

		agentID = registeredID.String()
		txHash = registeredTxHash
		status.AgentID = agentID
		status.RegistrationTxHash = txHash

		_ = client.SetMetadata(ctx, c.registrationKey, registeredID, "x402.supported", []byte{1})
		_ = client.SetMetadata(ctx, c.registrationKey, registeredID, "service.type", []byte(fallbackOfferType(offer)))

		document = buildActiveRegistrationDocument(offer, baseURL, agentID)
		documentJSON, contentHash, err = marshalRegistrationDocument(document)
		if err != nil {
			return err
		}
		if err := c.publishRegistrationResources(ctx, request, documentJSON, contentHash); err != nil {
			return err
		}
	}

	status.AgentID = agentID
	status.RegistrationTxHash = txHash
	if agentID != "" {
		status.Phase = registrationPhaseRegistered
		status.Message = fmt.Sprintf("Published registration document and recorded agent %s", agentID)
	} else {
		status.Phase = registrationPhaseOffChainOnly
		status.Message = "Published registration document; controller has no ERC-8004 signing key"
	}

	return c.updateRegistrationStatus(ctx, raw, status)
}

func (c *Controller) reconcileRegistrationTombstone(ctx context.Context, raw *unstructured.Unstructured, request *monetizeapi.RegistrationRequest, offer *monetizeapi.ServiceOffer, baseURL string) error {
	status := request.Status
	agentID := firstNonEmpty(status.AgentID, offer.Status.AgentID)

	if agentID != "" && c.registrationKey != nil {
		client, err := erc8004.NewClient(ctx, c.registrationRPCURL)
		if err != nil {
			status.Phase = registrationPhaseOffChainOnly
			status.Message = truncateMessage(fmt.Sprintf("Deleted registration resources but could not connect for tombstone: %v", err))
			if err := c.deleteRegistrationResources(ctx, request); err != nil {
				return err
			}
			return c.updateRegistrationStatus(ctx, raw, status)
		}
		defer client.Close()

		agentIDBig, ok := newBigInt(agentID)
		if !ok {
			return fmt.Errorf("invalid agent id %q", agentID)
		}
		tombstoneURI, err := registrationDataURL(buildTombstoneRegistrationDocument(offer, baseURL, agentID))
		if err != nil {
			return err
		}
		if err := client.SetAgentURI(ctx, c.registrationKey, agentIDBig, tombstoneURI); err != nil {
			status.Phase = registrationPhaseOffChainOnly
			status.Message = truncateMessage(fmt.Sprintf("Deleted registration resources but could not tombstone on-chain: %v", err))
			if err := c.deleteRegistrationResources(ctx, request); err != nil {
				return err
			}
			return c.updateRegistrationStatus(ctx, raw, status)
		}
		_ = client.SetMetadata(ctx, c.registrationKey, agentIDBig, "x402.supported", []byte{0})
		status.Phase = registrationPhaseTombstoned
		status.Message = fmt.Sprintf("Tombstoned registration for agent %s", agentID)
	} else if agentID != "" {
		status.Phase = registrationPhaseOffChainOnly
		status.Message = "Deleted registration resources; controller has no ERC-8004 signing key for tombstone"
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
	return nil
}

func (c *Controller) reconcileSkillCatalog(ctx context.Context) error {
	baseURL, err := c.registrationBaseURL(ctx)
	if err != nil {
		return err
	}

	list, err := c.offers.List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	offers := make([]*monetizeapi.ServiceOffer, 0, len(list.Items))
	for i := range list.Items {
		offer, err := decodeServiceOffer(&list.Items[i])
		if err != nil {
			return err
		}
		offers = append(offers, offer)
	}

	content := buildSkillCatalogMarkdown(offers, baseURL)
	contentHash := fmt.Sprintf("%x", md5Sum(content))[:8]

	if err := c.applyObject(ctx, c.configMaps.Namespace(skillCatalogNamespace), buildSkillCatalogConfigMap(content)); err != nil {
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
		{resource: c.middlewares.Namespace(offer.Namespace), name: "x402-" + offer.Name},
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

func (c *Controller) applyObject(ctx context.Context, resource dynamic.ResourceInterface, desired *unstructured.Unstructured) error {
	existing, err := resource.Get(ctx, desired.GetName(), metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = resource.Create(ctx, desired, metav1.CreateOptions{})
		return err
	case err != nil:
		return err
	default:
		desired.SetResourceVersion(existing.GetResourceVersion())
		_, err = resource.Update(ctx, desired, metav1.UpdateOptions{})
		return err
	}
}

func (c *Controller) updateOfferStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.ServiceOfferStatus) error {
	copy := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	copy.Object["status"] = statusObject
	_, err = c.offers.Namespace(copy.GetNamespace()).UpdateStatus(ctx, copy, metav1.UpdateOptions{})
	return err
}

func (c *Controller) updateRegistrationStatus(ctx context.Context, raw *unstructured.Unstructured, status monetizeapi.RegistrationRequestStatus) error {
	copy := raw.DeepCopy()
	statusObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
	if err != nil {
		return err
	}
	copy.Object["status"] = statusObject
	_, err = c.registrationRequests.Namespace(copy.GetNamespace()).UpdateStatus(ctx, copy, metav1.UpdateOptions{})
	return err
}

func (c *Controller) addFinalizer(ctx context.Context, raw *unstructured.Unstructured, finalizer string) error {
	copy := raw.DeepCopy()
	copy.SetFinalizers(append(copy.GetFinalizers(), finalizer))
	_, err := c.offers.Namespace(copy.GetNamespace()).Update(ctx, copy, metav1.UpdateOptions{})
	return err
}

func (c *Controller) removeFinalizer(ctx context.Context, raw *unstructured.Unstructured, finalizer string) error {
	copy := raw.DeepCopy()
	finalizers := copy.GetFinalizers()
	filtered := finalizers[:0]
	for _, item := range finalizers {
		if item != finalizer {
			filtered = append(filtered, item)
		}
	}
	copy.SetFinalizers(filtered)
	_, err := c.offers.Namespace(copy.GetNamespace()).Update(ctx, copy, metav1.UpdateOptions{})
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

func containsFinalizer(raw *unstructured.Unstructured, finalizer string) bool {
	for _, item := range raw.GetFinalizers() {
		if item == finalizer {
			return true
		}
	}
	return false
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

func statusFor(status *monetizeapi.ServiceOfferStatus) *monetizeapi.ServiceOfferStatus {
	return status
}

func requestPhaseReady(phase string) bool {
	return phase == registrationPhaseRegistered || phase == registrationPhaseOffChainOnly
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

func newBigInt(value string) (*big.Int, bool) {
	parsed, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	return parsed, ok
}

func loadRegistrationSigningKey() (*ecdsa.PrivateKey, error) {
	keyHex := strings.TrimSpace(os.Getenv("ERC8004_PRIVATE_KEY"))
	if keyHex == "" {
		if path := strings.TrimSpace(os.Getenv("ERC8004_PRIVATE_KEY_FILE")); path != "" {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read ERC8004_PRIVATE_KEY_FILE: %w", err)
			}
			keyHex = strings.TrimSpace(string(data))
		}
	}
	if keyHex == "" {
		return nil, nil
	}

	key, err := crypto.HexToECDSA(strings.TrimPrefix(keyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("parse ERC8004 private key: %w", err)
	}
	return key, nil
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
