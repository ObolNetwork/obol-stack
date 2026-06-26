package serviceoffercontroller

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func decodeServiceCatalog(t *testing.T, jsonStr string) schemas.ServiceCatalog {
	t.Helper()
	var catalog schemas.ServiceCatalog
	if err := json.Unmarshal([]byte(jsonStr), &catalog); err != nil {
		t.Fatalf("unmarshal catalog: %v\n%s", err, jsonStr)
	}
	return catalog
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

func TestBuildLimitsMiddlewares_SplitsInFlightAndRPS(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "gated", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Limits: monetizeapi.ServiceOfferLimits{MaxInFlight: 4, RPS: 10},
		},
	}
	mws := buildLimitsMiddlewares(offer)
	if len(mws) != 2 {
		t.Fatalf("len(middlewares) = %d, want 2 (one per Traefik type)", len(mws))
	}
	names := map[string]map[string]any{}
	for _, mw := range mws {
		spec, _ := mw.Object["spec"].(map[string]any)
		names[mw.GetName()] = spec
		// Each CR must carry exactly one middleware type key.
		if len(spec) != 1 {
			t.Fatalf("middleware %q has %d spec keys; Traefik requires one type per CR: %v", mw.GetName(), len(spec), spec)
		}
	}
	if _, ok := names[limitsInFlightMiddlewareName("gated")]["inFlightReq"]; !ok {
		t.Fatal("missing inFlightReq middleware")
	}
	if _, ok := names[limitsRPSMiddlewareName("gated")]["rateLimit"]; !ok {
		t.Fatal("missing rateLimit middleware")
	}
	filters := limitsFilters(offer)
	if len(filters) != 2 {
		t.Fatalf("len(filters) = %d, want 2 ExtensionRefs", len(filters))
	}
}

func TestBuildLimitsMiddlewares_SingleType(t *testing.T) {
	onlyInFlight := buildLimitsMiddlewares(&monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "llm"},
		Spec:       monetizeapi.ServiceOfferSpec{Limits: monetizeapi.ServiceOfferLimits{MaxInFlight: 2}},
	})
	if len(onlyInFlight) != 1 {
		t.Fatalf("maxInFlight only: len = %d", len(onlyInFlight))
	}
	onlyRPS := buildLimitsMiddlewares(&monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "llm"},
		Spec:       monetizeapi.ServiceOfferSpec{Limits: monetizeapi.ServiceOfferLimits{RPS: 5}},
	})
	if len(onlyRPS) != 1 {
		t.Fatalf("rps only: len = %d", len(onlyRPS))
	}
}

func TestBuildReferenceGrant(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "demo",
			Namespace:         "llm",
			CreationTimestamp: metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	grant := buildReferenceGrant(offer)
	if grant.GetNamespace() != "x402" {
		t.Fatalf("grant namespace = %q, want x402", grant.GetNamespace())
	}
	if grant.GetName() != backendReferenceGrantName("llm", "demo") {
		t.Fatalf("grant name = %q, want namespaced unique name", grant.GetName())
	}
	if !strings.Contains(grant.GetName(), "llm") || !strings.Contains(grant.GetName(), "demo") {
		t.Fatalf("grant name %q must include offer namespace and name", grant.GetName())
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

// Two offers with the same name in different namespaces must not share a
// ReferenceGrant object name in x402 (overwrite / flapping 500s).
func TestBuildReferenceGrant_DisambiguatesByNamespace(t *testing.T) {
	a := buildReferenceGrant(&monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "canary402", Namespace: "ns-a"},
	})
	b := buildReferenceGrant(&monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "canary402", Namespace: "ns-b"},
	})
	if a.GetName() == b.GetName() {
		t.Fatalf("same-named offers in different namespaces must get distinct grant names; both = %q", a.GetName())
	}
	if a.Object["spec"].(map[string]any)["from"].([]any)[0].(map[string]any)["namespace"] != "ns-a" {
		t.Fatal("grant A from.namespace must be ns-a")
	}
	if b.Object["spec"].(map[string]any)["from"].([]any)[0].(map[string]any)["namespace"] != "ns-b" {
		t.Fatal("grant B from.namespace must be ns-b")
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

func TestBuildAgentIdentityRegistrationHTTPRoute(t *testing.T) {
	identity := &monetizeapi.AgentIdentity{}
	identity.Name = monetizeapi.AgentIdentityDefaultName
	identity.Namespace = monetizeapi.AgentIdentityDefaultNamespace
	identity.UID = types.UID("identity-uid")

	route := buildAgentIdentityRegistrationHTTPRoute(identity)
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
	readyConditions := []monetizeapi.Condition{
		{Type: "ModelReady", Status: "True"},
		{Type: "UpstreamHealthy", Status: "True"},
		{Type: "PaymentGateReady", Status: "True"},
		{Type: "RoutePublished", Status: "True"},
	}
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "demo",
			Namespace:         "llm",
			CreationTimestamp: metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
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
				Enabled: true,
				Name:    "Demo Agent",
				Skills:  []string{"natural_language_processing/text_generation"},
				Domains: []string{"technology/artificial_intelligence"},
				Metadata: map[string]string{
					"gpu":          "A100-80GB",
					"best_val_bpb": "1.234",
				},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
	}
	secondary := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "blocks",
			Namespace:         "demo",
			CreationTimestamp: metav1.NewTime(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)),
		},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Path: "/services/blocks",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
				Skills:  []string{"blockchain/data"},
				Domains: []string{"technology/blockchain"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
	}

	identity := &monetizeapi.AgentIdentity{}
	identity.Namespace = monetizeapi.AgentIdentityDefaultNamespace
	identity.Name = monetizeapi.AgentIdentityDefaultName
	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, "base-sepolia", "7")
	document := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: identity,
		Offers:   []*monetizeapi.ServiceOffer{owner, secondary},
		BaseURL:  "https://example.com",
	})

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

func TestBuildActiveRegistrationDocument_PublishesAgentOfferMetadata(t *testing.T) {
	owner := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-quant", Namespace: "agent-demo-quant"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Path: "/services/demo-quant",
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled:     true,
				Name:        "demo-quant",
				Description: "Agent-backed chain analyst",
				Skills:      []string{"ethereum-networks", "addresses"},
				Metadata: map[string]string{
					"runtime":     "hermes",
					"model":       "qwen3.5:9b",
					"pricingUnit": "agent-turn",
					"x402Price":   "10",
					"x402Asset":   "OBOL",
					"x402Network": "ethereum",
				},
			},
		},
	}

	doc := buildActiveRegistrationDocument(owner, []*monetizeapi.ServiceOffer{owner}, "https://seller.example", "42")
	for k, want := range map[string]string{
		"runtime":     "hermes",
		"model":       "qwen3.5:9b",
		"pricingUnit": "agent-turn",
		"x402Price":   "10",
		"x402Asset":   "OBOL",
		"x402Network": "ethereum",
	} {
		if got := doc.Metadata[k]; got != want {
			t.Errorf("metadata[%s] = %q, want %q (full=%v)", k, got, want, doc.Metadata)
		}
	}
	if len(doc.Registrations) != 1 || doc.Registrations[0].AgentID != 42 {
		t.Errorf("registrations = %+v, want agentId 42", doc.Registrations)
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

func TestBuildRegistrationServices_IncludesDrainMetadata(t *testing.T) {
	drainAt := metav1.NewTime(time.Now())
	grace := metav1.Duration{Duration: time.Hour}
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "draining", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Path:             "/services/draining",
			DrainAt:          &drainAt,
			DrainGracePeriod: &grace,
			Registration: monetizeapi.ServiceOfferRegistration{
				Enabled: true,
				Services: []monetizeapi.ServiceOfferService{
					{Name: "A2A", Endpoint: "https://example.com/a2a", Version: "0.2.1"},
				},
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

	services := buildRegistrationServices(offer, []*monetizeapi.ServiceOffer{offer}, "https://example.com")
	if len(services) != 2 {
		t.Fatalf("services = %+v, want web + A2A", services)
	}
	for _, svc := range services {
		if svc.Available != nil {
			t.Fatalf("%s.Available = %v, want nil (drain is signalled via DrainEndsAt only): %+v", svc.Name, *svc.Available, svc)
		}
		if _, err := time.Parse(time.RFC3339, svc.DrainEndsAt); err != nil {
			t.Fatalf("%s drainEndsAt = %q is not RFC3339: %v", svc.Name, svc.DrainEndsAt, err)
		}
	}
}

func TestBuildIdentityRegistrationServices_IncludesDrainMetadata(t *testing.T) {
	drainAt := metav1.NewTime(time.Now())
	grace := metav1.Duration{Duration: 30 * time.Minute}
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "identity-drain", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Path:             "/services/identity-drain",
			DrainAt:          &drainAt,
			DrainGracePeriod: &grace,
			Registration: monetizeapi.ServiceOfferRegistration{
				Services: []monetizeapi.ServiceOfferService{
					{Name: "MCP", Endpoint: "https://example.com/mcp", Version: "2025-06-18"},
				},
			},
		},
	}

	services := buildIdentityRegistrationServices([]*monetizeapi.ServiceOffer{offer}, "https://example.com")
	if len(services) != 2 {
		t.Fatalf("services = %+v, want web + MCP", services)
	}
	for _, svc := range services {
		if svc.Available != nil {
			t.Fatalf("%s.Available = %v, want nil (drain is signalled via DrainEndsAt only): %+v", svc.Name, *svc.Available, svc)
		}
		if _, err := time.Parse(time.RFC3339, svc.DrainEndsAt); err != nil {
			t.Fatalf("%s drainEndsAt = %q is not RFC3339: %v", svc.Name, svc.DrainEndsAt, err)
		}
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
		ObjectMeta: metav1.ObjectMeta{
			Name:              "hello",
			Namespace:         "demo",
			UID:               types.UID("owner-uid"),
			CreationTimestamp: metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
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
			ObjectMeta: metav1.ObjectMeta{
				Name:              "blocks",
				Namespace:         "demo",
				CreationTimestamp: metav1.NewTime(time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)),
			},
			Spec: monetizeapi.ServiceOfferSpec{
				Path: "/services/blocks",
				Registration: monetizeapi.ServiceOfferRegistration{
					Enabled: true,
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "oracle",
				Namespace:         "demo",
				CreationTimestamp: metav1.NewTime(time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)),
			},
			Spec: monetizeapi.ServiceOfferSpec{
				Path: "/services/oracle",
				Registration: monetizeapi.ServiceOfferRegistration{
					Enabled: true,
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyConditions},
		},
	}

	identity := &monetizeapi.AgentIdentity{}
	identity.Namespace = monetizeapi.AgentIdentityDefaultNamespace
	identity.Name = monetizeapi.AgentIdentityDefaultName
	identity.UID = types.UID("identity-uid")
	identity.Status = monetizeapi.UpsertAgentIdentityRegistration(identity.Status, "base-sepolia", "42")
	document := BuildIdentityRegistrationDocument(IdentityRegistrationView{
		Identity: identity,
		Offers:   offers,
		BaseURL:  "https://example.com",
	})
	documentJSON, _, err := marshalRegistrationDocument(document)
	if err != nil {
		t.Fatalf("marshalRegistrationDocument: %v", err)
	}

	cm := buildAgentIdentityRegistrationConfigMap(identity, documentJSON)
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

func TestBuildSkillMarkdown(t *testing.T) {
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

	content := buildSkillMarkdown([]*monetizeapi.ServiceOffer{readyOffer, notReadyOffer}, "https://example.com", nil)

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

// TestBuildSkillMarkdown_DrainAdditiveDetail locks in the
// pure-additive markdown surface: active offers must NOT emit a
// `- **Available**:` detail bullet (that wire was removed when drain
// landed). Draining offers may have a `- **Drain ends at**:` bullet
// but never a separate Available bullet, because consumers detect
// drain solely via the timestamp's presence.
func TestBuildSkillMarkdown_DrainAdditiveDetail(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}
	activeOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
	}

	drainAt := metav1.NewTime(time.Now())
	grace := metav1.Duration{Duration: time.Hour}
	drainingOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "bravo", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:             "http",
			DrainAt:          &drainAt,
			DrainGracePeriod: &grace,
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x2222222222222222222222222222222222222222",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
	}

	content := buildSkillMarkdown(
		[]*monetizeapi.ServiceOffer{activeOffer, drainingOffer},
		"https://example.com",
		nil,
	)

	if strings.Contains(content, "- **Available**:") {
		t.Errorf("markdown contains `- **Available**:` bullet; drain wire is additive (drainEndsAt only):\n%s", content)
	}
	if !strings.Contains(content, "| [alpha](#alpha) | http | — | 0.001 USDC/request on base | available |") {
		t.Errorf("active offer status missing `available` table signal:\n%s", content)
	}
	if !strings.Contains(content, "- **Drain ends at**:") {
		t.Errorf("draining offer missing `- **Drain ends at**:` bullet:\n%s", content)
	}
	// Table header should expose Status, not the legacy Available column.
	if strings.Contains(content, "| Available |") {
		t.Errorf("markdown table header still has `Available` column; expected `Status`:\n%s", content)
	}
	if !strings.Contains(content, "| Status |") {
		t.Errorf("markdown table header missing `Status` column:\n%s", content)
	}
}

// TestBuildSkillMarkdown_AgentModelStripped locks in that agent offers
// never surface their underlying model in the catalog (the agent runs its own
// model and ignores the request `model` field — it's an internal detail), while
// inference offers keep it (there the buyer selects the model). Mirrors the
// 402 page / extra / bazaar model-strip in internal/x402.
func TestBuildSkillMarkdown_AgentModelStripped(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}
	agentOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "agent-analyst"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:  "agent",
			Model: monetizeapi.ServiceOfferModel{Name: "gemma4-aeon-uncensored"},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.01"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
	}
	inferenceOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "raw-llm", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:  "inference",
			Model: monetizeapi.ServiceOfferModel{Name: "qwen36-deep"},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base-sepolia",
				PayTo:   "0x2222222222222222222222222222222222222222",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
	}

	content := buildSkillMarkdown(
		[]*monetizeapi.ServiceOffer{agentOffer, inferenceOffer},
		"https://example.com",
		nil,
	)

	// Agent: model never appears (table column is "—", no **Model** detail).
	if strings.Contains(content, "gemma4-aeon-uncensored") {
		t.Errorf("agent offer leaked its internal model into the catalog:\n%s", content)
	}
	// Inference: model is buyer-facing and must stay (table + detail bullet).
	if !strings.Contains(content, "- **Model**: qwen36-deep") {
		t.Errorf("inference offer dropped its (buyer-selectable) model bullet:\n%s", content)
	}
}

func TestBuildStaticSiteHTTPRoute(t *testing.T) {
	route := buildStaticSiteHTTPRoute()
	if route.GetName() != staticSiteRouteName {
		t.Fatalf("route name = %q, want %q", route.GetName(), staticSiteRouteName)
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
	if backend["name"] != staticSiteConfigMapName {
		t.Fatalf("backend name = %v, want %s", backend["name"], staticSiteConfigMapName)
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

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{readyOffer, notReadyOffer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services

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
	if svc.Category != "demo" {
		t.Errorf("category = %q, want demo (back-compat: namespace=demo)", svc.Category)
	}
	if svc.Endpoint != "https://example.com/services/demo-hello" {
		t.Errorf("endpoint = %q, want https://example.com/services/demo-hello", svc.Endpoint)
	}
	if svc.Description != "Proof-of-payment echo service" {
		t.Errorf("description = %q, want 'Proof-of-payment echo service'", svc.Description)
	}
	if svc.DescriptionHTML != "<p>Proof-of-payment echo service</p>" {
		t.Errorf("descriptionHtml = %q, want sanitized paragraph", svc.DescriptionHTML)
	}
	// Single-payment offers still expose payments[] (one entry mirroring flat).
	if len(svc.Payments) != 1 || svc.Payments[0].Network != "base" {
		t.Errorf("single-payment offer payments = %+v, want one base entry", svc.Payments)
	}
}

// TestBuildServiceCatalogJSON_MarkdownDescription pins the richtext contract
// on the published feed: markdown renders to sanitized HTML, hostile input
// never survives in executable form.
func TestBuildServiceCatalogJSON_MarkdownDescription(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "md", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:    "http",
			Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0x1111111111111111111111111111111111111111", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"}},
			Registration: monetizeapi.ServiceOfferRegistration{
				Description: "We sell **audits**.\n\n<script>alert(1)</script>",
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}}},
	}

	profile := &schemas.StorefrontProfile{Description: "# About us\n\nFast *and* cheap."}
	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", profile)
	assertServiceCatalogSchema(t, jsonStr)
	catalog := decodeServiceCatalog(t, jsonStr)

	svc := catalog.Services[0]
	if !strings.Contains(svc.DescriptionHTML, "<strong>audits</strong>") {
		t.Errorf("descriptionHtml missing markdown rendering: %q", svc.DescriptionHTML)
	}
	if strings.Contains(svc.DescriptionHTML, "<script") {
		t.Fatalf("script survived sanitization: %q", svc.DescriptionHTML)
	}
	if !strings.Contains(catalog.DescriptionHTML, "<h3>About us</h3>") ||
		!strings.Contains(catalog.DescriptionHTML, "<em>and</em>") {
		t.Errorf("envelope descriptionHtml = %q, want demoted heading + emphasis", catalog.DescriptionHTML)
	}
}

func TestBuildServiceCatalogJSON_MultiPayment(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "bankr", Namespace: "agent-bankr"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:    "agent",
			Payment: monetizeapi.ServiceOfferPayment{Network: "base", PayTo: "0x1111111111111111111111111111111111111111", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"}},
			Payments: []monetizeapi.ServiceOfferPayment{
				{Network: "base", PayTo: "0x1111111111111111111111111111111111111111", Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "1"}},
				{
					Network: "ethereum", PayTo: "0x2222222222222222222222222222222222222222",
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "10"},
					Asset: monetizeapi.ServiceOfferAsset{Symbol: "OBOL", Address: "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7", Decimals: 18, TransferMethod: "permit2", EIP712Name: "Obol Network", EIP712Version: "1"},
				},
			},
			Registration: monetizeapi.ServiceOfferRegistration{Description: "multi-currency agent"},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}}},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
	if len(services) != 1 {
		t.Fatalf("want 1 service, got %d", len(services))
	}
	pays := services[0].Payments
	if len(pays) != 2 {
		t.Fatalf("payments = %d, want 2", len(pays))
	}
	// Flat fields mirror the primary (first) option.
	if services[0].Network != "base" || services[0].PayTo != "0x1111111111111111111111111111111111111111" {
		t.Errorf("flat fields should mirror primary option: %+v", services[0])
	}
	// Second option resolves OBOL on ethereum with its own atomic price.
	obol := pays[1]
	if obol.Network != "ethereum" || obol.Asset == nil || obol.Asset.Symbol != "OBOL" {
		t.Fatalf("second option = %+v, want OBOL on ethereum", obol)
	}
	if obol.PriceAtomicUnits != "10000000000000000000" { // 10 * 1e18
		t.Errorf("OBOL atomic price = %q, want 10e18", obol.PriceAtomicUnits)
	}
}

func TestBuildServiceCatalogJSON_ZeroDecimalAssetAtomicPrice(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "whole-token", Namespace: "default"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "2"},
				Asset: monetizeapi.ServiceOfferAsset{
					Address:        "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					Symbol:         "WHOLE",
					Decimals:       0,
					TransferMethod: "permit2",
					EIP712Name:     "Whole Token",
					EIP712Version:  "1",
				},
			},
			Registration: monetizeapi.ServiceOfferRegistration{Description: "zero-decimal token service"},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}}},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
	if len(services) != 1 || len(services[0].Payments) != 1 {
		t.Fatalf("services/payments = %+v, want one payment", services)
	}
	if got := services[0].Payments[0].PriceAtomicUnits; got != "2" {
		t.Fatalf("zero-decimal priceAtomicUnits = %q, want 2", got)
	}
}

func TestBuildServiceCatalogJSON_Empty(t *testing.T) {
	jsonStr := buildServiceCatalogJSON(nil, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)
	catalog := decodeServiceCatalog(t, jsonStr)
	if len(catalog.Services) != 0 {
		t.Errorf("expected empty services, got %d", len(catalog.Services))
	}
	if catalog.DisplayName == "" || catalog.Tagline == "" || catalog.LogoURL == "" {
		t.Errorf("expected default seller branding, got %+v", catalog)
	}
}

func TestBuildServiceCatalogJSON_AgentOfferOmitsModel(t *testing.T) {
	offer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-quant", Namespace: "agent-demo-quant"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "ethereum",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Asset: monetizeapi.ServiceOfferAsset{
					Address:        "0x2222222222222222222222222222222222222222",
					Symbol:         "OBOL",
					Decimals:       18,
					TransferMethod: "permit2",
					EIP712Name:     "OBOL",
					EIP712Version:  "1",
				},
				Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "10"},
			},
			Registration: monetizeapi.ServiceOfferRegistration{
				Description: "Agent-backed chain analyst",
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			AgentResolution: &monetizeapi.ServiceOfferAgentResolution{
				Model:   "qwen3.5:9b",
				Runtime: "hermes",
			},
			Conditions: []monetizeapi.Condition{{Type: "Ready", Status: "True"}},
		},
	}

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://seller.example", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d: %s", len(services), jsonStr)
	}
	svc := services[0]
	if svc.Type != "agent" {
		t.Errorf("type = %q, want agent", svc.Type)
	}
	// Agent offers never surface their internal model — it's an operator
	// detail (and goes stale on model swaps); the agent ignores the request
	// `model` field anyway. Mirrors the 402/extra/bazaar model-strip.
	if svc.Model != "" {
		t.Errorf("model = %q, want \"\" (agent model is internal)", svc.Model)
	}
	if svc.Price != "10 OBOL/request" {
		t.Errorf("price = %q, want 10 OBOL/request", svc.Price)
	}
	if svc.Endpoint != "https://seller.example/services/demo-quant" {
		t.Errorf("endpoint = %q", svc.Endpoint)
	}
}

// TestBuildServiceCatalogJSON_ExcludesNonReady locks in the filter pipeline:
// nil offers, drain-expired offers, and offers with a DeletionTimestamp
// must never leak onto the public storefront, even if they carry
// Ready=True. Mid-drain offers DO stay in the catalog with available=false
// and drainEndsAt set — that's the whole point of the drain replacement.
func TestBuildServiceCatalogJSON_ExcludesNonReady(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}

	deleting := metav1.Now()
	drainedAt := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	zeroGrace := metav1.Duration{Duration: 0}

	offers := []*monetizeapi.ServiceOffer{
		nil,
		{
			ObjectMeta: metav1.ObjectMeta{Name: "drained-svc", Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				DrainAt:          &drainedAt,
				DrainGracePeriod: &zeroGrace,
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
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

	jsonStr := buildServiceCatalogJSON(offers, "https://example.com", nil)

	services := decodeServiceCatalog(t, jsonStr).Services
	if len(services) != 1 {
		t.Fatalf("expected exactly 1 service (ready-svc), got %d: %+v", len(services), services)
	}
	if services[0].Name != "ready-svc" {
		t.Errorf("got %q, want ready-svc — filter pipeline leaked another offer", services[0].Name)
	}

	// Pure-additive wire schema: active offers must serialize without
	// `available` (no field at all). Consumers detect drain via the
	// presence of `drainEndsAt`, not via a legacy `available` boolean.
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		t.Fatalf("invalid raw JSON: %v\n%s", err, jsonStr)
	}
	servicesRaw, _ := raw["services"].([]any)
	if len(servicesRaw) != 1 {
		t.Fatalf("expected 1 raw service entry, got %d", len(servicesRaw))
	}
	svc0, _ := servicesRaw[0].(map[string]any)
	if _, ok := svc0["available"]; ok {
		t.Errorf("ready-svc JSON contains `available` key; drain wire schema must be additive (drainEndsAt only)")
	}
	if _, ok := svc0["drainEndsAt"]; ok {
		t.Errorf("ready-svc JSON contains `drainEndsAt`; should only appear on draining offers")
	}
}

// TestBuildServiceCatalogJSON_DrainLifecycle covers the three drain
// states explicitly under the pure-additive wire schema: pre-drain
// (no `available` key, no `drainEndsAt`), mid-drain (no `available`
// key, only `drainEndsAt` populated), and drain-expired (filtered out
// of the catalog because the controller has torn down the underlying
// route). Consumers detect drain with `if (entry.drainEndsAt)`.
func TestBuildServiceCatalogJSON_DrainLifecycle(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}
	mkOffer := func(name string) monetizeapi.ServiceOffer {
		return monetizeapi.ServiceOffer{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				Type: "http",
				Payment: monetizeapi.ServiceOfferPayment{
					Network: "base",
					PayTo:   "0x1111111111111111111111111111111111111111",
					Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
		}
	}

	// Pre-drain.
	pre := mkOffer("pre")

	// Mid-drain: drainAt = now, grace = 1h → ends ~1h from now.
	midDrainAt := metav1.NewTime(time.Now())
	midGrace := metav1.Duration{Duration: time.Hour}
	mid := mkOffer("mid")
	mid.Spec.DrainAt = &midDrainAt
	mid.Spec.DrainGracePeriod = &midGrace

	// Drain-expired.
	expDrainAt := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	expGrace := metav1.Duration{Duration: time.Hour}
	exp := mkOffer("expired")
	exp.Spec.DrainAt = &expDrainAt
	exp.Spec.DrainGracePeriod = &expGrace

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{&pre, &mid, &exp}, "https://example.com", nil)
	var raw map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonStr)
	}
	servicesRaw, _ := raw["services"].([]any)
	if len(servicesRaw) != 2 {
		t.Fatalf("expected 2 services (pre + mid; expired filtered out), got %d: %+v", len(servicesRaw), servicesRaw)
	}

	byName := map[string]map[string]any{}
	for _, item := range servicesRaw {
		s, _ := item.(map[string]any)
		name, _ := s["name"].(string)
		byName[name] = s
	}
	if entry, ok := byName["pre"]; !ok {
		t.Fatal("pre-drain offer missing from catalog")
	} else {
		if _, has := entry["available"]; has {
			t.Errorf("pre entry contains `available` key; drain wire schema must be additive")
		}
		if _, has := entry["drainEndsAt"]; has {
			t.Errorf("pre entry contains `drainEndsAt` key; should only appear on draining offers")
		}
	}
	if entry, ok := byName["mid"]; !ok {
		t.Fatal("mid-drain offer missing from catalog")
	} else {
		if _, has := entry["available"]; has {
			t.Errorf("mid entry contains `available` key; drain wire schema must be additive (drainEndsAt only)")
		}
		drainEndsAt, has := entry["drainEndsAt"].(string)
		if !has || drainEndsAt == "" {
			t.Errorf("mid entry missing `drainEndsAt`; should be populated for draining offers")
		}
		if _, err := time.Parse(time.RFC3339, drainEndsAt); err != nil {
			t.Errorf("mid.drainEndsAt = %q is not RFC3339: %v", drainEndsAt, err)
		}
	}
	if _, ok := byName["expired"]; ok {
		t.Error("drain-expired offer leaked into catalog; should be filtered")
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

	jsonStr := buildServiceCatalogJSON(offers, "https://example.com", nil)

	services := decodeServiceCatalog(t, jsonStr).Services
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

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
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

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
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

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com/", nil)

	services := decodeServiceCatalog(t, jsonStr).Services
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

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
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

	jsonStr := buildServiceCatalogJSON([]*monetizeapi.ServiceOffer{offer}, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	services := decodeServiceCatalog(t, jsonStr).Services
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

// TestOfferOperationallyReady_IncludesAwaitingExternalRegistration pins the
// behavioral fix that "operationally ready" no longer requires the on-chain
// ERC-8004 registration to have landed.
func TestOfferOperationallyReady_IncludesAwaitingExternalRegistration(t *testing.T) {
	awaiting := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "aeon", Namespace: "llm"},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{
				{Type: "ModelReady", Status: "True"},
				{Type: "UpstreamHealthy", Status: "True"},
				{Type: "PaymentGateReady", Status: "True"},
				{Type: "RoutePublished", Status: "True"},
				{Type: "Registered", Status: "False", Reason: "AwaitingExternalRegistration"},
				{Type: "Ready", Status: "False", Reason: "Reconciling"},
			},
		},
	}
	if !offerOperationallyReady(awaiting) {
		t.Fatal("offerOperationallyReady must return true for AwaitingExternalRegistration — the offer is usable for x402 payments today regardless of on-chain identity")
	}
	if !offerAwaitingRegistration(awaiting) {
		t.Fatal("offerAwaitingRegistration must flag this offer so the storefront badges it as registration-pending")
	}
}

// TestOfferOperationallyReady_RejectsRealNotReady pins the narrow scope of
// the relax: an offer whose foreground gateway is down (UpstreamHealthy=False)
// must still be excluded.
func TestOfferOperationallyReady_RejectsRealNotReady(t *testing.T) {
	notUsable := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "broken", Namespace: "llm"},
		Status: monetizeapi.ServiceOfferStatus{
			Conditions: []monetizeapi.Condition{
				{Type: "ModelReady", Status: "True"},
				{Type: "UpstreamHealthy", Status: "False", Reason: "Unhealthy"},
				{Type: "PaymentGateReady", Status: "False", Reason: "WaitingForUpstream"},
				{Type: "RoutePublished", Status: "False", Reason: "WaitingForPaymentGate"},
			},
		},
	}
	if offerOperationallyReady(notUsable) {
		t.Error("offerOperationallyReady must reject an offer with UpstreamHealthy=False")
	}
	if offerAwaitingRegistration(notUsable) {
		t.Error("offerAwaitingRegistration must NOT flag offers whose Registered condition isn't AwaitingExternalRegistration")
	}
}

// TestBuildServiceCatalogJSON_IncludesPendingRegistrationOffers pins the
// end-to-end wiring: an offer that's operationally ready but awaiting
// on-chain registration appears in /api/services.json with
// RegistrationPending=true.
func TestBuildServiceCatalogJSON_IncludesPendingRegistrationOffers(t *testing.T) {
	offers := []*monetizeapi.ServiceOffer{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "aeon", Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				Type: "inference",
				Path: "/services/aeon",
				Payment: monetizeapi.ServiceOfferPayment{
					Network: "base-sepolia",
					PayTo:   "0xeFAb75b7b199bf8512e2d5b379374Cb94dfdBA47",
					Price:   monetizeapi.ServiceOfferPriceTable{PerMTok: "23"},
				},
				Model: monetizeapi.ServiceOfferModel{Name: "aeon-ultimate"},
				Registration: monetizeapi.ServiceOfferRegistration{
					Enabled: true, Name: "AEON Ultimate",
					Description: "Uncensored Qwen3.6-27B",
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{
					{Type: "ModelReady", Status: "True"},
					{Type: "UpstreamHealthy", Status: "True"},
					{Type: "PaymentGateReady", Status: "True"},
					{Type: "RoutePublished", Status: "True"},
					{Type: "Registered", Status: "False", Reason: "AwaitingExternalRegistration"},
				},
			},
		},
	}
	jsonStr := buildServiceCatalogJSON(offers, "https://inference.example.com", nil)
	services := decodeServiceCatalog(t, jsonStr).Services
	if len(services) != 1 {
		t.Fatalf("expected 1 service in catalog, got %d: %+v", len(services), services)
	}
	if services[0].Name != "aeon" {
		t.Errorf("got %q, want aeon", services[0].Name)
	}
	if !services[0].RegistrationPending {
		t.Error("RegistrationPending must be true for AwaitingExternalRegistration offers")
	}
}

// TestBuildServiceCatalogJSON_RegistrationPendingFalseForFullyReady pins
// the negative: an offer that's fully Ready=True (on-chain register tx
// landed) must NOT carry RegistrationPending.
func TestBuildServiceCatalogJSON_RegistrationPendingFalseForFullyReady(t *testing.T) {
	offers := []*monetizeapi.ServiceOffer{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "fully-ready", Namespace: "llm"},
			Spec: monetizeapi.ServiceOfferSpec{
				Type: "http",
				Payment: monetizeapi.ServiceOfferPayment{
					Network: "base", PayTo: "0x1111111111111111111111111111111111111111",
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
			},
			Status: monetizeapi.ServiceOfferStatus{
				Conditions: []monetizeapi.Condition{
					{Type: "ModelReady", Status: "True"},
					{Type: "UpstreamHealthy", Status: "True"},
					{Type: "PaymentGateReady", Status: "True"},
					{Type: "RoutePublished", Status: "True"},
					{Type: "Registered", Status: "True", Reason: "Active"},
					{Type: "Ready", Status: "True"},
				},
			},
		},
	}
	jsonStr := buildServiceCatalogJSON(offers, "https://example.com", nil)
	services := decodeServiceCatalog(t, jsonStr).Services
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].RegistrationPending {
		t.Error("RegistrationPending must be false for fully Ready=True offers")
	}
}

// TestBuildCatalogHeadersMiddleware locks the CORS + cache posture on the
// public catalog surfaces: wildcard origin (read-only public data, no
// credentials), GET/OPTIONS only, and a 5-minute public cache.
func TestBuildCatalogHeadersMiddleware(t *testing.T) {
	mw := buildCatalogHeadersMiddleware()
	if got := mw.GetAPIVersion(); got != "traefik.io/v1alpha1" {
		t.Fatalf("apiVersion = %q, want traefik.io/v1alpha1", got)
	}
	if got := mw.GetKind(); got != "Middleware" {
		t.Fatalf("kind = %q, want Middleware", got)
	}
	if mw.GetName() != catalogHeadersMiddlewareName {
		t.Fatalf("name = %q, want %q", mw.GetName(), catalogHeadersMiddlewareName)
	}
	if mw.GetNamespace() != staticSiteNamespace {
		t.Fatalf("namespace = %q, want %q", mw.GetNamespace(), staticSiteNamespace)
	}

	headers := mw.Object["spec"].(map[string]any)["headers"].(map[string]any)
	origins := headers["accessControlAllowOriginList"].([]any)
	if len(origins) != 1 || origins[0] != "*" {
		t.Errorf("accessControlAllowOriginList = %v, want [*]", origins)
	}
	methods := headers["accessControlAllowMethods"].([]any)
	if len(methods) != 2 || methods[0] != "GET" || methods[1] != "OPTIONS" {
		t.Errorf("accessControlAllowMethods = %v, want [GET OPTIONS]", methods)
	}
	custom := headers["customResponseHeaders"].(map[string]any)
	if custom["Cache-Control"] != "public, max-age=300" {
		t.Errorf("Cache-Control = %v, want 'public, max-age=300'", custom["Cache-Control"])
	}
}

// TestCatalogRoutesCarryHeadersFilter asserts every catalog HTTPRoute
// (/skill.md, /api/services.json, /openapi.json, /api) attaches the headers
// Middleware via an ExtensionRef filter — the same mechanism the x402
// middleware uses on paid routes. Paid /services/* routes must NOT carry it
// (locked separately by TestBuildHTTPRoute's no-filters assertion).
func TestCatalogRoutesCarryHeadersFilter(t *testing.T) {
	routes := map[string]*unstructured.Unstructured{
		"/skill.md":          buildStaticSiteHTTPRoute(),
		"/api/services.json": buildServicesJSONHTTPRoute(),
		"/openapi.json":      buildOpenAPIHTTPRoute(),
		"/api":               buildAPIDocsHTTPRoute(),
	}
	for path, route := range routes {
		rules := route.Object["spec"].(map[string]any)["rules"].([]any)
		firstRule := rules[0].(map[string]any)
		filters, found := firstRule["filters"].([]any)
		if !found || len(filters) != 1 {
			t.Errorf("%s route: filters = %v, want exactly one ExtensionRef filter", path, firstRule["filters"])
			continue
		}
		filter := filters[0].(map[string]any)
		if filter["type"] != "ExtensionRef" {
			t.Errorf("%s route: filter type = %v, want ExtensionRef", path, filter["type"])
		}
		ref := filter["extensionRef"].(map[string]any)
		if ref["group"] != "traefik.io" || ref["kind"] != "Middleware" || ref["name"] != catalogHeadersMiddlewareName {
			t.Errorf("%s route: extensionRef = %v, want traefik.io Middleware %q", path, ref, catalogHeadersMiddlewareName)
		}
	}
}

// TestBuildServiceCatalogJSON_SchemaVersion locks the envelope version
// marker: always present, always "1" until a breaking wire change bumps it.
func TestBuildServiceCatalogJSON_SchemaVersion(t *testing.T) {
	jsonStr := buildServiceCatalogJSON(nil, "https://example.com", nil)
	assertServiceCatalogSchema(t, jsonStr)

	if got := decodeServiceCatalog(t, jsonStr).SchemaVersion; got != schemas.ServiceCatalogSchemaVersion {
		t.Errorf("schemaVersion = %q, want %q", got, schemas.ServiceCatalogSchemaVersion)
	}
	// Assert on the raw wire too, so a struct/tag rename can't silently
	// satisfy the decode-side check.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		t.Fatalf("unmarshal raw catalog: %v", err)
	}
	if string(raw["schemaVersion"]) != `"1"` {
		t.Errorf(`raw schemaVersion = %s, want "1"`, raw["schemaVersion"])
	}

	// The fallback envelope must carry the same version.
	fallback := fallbackServiceCatalogJSON("https://example.com")
	assertServiceCatalogSchema(t, fallback)
	if got := decodeServiceCatalog(t, fallback).SchemaVersion; got != schemas.ServiceCatalogSchemaVersion {
		t.Errorf("fallback schemaVersion = %q, want %q", got, schemas.ServiceCatalogSchemaVersion)
	}
}

// TestBuildSkillMarkdown_TryIt asserts every offer detail block ends
// with a copy-paste "Try it" section: a 402 probe curl plus a worked paid
// request. Chat-shaped offers must show the REAL model id (including the
// AgentResolution fallback for type=agent) — the same id services.json
// publishes — and http offers a curl of the gated path.
func TestBuildSkillMarkdown_TryIt(t *testing.T) {
	readyCond := []monetizeapi.Condition{{Type: "Ready", Status: "True"}}
	inferenceOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "flow-qwen", Namespace: "llm"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type:  "inference",
			Model: monetizeapi.ServiceOfferModel{Name: "qwen36-deep"},
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x1111111111111111111111111111111111111111",
				Price:   monetizeapi.ServiceOfferPriceTable{PerMTok: "0.10"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
	}
	agentOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "quant", Namespace: "agent-quant"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "agent",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x2222222222222222222222222222222222222222",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.05"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{
			AgentResolution: &monetizeapi.ServiceOfferAgentResolution{Model: "qwen3.5:9b", Runtime: "hermes"},
			Conditions:      readyCond,
		},
	}
	httpOffer := &monetizeapi.ServiceOffer{
		ObjectMeta: metav1.ObjectMeta{Name: "echo", Namespace: "demo"},
		Spec: monetizeapi.ServiceOfferSpec{
			Type: "http",
			Payment: monetizeapi.ServiceOfferPayment{
				Network: "base",
				PayTo:   "0x3333333333333333333333333333333333333333",
				Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
			},
		},
		Status: monetizeapi.ServiceOfferStatus{Conditions: readyCond},
	}

	content := buildSkillMarkdown(
		[]*monetizeapi.ServiceOffer{inferenceOffer, agentOffer, httpOffer},
		"https://seller.example", nil,
	)

	if got := strings.Count(content, "#### Try it"); got != 3 {
		t.Fatalf("Try it sections = %d, want 3:\n%s", got, content)
	}
	// 402 probe curl per offer.
	for _, probe := range []string{
		"curl -i https://seller.example/services/flow-qwen",
		"curl -i https://seller.example/services/quant",
		"curl -i https://seller.example/services/echo",
	} {
		if !strings.Contains(content, probe) {
			t.Errorf("missing 402 probe %q:\n%s", probe, content)
		}
	}
	// Chat-shaped offers: worked chat-completions example with the real model
	// id (identical bytes to services.json's buy.example — buyprompts is the
	// single authoring point).
	if !strings.Contains(content, "POST https://seller.example/services/flow-qwen/v1/chat/completions") {
		t.Errorf("inference Try it missing chat-completions example:\n%s", content)
	}
	if !strings.Contains(content, `"model": "qwen36-deep"`) {
		t.Errorf("inference Try it missing real model id qwen36-deep:\n%s", content)
	}
	if strings.Contains(content, "qwen3.5:9b") {
		t.Errorf("agent Try it leaked the internal AgentResolution model:\n%s", content)
	}
	if !strings.Contains(content, "X-PAYMENT: <pre-signed-EIP-3009-or-Permit2-voucher>") {
		t.Errorf("Try it examples missing X-PAYMENT placeholder:\n%s", content)
	}
	// http offer: paid retry is a curl of the gated path.
	if !strings.Contains(content, `curl -i https://seller.example/services/echo -H "X-PAYMENT: <base64-signed-authorization>"`) {
		t.Errorf("http Try it missing paid curl:\n%s", content)
	}
	// Payment mechanics are referenced, not duplicated.
	if !strings.Contains(content, "[How to pay (x402)](#how-to-pay-x402)") {
		t.Errorf("Try it missing How-to-pay anchor link:\n%s", content)
	}
}
