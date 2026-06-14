package embed

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// The ServiceBounty CRD manifest is hand-written (no controller-gen run), so
// every new Go field needs a matching schema property and vice versa. This
// test walks both directions: a Go json tag without a CRD property means
// kubectl silently strips the field on apply (structural-schema pruning); a
// CRD property without a Go field means stale schema the controller can never
// reconcile. spec.eval.mode was added by hand in two places — this makes that
// class of drift impossible.

// leafTypes are struct types serialized as scalars in the CRD schema.
var leafTypes = map[string]bool{
	"v1.Time":     true,
	"v1.Duration": true,
}

// collectGoPaths walks a struct type and records every reachable json path.
// Arrays descend through "[]"; maps are leaves (additionalProperties).
func collectGoPaths(t reflect.Type, prefix string, out map[string]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || leafTypes[t.String()] {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		path := prefix + tag
		out[path] = true

		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			collectGoPaths(ft, path+".", out)
		case reflect.Slice:
			el := ft.Elem()
			if el.Kind() == reflect.Struct && !leafTypes[el.String()] {
				collectGoPaths(el, path+"[].", out)
			}
		}
	}
}

// collectSchemaPaths walks an openAPIV3Schema properties tree.
func collectSchemaPaths(schema map[string]any, prefix string, out map[string]bool) {
	props, _ := schema["properties"].(map[string]any)
	for name, raw := range props {
		path := prefix + name
		out[path] = true
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if items, ok := node["items"].(map[string]any); ok {
			collectSchemaPaths(items, path+"[].", out)
			continue
		}
		collectSchemaPaths(node, path+".", out)
	}
}

func loadBountySchema(t *testing.T) map[string]any {
	t.Helper()
	return loadCRDSchema(t, "base/templates/servicebounty-crd.yaml")
}

func loadCRDSchema(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := ReadInfrastructureFile(path)
	if err != nil {
		t.Fatalf("ReadInfrastructureFile: %v", err)
	}
	var crd map[string]any
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("parse CRD: %v", err)
	}
	versions, _ := nested(crd, "spec", "versions").([]any)
	if len(versions) == 0 {
		t.Fatal("CRD has no versions")
	}
	v0, _ := versions[0].(map[string]any)
	schema, _ := nested(v0, "schema", "openAPIV3Schema").(map[string]any)
	if schema == nil {
		t.Fatal("CRD has no openAPIV3Schema")
	}
	return schema
}

func TestServiceBountyCRD_GoSchemaParity(t *testing.T) {
	assertCRDParity(t, loadBountySchema(t),
		reflect.TypeOf(monetizeapi.ServiceBountySpec{}),
		reflect.TypeOf(monetizeapi.ServiceBountyStatus{}))
}

// The EvaluatorEnrollment CRD is hand-written too — same drift class, same
// bidirectional pin.
func TestEvaluatorEnrollmentCRD_GoSchemaParity(t *testing.T) {
	assertCRDParity(t, loadCRDSchema(t, "base/templates/evaluatorenrollment-crd.yaml"),
		reflect.TypeOf(monetizeapi.EvaluatorEnrollmentSpec{}),
		reflect.TypeOf(monetizeapi.EvaluatorEnrollmentStatus{}))
}

func assertCRDParity(t *testing.T, schema map[string]any, specType, statusType reflect.Type) {
	t.Helper()
	for _, section := range []struct {
		name   string
		goType reflect.Type
	}{
		{"spec", specType},
		{"status", statusType},
	} {
		sectionSchema, _ := nested(schema, "properties", section.name).(map[string]any)
		if sectionSchema == nil {
			t.Fatalf("CRD schema missing .%s", section.name)
		}

		goPaths := map[string]bool{}
		collectGoPaths(section.goType, "", goPaths)
		schemaPaths := map[string]bool{}
		collectSchemaPaths(sectionSchema, "", schemaPaths)

		var missing, stale []string
		for p := range goPaths {
			if !schemaPaths[p] {
				missing = append(missing, p)
			}
		}
		for p := range schemaPaths {
			if !goPaths[p] {
				stale = append(stale, p)
			}
		}
		sort.Strings(missing)
		sort.Strings(stale)

		for _, p := range missing {
			t.Errorf("%s.%s exists in Go but not in the CRD schema — kubectl apply would silently prune it", section.name, p)
		}
		for _, p := range stale {
			t.Errorf("%s.%s exists in the CRD schema but not in Go — stale property the controller can never reconcile", section.name, p)
		}
	}
}

// TestServiceBountyCRD_EvalModeEnum pins the verification-gate enum: required
// must stay the default and dangerouslySkipped the only opt-out.
func TestServiceBountyCRD_EvalModeEnum(t *testing.T) {
	schema := loadBountySchema(t)
	mode, _ := nested(schema, "properties", "spec", "properties", "eval", "properties", "mode").(map[string]any)
	if mode == nil {
		t.Fatal("spec.eval.mode missing from CRD schema")
	}
	if d, _ := mode["default"].(string); d != monetizeapi.EvalModeRequired {
		t.Errorf("spec.eval.mode default = %q, want %q (verification is on by default)", d, monetizeapi.EvalModeRequired)
	}
	enum, _ := mode["enum"].([]any)
	got := fmt.Sprintf("%v", enum)
	want := fmt.Sprintf("%v", []any{monetizeapi.EvalModeRequired, monetizeapi.EvalModeDangerouslySkipped})
	if got != want {
		t.Errorf("spec.eval.mode enum = %s, want %s", got, want)
	}
}
