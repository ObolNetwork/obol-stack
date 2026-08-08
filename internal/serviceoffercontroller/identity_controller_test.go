package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
)

func TestReconcileRegistrationTombstone_PublishesIdentityDocument(t *testing.T) {
	identity := defaultIdentity("777")
	identity.APIVersion = monetizeapi.Group + "/" + monetizeapi.Version
	identity.Kind = monetizeapi.AgentIdentityKind
	identity.UID = types.UID("identity-uid")
	request := &monetizeapi.RegistrationRequest{}
	request.Namespace = "demo"
	request.Name = registrationRequestName("svc")
	request.Spec.ServiceOfferName = "svc"
	request.Spec.ServiceOfferNamespace = "demo"
	request.Spec.Chain = "base-sepolia"
	request.Status.AgentID = "777"
	request.Status.RegistrationTxHash = "0xabc"
	offer := readyOffer("svc")
	offer.Namespace = "demo"
	offer.Spec.Payment.Network = "base-sepolia"
	offer.Status.AgentID = "777"

	rawRequest := registrationRequestToUnstructured(t, request)
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		identityListKinds(),
		agentIdentityToUnstructured(identity),
		rawRequest,
	)
	c := controllerForIdentityTest(dynClient)

	if err := c.reconcileRegistrationTombstone(context.Background(), rawRequest, request, offer, "https://seller.test"); err != nil {
		t.Fatalf("reconcileRegistrationTombstone: %v", err)
	}

	cm, err := c.configMaps.Namespace(identity.Namespace).Get(context.Background(), agentIdentityRegistrationName(identity), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get identity ConfigMap: %v", err)
	}
	data, _, _ := unstructured.NestedStringMap(cm.Object, "data")
	body := data["agent-registration.json"]
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("unmarshal document: %v\n%s", err, body)
	}
	if doc["active"] != false {
		t.Fatalf("active = %v, want false", doc["active"])
	}
	if doc["x402Support"] != false {
		t.Fatalf("x402Support = %v, want false", doc["x402Support"])
	}
	regs, _ := doc["registrations"].([]any)
	if len(regs) != 1 {
		t.Fatalf("registrations = %#v, want one entry", doc["registrations"])
	}
	reg0, _ := regs[0].(map[string]any)
	if reg0["agentId"] != float64(777) {
		t.Fatalf("agentId = %#v, want 777", reg0["agentId"])
	}
}

// Chain-switch regression (Canary402 field report): identity already has a
// verified base-sepolia registration, but the offer (and its
// RegistrationRequest, re-applied with the new spec.chain on every apply)
// has switched to base. request.Status.AgentID / offer.Status.AgentID still
// carry the old base-sepolia numeric id — that subresource is never reset
// by a chain switch. reconcileRegistrationActive must not adopt the stale
// id for base, must not upsert it into the AgentIdentity CR, and must not
// publish it in the registration document either.
func TestReconcileRegistrationActive_ChainSwitchDoesNotLeakWrongChainAgentID(t *testing.T) {
	identity := defaultIdentity("777") // base-sepolia -> 777
	identity.APIVersion = monetizeapi.Group + "/" + monetizeapi.Version
	identity.Kind = monetizeapi.AgentIdentityKind
	identity.UID = types.UID("identity-uid")

	request := &monetizeapi.RegistrationRequest{}
	request.Namespace = "demo"
	request.Name = registrationRequestName("svc")
	request.Spec.ServiceOfferName = "svc"
	request.Spec.ServiceOfferNamespace = "demo"
	request.Spec.Chain = "base" // switched
	request.Status.AgentID = "777"
	request.Status.RegistrationTxHash = "0xsepolia"

	offer := readyOffer("svc")
	offer.Namespace = "demo"
	offer.Spec.Payment.Network = "base" // switched
	offer.Spec.Payment.PayTo = "0xNewOwner"
	offer.Status.AgentID = "777" // stale, mirrors pre-fix ServiceOffer.status

	rawRequest := registrationRequestToUnstructured(t, request)
	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		identityListKinds(),
		agentIdentityToUnstructured(identity),
		rawRequest,
	)
	c := controllerForIdentityTest(dynClient)

	if err := c.reconcileRegistrationActive(context.Background(), rawRequest, request, offer, "https://seller.test"); err != nil {
		t.Fatalf("reconcileRegistrationActive: %v", err)
	}

	identityRaw, err := c.agentIdentities.Namespace(identity.Namespace).Get(context.Background(), identity.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get AgentIdentity: %v", err)
	}
	persisted, err := decodeAgentIdentity(identityRaw)
	if err != nil {
		t.Fatalf("decode AgentIdentity: %v", err)
	}
	if got := monetizeapi.AgentIdentityAgentIDForChain(persisted.Status, "base"); got != "" {
		t.Fatalf("AgentIdentity gained a base registration %q sourced from the stale base-sepolia id", got)
	}
	if got := monetizeapi.AgentIdentityAgentIDForChain(persisted.Status, "base-sepolia"); got != "777" {
		t.Fatalf("base-sepolia registration = %q, want untouched 777", got)
	}

	cm, err := c.configMaps.Namespace(identity.Namespace).Get(context.Background(), agentIdentityRegistrationName(identity), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get identity ConfigMap: %v", err)
	}
	data, _, _ := unstructured.NestedStringMap(cm.Object, "data")
	var doc map[string]any
	if err := json.Unmarshal([]byte(data["agent-registration.json"]), &doc); err != nil {
		t.Fatalf("unmarshal document: %v\n%s", err, data["agent-registration.json"])
	}
	regs, _ := doc["registrations"].([]any)
	if len(regs) != 1 {
		t.Fatalf("registrations = %#v, want only the base-sepolia entry", doc["registrations"])
	}
	reg0, _ := regs[0].(map[string]any)
	if reg0["agentId"] != float64(777) {
		t.Fatalf("registrations[0].agentId = %#v, want 777", reg0["agentId"])
	}
	if reg0["agentRegistry"] != erc8004.BaseSepolia.CAIP10Registry() {
		t.Fatalf("registrations[0].agentRegistry = %#v, want base-sepolia's registry, not base's", reg0["agentRegistry"])
	}
}

// Disable must stop reporting whatever agentId was last recorded, including
// a stale/wrong-chain one — otherwise a disabled offer keeps showing it in
// ServiceOffer.status (and `obol sell status` output).
func TestReconcileRegistrationStatus_DisableClearsStaleAgentID(t *testing.T) {
	status := &monetizeapi.ServiceOfferStatus{
		AgentID:            "stale-sepolia-id",
		RegistrationTxHash: "0xstale",
	}
	offer := readyOffer("svc")
	offer.Namespace = "demo"
	offer.Spec.Registration.Enabled = false

	dynClient := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), identityListKinds())
	c := controllerForIdentityTest(dynClient)

	if err := c.reconcileRegistrationStatus(context.Background(), status, offer); err != nil {
		t.Fatalf("reconcileRegistrationStatus: %v", err)
	}

	if status.AgentID != "" {
		t.Fatalf("AgentID = %q, want cleared on disable", status.AgentID)
	}
	if status.RegistrationTxHash != "" {
		t.Fatalf("RegistrationTxHash = %q, want cleared on disable", status.RegistrationTxHash)
	}
	if !isConditionTrue(*status, "Registered") {
		t.Fatalf("Registered condition not set: %+v", status.Conditions)
	}
}

func controllerForIdentityTest(dynClient *fake.FakeDynamicClient) *Controller {
	return &Controller{
		dynClient:            dynClient,
		client:               dynClient,
		agentIdentities:      dynClient.Resource(monetizeapi.AgentIdentityGVR),
		registrationRequests: dynClient.Resource(monetizeapi.RegistrationRequestGVR),
		configMaps:           dynClient.Resource(monetizeapi.ConfigMapGVR),
		deployments:          dynClient.Resource(monetizeapi.DeploymentGVR),
		services:             dynClient.Resource(monetizeapi.ServiceGVR),
		httpRoutes:           dynClient.Resource(monetizeapi.HTTPRouteGVR),
	}
}

func identityListKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		monetizeapi.AgentIdentityGVR:       "AgentIdentityList",
		monetizeapi.RegistrationRequestGVR: "RegistrationRequestList",
		monetizeapi.ConfigMapGVR:           "ConfigMapList",
		monetizeapi.DeploymentGVR:          "DeploymentList",
		monetizeapi.ServiceGVR:             "ServiceList",
		monetizeapi.HTTPRouteGVR:           "HTTPRouteList",
	}
}

func registrationRequestToUnstructured(t *testing.T, request *monetizeapi.RegistrationRequest) *unstructured.Unstructured {
	t.Helper()
	request.APIVersion = monetizeapi.Group + "/" + monetizeapi.Version
	request.Kind = monetizeapi.RegistrationRequestKind
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(request)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	return &unstructured.Unstructured{Object: obj}
}
