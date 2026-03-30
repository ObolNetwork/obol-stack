package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestBuildMiddleware(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "llm", UID: types.UID("demo-uid")},
	}

	middleware := buildMiddleware(offer)

	if middleware.GetName() != "x402-demo" {
		t.Fatalf("middleware name = %q, want x402-demo", middleware.GetName())
	}
	if middleware.GetNamespace() != "llm" {
		t.Fatalf("middleware namespace = %q, want llm", middleware.GetNamespace())
	}
	spec := middleware.Object["spec"].(map[string]any)
	forwardAuth := spec["forwardAuth"].(map[string]any)
	address := forwardAuth["address"].(string)
	if address != "http://x402-verifier.x402.svc.cluster.local:8080/verify" {
		t.Fatalf("forwardAuth address = %q", address)
	}
}

func TestBuildHTTPRoute(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "llm", UID: types.UID("demo-uid")},
		Spec: monetizeapi.ServiceOfferSpec{
			Upstream: monetizeapi.ServiceOfferUpstream{
				Service:   "litellm",
				Namespace: "llm",
				Port:      4000,
			},
		},
	}

	route := buildHTTPRoute(offer)

	if route.GetName() != "so-demo" {
		t.Fatalf("route name = %q, want so-demo", route.GetName())
	}

	spec := route.Object["spec"].(map[string]any)
	rules := spec["rules"].([]any)
	firstRule := rules[0].(map[string]any)
	matches := firstRule["matches"].([]any)
	path := matches[0].(map[string]any)["path"].(map[string]any)
	if path["value"] != "/services/demo" {
		t.Fatalf("match path = %v, want /services/demo", path["value"])
	}
	backends := firstRule["backendRefs"].([]any)
	backend := backends[0].(map[string]any)
	if backend["name"] != "litellm" {
		t.Fatalf("backend name = %v, want litellm", backend["name"])
	}
	if backend["port"] != int64(4000) {
		t.Fatalf("backend port = %v, want 4000", backend["port"])
	}
}

func TestSetConditionUpdatesExistingEntry(t *testing.T) {
	status := monetizeapi.ServiceOfferStatus{
		Conditions: []monetizeapi.Condition{{
			Type:   "Ready",
			Status: "False",
		}},
	}

	setCondition(&status, "Ready", "True", "Reconciled", "Offer reconciled successfully")

	if len(status.Conditions) != 1 {
		t.Fatalf("len(conditions) = %d, want 1", len(status.Conditions))
	}
	if status.Conditions[0].Status != "True" {
		t.Fatalf("status = %q, want True", status.Conditions[0].Status)
	}
	if status.Conditions[0].Reason != "Reconciled" {
		t.Fatalf("reason = %q, want Reconciled", status.Conditions[0].Reason)
	}
}

func TestBuildRegistrationRequest(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "llm", UID: types.UID("demo-uid")},
	}

	request := buildRegistrationRequest(offer, registrationDesiredActive)

	if request.GetName() != "so-demo-registration" {
		t.Fatalf("request name = %q", request.GetName())
	}
	spec := request.Object["spec"].(map[string]any)
	if spec["desiredState"] != registrationDesiredActive {
		t.Fatalf("desiredState = %v, want %s", spec["desiredState"], registrationDesiredActive)
	}
}

func TestBuildRegistrationHTTPRoute(t *testing.T) {
	request := &monetizeapi.RegistrationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "so-demo-registration", Namespace: "llm", UID: types.UID("req-uid")},
		Spec: monetizeapi.RegistrationRequestSpec{
			ServiceOfferName: "demo",
		},
	}

	route := buildRegistrationHTTPRoute(request)
	spec := route.Object["spec"].(map[string]any)
	rules := spec["rules"].([]any)
	firstRule := rules[0].(map[string]any)
	matches := firstRule["matches"].([]any)
	path := matches[0].(map[string]any)["path"].(map[string]any)
	if path["value"] != "/.well-known/" {
		t.Fatalf("match path = %v, want /.well-known/", path["value"])
	}
	filters := firstRule["filters"].([]any)
	rewrite := filters[0].(map[string]any)["urlRewrite"].(map[string]any)
	rewritePath := rewrite["path"].(map[string]any)
	if rewritePath["replacePrefixMatch"] != "/" {
		t.Fatalf("rewrite target = %v, want /", rewritePath["replacePrefixMatch"])
	}
}

func TestBuildActiveRegistrationDocument(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "inference",
			Model: monetizeapi.ServiceOfferModel{
				Name: "qwen3.5:9b",
			},
			Path: "/services/demo",
			Registration: monetizeapi.ServiceOfferRegistration{
				Name:    "Demo Agent",
				Skills:  []string{"natural_language_processing/text_generation"},
				Domains: []string{"technology/artificial_intelligence"},
			},
		},
	}

	document := buildActiveRegistrationDocument(offer, "https://example.com", "7")

	if document.Type != erc8004.RegistrationType {
		t.Fatalf("type = %q", document.Type)
	}
	if document.Name != "Demo Agent" {
		t.Fatalf("name = %q", document.Name)
	}
	if !document.X402Support || !document.Active {
		t.Fatalf("document should be active x402 registration: %+v", document)
	}
	if len(document.Registrations) != 1 || document.Registrations[0].AgentID != 7 {
		t.Fatalf("registrations = %+v, want agentId 7", document.Registrations)
	}
	if len(document.Services) < 2 {
		t.Fatalf("services = %+v, want web + OASF", document.Services)
	}
}

func TestRegistrationDataURL(t *testing.T) {
	document := erc8004.AgentRegistration{
		Type:        erc8004.RegistrationType,
		Name:        "Demo",
		Description: "Demo registration",
		Image:       "https://example.com/icon.png",
		Services: []erc8004.ServiceDef{
			{Name: "web", Endpoint: "https://example.com/services/demo"},
		},
		X402Support: false,
		Active:      false,
	}

	uri, err := registrationDataURL(document)
	if err != nil {
		t.Fatalf("registrationDataURL: %v", err)
	}
	if !strings.HasPrefix(uri, "data:application/json,") {
		t.Fatalf("uri = %q", uri)
	}
}

func TestBuildSkillCatalogMarkdown(t *testing.T) {
	readyOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "flow-qwen", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Upstream: monetizeapi.ServiceOfferUpstream{
				Service: "ollama",
				Port:    11434,
			},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia",
				PayTo:   "0x1234",
				Price: monetizeapi.ServiceOfferPriceTable{
					PerRequest: "0.001",
				},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}
	notReadyOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "llm"},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "False"}},
		},
	}

	content := buildSkillCatalogMarkdown([]*monetizeapi.ServiceOffer{readyOffer, notReadyOffer}, "https://example.com")

	if !strings.Contains(content, "# Obol Stack Service Catalog") {
		t.Fatalf("catalog missing title:\n%s", content)
	}
	if !strings.Contains(content, "flow-qwen") {
		t.Fatalf("catalog missing ready offer:\n%s", content)
	}
	if strings.Contains(content, "pending") {
		t.Fatalf("catalog included non-ready offer:\n%s", content)
	}
	if !strings.Contains(content, "https://example.com/services/flow-qwen") {
		t.Fatalf("catalog missing public endpoint:\n%s", content)
	}
}

func TestBuildSkillCatalogHTTPRoute(t *testing.T) {
	route := buildSkillCatalogHTTPRoute()
	if route.GetName() != skillCatalogRouteName {
		t.Fatalf("route name = %q, want %q", route.GetName(), skillCatalogRouteName)
	}
	spec := route.Object["spec"].(map[string]any)
	rules := spec["rules"].([]any)
	firstRule := rules[0].(map[string]any)
	matches := firstRule["matches"].([]any)
	path := matches[0].(map[string]any)["path"].(map[string]any)
	if path["value"] != "/skill.md" {
		t.Fatalf("match path = %v, want /skill.md", path["value"])
	}
	backends := firstRule["backendRefs"].([]any)
	backend := backends[0].(map[string]any)
	if backend["name"] != skillCatalogConfigMapName {
		t.Fatalf("backend name = %v, want %s", backend["name"], skillCatalogConfigMapName)
	}
}
