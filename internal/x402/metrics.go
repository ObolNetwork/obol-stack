package x402

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type verifierMetrics struct {
	registry *prometheus.Registry

	requestsTotal   *prometheus.CounterVec
	paymentRequired *prometheus.CounterVec
	paymentVerified *prometheus.CounterVec
	paymentFailed   *prometheus.CounterVec
	chargedRequests *prometheus.CounterVec
}

func newVerifierMetrics() *verifierMetrics {
	m := &verifierMetrics{
		registry: prometheus.NewRegistry(),
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_requests_total",
				Help: "Requests evaluated by the x402 verifier for matched paid routes.",
			},
			[]string{"route", "offer_namespace", "offer_name"},
		),
		paymentRequired: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_payment_required_total",
				Help: "Requests rejected with 402 because payment was required.",
			},
			[]string{"route", "offer_namespace", "offer_name"},
		),
		paymentVerified: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_payment_verified_total",
				Help: "Requests approved after successful x402 payment verification.",
			},
			[]string{"route", "offer_namespace", "offer_name"},
		),
		paymentFailed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_payment_failed_total",
				Help: "Requests rejected after a provided x402 payment failed verification.",
			},
			[]string{"route", "offer_namespace", "offer_name"},
		),
		chargedRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_verifier_charged_requests_total",
				Help: "Requests that incurred a paid x402 charge.",
			},
			[]string{"route", "offer_namespace", "offer_name"},
		),
	}

	m.registry.MustRegister(
		m.requestsTotal,
		m.paymentRequired,
		m.paymentVerified,
		m.paymentFailed,
		m.chargedRequests,
	)

	return m
}

func (m *verifierMetrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
