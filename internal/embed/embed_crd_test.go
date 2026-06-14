package embed

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// multiDoc splits a YAML file that may start with a Helm conditional
// (e.g. {{- if ... }}) into individual YAML documents, stripping
// Helm template directives and blank documents.
func multiDoc(raw []byte) []map[string]any {
	// Strip Helm template lines ({{- ... }}).
	var cleaned []string

	text := strings.ReplaceAll(string(raw), "{{ .Values.id }}", "test-id")

	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{{") {
			continue
		}

		cleaned = append(cleaned, line)
	}

	docs := strings.Split(strings.Join(cleaned, "\n"), "\n---\n")

	var result []map[string]any

	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		var m map[string]any
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			continue
		}

		if len(m) > 0 {
			result = append(result, m)
		}
	}

	return result
}

// findDoc returns the first document matching kind.
func findDoc(docs []map[string]any, kind string) map[string]any {
	for _, d := range docs {
		if d["kind"] == kind {
			return d
		}
	}

	return nil
}

// findDocByName returns the first document matching kind and metadata.name.
func findDocByName(docs []map[string]any, kind, name string) map[string]any {
	for _, d := range docs {
		if d["kind"] == kind && nested(d, "metadata", "name") == name {
			return d
		}
	}

	return nil
}

// nested traverses a map[string]interface{} by dot-separated keys.
func nested(m map[string]any, keys ...string) any {
	var cur any = m
	for _, k := range keys {
		cm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}

		cur = cm[k]
	}

	return cur
}

// ─────────────────────────────────────────────────────────────────────────────
// ServiceOffer CRD tests
// ─────────────────────────────────────────────────────────────────────────────

func TestServiceOfferCRD_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/serviceoffer-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CustomResourceDefinition document found")
	}

	if got := crd["apiVersion"]; got != "apiextensions.k8s.io/v1" {
		t.Errorf("apiVersion = %v, want apiextensions.k8s.io/v1", got)
	}

	name := nested(crd, "metadata", "name")
	if name != "serviceoffers.obol.org" {
		t.Errorf("metadata.name = %v, want serviceoffers.obol.org", name)
	}

	group := nested(crd, "spec", "group")
	if group != "obol.org" {
		t.Errorf("spec.group = %v, want obol.org", group)
	}
}

func TestServiceOfferCRD_Fields(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/serviceoffer-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD document found")
	}

	// Navigate to spec.versions[0].schema.openAPIV3Schema.properties.spec.properties
	versions, ok := nested(crd, "spec", "versions").([]any)
	if !ok || len(versions) == 0 {
		t.Fatal("spec.versions is empty or wrong type")
	}

	v0, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatal("versions[0] is not a map")
	}

	specProps := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties")

	pm, ok := specProps.(map[string]any)
	if !ok {
		t.Fatalf("spec.properties is not a map: %T", specProps)
	}

	// Required fields in spec (aligned with x402/ERC-8004 schema). agent
	// joins this list as part of the type=agent offer flow; dataset joins
	// it for the type=dataset offer flow.
	for _, field := range []string{"type", "agent", "dataset", "model", "upstream", "payment", "path", "registration"} {
		if _, exists := pm[field]; !exists {
			t.Errorf("spec.properties missing field %q", field)
		}
	}

	typeProp, _ := pm["type"].(map[string]any)
	enum, _ := typeProp["enum"].([]any)
	got := make(map[string]bool, len(enum))
	for _, v := range enum {
		got[v.(string)] = true
	}
	for _, want := range []string{"inference", "fine-tuning", "http", "agent", "dataset"} {
		if !got[want] {
			t.Errorf("spec.type.enum missing %q", want)
		}
	}
}

// TestServiceOfferCRD_DatasetFields pins the type=dataset schema surface:
// the spec.dataset block carries the pinned artifact metadata (mirroring
// spec.agent), and price.perMB enables per-megabyte pricing. Mirrors
// TestServiceOfferCRD_Fields' navigation.
func TestServiceOfferCRD_DatasetFields(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/serviceoffer-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	crd := findDoc(multiDoc(data), "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD document found")
	}

	versions, ok := nested(crd, "spec", "versions").([]any)
	if !ok || len(versions) == 0 {
		t.Fatal("spec.versions is empty or wrong type")
	}
	v0, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatal("versions[0] is not a map")
	}

	specProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties").(map[string]any)
	if !ok {
		t.Fatal("spec.properties is not a map")
	}

	datasetProps, ok := nested(specProps, "dataset", "properties").(map[string]any)
	if !ok {
		t.Fatal("spec.dataset.properties missing — type=dataset offers can't pin a version")
	}
	for _, field := range []string{"manifestHash", "version", "fileHash", "sizeBytes"} {
		if _, exists := datasetProps[field]; !exists {
			t.Errorf("spec.dataset.properties missing field %q", field)
		}
	}
	if sb, ok := datasetProps["sizeBytes"].(map[string]any); ok && sb["type"] != "integer" {
		t.Errorf("spec.dataset.sizeBytes type = %v, want integer", sb["type"])
	}

	priceProps, ok := nested(specProps, "payment", "properties", "price", "properties").(map[string]any)
	if !ok {
		t.Fatal("spec.payment.price.properties missing")
	}
	if _, exists := priceProps["perMB"]; !exists {
		t.Error("spec.payment.price.properties missing perMB — dataset offers can't price per-MB")
	}
}

func TestServiceOfferCRD_PrinterColumns(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/serviceoffer-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD document found")
	}

	versions, ok := nested(crd, "spec", "versions").([]any)
	if !ok || len(versions) == 0 {
		t.Fatal("no versions")
	}

	v0 := versions[0].(map[string]any)

	cols, ok := v0["additionalPrinterColumns"].([]any)
	if !ok {
		t.Fatal("additionalPrinterColumns missing or wrong type")
	}

	expected := []string{"Type", "Model", "Price", "Network", "Ready", "Age"}
	if len(cols) != len(expected) {
		t.Fatalf("got %d printer columns, want %d", len(cols), len(expected))
	}

	for i, want := range expected {
		col := cols[i].(map[string]any)
		if got := col["name"]; got != want {
			t.Errorf("column[%d].name = %v, want %v", i, got, want)
		}
	}
}

func TestServiceOfferCRD_WalletValidation(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/serviceoffer-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD document found")
	}

	versions := nested(crd, "spec", "versions").([]any)
	v0 := versions[0].(map[string]any)
	// Wallet validation is now at spec.payment.properties.payTo (aligned with x402)
	payToProp := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties",
		"payment", "properties", "payTo")

	wm, ok := payToProp.(map[string]any)
	if !ok {
		t.Fatal("payment.payTo property not a map")
	}

	pattern, ok := wm["pattern"].(string)
	if !ok {
		t.Fatal("payment.payTo.pattern missing")
	}

	if pattern != "^0x[0-9a-fA-F]{40}$" {
		t.Errorf("payment.payTo.pattern = %q, want ^0x[0-9a-fA-F]{40}$", pattern)
	}
}

func TestRegistrationRequestCRD_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/registrationrequest-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)
	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no RegistrationRequest CRD found")
	}

	if name := nested(crd, "metadata", "name"); name != "registrationrequests.obol.org" {
		t.Errorf("metadata.name = %v, want registrationrequests.obol.org", name)
	}
	if kind := nested(crd, "spec", "names", "kind"); kind != "RegistrationRequest" {
		t.Errorf("spec.names.kind = %v, want RegistrationRequest", kind)
	}
}

// TestPurchaseRequestCRD_Parses guards the CRD schema shape against indentation
// regressions. Every printer-column path must resolve, so this test exercises
// the same fields k3s relies on at apply time. A broken indentation like the
// one introduced in commit f57498e (status.properties siblinged instead of
// nested) will fail here, before it can reach `obol stack up`.
func TestPurchaseRequestCRD_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/purchaserequest-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	// Parse through the project-wide yaml.v3 unmarshaler first — catches any
	// malformed YAML before the nested path lookups below silently return nil.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	docs := multiDoc(data)
	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no PurchaseRequest CRD found")
	}

	if name := nested(crd, "metadata", "name"); name != "purchaserequests.obol.org" {
		t.Errorf("metadata.name = %v, want purchaserequests.obol.org", name)
	}
	if kind := nested(crd, "spec", "names", "kind"); kind != "PurchaseRequest" {
		t.Errorf("spec.names.kind = %v, want PurchaseRequest", kind)
	}

	versions, ok := nested(crd, "spec", "versions").([]any)
	if !ok || len(versions) == 0 {
		t.Fatal("spec.versions is empty or wrong type")
	}
	v0, ok := versions[0].(map[string]any)
	if !ok {
		t.Fatal("versions[0] is not a map")
	}

	// spec.properties must carry the round-2 required fields.
	specProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties").(map[string]any)
	if !ok {
		t.Fatal("spec.properties not a map")
	}
	for _, field := range []string{"endpoint", "model", "count", "preSignedAuths", "payment"} {
		if _, exists := specProps[field]; !exists {
			t.Errorf("spec.properties missing %q", field)
		}
	}

	// status.properties is the path that f57498e broke. Every printer-column
	// jsonPath below must land on a real property, otherwise the CRD applies
	// with an empty status schema and all status fields get silently dropped.
	statusProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "status", "properties").(map[string]any)
	if !ok {
		t.Fatal("status.properties not a map — is the schema indentation broken?")
	}
	for _, field := range []string{"observedGeneration", "conditions", "publicModel", "remaining", "spent", "totalSigned", "totalSpent"} {
		if _, exists := statusProps[field]; !exists {
			t.Errorf("status.properties missing %q", field)
		}
	}

	// status.conditions.items must be a map with a properties block.
	condItems, ok := nested(statusProps, "conditions", "items").(map[string]any)
	if !ok {
		t.Fatal("status.conditions.items not a map")
	}
	condProps, ok := condItems["properties"].(map[string]any)
	if !ok {
		t.Fatal("status.conditions.items.properties not a map")
	}
	for _, field := range []string{"type", "status", "reason", "message", "lastTransitionTime"} {
		if _, exists := condProps[field]; !exists {
			t.Errorf("status.conditions.items.properties missing %q", field)
		}
	}

	// Printer columns must resolve — if status.properties is wrong, the
	// .status.remaining / .status.spent columns would show empty in kubectl.
	cols, ok := v0["additionalPrinterColumns"].([]any)
	if !ok || len(cols) == 0 {
		t.Fatal("additionalPrinterColumns missing")
	}
	for _, c := range cols {
		col := c.(map[string]any)
		path, _ := col["jsonPath"].(string)
		if strings.HasPrefix(path, ".status.") {
			field := strings.TrimPrefix(path, ".status.")
			// Strip JSONPath filter expressions like conditions[?(@.type=="Ready")].status
			if idx := strings.Index(field, "["); idx > 0 {
				field = field[:idx]
			}
			if _, exists := statusProps[field]; !exists {
				t.Errorf("printer column %q references .status.%s which is not in schema", col["name"], field)
			}
		}
	}

	_ = raw
}

// ─────────────────────────────────────────────────────────────────────────────
// Agent CRD tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAgentCRD_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/agent-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	docs := multiDoc(data)

	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no Agent CRD found")
	}

	if name := nested(crd, "metadata", "name"); name != "agents.obol.org" {
		t.Errorf("metadata.name = %v, want agents.obol.org", name)
	}
	if kind := nested(crd, "spec", "names", "kind"); kind != "Agent" {
		t.Errorf("spec.names.kind = %v, want Agent", kind)
	}
	if group := nested(crd, "spec", "group"); group != "obol.org" {
		t.Errorf("spec.group = %v, want obol.org", group)
	}
}

func TestAgentCRD_Fields(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/agent-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)
	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD found")
	}

	versions, ok := nested(crd, "spec", "versions").([]any)
	if !ok || len(versions) == 0 {
		t.Fatal("spec.versions empty")
	}
	v0 := versions[0].(map[string]any)

	specProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties").(map[string]any)
	if !ok {
		t.Fatal("spec.properties not a map")
	}
	for _, field := range []string{"runtime", "model", "skills", "objective", "wallet"} {
		if _, exists := specProps[field]; !exists {
			t.Errorf("spec.properties missing %q", field)
		}
	}

	statusProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "status", "properties").(map[string]any)
	if !ok {
		t.Fatal("status.properties not a map — schema indentation broken?")
	}
	for _, field := range []string{"observedGeneration", "phase", "pinnedModel", "walletAddress", "endpoint", "conditions"} {
		if _, exists := statusProps[field]; !exists {
			t.Errorf("status.properties missing %q", field)
		}
	}

	// Printer columns reference status fields — confirm each .status.X path
	// resolves so kubectl shows real values, not blanks.
	cols, ok := v0["additionalPrinterColumns"].([]any)
	if !ok || len(cols) == 0 {
		t.Fatal("additionalPrinterColumns missing")
	}
	for _, c := range cols {
		col := c.(map[string]any)
		path, _ := col["jsonPath"].(string)
		if !strings.HasPrefix(path, ".status.") {
			continue
		}
		field := strings.TrimPrefix(path, ".status.")
		if idx := strings.Index(field, "["); idx > 0 {
			field = field[:idx]
		}
		if _, exists := statusProps[field]; !exists {
			t.Errorf("printer column %q references .status.%s missing from schema", col["name"], field)
		}
	}
}

func TestAgentCRD_RuntimeEnum(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/agent-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)
	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD found")
	}

	versions := nested(crd, "spec", "versions").([]any)
	v0 := versions[0].(map[string]any)
	runtime := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties", "runtime")

	rm, ok := runtime.(map[string]any)
	if !ok {
		t.Fatal("spec.runtime not a map")
	}

	enum, ok := rm["enum"].([]any)
	if !ok || len(enum) == 0 {
		t.Fatal("spec.runtime.enum missing — controller relies on this to reject unknown runtimes")
	}

	got := make(map[string]bool)
	for _, e := range enum {
		got[e.(string)] = true
	}
	if !got["hermes"] {
		t.Error("spec.runtime.enum missing 'hermes'")
	}

	if def, ok := rm["default"].(string); !ok || def != "hermes" {
		t.Errorf("spec.runtime.default = %v, want hermes", rm["default"])
	}
}

// AgentIdentity CRD tests

func TestAgentIdentityCRD_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/agentidentity-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)
	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no AgentIdentity CRD found")
	}

	if name := nested(crd, "metadata", "name"); name != "agentidentities.obol.org" {
		t.Errorf("metadata.name = %v, want agentidentities.obol.org", name)
	}
	if kind := nested(crd, "spec", "names", "kind"); kind != "AgentIdentity" {
		t.Errorf("spec.names.kind = %v, want AgentIdentity", kind)
	}
	if scope := nested(crd, "spec", "scope"); scope != "Namespaced" {
		t.Errorf("spec.scope = %v, want Namespaced", scope)
	}

	versions := nested(crd, "spec", "versions").([]any)
	v0 := versions[0].(map[string]any)

	specProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties").(map[string]any)
	if ok && len(specProps) != 0 {
		t.Errorf("spec.properties = %v, want empty AgentIdentity spec", specProps)
	}
	if specType := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "type"); specType != "object" {
		t.Errorf("spec.type = %v, want object", specType)
	}

	statusProps, ok := nested(v0, "schema", "openAPIV3Schema", "properties", "status", "properties").(map[string]any)
	if !ok {
		t.Fatal("status.properties not a map")
	}
	if _, exists := statusProps["registrations"]; !exists {
		t.Error("status.properties missing registrations")
	}
	if len(statusProps) != 1 {
		t.Errorf("status.properties = %v, want only registrations", statusProps)
	}
}

func TestAgentCRD_WalletAddressPattern(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/agent-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)
	crd := findDoc(docs, "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD found")
	}

	versions := nested(crd, "spec", "versions").([]any)
	v0 := versions[0].(map[string]any)
	addr := nested(v0, "schema", "openAPIV3Schema", "properties", "status", "properties", "walletAddress")

	am, ok := addr.(map[string]any)
	if !ok {
		t.Fatal("status.walletAddress not a map")
	}
	pattern, _ := am["pattern"].(string)
	// Empty string is allowed (wallet.create=false), so the pattern must be optional.
	if pattern != "^(0x[0-9a-fA-F]{40})?$" {
		t.Errorf("status.walletAddress.pattern = %q, want ^(0x[0-9a-fA-F]{40})?$", pattern)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Monetize RBAC tests
// ─────────────────────────────────────────────────────────────────────────────

func TestMonetizeRBAC_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-agent-monetize-rbac.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	ns := findDocByName(docs, "Namespace", "hermes-obol-agent")
	if ns == nil {
		t.Fatal("no Namespace 'hermes-obol-agent' found")
	}

	// ── Read ClusterRole ────────────────────────────────────────────────
	readCR := findDocByName(docs, "ClusterRole", "openclaw-monetize-read")
	if readCR == nil {
		t.Fatal("no ClusterRole 'openclaw-monetize-read' found")
	}

	readRules, ok := readCR["rules"].([]any)
	if !ok || len(readRules) == 0 {
		t.Fatal("read ClusterRole has no rules")
	}

	// Read role should be read-only: no create/update/patch/delete verbs.
	for _, r := range readRules {
		rm := r.(map[string]any)

		verbs, ok := rm["verbs"].([]any)
		if !ok {
			continue
		}

		for _, v := range verbs {
			switch v.(string) {
			case "create", "update", "patch", "delete":
				t.Errorf("read ClusterRole has mutate verb %q — should be read-only", v)
			}
		}
	}

	// Read role should cover obol.org (serviceoffers) and core ("") groups.
	readGroups := collectAPIGroups(readRules)
	if !readGroups["obol.org"] {
		t.Error("read ClusterRole missing obol.org apiGroup")
	}

	if !readGroups[""] {
		t.Error("read ClusterRole missing core API group")
	}

	// ── Write ClusterRole ───────────────────────────────────────────────
	writeRole := findDocByName(docs, "ClusterRole", "openclaw-monetize-write")
	if writeRole == nil {
		t.Fatal("no ClusterRole 'openclaw-monetize-write' found")
	}
	writeRules, ok := writeRole["rules"].([]any)
	if !ok || len(writeRules) == 0 {
		t.Fatal("write ClusterRole has no rules")
	}

	if !hasVerbOnResource(writeRules, "obol.org", "serviceoffers", "create") {
		t.Error("write ClusterRole missing 'create' on obol.org/serviceoffers")
	}
	if !hasVerbOnResource(writeRules, "obol.org", "purchaserequests", "create") {
		t.Error("write ClusterRole missing 'create' on obol.org/purchaserequests")
	}
	if hasVerbOnResource(writeRules, "traefik.io", "middlewares", "create") {
		t.Error("write ClusterRole should not grant child-resource access")
	}
	if hasVerbOnResource(writeRules, "", "secrets", "create") {
		t.Error("write ClusterRole should not grant Secret writes")
	}

	factoryRole := findDocByName(docs, "ClusterRole", "hermes-agent-factory-write")
	if factoryRole == nil {
		t.Fatal("no ClusterRole 'hermes-agent-factory-write' found")
	}
	factoryRules, ok := factoryRole["rules"].([]any)
	if !ok || len(factoryRules) == 0 {
		t.Fatal("factory ClusterRole has no rules")
	}

	if !hasVerbOnResource(factoryRules, "", "namespaces", "create") {
		t.Error("factory ClusterRole missing 'create' on core/namespaces")
	}
	if !hasVerbOnResource(factoryRules, "", "secrets", "create") {
		t.Error("factory ClusterRole missing 'create' on core/secrets")
	}
	if !hasVerbOnResource(factoryRules, "obol.org", "agents", "create") {
		t.Error("factory ClusterRole missing 'create' on obol.org/agents")
	}
	if hasVerbOnResource(factoryRules, "", "namespaces", "delete") {
		t.Error("factory ClusterRole should not grant namespace deletes")
	}
	if hasVerbOnResource(factoryRules, "", "secrets", "delete") {
		t.Error("factory ClusterRole should not grant Secret deletes")
	}
	if hasVerbOnResource(factoryRules, "obol.org", "agents", "delete") {
		t.Error("factory ClusterRole should not grant Agent deletes")
	}

	// Wallet isolation: the factory's secrets get/patch must be resourceName-
	// restricted and must never expose wallet keystores (remote-signer-keystore*),
	// which are served only via the in-namespace remote-signer API.
	hasStr := func(rm map[string]any, key, want string) bool {
		vs, _ := rm[key].([]any)
		for _, v := range vs {
			if s, _ := v.(string); s == want {
				return true
			}
		}
		return false
	}
	secretsGetScoped := false
	for _, r := range factoryRules {
		rm, ok := r.(map[string]any)
		if !ok || !hasStr(rm, "apiGroups", "") || !hasStr(rm, "resources", "secrets") || !hasStr(rm, "verbs", "get") {
			continue
		}
		names, ok := rm["resourceNames"].([]any)
		if !ok || len(names) == 0 {
			t.Error("factory secrets 'get' must be resourceName-restricted (wallet isolation)")
			continue
		}
		secretsGetScoped = true
		for _, n := range names {
			if s, _ := n.(string); strings.Contains(s, "keystore") {
				t.Errorf("factory secrets get must not expose wallet keystore %q", s)
			}
		}
	}
	if !secretsGetScoped {
		t.Error("factory ClusterRole missing a resourceName-restricted secrets 'get' rule")
	}

	// ── ClusterRoleBindings ─────────────────────────────────────────────
	readCRB := findDocByName(docs, "ClusterRoleBinding", "openclaw-monetize-read-binding")
	if readCRB == nil {
		t.Fatal("no ClusterRoleBinding 'openclaw-monetize-read-binding' found")
	}

	if ref := nested(readCRB, "roleRef", "name"); ref != "openclaw-monetize-read" {
		t.Errorf("read binding roleRef.name = %v, want openclaw-monetize-read", ref)
	}
	if !bindingHasSubject(readCRB, "hermes", "hermes-obol-agent") {
		t.Error("read binding missing hermes-obol-agent/hermes subject")
	}
	if !bindingHasSubject(readCRB, "openclaw", "openclaw-obol-agent") {
		t.Error("read binding missing openclaw-obol-agent/openclaw subject")
	}

	writeRB := findDocByName(docs, "ClusterRoleBinding", "openclaw-monetize-write-binding")
	if writeRB == nil {
		t.Fatal("no ClusterRoleBinding 'openclaw-monetize-write-binding' found")
	}
	if ref := nested(writeRB, "roleRef", "name"); ref != "openclaw-monetize-write" {
		t.Errorf("write binding roleRef.name = %v, want openclaw-monetize-write", ref)
	}
	if ref := nested(writeRB, "roleRef", "kind"); ref != "ClusterRole" {
		t.Errorf("write binding roleRef.kind = %v, want ClusterRole", ref)
	}
	if !bindingHasSubject(writeRB, "hermes", "hermes-obol-agent") {
		t.Error("write binding missing hermes-obol-agent/hermes subject")
	}
	if !bindingHasSubject(writeRB, "openclaw", "openclaw-obol-agent") {
		t.Error("write binding missing openclaw-obol-agent/openclaw subject")
	}

	factoryRB := findDocByName(docs, "ClusterRoleBinding", "hermes-agent-factory-write-binding")
	if factoryRB == nil {
		t.Fatal("no ClusterRoleBinding 'hermes-agent-factory-write-binding' found")
	}
	if ref := nested(factoryRB, "roleRef", "name"); ref != "hermes-agent-factory-write" {
		t.Errorf("factory binding roleRef.name = %v, want hermes-agent-factory-write", ref)
	}
	if !bindingHasSubject(factoryRB, "hermes", "hermes-obol-agent") {
		t.Error("factory binding missing hermes-obol-agent/hermes subject")
	}
	if bindingHasSubject(factoryRB, "openclaw", "openclaw-obol-agent") {
		t.Error("factory binding should not include openclaw-obol-agent/openclaw subject")
	}
}

func bindingHasSubject(doc map[string]any, name, namespace string) bool {
	subjects, ok := doc["subjects"].([]any)
	if !ok {
		return false
	}
	for _, subject := range subjects {
		sm, ok := subject.(map[string]any)
		if !ok {
			continue
		}
		if sm["name"] == name && sm["namespace"] == namespace {
			return true
		}
	}
	return false
}

func TestX402VerifierRBAC_CanReadAgentAPISecrets(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/x402.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	docs := multiDoc(data)

	role := findDocByName(docs, "ClusterRole", "x402-verifier")
	if role == nil {
		t.Fatal("no ClusterRole 'x402-verifier' found")
	}
	rules, ok := role["rules"].([]any)
	if !ok {
		t.Fatal("x402-verifier ClusterRole has no rules")
	}

	for _, r := range rules {
		rm := r.(map[string]any)
		if !stringSet(rm["apiGroups"])[""] || !stringSet(rm["resources"])["secrets"] {
			continue
		}
		verbs := stringSet(rm["verbs"])
		if !verbs["get"] || !verbs["list"] || !verbs["watch"] {
			continue
		}
		names := stringSet(rm["resourceNames"])
		if !names["litellm-secrets"] {
			t.Fatal("x402-verifier secret rule lost litellm-secrets")
		}
		if !names["hermes-api-server"] {
			t.Fatal("x402-verifier secret rule must include hermes-api-server for agent upstream auth")
		}
		return
	}

	t.Fatal("x402-verifier ClusterRole missing scoped secret get/list/watch rule")
}

// TestX402VerifierImage_CarriesAgentAuthFix: the pin VALUE is maintained by
// the repin automation (.github/scripts/repin-x402-images.sh); what this test
// guards is that whatever commit is pinned still descends from abfd55a (the
// agent upstream auth fix). Bumping the pin backward past it fails here.
func TestX402VerifierImage_CarriesAgentAuthFix(t *testing.T) {
	tag, _ := extractEmbeddedImagePin(t, "base/templates/x402.yaml", "ghcr.io/obolnetwork/x402-verifier")
	requireGitAncestor(t, "abfd55a", tag)
}

// TestServiceOfferControllerImage_CarriesSecretCreateOnlyFix is the tripwire
// for the per-agent provisioning 403 (GA blocker). The controller ClusterRole
// in x402.yaml grants no secrets update/patch verb because the reconciler
// treats Secret as create-only (isCreateOnlyKind). The image MUST therefore be
// built from source that has that behaviour — the prior f5d94fc side-branch pin
// did not, so the deployed binary Updated the per-agent Secrets on re-reconcile
// and 403'd. b39bcaa (post-rc10 main) carries the fix.
// The pin value itself is maintained by the repin automation; this test
// asserts ancestry so a future downgrade can't silently re-ship the bug.
func TestServiceOfferControllerImage_CarriesSecretCreateOnlyFix(t *testing.T) {
	tag, _ := extractEmbeddedImagePin(t, "base/templates/x402.yaml", "ghcr.io/obolnetwork/serviceoffer-controller")
	requireGitAncestor(t, "b39bcaa", tag)
}

// TestServiceOfferControllerSecretRBAC_Scoped guards the controller's Secret
// access against the actual code paths in internal/serviceoffercontroller.
// The reconciler touches exactly three Secrets by name (litellm-secrets,
// hermes-api-server, remote-signer-keystore). Disclosure/mutation verbs
// (get/list/watch/update/patch/delete) must therefore be resourceName-scoped
// so a compromised controller cannot read or rewrite arbitrary cluster
// Secrets. Only `create` may be unscoped — resourceNames cannot scope create
// and agent namespaces are minted dynamically, but create is integrity-only
// (it cannot read existing Secret contents).
func TestServiceOfferControllerSecretRBAC_Scoped(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/x402.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	docs := multiDoc(data)

	role := findDocByName(docs, "ClusterRole", "serviceoffer-controller")
	if role == nil {
		t.Fatal("no ClusterRole 'serviceoffer-controller' found")
	}
	rules, ok := role["rules"].([]any)
	if !ok {
		t.Fatal("serviceoffer-controller ClusterRole has no rules")
	}

	// Any verb that can read or mutate an existing Secret. `create` is
	// deliberately excluded — it cannot be resourceName-scoped.
	scopableVerbs := map[string]bool{
		"get": true, "list": true, "watch": true,
		"update": true, "patch": true, "delete": true,
	}
	allowedNames := map[string]bool{
		"litellm-secrets":        true,
		"hermes-api-server":      true,
		"remote-signer-keystore": true,
	}

	readNames := map[string]bool{}
	deleteNames := map[string]bool{}
	var sawCreate bool
	for _, r := range rules {
		rm := r.(map[string]any)
		if !stringSet(rm["apiGroups"])[""] || !stringSet(rm["resources"])["secrets"] {
			continue
		}
		verbs := stringSet(rm["verbs"])
		names := stringSet(rm["resourceNames"])

		if len(names) == 0 {
			// Unscoped secrets rule: create is the only acceptable verb.
			for v := range verbs {
				if scopableVerbs[v] {
					t.Errorf("serviceoffer-controller grants cluster-wide secrets:%q with no resourceNames — read/mutate surface must be scoped to the three named secrets", v)
				}
			}
			if verbs["create"] {
				sawCreate = true
			}
			continue
		}

		// Scoped rule: every name must be one the reconciler actually uses.
		for n := range names {
			if !allowedNames[n] {
				t.Errorf("serviceoffer-controller secrets rule references unexpected resourceName %q (reconciler only touches litellm-secrets, hermes-api-server, remote-signer-keystore)", n)
			}
			if verbs["get"] {
				readNames[n] = true
			}
			if verbs["delete"] {
				deleteNames[n] = true
			}
		}
		if verbs["list"] || verbs["watch"] || verbs["update"] || verbs["patch"] {
			t.Error("serviceoffer-controller scoped secrets rule must not grant list/watch/update/patch — Secrets are create-only in the reconciler and all reads are by name")
		}
		if names["litellm-secrets"] && verbs["delete"] {
			t.Error("serviceoffer-controller must not grant secrets:delete on litellm-secrets; the code only reads LITELLM_MASTER_KEY")
		}
	}

	for _, name := range []string{"litellm-secrets", "hermes-api-server", "remote-signer-keystore"} {
		if !readNames[name] {
			t.Errorf("serviceoffer-controller must retain resourceName-scoped secrets:get on %s", name)
		}
	}
	for _, name := range []string{"hermes-api-server", "remote-signer-keystore"} {
		if !deleteNames[name] {
			// tearDownAgent (agent.go) deletes these on Agent CR deletion.
			// Without delete the teardown 403s, the finalizer never clears,
			// and the CR is stranded in Terminating.
			t.Errorf("serviceoffer-controller must grant resourceName-scoped secrets:delete on %s for agent teardown", name)
		}
	}
	if !sawCreate {
		t.Error("serviceoffer-controller must retain secrets:create for minting the per-agent API token + wallet keystore in dynamic namespaces")
	}
}

func TestAgentRBAC_NoOverlyBroadPermissions(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-agent-monetize-rbac.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	cases := []struct {
		name string
		docs []map[string]any
	}{
		{
			name: "base monetize RBAC",
			docs: multiDoc(data),
		},
	}

	for _, networkName := range []string{"ethereum", "aztec"} {
		data, err := ReadEmbeddedNetworkFile(networkName, "templates/agent-rbac.yaml")
		if err != nil {
			t.Fatalf("ReadEmbeddedNetworkFile(%s): %v", networkName, err)
		}

		cases = append(cases, struct {
			name string
			docs []map[string]any
		}{
			name: networkName + " agent RBAC",
			docs: multiDoc(data),
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := false

			for _, doc := range tc.docs {
				kind, _ := doc["kind"].(string)
				if kind != "Role" && kind != "ClusterRole" {
					continue
				}

				name, _ := nested(doc, "metadata", "name").(string)
				if name != "openclaw-monetize-read" && name != "openclaw-monetize-write" && name != "obol-agent-network-role" {
					continue
				}

				found = true
				assertAgentRBACRulesTight(t, name, doc)
			}

			if !found {
				t.Fatal("no agent RBAC Role or ClusterRole found")
			}
		})
	}
}

func assertAgentRBACRulesTight(t *testing.T, roleName string, role map[string]any) {
	t.Helper()

	rules, ok := role["rules"].([]any)
	if !ok || len(rules) == 0 {
		t.Fatalf("%s has no rules", roleName)
	}

	mutateVerbs := map[string]bool{
		"create":           true,
		"update":           true,
		"patch":            true,
		"delete":           true,
		"deletecollection": true,
	}

	for _, rule := range rules {
		rm, ok := rule.(map[string]any)
		if !ok {
			t.Fatalf("%s has malformed rule %T", roleName, rule)
		}

		groups := stringSet(rm["apiGroups"])
		resources := stringSet(rm["resources"])
		verbs := stringSet(rm["verbs"])

		if groups["*"] || resources["*"] || verbs["*"] {
			t.Errorf("%s grants wildcard RBAC rule: apiGroups=%v resources=%v verbs=%v", roleName, groups, resources, verbs)
		}

		hasMutate := false
		for verb := range verbs {
			if mutateVerbs[verb] {
				hasMutate = true
				break
			}
		}
		if !hasMutate {
			continue
		}

		for _, resource := range []string{"secrets", "configmaps"} {
			if groups[""] && resources[resource] {
				t.Errorf("%s grants write access to core/%s: verbs=%v", roleName, resource, verbs)
			}
		}

		for _, resource := range []string{"middlewares", "httproutes", "referencegrants"} {
			if (groups["traefik.io"] || groups["gateway.networking.k8s.io"]) && resources[resource] {
				t.Errorf("%s grants child-resource write access to %s: apiGroups=%v verbs=%v", roleName, resource, groups, verbs)
			}
		}
	}
}

func stringSet(v any) map[string]bool {
	out := make(map[string]bool)

	items, ok := v.([]any)
	if !ok {
		return out
	}

	for _, item := range items {
		s, ok := item.(string)
		if ok {
			out[s] = true
		}
	}

	return out
}

// collectAPIGroups extracts all unique apiGroup strings from a list of rules.
func collectAPIGroups(rules []any) map[string]bool {
	groups := make(map[string]bool)

	for _, r := range rules {
		rm := r.(map[string]any)

		gs, ok := rm["apiGroups"].([]any)
		if !ok {
			continue
		}

		for _, g := range gs {
			groups[g.(string)] = true
		}
	}

	return groups
}

// hasVerbOnResource checks if any rule grants the given verb on the given
// apiGroup + resource combination.
func hasVerbOnResource(rules []any, apiGroup, resource, verb string) bool {
	for _, r := range rules {
		rm := r.(map[string]any)

		gs, ok := rm["apiGroups"].([]any)
		if !ok {
			continue
		}

		groupMatch := false

		for _, g := range gs {
			if g.(string) == apiGroup {
				groupMatch = true
			}
		}

		if !groupMatch {
			continue
		}

		res, ok := rm["resources"].([]any)
		if !ok {
			continue
		}

		resMatch := false

		for _, rr := range res {
			if rr.(string) == resource {
				resMatch = true
			}
		}

		if !resMatch {
			continue
		}

		verbs, ok := rm["verbs"].([]any)
		if !ok {
			continue
		}

		for _, v := range verbs {
			if v.(string) == verb {
				return true
			}
		}
	}

	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Admission Policy tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAdmissionPolicy_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-agent-admission-policy.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	policy := findDoc(docs, "ValidatingAdmissionPolicy")
	if policy == nil {
		t.Fatal("no ValidatingAdmissionPolicy document found")
	}

	binding := findDoc(docs, "ValidatingAdmissionPolicyBinding")
	if binding == nil {
		t.Fatal("no ValidatingAdmissionPolicyBinding document found")
	}

	validations, ok := nested(policy, "spec", "validations").([]any)
	if !ok {
		t.Fatal("spec.validations missing or wrong type")
	}

	wantMessages := []string{
		"HTTPRoutes created by agent runtimes must reference traefik-gateway",
		"ForwardAuth middlewares must target x402-verifier.x402.svc",
		"Agent-created namespaces must be factory-owned agent-* namespaces",
		"Agent-created Secrets must be hermes-env or hermes-profile-seed inside agent-* namespaces",
		"Agent-created Agent CRs must be Hermes agents in their matching agent-* namespace",
	}
	if len(validations) != len(wantMessages) {
		t.Errorf("got %d validation rules, want %d", len(validations), len(wantMessages))
	}

	gotMessages := make(map[string]bool, len(validations))
	for _, validation := range validations {
		vm, ok := validation.(map[string]any)
		if !ok {
			t.Fatalf("validation has type %T, want map[string]any", validation)
		}
		message, ok := vm["message"].(string)
		if !ok {
			t.Fatalf("validation message has type %T, want string", vm["message"])
		}
		gotMessages[message] = true
	}
	for _, message := range wantMessages {
		if !gotMessages[message] {
			t.Errorf("missing validation message %q", message)
		}
	}

	// Binding should reference openclaw-resource-guard with Deny action
	if pName := nested(binding, "spec", "policyName"); pName != "openclaw-resource-guard" {
		t.Errorf("binding.spec.policyName = %v, want openclaw-resource-guard", pName)
	}

	actions, ok := nested(binding, "spec", "validationActions").([]any)
	if !ok || len(actions) == 0 {
		t.Fatal("binding.spec.validationActions missing")
	}

	if actions[0] != "Deny" {
		t.Errorf("validationActions[0] = %v, want Deny", actions[0])
	}
}
