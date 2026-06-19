package x402

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// WatchServiceOffers runs the ServiceOffer + upstream-auth Secret informers and
// pushes rendered RouteRules to apply on every change. The optional
// onFirstApply callback is invoked exactly once after the post-cache-sync
// refresh succeeds; it is the signal that the route source has produced its
// first usable snapshot. Pass nil to skip.
func WatchServiceOffers(ctx context.Context, cfg *rest.Config, apply func([]RouteRule) error, onFirstApply func()) error {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	offerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, nil)
	litellmSecretFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "litellm-secrets").String()
	})
	hermesSecretFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "hermes-api-server").String()
	})
	offers := offerFactory.ForResource(monetizeapi.ServiceOfferGVR).Informer()
	litellmSecrets := litellmSecretFactory.ForResource(monetizeapi.SecretGVR).Informer()
	hermesSecrets := hermesSecretFactory.ForResource(monetizeapi.SecretGVR).Informer()

	refresh := func() (ok bool) {
		secretItems := append([]any{}, litellmSecrets.GetStore().List()...)
		secretItems = append(secretItems, hermesSecrets.GetStore().List()...)
		routes, err := routesFromStore(offers.GetStore().List(), secretItems)
		if err != nil {
			log.Printf("x402-serviceoffer-source: render routes: %v", err)
			return false
		}
		if err := apply(routes); err != nil {
			log.Printf("x402-serviceoffer-source: apply routes: %v", err)
			return false
		}
		log.Printf("x402-serviceoffer-source: routes reloaded (%d routes)", len(routes))
		return true
	}

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { refresh() },
		UpdateFunc: func(_, _ any) { refresh() },
		DeleteFunc: func(any) { refresh() },
	}
	offers.AddEventHandler(handler)
	litellmSecrets.AddEventHandler(handler)
	hermesSecrets.AddEventHandler(handler)

	go offers.Run(ctx.Done())
	go litellmSecrets.Run(ctx.Done())
	go hermesSecrets.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), offers.HasSynced, litellmSecrets.HasSynced, hermesSecrets.HasSynced) {
		return fmt.Errorf("wait for serviceoffer informer sync")
	}

	if refresh() && onFirstApply != nil {
		onFirstApply()
	}
	<-ctx.Done()
	return nil
}

func routesFromStore(offerItems, secretItems []any) ([]RouteRule, error) {
	litellmAuthByNamespace, hermesAuthByNamespace, err := upstreamAuthByNamespace(secretItems)
	if err != nil {
		return nil, err
	}

	routes := make([]RouteRule, 0, len(offerItems))
	for _, item := range offerItems {
		obj, ok := item.(*unstructured.Unstructured)
		if !ok {
			continue
		}

		var offer monetizeapi.ServiceOffer
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &offer); err != nil {
			return nil, err
		}
		if offer.Spec.Upstream.Namespace == "" {
			offer.Spec.Upstream.Namespace = offer.Namespace
		}
		// Draining offers keep their route up until the grace period
		// expires so in-flight payments can settle. Only skip after the
		// drain window has elapsed — at that point the controller has
		// also torn down the HTTPRoute, so the verifier rule would
		// gate traffic against a non-existent backend.
		if offer.DrainExpired(time.Now()) || !offerConditionTrue(offer.Status, "RoutePublished") {
			continue
		}

		upstreamAuth := litellmAuthByNamespace[offer.EffectiveNamespace()]
		if offer.IsAgent() {
			upstreamAuth = hermesAuthByNamespace[offer.Spec.Agent.Ref.Namespace]
		}
		rule, err := routeRuleFromOffer(&offer, upstreamAuth)
		if err != nil {
			return nil, err
		}
		routes = append(routes, rule)
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].OfferNamespace != routes[j].OfferNamespace {
			return routes[i].OfferNamespace < routes[j].OfferNamespace
		}
		if routes[i].OfferName != routes[j].OfferName {
			return routes[i].OfferName < routes[j].OfferName
		}
		return routes[i].Pattern < routes[j].Pattern
	})

	return routes, nil
}

func routeRuleFromOffer(offer *monetizeapi.ServiceOffer, upstreamAuth string) (RouteRule, error) {
	// Build one RoutePayment per accepted payment option. EffectivePayments
	// returns the multi-payment list when present, else a one-element slice
	// from spec.payment — so single- and multi-payment offers share this path.
	payments := offer.EffectivePayments()
	routePayments := make([]RoutePayment, 0, len(payments))
	for i := range payments {
		rp, err := routePaymentFromSpec(payments[i])
		if err != nil {
			return RouteRule{}, err
		}
		routePayments = append(routePayments, rp)
	}
	// The primary option (payments[0]) populates the inline fields so the
	// HTML 402 page, metrics defaults, and any direct rule.PayTo/Network
	// readers keep working unchanged for single-payment offers.
	primary := routePayments[0]

	// Agent-type offers derive their upstream URL from the controller's
	// resolved view (ServiceOffer.status.agentResolution), which the
	// reconciler populates after looking up the referenced Agent CR. The
	// non-agent path keeps the existing spec-based synthesis.
	upstreamURL := ""
	if offer.IsAgent() && offer.Status.AgentResolution != nil && offer.Status.AgentResolution.Endpoint != "" {
		upstreamURL = offer.Status.AgentResolution.Endpoint
	} else {
		upstreamURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d",
			offer.Spec.Upstream.Service,
			offer.EffectiveNamespace(),
			offer.EffectivePort(),
		)
	}
	stripPrefix := offer.EffectivePath()

	rule := RouteRule{
		Pattern:                strings.TrimSuffix(offer.EffectivePath(), "/") + "/*",
		Price:                  primary.Price,
		Description:            offer.Spec.Registration.Description,
		OfferType:              offer.Spec.Type,
		PayTo:                  primary.PayTo,
		Network:                primary.Network,
		AssetAddress:           primary.AssetAddress,
		AssetSymbol:            primary.AssetSymbol,
		AssetDecimals:          primary.AssetDecimals,
		AssetTransferMethod:    primary.AssetTransferMethod,
		EIP712Name:             primary.EIP712Name,
		EIP712Version:          primary.EIP712Version,
		UpstreamAuth:           effectiveUpstreamAuth(offer, upstreamAuth),
		UpstreamURL:            upstreamURL,
		StripPrefix:            stripPrefix,
		PriceModel:             primary.PriceModel,
		PerMTok:                primary.PerMTok,
		ApproxTokensPerRequest: primary.ApproxTokensPerRequest,
		OfferNamespace:         offer.Namespace,
		OfferName:              offer.Name,
		MaxTimeoutSeconds:      primary.MaxTimeoutSeconds,
		Payments:               routePayments,
	}

	if offer.IsAgent() && offer.Status.AgentResolution != nil {
		res := offer.Status.AgentResolution
		rule.AgentModel = res.Model
		rule.AgentSkills = append([]string(nil), res.Skills...)
		rule.AgentRuntime = res.Runtime
		rule.Model = res.Model
	} else {
		rule.Model = offer.Spec.Model.Name
	}

	return rule, nil
}

// routePaymentFromSpec converts a single ServiceOffer payment option into a
// RoutePayment, resolving the enforced request price from whichever price
// slot is set (and approximating perMTok into a per-request charge for the
// phase-1 request-based gate). Network is normalized to CAIP-2 so the
// verifier's chain lookup resolves it.
func routePaymentFromSpec(p monetizeapi.ServiceOfferPayment) (RoutePayment, error) {
	price, priceModel, perMTok, approx, err := effectivePriceForOption(p)
	if err != nil {
		return RoutePayment{}, err
	}
	return RoutePayment{
		Price:                  price,
		PayTo:                  p.PayTo,
		Network:                NormalizeNetworkID(p.Network),
		AssetAddress:           p.Asset.Address,
		AssetSymbol:            p.Asset.Symbol,
		AssetDecimals:          int(p.Asset.Decimals),
		AssetTransferMethod:    p.Asset.TransferMethod,
		EIP712Name:             p.Asset.EIP712Name,
		EIP712Version:          p.Asset.EIP712Version,
		PriceModel:             priceModel,
		PerMTok:                perMTok,
		ApproxTokensPerRequest: approx,
		MaxTimeoutSeconds:      p.MaxTimeoutSeconds,
	}, nil
}

func effectivePriceForOption(p monetizeapi.ServiceOfferPayment) (price, priceModel, perMTok string, approx int, err error) {
	switch {
	case p.Price.PerRequest != "":
		return p.Price.PerRequest, "perRequest", "", 0, nil
	case p.Price.PerMTok != "":
		price, err := schemas.ApproximateRequestPriceFromPerMTok(p.Price.PerMTok)
		if err != nil {
			return "", "", "", 0, fmt.Errorf("invalid perMTok price %q: %w", p.Price.PerMTok, err)
		}
		return price, "perMTok", p.Price.PerMTok, schemas.ApproxTokensPerRequest, nil
	case p.Price.PerHour != "":
		return p.Price.PerHour, "perHour", "", 0, nil
	default:
		return "0", "", "", 0, nil
	}
}

func upstreamAuthByNamespace(items []any) (map[string]string, map[string]string, error) {
	litellmAuth := make(map[string]string)
	hermesAuth := make(map[string]string)
	for _, item := range items {
		obj, ok := item.(*unstructured.Unstructured)
		if !ok {
			continue
		}
		dataKey := ""
		switch obj.GetName() {
		case "litellm-secrets":
			dataKey = "LITELLM_MASTER_KEY"
		case "hermes-api-server":
			dataKey = "API_SERVER_KEY"
		default:
			continue
		}

		value, found, err := unstructured.NestedString(obj.Object, "data", dataKey)
		if err != nil {
			return nil, nil, err
		}
		if !found || value == "" {
			continue
		}

		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, nil, err
		}
		token := strings.TrimSpace(string(decoded))
		if token == "" {
			continue
		}
		switch obj.GetName() {
		case "litellm-secrets":
			litellmAuth[obj.GetNamespace()] = "Bearer " + token
		case "hermes-api-server":
			hermesAuth[obj.GetNamespace()] = "Bearer " + token
		}
	}
	return litellmAuth, hermesAuth, nil
}

func effectiveUpstreamAuth(offer *monetizeapi.ServiceOffer, upstreamAuth string) string {
	if offer.IsAgent() {
		return upstreamAuth
	}
	if !strings.EqualFold(offer.Spec.Upstream.Service, "litellm") {
		return ""
	}
	return upstreamAuth
}

func offerConditionTrue(status monetizeapi.ServiceOfferStatus, conditionType string) bool {
	for _, condition := range status.Conditions {
		if condition.Type == conditionType {
			return condition.Status == "True"
		}
	}
	return false
}
