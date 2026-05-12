package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// TestBuildSkillCatalogConfigMap: exposes skill.md + services.json + httpd conf.
func TestBuildSkillCatalogConfigMap(t *testing.T) {
	cm := buildSkillCatalogConfigMap("# Catalog", `[{"name":"a"}]`)

	if cm.GetName() != skillCatalogConfigMapName {
		t.Errorf("name = %q, want %q", cm.GetName(), skillCatalogConfigMapName)
	}
	if cm.GetNamespace() != skillCatalogNamespace {
		t.Errorf("namespace = %q, want %q", cm.GetNamespace(), skillCatalogNamespace)
	}
	data, _ := cm.Object["data"].(map[string]any)
	if data["skill.md"] != "# Catalog" {
		t.Errorf("skill.md payload mismatch, got %v", data["skill.md"])
	}
	if data["services.json"] != `[{"name":"a"}]` {
		t.Errorf("services.json payload mismatch, got %v", data["services.json"])
	}
	if conf, _ := data["httpd.conf"].(string); !strings.Contains(conf, ".md:text/markdown") || !strings.Contains(conf, ".json:application/json") {
		t.Errorf("httpd.conf missing required mime mappings: %q", conf)
	}
	// Managed-by label so the controller owns cleanup on uninstall.
	lbls, _ := cm.Object["metadata"].(map[string]any)["labels"].(map[string]any)
	if lbls["obol.org/managed-by"] != "serviceoffer-controller" {
		t.Errorf("managed-by label = %v, want serviceoffer-controller", lbls["obol.org/managed-by"])
	}
}

// TestBuildSkillCatalogDeployment: content-hash annotation + correct volume wiring
// (skill.md and api/services.json paths).
func TestBuildSkillCatalogDeployment(t *testing.T) {
	d1 := buildSkillCatalogDeployment("hash-1")
	d2 := buildSkillCatalogDeployment("hash-2")

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

	// Verify the services.json path gets mounted under api/ (so the route can
	// serve /api/services.json). Covers the switch in the skill-catalog volume
	// layout.
	podSpec, _ := template1["spec"].(map[string]any)
	volumes, _ := podSpec["volumes"].([]any)
	var foundServicesPath bool
	for _, v := range volumes {
		vm, _ := v.(map[string]any)
		cm, _ := vm["configMap"].(map[string]any)
		items, _ := cm["items"].([]any)
		for _, it := range items {
			item, _ := it.(map[string]any)
			if item["key"] == "services.json" && item["path"] == "api/services.json" {
				foundServicesPath = true
			}
		}
	}
	if !foundServicesPath {
		t.Error("expected services.json to be mounted at api/services.json")
	}
}

// TestBuildSkillCatalogService: ClusterIP service on port 8080 with the
// managed-by selector.
func TestBuildSkillCatalogService(t *testing.T) {
	svc := buildSkillCatalogService()

	if svc.GetName() != skillCatalogConfigMapName {
		t.Errorf("name = %q, want %q", svc.GetName(), skillCatalogConfigMapName)
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
