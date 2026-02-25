package embed

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// multiDoc splits a YAML file that may start with a Helm conditional
// (e.g. {{- if ... }}) into individual YAML documents, stripping
// Helm template directives and blank documents.
func multiDoc(raw []byte) []map[string]interface{} {
	// Strip Helm template lines ({{- ... }}).
	var cleaned []string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{{") {
			continue
		}
		cleaned = append(cleaned, line)
	}

	docs := strings.Split(strings.Join(cleaned, "\n"), "\n---\n")
	var result []map[string]interface{}
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		var m map[string]interface{}
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
func findDoc(docs []map[string]interface{}, kind string) map[string]interface{} {
	for _, d := range docs {
		if d["kind"] == kind {
			return d
		}
	}
	return nil
}

// nested traverses a map[string]interface{} by dot-separated keys.
func nested(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		cm, ok := cur.(map[string]interface{})
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
	if name != "serviceoffers.obol.network" {
		t.Errorf("metadata.name = %v, want serviceoffers.obol.network", name)
	}

	group := nested(crd, "spec", "group")
	if group != "obol.network" {
		t.Errorf("spec.group = %v, want obol.network", group)
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
	versions, ok := nested(crd, "spec", "versions").([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatal("spec.versions is empty or wrong type")
	}
	v0, ok := versions[0].(map[string]interface{})
	if !ok {
		t.Fatal("versions[0] is not a map")
	}

	specProps := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties")
	pm, ok := specProps.(map[string]interface{})
	if !ok {
		t.Fatalf("spec.properties is not a map: %T", specProps)
	}

	// Required fields in spec
	for _, field := range []string{"model", "upstream", "pricing", "wallet", "path", "register"} {
		if _, exists := pm[field]; !exists {
			t.Errorf("spec.properties missing field %q", field)
		}
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

	versions, ok := nested(crd, "spec", "versions").([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatal("no versions")
	}
	v0 := versions[0].(map[string]interface{})

	cols, ok := v0["additionalPrinterColumns"].([]interface{})
	if !ok {
		t.Fatal("additionalPrinterColumns missing or wrong type")
	}

	expected := []string{"Model", "Price", "Ready", "Age"}
	if len(cols) != len(expected) {
		t.Fatalf("got %d printer columns, want %d", len(cols), len(expected))
	}

	for i, want := range expected {
		col := cols[i].(map[string]interface{})
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

	versions := nested(crd, "spec", "versions").([]interface{})
	v0 := versions[0].(map[string]interface{})
	walletProp := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties", "wallet")
	wm, ok := walletProp.(map[string]interface{})
	if !ok {
		t.Fatal("wallet property not a map")
	}

	pattern, ok := wm["pattern"].(string)
	if !ok {
		t.Fatal("wallet.pattern missing")
	}
	if pattern != "^0x[0-9a-fA-F]{40}$" {
		t.Errorf("wallet.pattern = %q, want ^0x[0-9a-fA-F]{40}$", pattern)
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

	// Should have ClusterRole + ClusterRoleBinding
	cr := findDoc(docs, "ClusterRole")
	if cr == nil {
		t.Fatal("no ClusterRole document found")
	}

	crb := findDoc(docs, "ClusterRoleBinding")
	if crb == nil {
		t.Fatal("no ClusterRoleBinding document found")
	}

	// ClusterRole name
	if name := nested(cr, "metadata", "name"); name != "openclaw-monetize" {
		t.Errorf("ClusterRole name = %v, want openclaw-monetize", name)
	}

	// ClusterRole should have rules for obol.network, traefik.io, gateway.networking.k8s.io
	rules, ok := cr["rules"].([]interface{})
	if !ok || len(rules) == 0 {
		t.Fatal("ClusterRole has no rules")
	}

	apiGroups := make(map[string]bool)
	for _, r := range rules {
		rm := r.(map[string]interface{})
		groups, ok := rm["apiGroups"].([]interface{})
		if !ok {
			continue
		}
		for _, g := range groups {
			apiGroups[g.(string)] = true
		}
	}

	for _, want := range []string{"obol.network", "traefik.io", "gateway.networking.k8s.io"} {
		if !apiGroups[want] {
			t.Errorf("ClusterRole missing apiGroup %q", want)
		}
	}

	// ClusterRoleBinding should reference openclaw-monetize
	roleRef := nested(crb, "roleRef", "name")
	if roleRef != "openclaw-monetize" {
		t.Errorf("ClusterRoleBinding roleRef.name = %v, want openclaw-monetize", roleRef)
	}
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

	// Policy should have 2 validation rules
	validations, ok := nested(policy, "spec", "validations").([]interface{})
	if !ok {
		t.Fatal("spec.validations missing or wrong type")
	}
	if len(validations) != 2 {
		t.Errorf("got %d validation rules, want 2", len(validations))
	}

	// Binding should reference openclaw-resource-guard with Deny action
	if pName := nested(binding, "spec", "policyName"); pName != "openclaw-resource-guard" {
		t.Errorf("binding.spec.policyName = %v, want openclaw-resource-guard", pName)
	}

	actions, ok := nested(binding, "spec", "validationActions").([]interface{})
	if !ok || len(actions) == 0 {
		t.Fatal("binding.spec.validationActions missing")
	}
	if actions[0] != "Deny" {
		t.Errorf("validationActions[0] = %v, want Deny", actions[0])
	}
}
