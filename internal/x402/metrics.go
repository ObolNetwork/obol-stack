package x402

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type verifierMetrics struct {
	registry *prometheus.Registry

	requestsTotal       *prometheus.CounterVec
	paymentRequired     *prometheus.CounterVec
	paymentVerified     *prometheus.CounterVec
	paymentFailed       *prometheus.CounterVec
	chargedRequests     *prometheus.CounterVec
	lastPaymentSuccess  *prometheus.GaugeVec
}

func newVerifierMetrics() *verifierMetrics {
	m := &verifierMetrics{
		registry: prometheus.NewRegistry(),
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_requests_total",
				Help: "Requests evaluated by the x402 verifier for matched paid routes.",
			},
			[]string{"offer_namespace", "offer_name", "chain"},
		),
		paymentRequired: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_payment_required_total",
				Help: "Requests rejected with 402 because payment was required.",
			},
			[]string{"offer_namespace", "offer_name", "chain"},
		),
		paymentVerified: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_payment_verified_total",
				Help: "Requests approved after successful x402 payment verification.",
			},
			[]string{"offer_namespace", "offer_name", "chain"},
		),
		paymentFailed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_payment_failed_total",
				Help: "Requests rejected after a provided x402 payment failed verification.",
			},
			[]string{"offer_namespace", "offer_name", "chain"},
		),
		chargedRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_charged_requests_total",
				Help: "Requests that incurred a paid x402 charge.",
			},
			[]string{"offer_namespace", "offer_name", "chain"},
		),
		lastPaymentSuccess: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "obol_x402_verifier_last_payment_success_seconds",
				Help: "Unix timestamp (seconds) of the most recent successful paid x402 charge for a route.",
			},
			[]string{"offer_namespace", "offer_name", "chain"},
		),
	}

	m.registry.MustRegister(
		m.requestsTotal,
		m.paymentRequired,
		m.paymentVerified,
		m.paymentFailed,
		m.chargedRequests,
		m.lastPaymentSuccess,
	)

	return m
}

func (m *verifierMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// pruneSeriesNotIn drops every (offer_namespace, offer_name, chain) series
// from the verifier's counter/gauge vecs that is not present in `keep`.
// Called from Verifier.load whenever the route set changes so deleted offers
// (e.g. `obol sell delete`) stop emitting stale series — most importantly the
// last_payment_success_seconds gauge, which would otherwise hold the deleted
// offer's last-success timestamp forever and falsely satisfy "recent activity"
// alerts and dashboards.
//
// Key shape: "ns\x00name\x00chain" — \x00 is forbidden in Kubernetes object
// names and CAIP-2 chain ids, so the byte-join can't collide.
func (m *verifierMetrics) pruneSeriesNotIn(keep map[string]struct{}) {
	vecs := []interface {
		DeletePartialMatch(prometheus.Labels) int
	}{
		m.requestsTotal,
		m.paymentRequired,
		m.paymentVerified,
		m.paymentFailed,
		m.chargedRequests,
		m.lastPaymentSuccess,
	}

	gathered, err := m.registry.Gather()
	if err != nil {
		return
	}
	for _, family := range gathered {
		for _, metric := range family.GetMetric() {
			labels := metric.GetLabel()
			ns, name, chain := "", "", ""
			for _, l := range labels {
				switch l.GetName() {
				case "offer_namespace":
					ns = l.GetValue()
				case "offer_name":
					name = l.GetValue()
				case "chain":
					chain = l.GetValue()
				}
			}
			if ns == "" && name == "" {
				continue
			}
			if _, ok := keep[ns+"\x00"+name+"\x00"+chain]; ok {
				continue
			}
			match := prometheus.Labels{
				"offer_namespace": ns,
				"offer_name":      name,
				"chain":           chain,
			}
			for _, vec := range vecs {
				vec.DeletePartialMatch(match)
			}
		}
	}
}
