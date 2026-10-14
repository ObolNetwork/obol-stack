package network

import (
	"context"
	"fmt"
	"sync"
)

// forwardPool keeps port-forwards open across probes (network status
// --watch). Each namespace/service:port has one forward; a forward the
// caller reports as broken is closed and reopened on next use.
type forwardPool struct {
	ctx     context.Context //nolint:containedctx // forwards live as long as the watch session
	forward func(ctx context.Context, namespace, service string, port int) (string, func(), error)

	mu      sync.Mutex
	entries map[string]*poolEntry
	closed  bool
}

type poolEntry struct {
	mu      sync.Mutex
	base    string
	closeFn func()
}

func newForwardPool(ctx context.Context, forward func(context.Context, string, string, int) (string, func(), error)) *forwardPool {
	return &forwardPool{ctx: ctx, forward: forward, entries: map[string]*poolEntry{}}
}

// get returns the forward's base URL, opening it if needed. release(false)
// drops the forward so the next get reopens it (the pod behind a
// `kubectl port-forward svc/…` is fixed when the forward starts, so a
// restarted pod needs a new forward).
func (p *forwardPool) get(namespace, service string, port int) (string, func(ok bool), error) {
	key := fmt.Sprintf("%s/%s:%d", namespace, service, port)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return "", nil, context.Canceled
	}

	e, ok := p.entries[key]
	if !ok {
		e = &poolEntry{}
		p.entries[key] = e
	}
	p.mu.Unlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.base == "" {
		base, closeFn, err := p.forward(p.ctx, namespace, service, port)
		if err != nil {
			return "", nil, err
		}

		e.base, e.closeFn = base, closeFn
	}

	base := e.base
	release := func(ok bool) {
		if ok {
			return
		}

		e.mu.Lock()
		defer e.mu.Unlock()

		if e.base == base && e.closeFn != nil {
			e.closeFn()
			e.base, e.closeFn = "", nil
		}
	}

	return base, release, nil
}

// closeAll closes every open forward; later gets fail.
func (p *forwardPool) closeAll() {
	p.mu.Lock()
	p.closed = true
	entries := p.entries
	p.entries = map[string]*poolEntry{}
	p.mu.Unlock()

	for _, e := range entries {
		e.mu.Lock()
		if e.closeFn != nil {
			e.closeFn()
		}
		e.base, e.closeFn = "", nil
		e.mu.Unlock()
	}
}
