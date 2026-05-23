package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// These tests cover the CRD-design fixes in the
// spec.paused + metav1.Condition PR. The behaviour they pin down is
// also exercised end-to-end by render_test.go / controller_test.go,
// but this file isolates each invariant so a future refactor that
// breaks one of them fails with a name that matches the invariant.

// --- IsPaused: spec field wins, annotation is back-compat -------------------

func TestIsPaused_SpecPausedTakesPrecedence(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{Paused: true},
	}
	if !offer.IsPaused() {
		t.Fatalf("spec.paused=true should pause")
	}
	if offer.IsPausedByAnnotation() {
		t.Fatalf("spec.paused=true must not register as paused-by-annotation")
	}
}

func TestIsPaused_LegacyAnnotationStillHonoured(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{}
	offer.Annotations = map[string]string{monetizeapi.PausedAnnotation: "true"}
	if !offer.IsPaused() {
		t.Fatalf("legacy obol.org/paused=true should still pause for back-compat")
	}
	if !offer.IsPausedByAnnotation() {
		t.Fatalf("annotation-only pause must register as paused-by-annotation so deprecation log fires")
	}
}

func TestIsPaused_NotPaused(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{}
	if offer.IsPaused() {
		t.Fatalf("default ServiceOffer must not be paused")
	}
}

// --- recordPausedCondition writes a typed status condition ------------------

func TestRecordPausedCondition_SpecPath(t *testing.T) {
	c := &Controller{}
	offer := &monetizeapi.ServiceOffer{
		Spec: monetizeapi.ServiceOfferSpec{Paused: true},
	}
	offer.Generation = 7
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 7}

	c.recordPausedCondition(&status, offer, offer.IsPaused(), offer.IsPausedByAnnotation())

	cond := apimeta.FindStatusCondition(status.Conditions, "Paused")
	if cond == nil {
		t.Fatalf("Paused condition was not appended to status")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Fatalf("Paused.Status = %q, want True", cond.Status)
	}
	if cond.Reason != "PausedBySpec" {
		t.Fatalf("Paused.Reason = %q, want PausedBySpec", cond.Reason)
	}
	if cond.ObservedGeneration != 7 {
		t.Fatalf("Paused.ObservedGeneration = %d, want 7", cond.ObservedGeneration)
	}
}

func TestRecordPausedCondition_AnnotationPath(t *testing.T) {
	c := &Controller{}
	offer := &monetizeapi.ServiceOffer{}
	offer.Generation = 3
	offer.Annotations = map[string]string{monetizeapi.PausedAnnotation: "true"}
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 3}

	c.recordPausedCondition(&status, offer, offer.IsPaused(), offer.IsPausedByAnnotation())

	cond := apimeta.FindStatusCondition(status.Conditions, "Paused")
	if cond == nil {
		t.Fatalf("Paused condition missing")
	}
	if cond.Reason != "PausedByAnnotation" {
		t.Fatalf("Paused.Reason = %q, want PausedByAnnotation (so operators see the deprecation)", cond.Reason)
	}
	if !strings.Contains(cond.Message, "v0.11.0") {
		t.Fatalf("Paused.Message %q should mention the v0.11.0 deprecation cut", cond.Message)
	}
}

func TestRecordPausedCondition_NotPaused(t *testing.T) {
	c := &Controller{}
	offer := &monetizeapi.ServiceOffer{}
	offer.Generation = 1
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 1}

	c.recordPausedCondition(&status, offer, false, false)

	cond := apimeta.FindStatusCondition(status.Conditions, "Paused")
	if cond == nil {
		t.Fatalf("Paused=False condition should be present (api-conventions: explicit False, not absent)")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Fatalf("Paused.Status = %q, want False", cond.Status)
	}
	if cond.Reason != "NotPaused" {
		t.Fatalf("Paused.Reason = %q, want NotPaused", cond.Reason)
	}
}

// --- setCondition populates ObservedGeneration ------------------------------

func TestSetCondition_PopulatesObservedGeneration(t *testing.T) {
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 42}
	setCondition(&status, "Ready", "True", "Reconciled", "ok")

	cond := apimeta.FindStatusCondition(status.Conditions, "Ready")
	if cond == nil {
		t.Fatalf("Ready not set")
	}
	if cond.ObservedGeneration != 42 {
		t.Fatalf("ObservedGeneration = %d, want 42 (must inherit from status.ObservedGeneration)", cond.ObservedGeneration)
	}
}

func TestSetConditionWithGeneration_PinsExplicitValue(t *testing.T) {
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 1}
	setConditionWithGeneration(&status, "Ready", "False", "Reconciling", "wait", 9)

	cond := apimeta.FindStatusCondition(status.Conditions, "Ready")
	if cond == nil || cond.ObservedGeneration != 9 {
		t.Fatalf("explicit generation arg should win over status.ObservedGeneration; got cond=%+v", cond)
	}
}

// --- rollupReady is the AND of the five predicate conditions ----------------

func TestRollupReady_AllTrue(t *testing.T) {
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 4}
	for _, t := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished", "Registered"} {
		setCondition(&status, t, "True", "ok", "ok")
	}

	if !rollupReady(&status, 4) {
		t.Fatalf("rollupReady should be true when all five predicates are True")
	}
	ready := apimeta.FindStatusCondition(status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition should be True, got %+v", ready)
	}
}

func TestRollupReady_OneFalseFlipsReadyFalse(t *testing.T) {
	status := monetizeapi.ServiceOfferStatus{ObservedGeneration: 5}
	for _, t := range []string{"ModelReady", "UpstreamHealthy", "PaymentGateReady", "RoutePublished"} {
		setCondition(&status, t, "True", "ok", "ok")
	}
	// One predicate False — Ready must NOT be True.
	setCondition(&status, "Registered", "False", "Pending", "wait")

	if rollupReady(&status, 5) {
		t.Fatalf("rollupReady must be false when any predicate is False")
	}
	ready := apimeta.FindStatusCondition(status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("Ready should be False, got %+v", ready)
	}
	if ready.ObservedGeneration != 5 {
		t.Fatalf("Ready.ObservedGeneration should propagate the generation arg, got %d", ready.ObservedGeneration)
	}
}
