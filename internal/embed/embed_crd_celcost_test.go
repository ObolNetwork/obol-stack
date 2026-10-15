package embed

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// embeddedCRDs returns every CustomResourceDefinition document shipped in the
// infrastructure and networks embeds, keyed by "<file>#<crd name>".
func embeddedCRDs(t *testing.T) map[string]map[string]any {
	t.Helper()

	out := map[string]map[string]any{}

	for root, fsys := range map[string]fs.FS{"infrastructure": infrastructureFS, "networks": networksFS} {
		err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() || (path.Ext(p) != ".yaml" && path.Ext(p) != ".yml") {
				return nil
			}

			raw, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}

			if !strings.Contains(string(raw), "kind: CustomResourceDefinition") {
				return nil
			}

			for _, doc := range multiDoc(raw) {
				if doc["kind"] != "CustomResourceDefinition" {
					continue
				}

				meta, _ := doc["metadata"].(map[string]any)
				out[fmt.Sprintf("%s#%v", p, meta["name"])] = doc
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	if len(out) == 0 {
		t.Fatal("no embedded CRDs found")
	}

	return out
}

// schemaHasCEL reports whether s or any schema nested under it carries
// x-kubernetes-validations.
func schemaHasCEL(s map[string]any) bool {
	if s == nil {
		return false
	}

	if rules, _ := s["x-kubernetes-validations"].([]any); len(rules) > 0 {
		return true
	}

	if props, ok := s["properties"].(map[string]any); ok {
		for _, v := range props {
			if child, ok := v.(map[string]any); ok && schemaHasCEL(child) {
				return true
			}
		}
	}

	if items, ok := s["items"].(map[string]any); ok && schemaHasCEL(items) {
		return true
	}

	if ap, ok := s["additionalProperties"].(map[string]any); ok && schemaHasCEL(ap) {
		return true
	}

	return false
}

// checkCELBounds walks an OpenAPI v3 schema and records every node whose
// size the apiserver's CEL cost estimator cannot bound. An unbounded list or
// map multiplies the cost of every rule below it by the worst case the
// request size allows, which is how the ServiceOffer CRD came to exceed the
// per-rule and per-CRD budgets on a live apiserver while every offline test
// passed.
func checkCELBounds(s map[string]any, at string, problems *[]string) {
	if s == nil {
		return
	}

	hasOwnRules := false
	if rules, _ := s["x-kubernetes-validations"].([]any); len(rules) > 0 {
		hasOwnRules = true
	}

	switch s["type"] {
	case "array":
		if schemaHasCEL(s) {
			if _, ok := s["maxItems"]; !ok {
				*problems = append(*problems, at+": array with CEL rules (on itself or its items) has no maxItems")
			}
		}
	case "string":
		if hasOwnRules {
			if _, ok := s["maxLength"]; !ok {
				*problems = append(*problems, at+": string with CEL rules has no maxLength")
			}
		}
	case "object":
		if ap, ok := s["additionalProperties"].(map[string]any); ok && (hasOwnRules || schemaHasCEL(ap)) {
			if _, ok := s["maxProperties"]; !ok {
				*problems = append(*problems, at+": map with CEL rules (on itself or its values) has no maxProperties")
			}
		}
	}

	if props, ok := s["properties"].(map[string]any); ok {
		for k, v := range props {
			if child, ok := v.(map[string]any); ok {
				checkCELBounds(child, at+"."+k, problems)
			}
		}
	}

	if items, ok := s["items"].(map[string]any); ok {
		checkCELBounds(items, at+"[]", problems)
	}

	if ap, ok := s["additionalProperties"].(map[string]any); ok {
		checkCELBounds(ap, at+"{}", problems)
	}
}

// TestEmbeddedCRDs_CELRulesAreCostBounded is the offline guard for the
// apiserver's CEL cost budget: every list/map that contains a CEL rule at any
// depth must declare maxItems/maxProperties, and every string a rule runs on
// must declare maxLength. It does not compute the cost itself; CI's
// crd-apply job dry-run-applies the CRDs to a real kube-apiserver for that.
func TestEmbeddedCRDs_CELRulesAreCostBounded(t *testing.T) {
	crds := embeddedCRDs(t)

	keys := make([]string, 0, len(crds))
	for k := range crds {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, key := range keys {
		spec, _ := crds[key]["spec"].(map[string]any)
		versions, _ := spec["versions"].([]any)

		for _, v := range versions {
			ver, _ := v.(map[string]any)
			schema, _ := ver["schema"].(map[string]any)
			root, _ := schema["openAPIV3Schema"].(map[string]any)

			var problems []string
			checkCELBounds(root, fmt.Sprintf("%v", ver["name"]), &problems)

			sort.Strings(problems)

			for _, p := range problems {
				t.Errorf("%s: %s", key, p)
			}
		}
	}
}

// TestServiceOfferListCapsMatchCRD keeps the CLI-side caps in
// internal/monetizeapi in step with the kubebuilder MaxItems markers that
// generate the CRD.
func TestServiceOfferListCapsMatchCRD(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/serviceoffer-crd.yaml")
	if err != nil {
		t.Fatalf("read serviceoffer CRD: %v", err)
	}

	crd := findDoc(multiDoc(data), "CustomResourceDefinition")
	if crd == nil {
		t.Fatal("serviceoffer CRD document not found")
	}

	spec, _ := crd["spec"].(map[string]any)
	versions, _ := spec["versions"].([]any)

	if len(versions) == 0 {
		t.Fatal("serviceoffer CRD has no versions")
	}

	for _, v := range versions {
		ver, _ := v.(map[string]any)
		schema, _ := ver["schema"].(map[string]any)
		root, _ := schema["openAPIV3Schema"].(map[string]any)
		props, _ := root["properties"].(map[string]any)
		specSchema, _ := props["spec"].(map[string]any)
		specProps, _ := specSchema["properties"].(map[string]any)

		for field, want := range map[string]int{
			"payments": monetizeapi.MaxServiceOfferPayments,
			"routes":   monetizeapi.MaxServiceOfferRoutes,
		} {
			fs, _ := specProps[field].(map[string]any)
			if got, _ := fs["maxItems"].(int); got != want {
				t.Errorf("version %v: spec.%s maxItems = %v, want %d (monetizeapi constant)", ver["name"], field, fs["maxItems"], want)
			}
		}
	}
}
