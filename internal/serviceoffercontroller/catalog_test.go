package serviceoffercontroller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestComputeStaticSiteContentHashDeterministic(t *testing.T) {
	a := computeStaticSiteContentHash("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", `{"resources":[]}`, nil)
	b := computeStaticSiteContentHash("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", `{"resources":[]}`, nil)
	if a != b {
		t.Fatalf("hash not deterministic: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Fatalf("hash length = %d, want 8", len(a))
	}

	changed := computeStaticSiteContentHash("# cat", `{"services":[{"name":"a"}]}`, `{"openapi":"3.1.0"}`, "<html></html>", `{"resources":[]}`, nil)
	if changed == a {
		t.Fatal("expected different hash when catalog content changes")
	}
}

func TestStaticSiteContentMatches(t *testing.T) {
	cm := buildStaticSiteConfigMap("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", `{"resources":[]}`, nil)
	if !staticSiteContentMatches(cm, "# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", `{"resources":[]}`, nil) {
		t.Fatal("expected matching catalog content")
	}
	if staticSiteContentMatches(cm, "# changed", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", `{"resources":[]}`, nil) {
		t.Fatal("expected different skill.md to not match")
	}
	if staticSiteContentMatches(nil, "# cat", `{}`, `{}`, "", "", nil) {
		t.Fatal("nil configmap must not match")
	}
}

func TestStaticSiteDeployedContentHash(t *testing.T) {
	deployment := buildStaticSiteDeployment("abc12345", nil)
	if got := staticSiteDeployedContentHash(deployment); got != "abc12345" {
		t.Fatalf("hash = %q, want abc12345", got)
	}
	if got := staticSiteDeployedContentHash(nil); got != "" {
		t.Fatalf("nil deployment hash = %q, want empty", got)
	}

	empty := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{}}}
	if got := staticSiteDeployedContentHash(empty); got != "" {
		t.Fatalf("missing annotation hash = %q, want empty", got)
	}
}
