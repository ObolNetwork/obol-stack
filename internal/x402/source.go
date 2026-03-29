package x402

import (
	"sync"
)

type ConfigAccumulator struct {
	mu       sync.Mutex
	base     PricingConfig
	routes   []RouteRule
	verifier *Verifier
}

func NewConfigAccumulator(base *PricingConfig, verifier *Verifier) *ConfigAccumulator {
	acc := &ConfigAccumulator{verifier: verifier}
	if base != nil {
		acc.base = *base
		acc.base.Routes = append([]RouteRule(nil), base.Routes...)
	}
	return acc
}

func (a *ConfigAccumulator) SetBase(base *PricingConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if base == nil {
		a.base = PricingConfig{}
	} else {
		a.base = *base
		a.base.Routes = append([]RouteRule(nil), base.Routes...)
	}

	return a.applyLocked()
}

func (a *ConfigAccumulator) SetRoutes(routes []RouteRule) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.routes = append([]RouteRule(nil), routes...)
	return a.applyLocked()
}

func (a *ConfigAccumulator) applyLocked() error {
	cfg := a.base
	cfg.Routes = append([]RouteRule(nil), a.routes...)
	cfg.Routes = append(cfg.Routes, a.base.Routes...)
	return a.verifier.Reload(&cfg)
}
