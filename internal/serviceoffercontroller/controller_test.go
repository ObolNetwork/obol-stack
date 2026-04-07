package serviceoffercontroller

import (
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSelectRegistrationOwnerPrefersOldestEnabledOffer(t *testing.T) {
	now := time.Now().UTC()
	offers := []*monetizeapi.ServiceOffer{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "newer",
				Namespace:         "llm",
				CreationTimestamp: metav1.NewTime(now.Add(2 * time.Minute)),
			},
			Spec: monetizeapi.ServiceOfferSpec{
				Registration: monetizeapi.ServiceOfferRegistration{Enabled: true},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "older",
				Namespace:         "llm",
				CreationTimestamp: metav1.NewTime(now),
			},
			Spec: monetizeapi.ServiceOfferSpec{
				Registration: monetizeapi.ServiceOfferRegistration{Enabled: true},
			},
		},
	}

	owner := selectRegistrationOwner(offers)
	if owner == nil || owner.Name != "older" {
		t.Fatalf("owner = %v, want older", owner)
	}
}

func TestSelectRegistrationOwnerBreaksTiesByNamespaceAndName(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	offers := []*monetizeapi.ServiceOffer{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "zz", CreationTimestamp: now},
			Spec:       monetizeapi.ServiceOfferSpec{Registration: monetizeapi.ServiceOfferRegistration{Enabled: true}},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "aa", CreationTimestamp: now},
			Spec:       monetizeapi.ServiceOfferSpec{Registration: monetizeapi.ServiceOfferRegistration{Enabled: true}},
		},
	}

	owner := selectRegistrationOwner(offers)
	if owner == nil || owner.Namespace != "aa" || owner.Name != "a" {
		t.Fatalf("owner = %v, want aa/a", owner)
	}
}

func TestSelectRegistrationOwnerEmpty(t *testing.T) {
	if owner := selectRegistrationOwner(nil); owner != nil {
		t.Fatalf("owner = %v, want nil", owner)
	}
}

func TestShouldRefreshSkillCatalogWhenGenerationObservedLags(t *testing.T) {
	controller := &Controller{}
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "demo",
			Namespace:  "llm",
			Generation: 2,
		},
		Status: monetizeapi.ServiceOfferStatus{
			ObservedGeneration: 1,
		},
	}

	if !controller.shouldRefreshSkillCatalog(offer, offer.Status) {
		t.Fatal("expected catalog refresh when observedGeneration lags the current generation")
	}
}

func TestShouldRefreshSkillCatalogWhenOfferIsPaused(t *testing.T) {
	controller := &Controller{}
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "demo",
			Namespace:   "llm",
			Annotations: map[string]string{monetizeapi.PausedAnnotation: "true"},
		},
		Status: monetizeapi.ServiceOfferStatus{
			ObservedGeneration: 1,
		},
	}

	if !controller.shouldRefreshSkillCatalog(offer, offer.Status) {
		t.Fatal("expected catalog refresh when a ready offer becomes paused")
	}
}
