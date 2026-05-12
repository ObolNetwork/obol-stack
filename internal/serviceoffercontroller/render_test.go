package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func assertServiceCatalogSchema(t *testing.T, jsonStr string) {
	t.Helper()

	schemaDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemas.ServiceCatalogJSONSchema))
	if err != nil {
		t.Fatalf("service catalog schema is invalid JSON: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("service-catalog.schema.json", schemaDoc); err != nil {
		t.Fatalf("failed to register service catalog schema: %v", err)
	}
	schema, err := compiler.Compile("service-catalog.schema.json")
	if err != nil {
		t.Fatalf("service catalog schema failed to compile: %v", err)
	}
	payload, err := jsonschema.UnmarshalJSON(strings.NewReader(jsonStr))
	if err != nil {
		t.Fatalf("service catalog JSON is invalid: %v\n%s", err, jsonStr)
	}
	if err := schema.Validate(payload); err != nil {
		t.Fatalf("service catalog JSON violates schema: %v\n%s", err, jsonStr)
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
	if _, found := firstRule["filters"]; found {
		t.Fatalf("sell http route should not use Traefik ForwardAuth middleware anymore: %+v", firstRule["filters"])
	}
	backends := firstRule["backendRefs"].([]any)
	backend := backends[0].(map[string]any)
	if backend["name"] != "x402-verifier" {
		t.Fatalf("backend name = %v, want x402-verifier", backend["name"])
	}
	if backend["namespace"] != "x402" {
		t.Fatalf("backend namespace = %v, want x402", backend["namespace"])
	}
	if backend["port"] != int64(8080) {
		t.Fatalf("backend port = %v, want 8080", backend["port"])
	}
}

func TestBuildReferenceGrant(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "llm"},
	}

	grant := buildReferenceGrant(offer)
	if grant.GetNamespace() != "x402" {
		t.Fatalf("grant namespace = %q, want x402", grant.GetNamespace())
	}
	spec := grant.Object["spec"].(map[string]any)
	from := spec["from"].([]any)[0].(map[string]any)
	to := spec["to"].([]any)[0].(map[string]any)
	if from["namespace"] != "llm" {
		t.Fatalf("grant from.namespace = %v, want llm", from["namespace"])
	}
	if to["name"] != "x402-verifier" {
		t.Fatalf("grant to.name = %v, want x402-verifier", to["name"])
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
	if path["value"] != "/.well-known/agent-registration.json" {
		t.Fatalf("match path = %v, want /.well-known/agent-registration.json", path["value"])
	}
	if _, found := firstRule["filters"]; found {
		t.Fatalf("registration route should not rewrite the full /.well-known/ prefix: %+v", firstRule["filters"])
	}
}

func TestBuildActiveRegistrationDocument(t *testing.T) {
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "inference",
			Model: monetizeapi.ServiceOfferModel{
				Name: "qwen3.5:9b",
			},
			Path: "/services/demo",
			Provenance: map[string]string{
				"framework":   "autoresearch",
				"metricName":  "val_bpb",
				"metricValue": "0.9973",
			},
			Registration: monetizeapi.ServiceOfferRegistration{
				Name:    "Demo Agent",
				Skills:  []string{"natural_language_processing/text_generation"},
				Domains: []string{"technology/artificial_intelligence"},
				Metadata: map[string]string{
					"gpu":          "A100-80GB",
					"best_val_bpb": "1.234",
				},
			},
		},
	}
	secondary := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "blocks", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Path: "/services/blocks",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
				Skills:  []string{"blockchain/data"},
				Domains: []string{"technology/blockchain"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{
				{Type: "ModelReady", Status: "True"},
				{Type: "UpstreamHealthy", Status: "True"},
				{Type: "PaymentGateReady", Status: "True"},
				{Type: "RoutePublished", Status: "True"},
			},
		},
	}

	document := buildActiveRegistrationDocument(owner, []*monetizeapi.ServiceOffer{owner, secondary}, "https://example.com", "7")

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
	if len(document.Services) < 4 {
		t.Fatalf("services = %+v, want aggregated web + OASF entries", document.Services)
	}
	if document.Metadata["gpu"] != "A100-80GB" {
		t.Fatalf("metadata = %+v, want gpu entry", document.Metadata)
	}
	if document.Provenance["framework"] != "autoresearch" {
		t.Fatalf("provenance = %+v, want framework entry", document.Provenance)
	}

	var seenBlocks bool
	for _, svc := range document.Services {
		if svc.Endpoint == "https://example.com/services/blocks" {
			seenBlocks = true
			break
		}
	}
	if !seenBlocks {
		t.Fatalf("aggregated document missing secondary service endpoint: %+v", document.Services)
	}
}

// TestBuildActiveRegistrationDocument_KeepsOperatorDescription pins the fix
// for the controller-side description-clobber bug:
// `buildActiveRegistrationDocument` used to unconditionally overwrite
// `owner.Spec.Registration.Description` for inference-typed offers with
// `"<model.name> inference via x402 micropayments"`, so any explicit operator
// description set at sell time was silently lost in the published
// /.well-known/agent-registration.json document. The fix only fills the
// description from the model name when the operator's value is empty.
func TestBuildActiveRegistrationDocument_KeepsOperatorDescription(t *testing.T) {
	operatorDesc := "Uncensored Qwen3.6-27B abliteration on DGX Spark"
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "aeon", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:  "inference",
			Model: monetizeapi.ServiceOfferModel{Name: "aeon-ultimate"},
			Path:  "/services/aeon",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled:     true,
				Name:        "Qwen36 AEON Ultimate",
				Description: operatorDesc,
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{
				{Type: "ModelReady", Status: "True"},
				{Type: "UpstreamHealthy", Status: "True"},
				{Type: "PaymentGateReady", Status: "True"},
				{Type: "RoutePublished", Status: "True"},
			},
		},
	}
	doc := buildActiveRegistrationDocument(owner, []*monetizeapi.ServiceOffer{owner}, "https://inference.example.com", "")
	if doc.Description != operatorDesc {
		t.Fatalf("description = %q, want operator value %q (the controller used to overwrite this with %q-inference-via-x402-micropayments)",
			doc.Description, operatorDesc, owner.Spec.Model.Name)
	}
	if doc.Name != "Qwen36 AEON Ultimate" {
		t.Errorf("name = %q, want operator value %q", doc.Name, "Qwen36 AEON Ultimate")
	}
}

// TestBuildActiveRegistrationDocument_FallsBackToModelDescriptionForInference
// pins the *other* side of the description contract: when the operator does
// not supply a description, inference offers should still get the
// model-aware default ("<model.name> inference via x402 micropayments"),
// not the generic "x402 payment-gated <type> service: <name>" string used
// for non-inference fallback. The refactor must preserve both branches.
func TestBuildActiveRegistrationDocument_FallsBackToModelDescriptionForInference(t *testing.T) {
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "aeon", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:  "inference",
			Model: monetizeapi.ServiceOfferModel{Name: "aeon-ultimate"},
			Path:  "/services/aeon",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
				Name:    "aeon",
				// Description intentionally left blank.
			},
		},
	}
	doc := buildActiveRegistrationDocument(owner, []*monetizeapi.ServiceOffer{owner}, "https://inference.example.com", "")
	want := "aeon-ultimate inference via x402 micropayments"
	if doc.Description != want {
		t.Errorf("description = %q, want %q (model-aware default for inference offers with no operator description)", doc.Description, want)
	}
}

func TestBuildRegistrationServices_IncludesOwnerWhenOwnerNotYetPublished(t *testing.T) {
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "owner", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Path: "/services/owner",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
			},
		},
	}
	other := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Path: "/services/other",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{
				{Type: "ModelReady", Status: "True"},
				{Type: "UpstreamHealthy", Status: "True"},
				{Type: "PaymentGateReady", Status: "True"},
				{Type: "RoutePublished", Status: "True"},
			},
		},
	}

	services := buildRegistrationServices(owner, []*monetizeapi.ServiceOffer{owner, other}, "https://example.com")
	if len(services) != 2 {
		t.Fatalf("services = %+v, want 2 web entries", services)
	}
	if services[0].Endpoint != "https://example.com/services/owner" {
		t.Fatalf("owner service endpoint = %q", services[0].Endpoint)
	}
	if services[1].Endpoint != "https://example.com/services/other" {
		t.Fatalf("other service endpoint = %q", services[1].Endpoint)
	}
}

func TestBuildRegistrationConfigMap_PublishesAggregatedAgentRegistration(t *testing.T) {
	readyConditions := []monetizeapi.Condition{
		{Type: "ModelReady", Status: "True"},
		{Type: "UpstreamHealthy", Status: "True"},
		{Type: "PaymentGateReady", Status: "True"},
		{Type: "RoutePublished", Status: "True"},
	}
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "hello", Namespace: "demo", UID: types.UID("owner-uid")},
		Spec: monetizeapi.ServiceOfferSpec{
			Path: "/services/hello",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
				Name:    "Demo Agent",
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
	}
	offers := []*monetizeapi.ServiceOffer{
		owner,
		{
			ObjectMeta: metav1.ObjectMeta{Name: "blocks", Namespace: "demo"},
			Spec: monetizeapi.ServiceOfferSpec{
				Path: "/services/blocks",
				Registration: monetizeapi.ServiceOfferRegistration{
					Enabled: true,
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "oracle", Namespace: "demo"},
			Spec: monetizeapi.ServiceOfferSpec{
				Path: "/services/oracle",
				Registration: monetizeapi.ServiceOfferRegistration{
					Enabled: true,
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
		},
	}

	document := buildActiveRegistrationDocument(owner, offers, "https://example.com", "42")
	documentJSON, _, err := marshalRegistrationDocument(document)
	if err != nil {
		t.Fatalf("marshalRegistrationDocument: %v", err)
	}
	request := &monetizeapi.RegistrationRequest{
		ObjectMeta: metav1.ObjectMeta{Name: registrationRequestName(owner.Name), Namespace: owner.Namespace, UID: types.UID("req-uid")},
		Spec: monetizeapi.RegistrationRequestSpec{
			ServiceOfferName:      owner.Name,
			ServiceOfferNamespace: owner.Namespace,
		},
	}

	cm := buildRegistrationConfigMap(request, documentJSON)
	data := cm.Object["data"].(map[string]any)
	rawDoc, ok := data["agent-registration.json"].(string)
	if !ok || rawDoc == "" {
		t.Fatalf("agent-registration.json missing from configmap: %+v", data)
	}

	var published erc8004.AgentRegistration
	if err := json.Unmarshal([]byte(rawDoc), &published); err != nil {
		t.Fatalf("unmarshal published registration document: %v", err)
	}

	wantEndpoints := map[string]bool{
		"https://example.com/services/hello":  false,
		"https://example.com/services/blocks": false,
		"https://example.com/services/oracle": false,
	}
	for _, svc := range published.Services {
		if _, ok := wantEndpoints[svc.Endpoint]; ok {
			wantEndpoints[svc.Endpoint] = true
		}
	}
	for endpoint, seen := range wantEndpoints {
		if !seen {
			t.Fatalf("published registration document missing endpoint %s: %+v", endpoint, published.Services)
		}
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

func TestBuildServiceCatalogJSON(t *testing.T) {
	readyOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-hello", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Upstream: monetizeapi.ServiceOfferUpstream{
				Service: "demo-hello",
				Port:    8080,
			},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price: monetizeapi.ServiceOfferPriceTable{
					PerRequest: "0.00001",
				},
			},
			Registration: monetizeapi.ServiceOfferRegistration{
				Description: "Proof-of-payment echo service",
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}
	notReadyOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "demo"},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "False"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{readyOffer, notReadyOffer}, "https://example.com")
	assertServiceCatalogSchema(t, jsonStr)

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonStr)
	}

	if len(services) != 1 {
		t.Fatalf("expected 1 ready service, got %d", len(services))
	}
	svc := services[0]
	if svc.Name != "demo-hello" {
		t.Errorf("name = %q, want demo-hello", svc.Name)
	}
	if svc.Price != "0.00001 USDC/request" {
		t.Errorf("price = %q, want '0.00001 USDC/request'", svc.Price)
	}
	if !svc.IsDemo {
		t.Error("expected isDemo=true for namespace=demo")
	}
	if svc.Endpoint != "https://example.com/services/demo-hello" {
		t.Errorf("endpoint = %q, want https://example.com/services/demo-hello", svc.Endpoint)
	}
	if svc.Description != "Proof-of-payment echo service" {
		t.Errorf("description = %q, want 'Proof-of-payment echo service'", svc.Description)
	}
}

func TestBuildServiceCatalogJSON_Empty(t *testing.T) {
	jsonStr := buildServiceCatalogJSON(nil, "https://example.com")
	assertServiceCatalogSchema(t, jsonStr)
	if jsonStr != "[]" {
		t.Errorf("expected empty array, got %q", jsonStr)
	}
}

// TestBuildServiceCatalogJSON_ExcludesNonReady locks in the filter pipeline:
// nil offers, paused offers, and offers with a DeletionTimestamp must never
// leak onto the public storefront, even if they carry Ready=True.
func TestBuildServiceCatalogJSON_ExcludesNonReady(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}

	deleting := metav1.Now()
	offers := []*monetizeapi.ServiceOffer{
		nil,
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "paused-svc", Namespace: "llm",
				Annotations: map[string]string{monetizeapi.PausedAnnotation: "true"},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "deleting-svc", Namespace: "llm",
				DeletionTimestamp: &deleting,
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "not-ready-svc", Namespace: "llm"},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "False"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ready-svc", Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				Type: "http",
				Payment: monetizeapi.ServiceOfferPayment{
					Network: "base",
					PayTo:   "0x1111111111111111111111111111111111111111",
					Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
		},
	}

	jsonStr := buildServiceCatalogJSON(offers, "https://example.com")

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonStr)
	}
	if len(services) != 1 {
		t.Fatalf("expected exactly 1 service (ready-svc), got %d: %+v", len(services), services)
	}
	if services[0].Name != "ready-svc" {
		t.Errorf("got %q, want ready-svc — filter pipeline leaked another offer", services[0].Name)
	}
}

// TestBuildServiceCatalogJSON_SortOrder ensures offers render in
// deterministic alphabetical order, not insertion order. The informer
// store yields items in arbitrary order, so without a sort the public
// storefront reorders itself between reconciles.
func TestBuildServiceCatalogJSON_SortOrder(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}
	makeOffer := func(name string) *monetizeapi.ServiceOffer {
		return &monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
		}
	}
	offers := []*monetizeapi.ServiceOffer{
		makeOffer("charlie"),
		makeOffer("alpha"),
		makeOffer("bravo"),
	}

	jsonStr := buildServiceCatalogJSON(offers, "https://example.com")

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	names := []string{services[0].Name, services[1].Name, services[2].Name}
	want := []string{"alpha", "bravo", "charlie"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("sort order = %v, want %v", names, want)
		}
	}
}

// TestBuildServiceCatalogJSON_PerMTokPricing verifies that per-mtok offers
// render a non-empty Price string AND a populated PriceRaw + PriceUnit so
// agents can disambiguate the unit. Without this test a refactor could
// silently drop the unit metadata.
func TestBuildServiceCatalogJSON_PerMTokPricing(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "mtok-svc", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:  "inference",
			Model: monetizeapi.ServiceOfferModel{Name: "qwen3.5:9b"},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerMTok: "5.00"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com")
	assertServiceCatalogSchema(t, jsonStr)

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	got := services[0]
	if got.PriceRaw != "5.00" {
		t.Errorf("PriceRaw = %q, want %q", got.PriceRaw, "5.00")
	}
	if got.PriceUnit != "perMTok" {
		t.Errorf("PriceUnit = %q, want perMTok", got.PriceUnit)
	}
	if got.Price == "" {
		t.Error("Price must not be empty for per-mtok pricing")
	}
	if got.Model != "qwen3.5:9b" {
		t.Errorf("Model = %q, want qwen3.5:9b", got.Model)
	}
	// Endpoint falls back to /services/<name> when Spec.Path is unset.
	if got.Endpoint != "https://example.com/services/mtok-svc" {
		t.Errorf("Endpoint = %q, want https://example.com/services/mtok-svc", got.Endpoint)
	}
}

// TestBuildServiceCatalogJSON_FallbackDescription verifies the autogenerated
// description when Spec.Registration.Description is empty.
func TestBuildServiceCatalogJSON_FallbackDescription(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "no-desc", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "inference",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
			// Spec.Registration.Description intentionally omitted.
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com")
	assertServiceCatalogSchema(t, jsonStr)

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if !strings.Contains(services[0].Description, "inference") {
		t.Errorf("fallback description should mention type, got %q", services[0].Description)
	}
}

// TestBuildServiceCatalogJSON_BaseURLTrailingSlash verifies the baseURL
// trimming so we don't emit double-slash endpoints like https://ex.com//services/...
func TestBuildServiceCatalogJSON_BaseURLTrailingSlash(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "trim-svc", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Payment: monetizeapi.ServiceOfferPayment{
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com/")

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if strings.Contains(services[0].Endpoint, "//services") {
		t.Errorf("endpoint has double-slash, got %q", services[0].Endpoint)
	}
	if services[0].Endpoint != "https://example.com/services/trim-svc" {
		t.Errorf("endpoint = %q, want https://example.com/services/trim-svc", services[0].Endpoint)
	}
}

// TestBuildServiceCatalogJSON_AssetAndCAIP2Defaults locks in the wire schema
// agents rely on: when the seller did not specify --token, the controller
// must backfill the chain's default USDC asset block (address, decimals,
// transferMethod, signing-domain), the CAIP-2 network, the chain id, and
// the price in atomic units. Buyers (buy.py and external agents) construct
// EIP-712 typed data straight from these fields without re-deriving them.
func TestBuildServiceCatalogJSON_AssetAndCAIP2Defaults(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-hello", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com")
	assertServiceCatalogSchema(t, jsonStr)

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonStr)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	got := services[0]

	if got.CAIP2Network != "eip155:84532" {
		t.Errorf("CAIP2Network = %q, want eip155:84532", got.CAIP2Network)
	}
	if got.ChainID != 84532 {
		t.Errorf("ChainID = %d, want 84532", got.ChainID)
	}
	if got.PriceAtomicUnits != "1000" {
		t.Errorf("PriceAtomicUnits = %q, want 1000 (0.001 USDC × 1e6)", got.PriceAtomicUnits)
	}
	if strings.Contains(jsonStr, "priceMicroUnits") {
		t.Fatalf("services.json must not expose legacy priceMicroUnits field: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"priceAtomicUnits"`) {
		t.Fatalf("services.json missing priceAtomicUnits field: %s", jsonStr)
	}
	if got.PriceUnit != "perRequest" {
		t.Errorf("PriceUnit = %q, want perRequest", got.PriceUnit)
	}
	if got.Asset == nil {
		t.Fatalf("Asset is nil; expected USDC default backfill for base-sepolia")
	}
	if got.Asset.Address != "0x036CbD53842c5426634e7929541eC2318f3dCF7e" {
		t.Errorf("Asset.Address = %q, want base-sepolia USDC", got.Asset.Address)
	}
	if got.Asset.Symbol != "USDC" {
		t.Errorf("Asset.Symbol = %q, want USDC", got.Asset.Symbol)
	}
	if got.Asset.Decimals != 6 {
		t.Errorf("Asset.Decimals = %d, want 6", got.Asset.Decimals)
	}
	if got.Asset.TransferMethod != "eip3009" {
		t.Errorf("Asset.TransferMethod = %q, want eip3009", got.Asset.TransferMethod)
	}
	if got.Asset.EIP712Domain == nil {
		t.Fatalf("Asset.EIP712Domain is nil")
	}
	// Base Sepolia USDC empirically signs with domain name "USDC", not
	// "USD Coin" (the contract's name() getter). Locking in that the
	// catalog publishes the SIGNING domain, not the display name.
	if got.Asset.EIP712Domain.Name != "USDC" {
		t.Errorf("EIP712Domain.Name = %q, want USDC (signing domain on Base Sepolia)", got.Asset.EIP712Domain.Name)
	}
	if got.Asset.EIP712Domain.Version != "2" {
		t.Errorf("EIP712Domain.Version = %q, want 2", got.Asset.EIP712Domain.Version)
	}
}

// TestBuildServiceCatalogJSON_ExplicitOBOLToken verifies that a seller who
// chose --token OBOL (Permit2 transfer method) sees their explicit asset
// fields preserved on the storefront, not silently overwritten by USDC
// defaults.
func TestBuildServiceCatalogJSON_ExplicitOBOLToken(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "obol-svc", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "ethereum",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.5"},
				Asset: monetizeapi.ServiceOfferAsset{
					Address:        "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7",
					Symbol:         "OBOL",
					Decimals:       18,
					TransferMethod: "permit2",
					EIP712Name:     "Obol Network",
					EIP712Version:  "1",
				},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com")
	assertServiceCatalogSchema(t, jsonStr)

	var services []schemas.ServiceCatalogEntry
	if err := json.Unmarshal([]byte(jsonStr), &services); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonStr)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	got := services[0]

	if got.CAIP2Network != "eip155:1" || got.ChainID != 1 {
		t.Errorf("CAIP-2/chainID = %s/%d, want eip155:1/1", got.CAIP2Network, got.ChainID)
	}
	if got.Asset == nil {
		t.Fatalf("Asset must be present for OBOL")
	}
	if got.Asset.Symbol != "OBOL" || got.Asset.TransferMethod != "permit2" {
		t.Errorf("OBOL fields drifted: symbol=%q transfer=%q", got.Asset.Symbol, got.Asset.TransferMethod)
	}
	if got.Asset.Decimals != 18 {
		t.Errorf("OBOL decimals = %d, want 18", got.Asset.Decimals)
	}
	if got.Asset.EIP712Domain == nil || got.Asset.EIP712Domain.Name != "Obol Network" {
		t.Errorf("OBOL signing domain dropped, got %+v", got.Asset.EIP712Domain)
	}
	// 0.5 OBOL × 1e18 = 500_000_000_000_000_000.
	if got.PriceAtomicUnits != "500000000000000000" {
		t.Errorf("PriceAtomicUnits = %q, want 500000000000000000", got.PriceAtomicUnits)
	}
}

func TestBuildServicesJSONHTTPRoute(t *testing.T) {
	route := buildServicesJSONHTTPRoute()
	if route.GetName() != servicesJSONRouteName {
		t.Fatalf("route name = %q, want %q", route.GetName(), servicesJSONRouteName)
	}
	spec := route.Object["spec"].(map[string]any)
	rules := spec["rules"].([]any)
	match := rules[0].(map[string]any)["matches"].([]any)[0].(map[string]any)
	path := match["path"].(map[string]any)
	if path["value"] != "/api/services.json" {
		t.Errorf("path = %q, want /api/services.json", path["value"])
	}
}

func TestSafeName_Short(t *testing.T) {
	// Short names should pass through unchanged.
	if got := childName("demo"); got != "so-demo" {
		t.Errorf("childName(demo) = %q, want so-demo", got)
	}
	if got := registrationRequestName("demo"); got != "so-demo-registration" {
		t.Errorf("registrationRequestName(demo) = %q, want so-demo-registration", got)
	}
	if got := registrationRouteName("demo"); got != "so-demo-wellknown" {
		t.Errorf("registrationRouteName(demo) = %q, want so-demo-wellknown", got)
	}
}

func TestSafeName_Truncation(t *testing.T) {
	// Name long enough to exceed 253 chars with prefix+suffix.
	longName := strings.Repeat("a", 260)

	got := childName(longName)
	if len(got) > maxK8sNameLen {
		t.Errorf("childName(260 chars) length = %d, want <= %d", len(got), maxK8sNameLen)
	}
	if !strings.HasPrefix(got, "so-") {
		t.Errorf("childName should start with so-, got %q", got[:10])
	}

	got2 := registrationRouteName(longName)
	if len(got2) > maxK8sNameLen {
		t.Errorf("registrationRouteName(260 chars) length = %d, want <= %d", len(got2), maxK8sNameLen)
	}
	if !strings.HasSuffix(got2, "-wellknown") {
		t.Errorf("registrationRouteName should end with -wellknown, got %q", got2[len(got2)-15:])
	}

	// Different long names should produce different results (hash disambiguates).
	otherLong := strings.Repeat("b", 260)
	if childName(longName) == childName(otherLong) {
		t.Error("different long names should produce different childNames")
	}
}
