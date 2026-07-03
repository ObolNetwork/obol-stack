package serviceoffercontroller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestComputeSkillCatalogContentHashDeterministic(t *testing.T) {
	a := computeSkillCatalogContentHash("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>")
	b := computeSkillCatalogContentHash("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>")
	if a != b {
		t.Fatalf("hash not deterministic: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Fatalf("hash length = %d, want 8", len(a))
	}

	changed := computeSkillCatalogContentHash("# cat", `{"services":[{"name":"a"}]}`, `{"openapi":"3.1.0"}`, "<html></html>")
	if changed == a {
		t.Fatal("expected different hash when catalog content changes")
	}
}

func TestSkillCatalogContentMatches(t *testing.T) {
	cm := buildSkillCatalogConfigMap("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>")
	if !skillCatalogContentMatches(cm, "# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>") {
		t.Fatal("expected matching catalog content")
	}
	if skillCatalogContentMatches(cm, "# changed", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>") {
		t.Fatal("expected different skill.md to not match")
	}
	if skillCatalogContentMatches(nil, "# cat", `{}`, `{}`, "") {
		t.Fatal("nil configmap must not match")
	}
}

func TestSkillCatalogDeployedContentHash(t *testing.T) {
	deployment := buildSkillCatalogDeployment("abc12345")
	if got := skillCatalogDeployedContentHash(deployment); got != "abc12345" {
		t.Fatalf("hash = %q, want abc12345", got)
	}
	if got := skillCatalogDeployedContentHash(nil); got != "" {
		t.Fatalf("nil deployment hash = %q, want empty", got)
	}

	empty := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{}}}
	if got := skillCatalogDeployedContentHash(empty); got != "" {
		t.Fatalf("missing annotation hash = %q, want empty", got)
	}
}
