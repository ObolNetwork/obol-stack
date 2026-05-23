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
