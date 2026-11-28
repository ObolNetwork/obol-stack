package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ExposeOptions configure Expose.
type ExposeOptions struct {
	// Identifier is "<network>/<id>" of an installed ethereum deployment.
	Identifier string
	// Beacon and Execution pick the APIs; neither means both.
	Beacon, Execution bool
	// Bind is the local IP address to listen on (default 127.0.0.1).
	Bind string
	// BeaconPort / ExecutionPort are the local ports (default 5052 / 8545).
	BeaconPort, ExecutionPort int
	// AllowPublic permits a Bind that is neither loopback nor private.
	AllowPublic bool
	// HealthInterval is how often each forward is checked (default 15s).
	HealthInterval time.Duration
}

// Swappable for tests.
var (
	exposeEnsureClusterFn = kubectl.EnsureCluster
	exposeForwardFn       = kubectlForwardFn
)

// kubectlForwardFn opens `kubectl port-forward svc/<service>` on a loopback
// port of the stack's kubeconfig and returns its base URL.
func kubectlForwardFn(cfg *config.Config) func(context.Context, string, string, int) (string, func(), error) {
	bin, kubeconfig := kubectl.Paths(cfg)

	return func(ctx context.Context, namespace, service string, port int) (string, func(), error) {
		fwd, err := kubectl.PortForward(ctx, bin, kubeconfig, namespace, "svc/"+service, port, 5*time.Second)
		if err != nil {
			return "", nil, err
		}

		return fwd.URL(), fwd.Close, nil
	}
}

// CheckBindAddress parses a --bind value. Loopback and private addresses
// (RFC 1918, IPv6 ULA) are allowed; anything else (0.0.0.0, ::, public,
// CGNAT, link-local) can reach beyond this machine and its local networks,
// so it needs allowPublic. public reports such an address.
func CheckBindAddress(bind string, allowPublic bool) (addr netip.Addr, public bool, err error) {
	if bind == "" || strings.EqualFold(bind, "localhost") {
		bind = "127.0.0.1"
	}

	addr, err = netip.ParseAddr(bind)
	if err != nil {
		return netip.Addr{}, false, fmt.Errorf("--bind %q is not an IP address (e.g. 127.0.0.1 or the docker0 address 172.17.0.1)", bind)
	}

	if addr.Zone() != "" {
		return netip.Addr{}, false, fmt.Errorf("--bind %q: zoned addresses are not supported", bind)
	}

	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() {
		return addr, false, nil
	}

	if !allowPublic {
		return netip.Addr{}, true, fmt.Errorf("--bind %s is not a loopback or private address: the node's API would be reachable from other networks, possibly the internet. Bind 127.0.0.1 or your Docker bridge address (e.g. 172.17.0.1), or pass --allow-public if you really mean it", addr)
	}

	return addr, true, nil
}

type exposeTarget struct {
	label     string
	service   string
	remote    int
	local     int
	probePath string // GET path for the beacon API; "" = JSON-RPC
}

// Expose forwards a local node's beacon and/or execution JSON-RPC API to a
// local address until ctx is done, so containers outside the cluster (a
// charon, a test validator client) can use it. Each connection goes through
// a kubectl port-forward that is reopened when the pod behind it changes.
// The engine API (8551) is never exposed.
func Expose(ctx context.Context, cfg *config.Config, u *ui.UI, opts ExposeOptions) error {
	networkType, id, ok := strings.Cut(opts.Identifier, "/")
	if !ok || networkType != TypeEthereum {
		return fmt.Errorf("expose supports ethereum deployments only (got %s)", opts.Identifier)
	}

	if !slices.Contains(ListDeployments(cfg), opts.Identifier) {
		return fmt.Errorf("deployment %s not found", opts.Identifier)
	}

	d := readDeployment(cfg, networkType, id)
	if d.Stopped {
		return fmt.Errorf("%s is stopped; start it first: obol network start %s", opts.Identifier, opts.Identifier)
	}

	addr, public, err := CheckBindAddress(opts.Bind, opts.AllowPublic)
	if err != nil {
		return err
	}

	targets, err := exposeTargets(opts)
	if err != nil {
		return err
	}

	if err := exposeEnsureClusterFn(cfg); err != nil {
		return err
	}

	if public {
		u.Warnf("Binding %s with --allow-public: the %s of %s is open to every network that can reach this address, with no authentication. Anyone who reaches it can read the chain through your node and send it transactions. Firewall it, and stop this when done.",
			addr, targetLabels(targets), opts.Identifier)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pool := newForwardPool(ctx, exposeForwardFn(cfg))
	defer pool.closeAll()

	listeners := make([]net.Listener, 0, len(targets))

	defer func() {
		for _, ln := range listeners {
			_ = ln.Close()
		}
	}()

	for _, t := range targets {
		var lc net.ListenConfig

		ln, err := lc.Listen(ctx, "tcp", net.JoinHostPort(addr.String(), strconv.Itoa(t.local)))
		if err != nil {
			return fmt.Errorf("listen for the %s: %w", t.label, err)
		}

		listeners = append(listeners, ln)
	}

	for _, t := range targets {
		u.Successf("%-14s http://%s  →  %s svc/%s:%d", t.label, net.JoinHostPort(addr.String(), strconv.Itoa(t.local)), d.Namespace, t.service, t.remote)
	}

	u.Dim("  From Docker: Linux containers reach the host on the docker0 address (e.g. --bind 172.17.0.1);")
	u.Dim("  Docker Desktop containers use host.docker.internal, which reaches 127.0.0.1.")
	u.Dim("  Press Ctrl-C to stop.")

	var wg sync.WaitGroup

	for i, t := range targets {
		wg.Go(func() { serveExpose(ctx, u, pool, listeners[i], d.Namespace, t) })
	}

	interval := opts.HealthInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}

	wg.Go(func() { watchExpose(ctx, u, pool, d.Namespace, targets, interval) })

	<-ctx.Done()

	for _, ln := range listeners {
		_ = ln.Close()
	}

	wg.Wait()
	u.Infof("Stopped exposing %s", opts.Identifier)

	return nil
}

func exposeTargets(opts ExposeOptions) ([]exposeTarget, error) {
	beacon, execution := opts.Beacon, opts.Execution
	if !beacon && !execution {
		beacon, execution = true, true
	}

	port := func(p, def int, flag string) (int, error) {
		if p == 0 {
			return def, nil
		}

		if p < 1 || p > 65535 {
			return 0, fmt.Errorf("--%s %d is not a TCP port", flag, p)
		}

		return p, nil
	}

	var targets []exposeTarget

	if beacon {
		p, err := port(opts.BeaconPort, beaconAPIPort, "beacon-port")
		if err != nil {
			return nil, err
		}

		targets = append(targets, exposeTarget{label: "Beacon API", service: beaconService, remote: beaconAPIPort, local: p, probePath: "/eth/v1/node/version"})
	}

	if execution {
		p, err := port(opts.ExecutionPort, executionRPCPort, "execution-port")
		if err != nil {
			return nil, err
		}

		targets = append(targets, exposeTarget{label: "Execution RPC", service: executionService, remote: executionRPCPort, local: p})
	}

	if len(targets) == 2 && targets[0].local == targets[1].local {
		return nil, fmt.Errorf("--beacon-port and --execution-port are both %d", targets[0].local)
	}

	return targets, nil
}

func targetLabels(targets []exposeTarget) string {
	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.label
	}

	return strings.Join(labels, " and ")
}

// serveExpose accepts connections on ln and pipes each to the forward.
func serveExpose(ctx context.Context, u *ui.UI, pool *forwardPool, ln net.Listener, namespace string, t exposeTarget) {
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				u.Warnf("%s: accept: %v", t.label, err)
			}

			return
		}

		wg.Go(func() { pipeExpose(ctx, u, pool, conn, namespace, t) })
	}
}

func pipeExpose(ctx context.Context, u *ui.UI, pool *forwardPool, conn net.Conn, namespace string, t exposeTarget) {
	defer conn.Close()

	base, release, err := pool.get(namespace, t.service, t.remote)
	if err != nil {
		if ctx.Err() == nil {
			u.Warnf("%s: port-forward to svc/%s: %v", t.label, t.service, err)
		}

		return
	}

	host, err := forwardHost(base)
	if err != nil {
		release(false)
		return
	}

	var d net.Dialer

	upstream, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		release(false)
		return
	}
	defer upstream.Close()

	done := make(chan struct{}, 2)

	go func() { _, _ = io.Copy(upstream, conn); closeWrite(upstream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); closeWrite(conn); done <- struct{}{} }()

	for range 2 {
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
}

func forwardHost(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	return u.Host, nil
}

// watchExpose checks each forward with a real API request: kubectl can keep
// a port-forward listening after the pod behind svc/… is gone. A failed
// check drops the forward, so the next connection reopens it.
func watchExpose(ctx context.Context, u *ui.UI, pool *forwardPool, namespace string, targets []exposeTarget, interval time.Duration) {
	client := &http.Client{Timeout: 5 * time.Second}
	healthy := make([]bool, len(targets))

	for i := range healthy {
		healthy[i] = true
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		for i, t := range targets {
			ok, detail := probeExpose(ctx, client, pool, namespace, t)
			if ctx.Err() != nil {
				return
			}

			switch {
			case !ok && healthy[i]:
				u.Warnf("%s: not answering (%s); reconnecting", t.label, detail)
			case ok && !healthy[i]:
				u.Successf("%s: answering again", t.label)
			}

			healthy[i] = ok
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func probeExpose(ctx context.Context, client *http.Client, pool *forwardPool, namespace string, t exposeTarget) (bool, string) {
	base, release, err := pool.get(namespace, t.service, t.remote)
	if err != nil {
		return false, err.Error()
	}

	var req *http.Request
	if t.probePath != "" {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+t.probePath, nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base,
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"web3_clientVersion","params":[]}`))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	if err != nil {
		release(false)
		return false, err.Error()
	}

	resp, err := client.Do(req)
	if err != nil {
		release(false)
		return false, err.Error()
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Any HTTP answer means the forward reaches a live API; a syncing or
	// erroring node is still worth forwarding to.
	release(true)

	return true, ""
}
