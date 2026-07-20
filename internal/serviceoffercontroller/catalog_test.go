package serviceoffercontroller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestComputeStaticSiteContentHashDeterministic(t *testing.T) {
	a := computeStaticSiteContentHash("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", nil)
	b := computeStaticSiteContentHash("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", nil)
	if a != b {
		t.Fatalf("hash not deterministic: %q vs %q", a, b)
	}
	if len(a) != 8 {
		t.Fatalf("hash length = %d, want 8", len(a))
	}

	changed := computeStaticSiteContentHash("# cat", `{"services":[{"name":"a"}]}`, `{"openapi":"3.1.0"}`, "<html></html>", nil)
	if changed == a {
		t.Fatal("expected different hash when catalog content changes")
	}
}

func TestStaticSiteContentMatches(t *testing.T) {
	cm := buildStaticSiteConfigMap("# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", nil)
	if !staticSiteContentMatches(cm, "# cat", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", nil) {
		t.Fatal("expected matching catalog content")
	}
	if staticSiteContentMatches(cm, "# changed", `{"services":[]}`, `{"openapi":"3.1.0"}`, "<html></html>", nil) {
		t.Fatal("expected different skill.md to not match")
	}
	if staticSiteContentMatches(nil, "# cat", `{}`, `{}`, "", nil) {
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

// TestStaticSiteStaleChatWidgetTriggersUpdate pins the upgrade path: a
// deployed ConfigMap whose chat widget differs from the binary's embedded
// copy must NOT match, otherwise the skip-when-unchanged fast path pins the
// old asset across controller upgrades forever. (Per-offer chat pages flow
// through the offer bundles, which the match already covers.)
func TestStaticSiteStaleChatWidgetTriggersUpdate(t *testing.T) {
	cm := buildStaticSiteConfigMap("# cat", `{}`, `{}`, "<html></html>", nil)
	if !staticSiteContentMatches(cm, "# cat", `{}`, `{}`, "<html></html>", nil) {
		t.Fatalf("fresh ConfigMap should match its own inputs")
	}
	if err := unstructured.SetNestedField(cm.Object, "stale vendor", "data", "chat-vendor.js"); err != nil {
		t.Fatal(err)
	}
	if staticSiteContentMatches(cm, "# cat", `{}`, `{}`, "<html></html>", nil) {
		t.Fatalf("stale chat-vendor.js must trigger a ConfigMap update")
	}
}
