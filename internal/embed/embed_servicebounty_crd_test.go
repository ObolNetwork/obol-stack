package embed

import "testing"

// ─────────────────────────────────────────────────────────────────────────────
// ServiceBounty CRD tests
// ─────────────────────────────────────────────────────────────────────────────

func TestServiceBountyCRD_Parses(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/servicebounty-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	crd := findDoc(multiDoc(data), "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CustomResourceDefinition document found")
	}

	if got := nested(crd, "metadata", "name"); got != "servicebounties.obol.org" {
		t.Errorf("metadata.name = %v, want servicebounties.obol.org", got)
	}
	if got := nested(crd, "spec", "group"); got != "obol.org" {
		t.Errorf("spec.group = %v, want obol.org", got)
	}
	if got := nested(crd, "spec", "names", "kind"); got != "ServiceBounty" {
		t.Errorf("spec.names.kind = %v, want ServiceBounty", got)
	}
	if got := nested(crd, "spec", "scope"); got != "Namespaced" {
		t.Errorf("spec.scope = %v, want Namespaced", got)
	}

	short, _ := nested(crd, "spec", "names", "shortNames").([]any)
	found := false
	for _, s := range short {
		if s == "sb" {
			found = true
		}
	}
	if !found {
		t.Errorf("shortNames = %v, want it to include sb", short)
	}
}

func TestServiceBountyCRD_KeyFields(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/servicebounty-crd.yaml")
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}

	crd := findDoc(multiDoc(data), "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("no CRD doc")
	}

	versions, ok := nested(crd, "spec", "versions").([]any)
	if !ok || len(versions) == 0 {
		t.Fatal("spec.versions missing")
	}
	v0, _ := versions[0].(map[string]any)

	// status subresource present (the controller patches status).
	if nested(v0, "subresources", "status") == nil {
		t.Error("v1alpha1 missing status subresource")
	}

	specProps := nested(v0, "schema", "openAPIV3Schema", "properties", "spec", "properties")
	sp, ok := specProps.(map[string]any)
	if !ok {
		t.Fatal("spec.properties not an object")
	}

	// spec.task.typeRef is the modular task-type anchor.
	if nested(sp, "task", "properties", "typeRef") == nil {
		t.Error("spec.task.typeRef missing — task-type modularity anchor")
	}

	// hardwareProof enum present.
	hw, _ := nested(sp, "task", "properties", "hardwareProof", "enum").([]any)
	if len(hw) == 0 {
		t.Error("spec.task.hardwareProof enum missing")
	}

	// escrow scheme enum includes the live + future rails.
	scheme, _ := nested(sp, "reward", "properties", "escrow", "properties", "scheme", "enum").([]any)
	var hasUpto bool
	for _, s := range scheme {
		if s == "upto" {
			hasUpto = true
		}
	}
	if !hasUpto {
		t.Errorf("reward.escrow.scheme enum = %v, want it to include upto", scheme)
	}

	// reward carries the payment envelope needed to construct the upto auth:
	// the chain it settles on and the poster's refund address.
	for _, f := range []string{"network", "payTo"} {
		if nested(sp, "reward", "properties", f) == nil {
			t.Errorf("spec.reward.%s missing — required to build the escrow authorization", f)
		}
	}
}
