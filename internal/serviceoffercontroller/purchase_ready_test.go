package serviceoffercontroller

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestReconcilePurchaseReady_BuyerOutageFlipsReady pins that a Ready purchase
// does not keep reporting Ready=True while the x402-buyer status endpoint is
// unavailable, and that it returns to Ready=True once the buyer answers again
// (even with fewer remaining auths than were pre-signed).
func TestReconcilePurchaseReady_BuyerOutageFlipsReady(t *testing.T) {
	buyerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "x402-buyer",
			Namespace: "llm",
			Labels:    map[string]string{"app": "x402-buyer"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.2"},
	}
	transport := &staticTransport{hostToBody: map[string]string{
		"10.0.0.2:8402": `{"solo":{"remaining":3,"spent":0}}`,
	}}
	kubeClient := fake.NewSimpleClientset(buyerPod)
	c := &Controller{
		kubeClient: kubeClient,
		httpClient: &http.Client{Transport: transport},
	}
	pr := &monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "solo", Namespace: "agent"},
		Spec:       monetizeapi.PurchaseRequestSpec{PreSignedAuths: makePreSignedAuths("n", 3)},
	}
	status := &monetizeapi.PurchaseRequestStatus{}
	ready := func() monetizeapi.Condition {
		t.Helper()
		for _, cond := range status.Conditions {
			if cond.Type == "Ready" {
				return cond
			}
		}
		t.Fatal("Ready condition missing")
		return monetizeapi.Condition{}
	}
	ctx := context.Background()

	c.reconcilePurchaseReady(ctx, status, pr)
	if got := ready(); got.Status != "True" {
		t.Fatalf("initial Ready = %s/%s, want True", got.Status, got.Reason)
	}

	// Buyer pod disappears: Ready must go False and carry the error.
	if err := kubeClient.CoreV1().Pods("llm").Delete(ctx, "x402-buyer", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	c.reconcilePurchaseReady(ctx, status, pr)
	got := ready()
	if got.Status != "False" || got.Reason != purchaseReasonBuyerUnavailable {
		t.Fatalf("Ready during outage = %s/%s, want False/%s", got.Status, got.Reason, purchaseReasonBuyerUnavailable)
	}
	if !strings.Contains(got.Message, "no x402-buyer pods") {
		t.Fatalf("Ready message = %q, want it to carry the status error", got.Message)
	}

	// A second failed check keeps it False.
	c.reconcilePurchaseReady(ctx, status, pr)
	if got := ready(); got.Status != "False" || got.Reason != purchaseReasonBuyerUnavailable {
		t.Fatalf("Ready on repeated outage = %s/%s, want False/%s", got.Status, got.Reason, purchaseReasonBuyerUnavailable)
	}

	// Buyer comes back after one auth was spent: Ready returns to True.
	transport.hostToBody["10.0.0.2:8402"] = `{"solo":{"remaining":2,"spent":1}}`
	if _, err := kubeClient.CoreV1().Pods("llm").Create(ctx, buyerPod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	c.reconcilePurchaseReady(ctx, status, pr)
	if got := ready(); got.Status != "True" {
		t.Fatalf("Ready after recovery = %s/%s (%s), want True", got.Status, got.Reason, got.Message)
	}
	if status.Remaining != 2 || status.Spent != 1 {
		t.Fatalf("remaining/spent = %d/%d, want 2/1", status.Remaining, status.Spent)
	}
}

// TestReconcilePurchaseReady_NeverReadyKeepsSidecarNotReady pins that a
// purchase which never became Ready keeps the initial-sync reason.
func TestReconcilePurchaseReady_NeverReadyKeepsSidecarNotReady(t *testing.T) {
	c := &Controller{
		kubeClient: fake.NewSimpleClientset(),
		httpClient: &http.Client{Transport: &staticTransport{}},
	}
	pr := &monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "solo", Namespace: "agent"},
	}
	status := &monetizeapi.PurchaseRequestStatus{}
	c.reconcilePurchaseReady(context.Background(), status, pr)
	if len(status.Conditions) != 1 || status.Conditions[0].Reason != "SidecarNotReady" || status.Conditions[0].Status != "False" {
		t.Fatalf("conditions = %+v, want Ready=False/SidecarNotReady", status.Conditions)
	}
}
