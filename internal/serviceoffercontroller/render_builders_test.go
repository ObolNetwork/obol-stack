package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// assertRestrictedPSS checks that a controller-rendered Deployment satisfies
// the Restricted Pod Security Standard. PR #521 enforces Restricted PSS on
// the x402 namespace, so any httpd workload missing these fields gets
// rejected at admission and never starts (Bug #3 from the 14-PR integration
// test campaign).
func assertRestrictedPSS(t *testing.T, deploymentName string, spec map[string]any) {
	t.Helper()
	template, _ := spec["template"].(map[string]any)
	podSpec, _ := template["spec"].(map[string]any)

	psc, ok := podSpec["securityContext"].(map[string]any)
	if !ok {
		t.Fatalf("%s: pod spec missing securityContext", deploymentName)
	}
	if v, _ := psc["runAsNonRoot"].(bool); !v {
		t.Errorf("%s: pod securityContext.runAsNonRoot = %v, want true", deploymentName, psc["runAsNonRoot"])
	}
	if v, _ := psc["runAsUser"].(int64); v == 0 {
		t.Errorf("%s: pod securityContext.runAsUser must be set to a non-zero UID", deploymentName)
	}
	if v, _ := psc["runAsGroup"].(int64); v == 0 {
		t.Errorf("%s: pod securityContext.runAsGroup must be set to a non-zero GID", deploymentName)
	}
	sp, ok := psc["seccompProfile"].(map[string]any)
	if !ok {
		t.Errorf("%s: pod securityContext missing seccompProfile", deploymentName)
	} else if t2, _ := sp["type"].(string); t2 != "RuntimeDefault" && t2 != "Localhost" {
		t.Errorf("%s: pod seccompProfile.type = %q, want RuntimeDefault or Localhost", deploymentName, t2)
	}

	containers, _ := podSpec["containers"].([]any)
	if len(containers) == 0 {
		t.Fatalf("%s: no containers in pod spec", deploymentName)
	}
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		name, _ := cm["name"].(string)
		csc, ok := cm["securityContext"].(map[string]any)
		if !ok {
			t.Errorf("%s/%s: container missing securityContext", deploymentName, name)
			continue
		}
		if v, _ := csc["allowPrivilegeEscalation"].(bool); v {
			t.Errorf("%s/%s: container allowPrivilegeEscalation = true, want false", deploymentName, name)
		}
		if _, present := csc["allowPrivilegeEscalation"]; !present {
			t.Errorf("%s/%s: container missing allowPrivilegeEscalation (must be false)", deploymentName, name)
		}
		caps, ok := csc["capabilities"].(map[string]any)
		if !ok {
			t.Errorf("%s/%s: container securityContext missing capabilities", deploymentName, name)
			continue
		}
		drop, _ := caps["drop"].([]any)
		var droppedAll bool
		for _, d := range drop {
			if s, _ := d.(string); s == "ALL" {
				droppedAll = true
			}
		}
		if !droppedAll {
			t.Errorf("%s/%s: container capabilities.drop must include \"ALL\", got %v", deploymentName, name, drop)
		}
	}
}

// TestBuildStaticSiteDeployment_RestrictedPSS verifies the skill-md
// httpd Deployment ships a Restricted-PSS-compliant securityContext.
// Regression test for the cross-PR interaction with #521 surfaced by
// the 14-PR integration test (Bug #3).
func TestBuildStaticSiteDeployment_RestrictedPSS(t *testing.T) {
	d := buildStaticSiteDeployment("hash-x", nil)
	spec, _ := d.Object["spec"].(map[string]any)
	assertRestrictedPSS(t, staticSiteConfigMapName, spec)
}

// TestBuildAgentIdentityRegistrationDeployment_RestrictedPSS verifies the
// agentidentity well-known/agent-registration.json publisher httpd
// Deployment ships a Restricted-PSS-compliant securityContext.
func TestBuildAgentIdentityRegistrationDeployment_RestrictedPSS(t *testing.T) {
	identity := &monetizeapi.AgentIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      monetizeapi.AgentIdentityDefaultName,
			Namespace: "x402",
			UID:       "test-uid",
		},
	}
	d := buildAgentIdentityRegistrationDeployment(identity, "hash-y")
	spec, _ := d.Object["spec"].(map[string]any)
	assertRestrictedPSS(t, agentIdentityRegistrationName(identity), spec)
}

// TestBuildStaticSiteConfigMap: exposes skill.md + services.json + openapi.json
// + api docs HTML + httpd conf.
func TestBuildStaticSiteConfigMap(t *testing.T) {
	cm := buildStaticSiteConfigMap("# Catalog", `{"displayName":"Acme","tagline":"t","logoUrl":"https://x/logo.png","services":[{"name":"a"}]}`, `{"openapi":"3.1.0"}`, "<html>shell</html>", nil)

	if cm.GetName() != staticSiteConfigMapName {
		t.Errorf("name = %q, want %q", cm.GetName(), staticSiteConfigMapName)
	}
	if cm.GetNamespace() != staticSiteNamespace {
		t.Errorf("namespace = %q, want %q", cm.GetNamespace(), staticSiteNamespace)
	}
	data, _ := cm.Object["data"].(map[string]any)
	if data["skill.md"] != "# Catalog" {
		t.Errorf("skill.md payload mismatch, got %v", data["skill.md"])
	}
	if !strings.Contains(data["services.json"].(string), `"displayName":"Acme"`) {
		t.Errorf("services.json payload mismatch, got %v", data["services.json"])
	}
	if data["openapi.json"] != `{"openapi":"3.1.0"}` {
		t.Errorf("openapi.json payload mismatch, got %v", data["openapi.json"])
	}
	if data["api.html"] != "<html>shell</html>" {
		t.Errorf("api.html payload mismatch, got %v", data["api.html"])
	}
	if conf, _ := data["httpd.conf"].(string); !strings.Contains(conf, ".md:text/markdown") || !strings.Contains(conf, ".json:application/json") || !strings.Contains(conf, ".html:text/html") {
		t.Errorf("httpd.conf missing required mime mappings: %q", conf)
	}
	// Managed-by label so the controller owns cleanup on uninstall.
	lbls, _ := cm.Object["metadata"].(map[string]any)["labels"].(map[string]any)
	if lbls["obol.org/managed-by"] != "serviceoffer-controller" {
		t.Errorf("managed-by label = %v, want serviceoffer-controller", lbls["obol.org/managed-by"])
	}
}

// TestBuildStaticSiteDeployment: content-hash annotation + correct volume wiring
// (skill.md and api/services.json paths).
func TestBuildStaticSiteDeployment(t *testing.T) {
	d1 := buildStaticSiteDeployment("hash-1", nil)
	d2 := buildStaticSiteDeployment("hash-2", nil)

	spec1, _ := d1.Object["spec"].(map[string]any)
	template1, _ := spec1["template"].(map[string]any)
	meta1, _ := template1["metadata"].(map[string]any)
	ann1, _ := meta1["annotations"].(map[string]any)
	if ann1["obol.org/content-hash"] != "hash-1" {
		t.Errorf("content-hash = %v, want hash-1", ann1["obol.org/content-hash"])
	}

	spec2, _ := d2.Object["spec"].(map[string]any)
	template2, _ := spec2["template"].(map[string]any)
	meta2, _ := template2["metadata"].(map[string]any)
	ann2, _ := meta2["annotations"].(map[string]any)
	if ann1["obol.org/content-hash"] == ann2["obol.org/content-hash"] {
		t.Error("different content hashes must produce different annotations")
	}

	// Verify the services.json, openapi.json, and api/index.html paths get
	// mounted under the right locations. Covers the volume layout the routes
	// expect: /api/services.json, /openapi.json, /api/ → api/index.html.
	podSpec, _ := template1["spec"].(map[string]any)
	volumes, _ := podSpec["volumes"].([]any)
	expectedPaths := map[string]string{
		"services.json": "api/services.json",
		"openapi.json":  "openapi.json",
		"api.html":      "api/index.html",
	}
	foundPaths := map[string]string{}
	for _, v := range volumes {
		vm, _ := v.(map[string]any)
		cm, _ := vm["configMap"].(map[string]any)
		items, _ := cm["items"].([]any)
		for _, it := range items {
			item, _ := it.(map[string]any)
			key, _ := item["key"].(string)
			path, _ := item["path"].(string)
			if _, ok := expectedPaths[key]; ok {
				foundPaths[key] = path
			}
		}
	}
	for key, wantPath := range expectedPaths {
		if got := foundPaths[key]; got != wantPath {
			t.Errorf("volume mount for %q = %q, want %q", key, got, wantPath)
		}
	}
}

// TestBuildStaticSiteService: ClusterIP service on port 8080 with the
// managed-by selector.
func TestBuildStaticSiteService(t *testing.T) {
	svc := buildStaticSiteService()

	if svc.GetName() != staticSiteConfigMapName {
		t.Errorf("name = %q, want %q", svc.GetName(), staticSiteConfigMapName)
	}
	spec, _ := svc.Object["spec"].(map[string]any)
	if spec["type"] != "ClusterIP" {
		t.Errorf("type = %v, want ClusterIP", spec["type"])
	}
	sel, _ := spec["selector"].(map[string]any)
	if sel["obol.org/managed-by"] != "serviceoffer-controller" {
		t.Errorf("selector missing managed-by, got %+v", sel)
	}
}

// TestDefaultString: explicit fallback wiring — all whitespace inputs should
// trigger the fallback.
func TestDefaultString(t *testing.T) {
	tests := []struct {
		name            string
		value, fallback string
		want            string
	}{
		{"non-empty value wins", "actual", "fallback", "actual"},
		{"empty falls back", "", "fallback", "fallback"},
		{"whitespace falls back", "  \t ", "fallback", "fallback"},
		{"value surrounded by whitespace preserved", "  hello  ", "fallback", "  hello  "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultString(tt.value, tt.fallback); got != tt.want {
				t.Errorf("defaultString(%q, %q) = %q, want %q", tt.value, tt.fallback, got, tt.want)
			}
		})
	}
}

// TestDescribeOfferPrice covers the price-fallthrough ladder.
func TestDescribeOfferPrice(t *testing.T) {
	tests := []struct {
		name string
		spec monetizeapi.ServiceOfferSpec
		want string
	}{
		{
			name: "per-request wins",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{
						PerRequest: "0.001",
						PerMTok:    "5",
						PerHour:    "10",
					},
				},
			},
			want: "0.001 USDC/request",
		},
		{
			name: "per-mtok wins when no per-request",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{
						PerMTok: "5.00",
						PerHour: "10",
					},
				},
			},
			want: "5.00 USDC/MTok",
		},
		{
			name: "per-hour falls through when neither set",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerHour: "2.5"},
				},
			},
			want: "2.5 USDC/hour",
		},
		{
			name: "no pricing set falls through to em-dash",
			spec: monetizeapi.ServiceOfferSpec{},
			want: "—",
		},
		{
			name: "OBOL symbol surfaces in per-request label",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Asset: monetizeapi.ServiceOfferAsset{Symbol: "OBOL"},
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
			},
			want: "0.001 OBOL/request",
		},
		{
			name: "OBOL symbol surfaces in per-mtok label",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Asset: monetizeapi.ServiceOfferAsset{Symbol: "OBOL"},
					Price: monetizeapi.ServiceOfferPriceTable{PerMTok: "5.00"},
				},
			},
			want: "5.00 OBOL/MTok",
		},
		{
			name: "OBOL symbol surfaces in per-hour label",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Asset: monetizeapi.ServiceOfferAsset{Symbol: "OBOL"},
					Price: monetizeapi.ServiceOfferPriceTable{PerHour: "2.5"},
				},
			},
			want: "2.5 OBOL/hour",
		},
		{
			name: "empty asset symbol falls back to USDC",
			spec: monetizeapi.ServiceOfferSpec{
				Payment: monetizeapi.ServiceOfferPayment{
					Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
				},
			},
			want: "0.001 USDC/request",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			offer := &monetizeapi.ServiceOffer{Spec: tt.spec}
			if got := describeOfferPrice(offer); got != tt.want {
				t.Errorf("describeOfferPrice = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParseInt64: silent-zero-on-error contract.
func TestParseInt64(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"42", 42},
		{"  42  ", 42},
		{"-17", -17},
		{"not-a-number", 0},
		{"0xff", 0}, // hex rejected: decimal parser
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseInt64(tt.in); got != tt.want {
				t.Errorf("parseInt64(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestNonEmptyStringMap: removes empty keys/values and returns nil for fully
// empty input (lets callers cheaply skip the field).
func TestNonEmptyStringMap(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := nonEmptyStringMap(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("empty map returns nil", func(t *testing.T) {
		if got := nonEmptyStringMap(map[string]string{}); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
	t.Run("all entries empty returns nil", func(t *testing.T) {
		in := map[string]string{"": "v", "k": "", "   ": "   "}
		if got := nonEmptyStringMap(in); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
	t.Run("trims keys and values", func(t *testing.T) {
		in := map[string]string{"  k  ": "  v  "}
		got := nonEmptyStringMap(in)
		if got["k"] != "v" {
			t.Errorf("got[%q] = %q, want v", "k", got["k"])
		}
	})
	t.Run("keeps only non-empty pairs", func(t *testing.T) {
		in := map[string]string{
			"a": "1",
			"b": "",
			"c": "3",
			" ": "ghost",
			"e": "  ",
		}
		got := nonEmptyStringMap(in)
		if len(got) != 2 {
			t.Errorf("len = %d, want 2 (only a and c), got %+v", len(got), got)
		}
		if got["a"] != "1" || got["c"] != "3" {
			t.Errorf("got %+v, want {a:1, c:3}", got)
		}
	})
}

// TestFallbackOfferType asserts the "http" default and passthrough of
// explicit types.
func TestFallbackOfferType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "http"},
		{"inference", "inference"},
		{"http", "http"},
		{"fine-tuning", "fine-tuning"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			offer := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{Type: tt.in}}
			if got := fallbackOfferType(offer); got != tt.want {
				t.Errorf("fallbackOfferType = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSafeName_ExtremePrefixSuffix covers the guard branch where prefix+suffix
// alone are already close to the limit, forcing the `maxName < 1` clamp.
func TestSafeName_ExtremePrefixSuffix(t *testing.T) {
	// Prefix alone exceeds maxK8sNameLen — forces the maxName < 1 clamp.
	prefix := strings.Repeat("p", maxK8sNameLen)
	got := safeName(prefix, "abc", "-x")
	if got == "" {
		t.Error("safeName should never return empty")
	}
	// Even under extreme prefix, the name portion should still be at least 1 char.
	if !strings.Contains(got, "a") {
		t.Errorf("safeName output %q should retain at least 1 char of the name", got)
	}
}
