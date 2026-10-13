package serviceoffercontroller

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func liteLLMHTTPOffer() *monetizeapi.ServiceOffer {
	o := &monetizeapi.ServiceOffer{Spec: monetizeapi.ServiceOfferSpec{
		Type:     "http",
		Model:    monetizeapi.ServiceOfferModel{Name: "qwen3.5:9b", Runtime: "litellm"},
		Upstream: monetizeapi.ServiceOfferUpstream{Service: "litellm", Namespace: "llm", Port: 4000},
		Payment: monetizeapi.ServiceOfferPayment{
			Network: "base-sepolia", PayTo: "0x1111111111111111111111111111111111111111",
			Price: monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
		},
	}}
	o.Name, o.Namespace = "my-llm", "llm"
	return o
}

// A LiteLLM-backed http offer is only served on POST chat/completions/
// embeddings, so discovery must advertise exactly those — never GET <root>,
// which the gateway 404s — and must not republish LiteLLM's own (admin)
// OpenAPI document.
func TestLiteLLMHTTPOfferDiscovery(t *testing.T) {
	offer := liteLLMHTTPOffer()

	paths := openAPIPathsForOffer(offer)
	ids := map[string]bool{}
	for _, rel := range []string{"/v1/chat/completions", "/v1/completions", "/v1/embeddings"} {
		item, ok := paths[rel]
		if !ok {
			t.Fatalf("missing %s in %v", rel, paths)
		}
		op, ok := item["post"].(map[string]any)
		if !ok || len(item) != 1 {
			t.Fatalf("%s must be POST-only, got %v", rel, item)
		}
		if _, paid := op["x-payment-info"]; !paid {
			t.Fatalf("%s lacks x-payment-info", rel)
		}
		id, _ := op["operationId"].(string)
		if ids[id] {
			t.Fatalf("duplicate operationId %q", id)
		}
		ids[id] = true
	}
	if len(paths) != 3 {
		t.Fatalf("unexpected extra paths: %v", paths)
	}

	if got := openAPIPrimaryPathForOffer(offer); got != "/services/my-llm/v1/chat/completions" {
		t.Fatalf("primary path = %q", got)
	}
	if got := primaryPaidMethod(offer); got != "POST" {
		t.Fatalf("primary method = %q", got)
	}
	if got := openAPIRelPathForOfferRoute(offer, "/*"); got != "/v1/chat/completions" {
		t.Fatalf("well-known resource path = %q", got)
	}
	if offerHasProbeableUpstream(offer) {
		t.Fatal("LiteLLM's own OpenAPI (admin API) must never be republished")
	}

	tryIt := strings.Join(skillMarkdownTryIt(offer, "https://x.example/services/my-llm"), "\n")
	if !strings.Contains(tryIt, "-X POST https://x.example/services/my-llm/v1/chat/completions") ||
		!strings.Contains(tryIt, `"model": "qwen3.5:9b"`) {
		t.Fatalf("skill.md try-it should POST the chat path with the pinned model:\n%s", tryIt)
	}

	// Route-table offers keep their declared surface.
	offer.Spec.Routes = []monetizeapi.ServiceOfferRoute{{Path: "/v1/chat/completions"}}
	if isLiteLLMHTTPOffer(offer) {
		t.Fatal("route-table offers keep their declared routes")
	}
}
