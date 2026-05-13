package serviceoffercontroller

import (
	"context"
	"encoding/json"
	"testing"

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

func TestRecreatedServiceOffer_MirrorsAgentIdentityAgentID(t *testing.T) {
	identity := defaultIdentity("777")
	request := &monetizeapi.RegistrationRequest{}
	request.Spec.Chain = "base-sepolia"
	request.Status.RegistrationTxHash = "0xabc"
	status := registrationRequestStatusWithIdentity(request, identity)
	if status.AgentID != "777" {
		t.Fatalf("AgentID = %q, want 777", status.AgentID)
	}
	if status.RegistrationTxHash != "0xabc" {
		t.Fatalf("RegistrationTxHash = %q, want 0xabc", status.RegistrationTxHash)
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
