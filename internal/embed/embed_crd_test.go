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

// findDocByName returns the first document matching kind and metadata.name.
func findDocByName(docs []map[string]interface{}, kind, name string) map[string]interface{} {
	for _, d := range docs {
		if d["kind"] == kind && nested(d, "metadata", "name") == name {
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

	// Required fields in spec (aligned with x402/ERC-8004 schema)
	for _, field := range []string{"type", "model", "upstream", "payment", "path", "registration"} {
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

	expected := []string{"Type", "Model", "Price", "Network", "Ready", "Age"}
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
	// Wallet validation is now at spec.payment.properties.payTo (aligned with x402)
	payToProp := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties",
		"payment", "properties", "payTo")
	wm, ok := payToProp.(map[string]interface{})
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

// ─────────────────────────────────────────────────────────────────────────────
// Monetize RBAC tests
// ─────────────────────────────────────────────────────────────────────────────

func TestMonetizeRBAC_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/obol-agent-monetize-rbac.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	docs := multiDoc(data)

	// ── Read ClusterRole ────────────────────────────────────────────────
	readCR := findDocByName(docs, "ClusterRole", "openclaw-monetize-read")
	if readCR == nil {
		t.Fatal("no ClusterRole 'openclaw-monetize-read' found")
	}

	readRules, ok := readCR["rules"].([]interface{})
	if !ok || len(readRules) == 0 {
		t.Fatal("read ClusterRole has no rules")
	}

	// Read role should be read-only: no create/update/patch/delete verbs.
	for _, r := range readRules {
		rm := r.(map[string]interface{})
		verbs, ok := rm["verbs"].([]interface{})
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

	// ── Workload ClusterRole ────────────────────────────────────────────
	workloadCR := findDocByName(docs, "ClusterRole", "openclaw-monetize-workload")
	if workloadCR == nil {
		t.Fatal("no ClusterRole 'openclaw-monetize-workload' found")
	}

	workloadRules, ok := workloadCR["rules"].([]interface{})
	if !ok || len(workloadRules) == 0 {
		t.Fatal("workload ClusterRole has no rules")
	}

	// Workload role should have mutate verbs and cover all agent-managed apiGroups.
	workloadGroups := collectAPIGroups(workloadRules)
	for _, want := range []string{"obol.org", "traefik.io", "gateway.networking.k8s.io", "", "apps"} {
		if !workloadGroups[want] {
			t.Errorf("workload ClusterRole missing apiGroup %q", want)
		}
	}

	// Workload: apps/deployments should have create (for registration httpd).
	if !hasVerbOnResource(workloadRules, "apps", "deployments", "create") {
		t.Error("workload ClusterRole missing 'create' on apps/deployments")
	}

	// Workload: configmaps should have create (for registration JSON).
	if !hasVerbOnResource(workloadRules, "", "configmaps", "create") {
		t.Error("workload ClusterRole missing 'create' on configmaps")
	}

	// ── ClusterRoleBindings ─────────────────────────────────────────────
	readCRB := findDocByName(docs, "ClusterRoleBinding", "openclaw-monetize-read-binding")
	if readCRB == nil {
		t.Fatal("no ClusterRoleBinding 'openclaw-monetize-read-binding' found")
	}
	if ref := nested(readCRB, "roleRef", "name"); ref != "openclaw-monetize-read" {
		t.Errorf("read binding roleRef.name = %v, want openclaw-monetize-read", ref)
	}

	workloadCRB := findDocByName(docs, "ClusterRoleBinding", "openclaw-monetize-workload-binding")
	if workloadCRB == nil {
		t.Fatal("no ClusterRoleBinding 'openclaw-monetize-workload-binding' found")
	}
	if ref := nested(workloadCRB, "roleRef", "name"); ref != "openclaw-monetize-workload" {
		t.Errorf("workload binding roleRef.name = %v, want openclaw-monetize-workload", ref)
	}

	// ── x402 namespace Role + RoleBinding ───────────────────────────────
	x402Role := findDocByName(docs, "Role", "openclaw-x402-pricing")
	if x402Role == nil {
		t.Fatal("no Role 'openclaw-x402-pricing' found")
	}
	if ns := nested(x402Role, "metadata", "namespace"); ns != "x402" {
		t.Errorf("x402 Role namespace = %v, want x402", ns)
	}

	// x402 Role should be scoped to x402-pricing ConfigMap only.
	x402Rules, ok := x402Role["rules"].([]interface{})
	if !ok || len(x402Rules) != 1 {
		t.Fatalf("x402 Role should have exactly 1 rule, got %d", len(x402Rules))
	}
	rm := x402Rules[0].(map[string]interface{})
	resNames, ok := rm["resourceNames"].([]interface{})
	if !ok || len(resNames) != 1 || resNames[0] != "x402-pricing" {
		t.Errorf("x402 Role should be scoped to resourceNames: [x402-pricing], got %v", resNames)
	}

	x402RB := findDocByName(docs, "RoleBinding", "openclaw-x402-pricing-binding")
	if x402RB == nil {
		t.Fatal("no RoleBinding 'openclaw-x402-pricing-binding' found")
	}
	if ns := nested(x402RB, "metadata", "namespace"); ns != "x402" {
		t.Errorf("x402 RoleBinding namespace = %v, want x402", ns)
	}
	if ref := nested(x402RB, "roleRef", "name"); ref != "openclaw-x402-pricing" {
		t.Errorf("x402 RoleBinding roleRef.name = %v, want openclaw-x402-pricing", ref)
	}
}

// collectAPIGroups extracts all unique apiGroup strings from a list of rules.
func collectAPIGroups(rules []interface{}) map[string]bool {
	groups := make(map[string]bool)
	for _, r := range rules {
		rm := r.(map[string]interface{})
		gs, ok := rm["apiGroups"].([]interface{})
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
func hasVerbOnResource(rules []interface{}, apiGroup, resource, verb string) bool {
	for _, r := range rules {
		rm := r.(map[string]interface{})
		gs, ok := rm["apiGroups"].([]interface{})
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
		res, ok := rm["resources"].([]interface{})
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
		verbs, ok := rm["verbs"].([]interface{})
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
