package buyer

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type metrics struct {
	registry *prometheus.Registry

	requestsTotal       *prometheus.CounterVec
	paymentAttempts     *prometheus.CounterVec
	paymentSuccessTotal *prometheus.CounterVec
	paymentFailureTotal *prometheus.CounterVec
	authRemaining       *prometheus.GaugeVec
	authSpent           *prometheus.GaugeVec
	activeModelMappings *prometheus.GaugeVec
}

func newMetrics() *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_buyer_requests_total",
				Help: "Total requests routed through the x402 buyer sidecar.",
			},
			[]string{"upstream", "remote_model"},
		),
		paymentAttempts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_buyer_payment_attempts_total",
				Help: "Total x402 payment attempts made by the buyer sidecar.",
			},
			[]string{"upstream", "remote_model"},
		),
		paymentSuccessTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_buyer_payment_success_total",
				Help: "Total successful x402 payments made by the buyer sidecar.",
			},
			[]string{"upstream", "remote_model"},
		),
		paymentFailureTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "obol_x402_buyer_payment_failure_total",
				Help: "Total failed x402 payments attempted by the buyer sidecar.",
			},
			[]string{"upstream", "remote_model"},
		),
		authRemaining: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "obol_x402_buyer_auth_remaining",
				Help: "Remaining pre-signed authorizations for an upstream model mapping.",
			},
			[]string{"upstream", "remote_model"},
		),
		authSpent: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "obol_x402_buyer_auth_spent",
				Help: "Consumed pre-signed authorizations for an upstream model mapping.",
			},
			[]string{"upstream", "remote_model"},
		),
		activeModelMappings: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "obol_x402_buyer_active_model_mappings",
				Help: "Active paid model mappings loaded in the buyer sidecar.",
			},
			[]string{"upstream", "remote_model"},
		),
	}

	m.registry.MustRegister(
		m.requestsTotal,
		m.paymentAttempts,
		m.paymentSuccessTotal,
		m.paymentFailureTotal,
		m.authRemaining,
		m.authSpent,
		m.activeModelMappings,
	)

	return m
}

func (m *metrics) handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
