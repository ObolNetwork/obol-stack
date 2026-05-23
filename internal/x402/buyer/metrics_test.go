package buyer

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// TestPrometheusLabels_ChainPropagation asserts that prometheusLabels surfaces
// the `chain` label sourced from UpstreamConfig.Network so paid-request metrics
// can be partitioned by chain (base, base-sepolia, etc.). The empty-chain case
// is also exercised so the label is always rendered cleanly even when an
// upstream has no Network set.
func TestPrometheusLabels_ChainPropagation(t *testing.T) {
	tests := []struct {
		name        string
		upstream    string
		remoteModel string
		chain       string
		want        map[string]string
	}{
		{
			name:        "base-sepolia chain propagates",
			upstream:    "upstream-a",
			remoteModel: "qwen3.5:9b",
			chain:       "base-sepolia",
			want: map[string]string{
				"upstream":     "upstream-a",
				"remote_model": "qwen3.5:9b",
				"chain":        "base-sepolia",
			},
		},
		{
			name:        "base mainnet chain propagates",
			upstream:    "upstream-b",
			remoteModel: "qwen3.5:4b",
			chain:       "base",
			want: map[string]string{
				"upstream":     "upstream-b",
				"remote_model": "qwen3.5:4b",
				"chain":        "base",
			},
		},
		{
			name:        "empty chain renders cleanly",
			upstream:    "upstream-c",
			remoteModel: "qwen3.5:1b",
			chain:       "",
			want: map[string]string{
				"upstream":     "upstream-c",
				"remote_model": "qwen3.5:1b",
				"chain":        "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prometheusLabels(tt.upstream, tt.remoteModel, tt.chain)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d labels, want %d (%v vs %v)", len(got), len(tt.want), got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("label %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestMetrics_ChainLabelScrapeRoundtrip increments each of the 9 buyer
// counters/gauges using prometheusLabels and then scrapes /metrics through the
// registry's handler, asserting the `chain` label appears (with the expected
// value) on every series.
func TestMetrics_ChainLabelScrapeRoundtrip(t *testing.T) {
	tests := []struct {
		name        string
		upstream    string
		remoteModel string
		chain       string
	}{
		{
			name:        "base-sepolia label visible on every series",
			upstream:    "upstream-a",
			remoteModel: "qwen3.5:9b",
			chain:       "base-sepolia",
		},
		{
			name:        "empty chain label is present and empty",
			upstream:    "upstream-b",
			remoteModel: "qwen3.5:4b",
			chain:       "",
		},
	}

	// Every metric registered by newMetrics carries the same {upstream,
	// remote_model, chain} label set.
	wantFamilies := []string{
		"obol_x402_buyer_requests_total",
		"obol_x402_buyer_payment_attempts_total",
		"obol_x402_buyer_payment_success_total",
		"obol_x402_buyer_payment_failure_total",
		"obol_x402_buyer_confirm_spend_failure_total",
		"obol_x402_buyer_payment_unsettled_confirmations_total",
		"obol_x402_buyer_auth_remaining",
		"obol_x402_buyer_auth_spent",
		"obol_x402_buyer_active_model_mappings",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMetrics()
			labels := prometheusLabels(tt.upstream, tt.remoteModel, tt.chain)

			// Counters: incremented once each.
			m.requestsTotal.With(labels).Inc()
			m.paymentAttempts.With(labels).Inc()
			m.paymentSuccessTotal.With(labels).Inc()
			m.paymentFailureTotal.With(labels).Inc()
			m.confirmSpendFailureTotal.With(labels).Inc()
			m.paymentUnsettledConfirmations.With(labels).Inc()
			// Gauges: stamped with arbitrary non-zero values.
			m.authRemaining.With(labels).Set(7)
			m.authSpent.With(labels).Set(3)
			m.activeModelMappings.With(labels).Set(1)

			families := scrapeBuyerMetrics(t, m)

			wantLabels := map[string]string{
				"upstream":     tt.upstream,
				"remote_model": tt.remoteModel,
				"chain":        tt.chain,
			}
			for _, name := range wantFamilies {
				fam, ok := families[name]
				if !ok {
					t.Errorf("missing metric family %s", name)
					continue
				}
				if !buyerHasSeriesWithLabels(fam, wantLabels) {
					t.Errorf("metric %s missing series with labels %v", name, wantLabels)
				}
			}
		})
	}
}

// scrapeBuyerMetrics renders the metrics registry through its HTTP handler and
// parses the Prometheus text exposition into a name → MetricFamily map.
func scrapeBuyerMetrics(t *testing.T, m *metrics) map[string]*dto.MetricFamily {
	t.Helper()

	rec := httptest.NewRecorder()
	m.handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", rec.Code)
	}

	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse metrics: %v", err)
	}
	return families
}

// buyerHasSeriesWithLabels returns true iff `family` contains at least one
// series whose label set exactly equals `want`.
func buyerHasSeriesWithLabels(family *dto.MetricFamily, want map[string]string) bool {
	for _, metric := range family.GetMetric() {
		if len(metric.GetLabel()) != len(want) {
			continue
		}
		match := true
		for _, label := range metric.GetLabel() {
			if want[label.GetName()] != label.GetValue() {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
