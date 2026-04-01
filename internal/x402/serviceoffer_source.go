package x402

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strings"

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

func WatchServiceOffers(ctx context.Context, cfg *rest.Config, apply func([]RouteRule) error) error {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	offerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, nil)
	secretFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, metav1.NamespaceAll, func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", "litellm-secrets").String()
	})
	offers := offerFactory.ForResource(monetizeapi.ServiceOfferGVR).Informer()
	secrets := secretFactory.ForResource(monetizeapi.SecretGVR).Informer()

	refresh := func() {
		routes, err := routesFromStore(offers.GetStore().List(), secrets.GetStore().List())
		if err != nil {
			log.Printf("x402-serviceoffer-source: render routes: %v", err)
			return
		}
		if err := apply(routes); err != nil {
			log.Printf("x402-serviceoffer-source: apply routes: %v", err)
			return
		}
		log.Printf("x402-serviceoffer-source: routes reloaded (%d routes)", len(routes))
	}

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { refresh() },
		UpdateFunc: func(_, _ any) { refresh() },
		DeleteFunc: func(any) { refresh() },
	}
	offers.AddEventHandler(handler)
	secrets.AddEventHandler(handler)

	go offers.Run(ctx.Done())
	go secrets.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), offers.HasSynced, secrets.HasSynced) {
		return fmt.Errorf("wait for serviceoffer informer sync")
	}

	refresh()
	<-ctx.Done()
	return nil
}

func routesFromStore(offerItems, secretItems []any) ([]RouteRule, error) {
	upstreamAuthByNamespace, err := upstreamAuthByNamespace(secretItems)
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
		if offer.IsPaused() || !offerConditionTrue(offer.Status, "RoutePublished") {
			continue
		}

		rule, err := routeRuleFromOffer(&offer, upstreamAuthByNamespace[offer.EffectiveNamespace()])
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
	price, priceModel, perMTok, approx, err := effectivePrice(offer)
	if err != nil {
		return RouteRule{}, err
	}

	return RouteRule{
		Pattern:                strings.TrimSuffix(offer.EffectivePath(), "/") + "/*",
		Price:                  price,
		Description:            fmt.Sprintf("ServiceOffer %s", offer.Name),
		PayTo:                  offer.Spec.Payment.PayTo,
		Network:                offer.Spec.Payment.Network,
		UpstreamAuth:           effectiveUpstreamAuth(offer, upstreamAuth),
		PriceModel:             priceModel,
		PerMTok:                perMTok,
		ApproxTokensPerRequest: approx,
		OfferNamespace:         offer.Namespace,
		OfferName:              offer.Name,
	}, nil
}

func effectivePrice(offer *monetizeapi.ServiceOffer) (price, priceModel, perMTok string, approx int, err error) {
	switch {
	case offer.Spec.Payment.Price.PerRequest != "":
		return offer.Spec.Payment.Price.PerRequest, "perRequest", "", 0, nil
	case offer.Spec.Payment.Price.PerMTok != "":
		price, err := schemas.ApproximateRequestPriceFromPerMTok(offer.Spec.Payment.Price.PerMTok)
		if err != nil {
			return "", "", "", 0, fmt.Errorf("invalid perMTok price %q: %w", offer.Spec.Payment.Price.PerMTok, err)
		}
		return price, "perMTok", offer.Spec.Payment.Price.PerMTok, schemas.ApproxTokensPerRequest, nil
	case offer.Spec.Payment.Price.PerHour != "":
		return offer.Spec.Payment.Price.PerHour, "perHour", "", 0, nil
	default:
		return "0", "", "", 0, nil
	}
}

func upstreamAuthByNamespace(items []any) (map[string]string, error) {
	result := make(map[string]string)
	for _, item := range items {
		obj, ok := item.(*unstructured.Unstructured)
		if !ok || obj.GetName() != "litellm-secrets" {
			continue
		}

		value, found, err := unstructured.NestedString(obj.Object, "data", "LITELLM_MASTER_KEY")
		if err != nil {
			return nil, err
		}
		if !found || value == "" {
			continue
		}

		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, err
		}
		token := strings.TrimSpace(string(decoded))
		if token == "" {
			continue
		}
		result[obj.GetNamespace()] = "Bearer " + token
	}
	return result, nil
}

func effectiveUpstreamAuth(offer *monetizeapi.ServiceOffer, upstreamAuth string) string {
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
