package x402

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRoutesFromStore_NetworkCAIP2Normalization(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"legacy base-sepolia name", "base-sepolia", "eip155:84532"},
		{"legacy base-mainnet name", "base", "eip155:8453"},
		{"already CAIP-2", "eip155:84532", "eip155:84532"},
		{"empty falls through", "", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			items := []any{
				mustOfferObject(t, monetizeapi.ServiceOffer{
					ObjectMeta: metav1.ObjectMeta{Name: "x", Namespace: "ns"},
					Spec: monetizeapi.ServiceOfferSpec{
						Upstream: monetizeapi.ServiceOfferUpstream{Service: "svc"},
						Payment: monetizeapi.ServiceOfferPayment{
							PayTo:   "0x1111111111111111111111111111111111111111",
							Network: c.input,
							Price:   monetizeapi.ServiceOfferPriceTable{PerRequest: "0.001"},
						},
					},
					Status: monetizeapi.ServiceOfferStatus{
						Conditions: []monetizeapi.Condition{{Type: "RoutePublished", Status: "True"}},
					},
				}),
			}
			rules, err := routesFromStore(items, nil)
			if err != nil {
				t.Fatalf("routesFromStore: %v", err)
			}
			if len(rules) != 1 {
				t.Fatalf("want 1 rule, got %d", len(rules))
			}
			if rules[0].Network != c.want {
				t.Errorf("Network = %q, want %q (input=%q)", rules[0].Network, c.want, c.input)
			}
		})
	}
}
