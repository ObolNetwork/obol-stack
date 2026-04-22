package serviceoffercontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type fakeBuyerStatus struct {
	Remaining int `json:"remaining"`
	Spent     int `json:"spent"`
}

type fakeBuyerTransport struct {
	mu       sync.Mutex
	payloads []map[string]fakeBuyerStatus
	calls    int
}

func (f *fakeBuyerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "127.0.0.1:8402" {
		switch req.URL.Path {
		case "/status":
			payload := f.currentPayload()
			body, _ := json.Marshal(payload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(bytes.NewReader(body)),
			}, nil
		case "/admin/reload":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
			}, nil
		}
	}

	return http.DefaultTransport.RoundTrip(req)
}

func (f *fakeBuyerTransport) currentPayload() map[string]fakeBuyerStatus {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.payloads) == 0 {
		return map[string]fakeBuyerStatus{}
	}

	idx := f.calls
	if idx >= len(f.payloads) {
		idx = len(f.payloads) - 1
	}
	f.calls++
	return f.payloads[idx]
}

func (f *fakeBuyerTransport) setPayloads(payloads ...map[string]fakeBuyerStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.payloads = append([]map[string]fakeBuyerStatus(nil), payloads...)
	f.calls = 0
}

func mustPurchaseObject(t *testing.T, pr monetizeapi.PurchaseRequest) *unstructured.Unstructured {
	t.Helper()

	pr.TypeMeta = metav1.TypeMeta{
		APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
		Kind:       monetizeapi.PurchaseRequestKind,
	}
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pr)
	if err != nil {
		t.Fatalf("ToUnstructured purchase: %v", err)
	}
	return &unstructured.Unstructured{Object: data}
}

func makePreSignedAuths(prefix string, count int) []monetizeapi.PreSignedAuth {
	auths := make([]monetizeapi.PreSignedAuth, 0, count)
	for i := 0; i < count; i++ {
		auths = append(auths, monetizeapi.PreSignedAuth{
			Signature:   "0xsignature",
			From:        "0xsigner",
			To:          "0xpayto",
			Value:       "1000",
			ValidAfter:  "0",
			ValidBefore: "4294967295",
			Nonce:       prefix + string(rune('a'+i)),
		})
	}
	return auths
}

func newPurchaseInformer() cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{},
		&unstructured.Unstructured{},
		0,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
}

func replacePurchaseInformerStore(t *testing.T, informer cache.SharedIndexInformer, purchases ...*unstructured.Unstructured) {
	t.Helper()

	items := make([]any, 0, len(purchases))
	for _, purchase := range purchases {
		items = append(items, purchase)
	}
	if err := informer.GetStore().Replace(items, "0"); err != nil {
		t.Fatalf("replace informer store: %v", err)
	}
}

func newPurchaseLifecycleController(t *testing.T, purchases ...monetizeapi.PurchaseRequest) (*Controller, *litellmFake, *fakeBuyerTransport) {
	t.Helper()

	objects := []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "litellm-config", Namespace: "llm"},
			Data:       map[string]string{"config.yaml": "model_list: []\n"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "litellm-secrets", Namespace: "llm"},
			Data:       map[string][]byte{"LITELLM_MASTER_KEY": []byte("sk-obol-test")},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: buyerConfigCM, Namespace: "llm"},
			Data:       map[string]string{},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: buyerAuthsCM, Namespace: "llm"},
			Data:       map[string]string{},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "litellm-0",
				Namespace: "llm",
				Labels:    map[string]string{"app": "litellm"},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				PodIP: "127.0.0.1",
			},
		},
	}
	kubeClient := kubefake.NewSimpleClientset(objects...)

	unstructuredPurchases := make([]runtime.Object, 0, len(purchases))
	informerPurchases := make([]*unstructured.Unstructured, 0, len(purchases))
	for _, purchase := range purchases {
		obj := mustPurchaseObject(t, purchase)
		unstructuredPurchases = append(unstructuredPurchases, obj)
		informerPurchases = append(informerPurchases, obj.DeepCopy())
	}

	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			monetizeapi.PurchaseRequestGVR: "PurchaseRequestList",
		},
		unstructuredPurchases...,
	)

	informer := newPurchaseInformer()
	replacePurchaseInformerStore(t, informer, informerPurchases...)

	litellm := newLiteLLMFake()
	buyerTransport := &fakeBuyerTransport{}

	controller := &Controller{
		kubeClient:         kubeClient,
		dynClient:          dynClient,
		purchaseInformer:   informer,
		purchaseQueue:      workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[string]()),
		httpClient:         &http.Client{Transport: buyerTransport, Timeout: 5 * time.Second},
		litellmURLOverride: litellm.server.URL,
	}

	return controller, litellm, buyerTransport
}

func getPurchaseRequest(t *testing.T, c *Controller, namespace, name string) *monetizeapi.PurchaseRequest {
	t.Helper()

	raw, err := c.dynClient.Resource(monetizeapi.PurchaseRequestGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get purchase %s/%s: %v", namespace, name, err)
	}

	var pr monetizeapi.PurchaseRequest
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw.Object, &pr); err != nil {
		t.Fatalf("decode purchase %s/%s: %v", namespace, name, err)
	}
	return &pr
}

func purchaseCondition(t *testing.T, pr *monetizeapi.PurchaseRequest, condType string) monetizeapi.Condition {
	t.Helper()

	for _, condition := range pr.Status.Conditions {
		if condition.Type == condType {
			return condition
		}
	}
	t.Fatalf("missing purchase condition %q", condType)
	return monetizeapi.Condition{}
}

func seedBuyerConfigMaps(t *testing.T, c *Controller, entries map[string]string) {
	t.Helper()

	ctx := context.Background()
	configCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(ctx, buyerConfigCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", buyerConfigCM, err)
	}
	authsCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(ctx, buyerAuthsCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", buyerAuthsCM, err)
	}

	configCM.Data = map[string]string{}
	authsCM.Data = map[string]string{}
	for name, value := range entries {
		configCM.Data[name+".json"] = value
		authsCM.Data[name+".json"] = value
	}
	if _, err := c.kubeClient.CoreV1().ConfigMaps("llm").Update(ctx, configCM, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update %s: %v", buyerConfigCM, err)
	}
	if _, err := c.kubeClient.CoreV1().ConfigMaps("llm").Update(ctx, authsCM, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update %s: %v", buyerAuthsCM, err)
	}
}

func TestReconcilePurchaseHappyPath(t *testing.T) {
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"accepts":[{"network":"base-sepolia","amount":"1000","asset":"0xasset","payTo":"0xpayto"}]}`)
	}))
	defer probeServer.Close()

	purchase := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "solo",
			Namespace:  "agent-ns",
			Generation: 1,
		},
		Spec: monetizeapi.PurchaseRequestSpec{
			Endpoint:       probeServer.URL + "/v1/chat/completions",
			Model:          "qwen3.5:9b",
			Count:          2,
			PreSignedAuths: makePreSignedAuths("solo-", 2),
			Payment: monetizeapi.PurchasePayment{
				Network: "base-sepolia",
				PayTo:   "0xpayto",
				Price:   "1000",
				Asset:   "0xasset",
			},
		},
	}

	c, litellm, buyerTransport := newPurchaseLifecycleController(t, purchase)
	defer litellm.close()
	buyerTransport.setPayloads(map[string]fakeBuyerStatus{
		"solo": {Remaining: 2, Spent: 0},
	})

	if err := c.reconcilePurchase(context.Background(), "agent-ns/solo"); err != nil {
		t.Fatalf("reconcilePurchase add finalizer: %v", err)
	}
	if err := c.reconcilePurchase(context.Background(), "agent-ns/solo"); err != nil {
		t.Fatalf("reconcilePurchase happy path: %v", err)
	}

	got := getPurchaseRequest(t, c, "agent-ns", "solo")
	if !containsFinalizer(mustPurchaseObject(t, *got), purchaseRequestFinalizer) {
		t.Fatal("purchase finalizer missing after reconcile")
	}
	if purchaseCondition(t, got, "Probed").Status != "True" {
		t.Fatal("purchase probe condition should be True")
	}
	if purchaseCondition(t, got, "AuthsLoaded").Status != "True" {
		t.Fatal("purchase auths-loaded condition should be True")
	}
	if purchaseCondition(t, got, "Configured").Status != "True" {
		t.Fatal("purchase configured condition should be True")
	}
	if purchaseCondition(t, got, "Ready").Status != "True" {
		t.Fatal("purchase ready condition should be True")
	}
	if got.Status.Remaining != 2 || got.Status.Spent != 0 {
		t.Fatalf("purchase status remaining/spent = %d/%d, want 2/0", got.Status.Remaining, got.Status.Spent)
	}
	if got.Status.PublicModel != "paid/qwen3.5:9b" {
		t.Fatalf("purchase public model = %q, want paid/qwen3.5:9b", got.Status.PublicModel)
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Fatalf("observedGeneration = %d, want %d", got.Status.ObservedGeneration, got.Generation)
	}

	configCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerConfigCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer config: %v", err)
	}
	if _, ok := configCM.Data["solo.json"]; !ok {
		t.Fatal("buyer config missing solo.json")
	}

	authsCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerAuthsCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer auths: %v", err)
	}
	if _, ok := authsCM.Data["solo.json"]; !ok {
		t.Fatal("buyer auths missing solo.json")
	}

	if gotCalls := litellm.addCalls.Load(); gotCalls != 1 {
		t.Fatalf("litellm hot-add calls = %d, want 1", gotCalls)
	}
}

func TestReconcilePurchaseReadyCatchesUpAfterRuntimeSync(t *testing.T) {
	probeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"accepts":[{"network":"base-sepolia","amount":"1000","asset":"0xasset","payTo":"0xpayto"}]}`)
	}))
	defer probeServer.Close()

	purchase := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "solo",
			Namespace:  "agent-ns",
			Generation: 2,
			Finalizers: []string{purchaseRequestFinalizer},
		},
		Spec: monetizeapi.PurchaseRequestSpec{
			Endpoint:       probeServer.URL + "/v1/chat/completions",
			Model:          "qwen3.5:9b",
			Count:          3,
			PreSignedAuths: makePreSignedAuths("solo-", 3),
			Payment: monetizeapi.PurchasePayment{
				Network: "base-sepolia",
				PayTo:   "0xpayto",
				Price:   "1000",
				Asset:   "0xasset",
			},
		},
	}

	c, litellm, buyerTransport := newPurchaseLifecycleController(t, purchase)
	defer litellm.close()
	buyerTransport.setPayloads(
		map[string]fakeBuyerStatus{"solo": {Remaining: 1, Spent: 2}},
		map[string]fakeBuyerStatus{"solo": {Remaining: 3, Spent: 2}},
	)

	if err := c.reconcilePurchase(context.Background(), "agent-ns/solo"); err != nil {
		t.Fatalf("reconcilePurchase stale runtime: %v", err)
	}

	stale := getPurchaseRequest(t, c, "agent-ns", "solo")
	ready := purchaseCondition(t, stale, "Ready")
	if ready.Status != "False" || ready.Reason != "RuntimeSyncing" {
		t.Fatalf("ready condition after stale runtime = %s/%s, want False/RuntimeSyncing", ready.Status, ready.Reason)
	}
	if stale.Status.Remaining != 1 || stale.Status.Spent != 2 {
		t.Fatalf("stale purchase status remaining/spent = %d/%d, want 1/2", stale.Status.Remaining, stale.Status.Spent)
	}

	if err := c.reconcilePurchase(context.Background(), "agent-ns/solo"); err != nil {
		t.Fatalf("reconcilePurchase synced runtime: %v", err)
	}

	synced := getPurchaseRequest(t, c, "agent-ns", "solo")
	ready = purchaseCondition(t, synced, "Ready")
	if ready.Status != "True" || ready.Reason != "Reconciled" {
		t.Fatalf("ready condition after catch-up = %s/%s, want True/Reconciled", ready.Status, ready.Reason)
	}
	if synced.Status.Remaining != 3 || synced.Status.Spent != 2 {
		t.Fatalf("synced purchase status remaining/spent = %d/%d, want 3/2", synced.Status.Remaining, synced.Status.Spent)
	}
}

func TestReconcilePurchaseReadyKeepsReadyWhileUpdatingLiveSpend(t *testing.T) {
	purchase := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "solo",
			Namespace:  "agent-ns",
			Generation: 1,
			Finalizers: []string{purchaseRequestFinalizer},
		},
		Spec: monetizeapi.PurchaseRequestSpec{
			Endpoint:       "http://seller/v1/chat/completions",
			Model:          "qwen3.5:9b",
			Count:          3,
			PreSignedAuths: makePreSignedAuths("solo-", 3),
			Payment: monetizeapi.PurchasePayment{
				Network: "base-sepolia",
				PayTo:   "0xpayto",
				Price:   "1000",
				Asset:   "0xasset",
			},
		},
		Status: monetizeapi.PurchaseRequestStatus{
			ObservedGeneration: 1,
			Remaining:          3,
			Spent:              0,
			PublicModel:        "paid/qwen3.5:9b",
			Conditions: []monetizeapi.Condition{
				{Type: "Probed", Status: "True", Reason: "Validated"},
				{Type: "AuthsLoaded", Status: "True", Reason: "Loaded"},
				{Type: "Configured", Status: "True", Reason: "Written"},
				{Type: "Ready", Status: "True", Reason: "Reconciled", Message: "Sidecar: 3 remaining, 0 spent"},
			},
		},
	}

	c, litellm, buyerTransport := newPurchaseLifecycleController(t, purchase)
	defer litellm.close()
	buyerTransport.setPayloads(map[string]fakeBuyerStatus{
		"solo": {Remaining: 1, Spent: 2},
	})

	if err := c.reconcilePurchase(context.Background(), "agent-ns/solo"); err != nil {
		t.Fatalf("reconcilePurchase live spend update: %v", err)
	}

	got := getPurchaseRequest(t, c, "agent-ns", "solo")
	ready := purchaseCondition(t, got, "Ready")
	if ready.Status != "True" || ready.Reason != "Reconciled" {
		t.Fatalf("ready condition after spend drift = %s/%s, want True/Reconciled", ready.Status, ready.Reason)
	}
	if got.Status.Remaining != 1 || got.Status.Spent != 2 {
		t.Fatalf("purchase status remaining/spent = %d/%d, want 1/2", got.Status.Remaining, got.Status.Spent)
	}
}

func TestReconcilePurchaseConfigureRejectsDuplicateModel(t *testing.T) {
	alpha := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "agent-ns"},
		Spec: monetizeapi.PurchaseRequestSpec{
			Model: "qwen3.5:9b",
		},
	}
	beta := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "agent-ns"},
		Spec: monetizeapi.PurchaseRequestSpec{
			Model: "qwen3.5:9b",
		},
	}

	c, litellm, _ := newPurchaseLifecycleController(t, alpha, beta)
	defer litellm.close()

	status := &monetizeapi.PurchaseRequestStatus{}
	if err := c.reconcilePurchaseConfigure(context.Background(), status, &beta); err != nil {
		t.Fatalf("reconcilePurchaseConfigure duplicate model: %v", err)
	}

	configured := purchaseCondition(t, &monetizeapi.PurchaseRequest{Status: *status}, "Configured")
	if configured.Status != "False" || configured.Reason != "DuplicateModel" {
		t.Fatalf("configured condition = %s/%s, want False/DuplicateModel", configured.Status, configured.Reason)
	}

	configCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerConfigCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer config: %v", err)
	}
	if len(configCM.Data) != 0 {
		t.Fatalf("buyer config mutated on duplicate model: %#v", configCM.Data)
	}
}

func TestReconcileDeletingPurchasePreservesAliasWhileAnotherOwnerExists(t *testing.T) {
	now := metav1.Now()
	alpha := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "alpha",
			Namespace:         "agent-ns",
			Finalizers:        []string{purchaseRequestFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: monetizeapi.PurchaseRequestSpec{Model: "qwen3.5:9b"},
		Status: monetizeapi.PurchaseRequestStatus{
			Remaining: 0,
		},
	}
	beta := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "beta", Namespace: "agent-ns"},
		Spec:       monetizeapi.PurchaseRequestSpec{Model: "qwen3.5:9b"},
	}

	c, litellm, _ := newPurchaseLifecycleController(t, alpha, beta)
	defer litellm.close()
	seedBuyerConfigMaps(t, c, map[string]string{
		"alpha": `{"remoteModel":"qwen3.5:9b"}`,
		"beta":  `{"remoteModel":"qwen3.5:9b"}`,
	})
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")

	raw := mustPurchaseObject(t, alpha)
	if err := c.reconcileDeletingPurchase(context.Background(), &alpha, raw); err != nil {
		t.Fatalf("reconcileDeletingPurchase preserve alias: %v", err)
	}

	configCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerConfigCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer config: %v", err)
	}
	if _, ok := configCM.Data["alpha.json"]; ok {
		t.Fatal("alpha buyer config still present after delete")
	}
	if _, ok := configCM.Data["beta.json"]; !ok {
		t.Fatal("beta buyer config missing after alpha delete")
	}

	authsCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerAuthsCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer auths: %v", err)
	}
	if _, ok := authsCM.Data["alpha.json"]; ok {
		t.Fatal("alpha buyer auths still present after delete")
	}
	if _, ok := authsCM.Data["beta.json"]; !ok {
		t.Fatal("beta buyer auths missing after alpha delete")
	}

	litellmConfig, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm config: %v", err)
	}
	if !strings.Contains(litellmConfig.Data["config.yaml"], "paid/qwen3.5:9b") {
		t.Fatal("paid route removed even though beta still owns the model")
	}
	if got := litellm.delCalls.Load(); got != 0 {
		t.Fatalf("hot-delete calls = %d, want 0", got)
	}
}

func TestReconcileDeletingPurchaseDrainsUntilRemainingZero(t *testing.T) {
	now := metav1.Now()
	alpha := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "alpha",
			Namespace:         "agent-ns",
			Finalizers:        []string{purchaseRequestFinalizer},
			DeletionTimestamp: &now,
			Generation:        1,
		},
		Spec: monetizeapi.PurchaseRequestSpec{
			Model: "qwen3.5:9b",
		},
		Status: monetizeapi.PurchaseRequestStatus{
			ObservedGeneration: 1,
			Remaining:          2,
			Spent:              1,
			Conditions: []monetizeapi.Condition{
				{Type: "Configured", Status: "True", Reason: "Written"},
				{Type: "Ready", Status: "True", Reason: "Reconciled"},
			},
		},
	}

	c, litellm, buyerTransport := newPurchaseLifecycleController(t, alpha)
	defer litellm.close()
	seedBuyerConfigMaps(t, c, map[string]string{
		"alpha": `{"remoteModel":"qwen3.5:9b"}`,
	})
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")
	buyerTransport.setPayloads(map[string]fakeBuyerStatus{
		"alpha": {Remaining: 2, Spent: 1},
	})

	raw := mustPurchaseObject(t, alpha)
	if err := c.reconcileDeletingPurchase(context.Background(), &alpha, raw); err != nil {
		t.Fatalf("reconcileDeletingPurchase drain: %v", err)
	}

	got := getPurchaseRequest(t, c, "agent-ns", "alpha")
	if !containsFinalizer(mustPurchaseObject(t, *got), purchaseRequestFinalizer) {
		t.Fatal("finalizer removed before auth pool drained")
	}
	deleting := purchaseCondition(t, got, "Deleting")
	if deleting.Status != "True" || deleting.Reason != "Draining" {
		t.Fatalf("deleting condition = %s/%s, want True/Draining", deleting.Status, deleting.Reason)
	}
	if got.Status.Remaining != 2 || got.Status.Spent != 1 {
		t.Fatalf("delete-drain remaining/spent = %d/%d, want 2/1", got.Status.Remaining, got.Status.Spent)
	}

	configCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerConfigCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer config: %v", err)
	}
	if _, ok := configCM.Data["alpha.json"]; !ok {
		t.Fatal("alpha buyer config removed before drain completed")
	}
	authsCM, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), buyerAuthsCM, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get buyer auths: %v", err)
	}
	if _, ok := authsCM.Data["alpha.json"]; !ok {
		t.Fatal("alpha buyer auths removed before drain completed")
	}
	litellmConfig, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm config: %v", err)
	}
	if !strings.Contains(litellmConfig.Data["config.yaml"], "paid/qwen3.5:9b") {
		t.Fatal("paid route removed before drain completed")
	}
	if got := litellm.delCalls.Load(); got != 0 {
		t.Fatalf("hot-delete calls = %d, want 0 while draining", got)
	}
}

func TestReconcileDeletingPurchaseRemovesAliasForLastOwner(t *testing.T) {
	now := metav1.Now()
	alpha := monetizeapi.PurchaseRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "alpha",
			Namespace:         "agent-ns",
			Finalizers:        []string{purchaseRequestFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: monetizeapi.PurchaseRequestSpec{Model: "qwen3.5:9b"},
		Status: monetizeapi.PurchaseRequestStatus{
			Remaining: 0,
		},
	}

	c, litellm, _ := newPurchaseLifecycleController(t, alpha)
	defer litellm.close()
	seedBuyerConfigMaps(t, c, map[string]string{
		"alpha": `{"remoteModel":"qwen3.5:9b"}`,
	})
	c.addLiteLLMModelEntry(context.Background(), "llm", "paid/qwen3.5:9b")
	litellm.infoResp = []map[string]any{
		{
			"model_name": "paid/qwen3.5:9b",
			"model_info": map[string]any{"id": "route-1"},
		},
	}

	raw := mustPurchaseObject(t, alpha)
	if err := c.reconcileDeletingPurchase(context.Background(), &alpha, raw); err != nil {
		t.Fatalf("reconcileDeletingPurchase remove alias: %v", err)
	}

	litellmConfig, err := c.kubeClient.CoreV1().ConfigMaps("llm").Get(context.Background(), "litellm-config", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get litellm config: %v", err)
	}
	if strings.Contains(litellmConfig.Data["config.yaml"], "paid/qwen3.5:9b") {
		t.Fatal("paid route still present after last owner delete")
	}
	if got := litellm.delCalls.Load(); got != 1 {
		t.Fatalf("hot-delete calls = %d, want 1", got)
	}
}
