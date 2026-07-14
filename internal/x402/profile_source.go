package x402

import (
	"context"
	"fmt"
	"log"

	"github.com/ObolNetwork/obol-stack/internal/storefront"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

var configMapGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

// WatchStorefrontProfile keeps the verifier's page branding in sync with the
// operator's obol-storefront-profile ConfigMap (written by `obol sell info
// set`). Absence of the ConfigMap — or an unparseable payload — reverts to
// stack defaults; branding must never take a page (or the verifier) down.
func WatchStorefrontProfile(ctx context.Context, cfg *rest.Config) error {
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(client, 0, storefront.ProfileNamespace, func(options *metav1.ListOptions) {
		options.FieldSelector = fields.OneTermEqualSelector("metadata.name", storefront.ProfileConfigMap).String()
	})
	informer := factory.ForResource(configMapGVR).Informer()

	refresh := func() {
		items := informer.GetStore().List()
		if len(items) == 0 {
			SetStorefrontProfile(nil)
			return
		}
		u, ok := items[0].(*unstructured.Unstructured)
		if !ok {
			return
		}
		raw, _, _ := unstructured.NestedString(u.Object, "data", storefront.ProfileDataKey)
		profile, err := storefront.ParseProfile(raw)
		if err != nil {
			log.Printf("x402-profile-source: parse %s/%s: %v (keeping defaults)",
				storefront.ProfileNamespace, storefront.ProfileConfigMap, err)
			SetStorefrontProfile(nil)
			return
		}
		SetStorefrontProfile(profile)
		log.Printf("x402-profile-source: storefront branding reloaded")
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { refresh() },
		UpdateFunc: func(_, _ any) { refresh() },
		DeleteFunc: func(any) { refresh() },
	})

	go informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("wait for storefront profile informer sync")
	}
	refresh()
	<-ctx.Done()
	return nil
}
