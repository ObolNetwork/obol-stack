package network

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestCheckBindAddress(t *testing.T) {
	cases := []struct {
		bind        string
		allowPublic bool
		want        string
		public      bool
		wantErr     bool
	}{
		{bind: "", want: "127.0.0.1"},
		{bind: "localhost", want: "127.0.0.1"},
		{bind: "127.0.0.1", want: "127.0.0.1"},
		{bind: "::1", want: "::1"},
		{bind: "172.17.0.1", want: "172.17.0.1"},
		{bind: "192.168.1.20", want: "192.168.1.20"},
		{bind: "10.0.0.5", want: "10.0.0.5"},
		{bind: "fd00::1", want: "fd00::1"},
		{bind: "::ffff:172.17.0.1", want: "172.17.0.1"},
		{bind: "0.0.0.0", wantErr: true},
		{bind: "::", wantErr: true},
		{bind: "203.0.113.7", wantErr: true},
		{bind: "100.64.0.1", wantErr: true}, // CGNAT
		{bind: "169.254.1.1", wantErr: true},
		{bind: "fe80::1", wantErr: true},
		{bind: "host.docker.internal", wantErr: true},
		{bind: "0.0.0.0", allowPublic: true, want: "0.0.0.0", public: true},
		{bind: "203.0.113.7", allowPublic: true, want: "203.0.113.7", public: true},
	}

	for _, c := range cases {
		addr, public, err := CheckBindAddress(c.bind, c.allowPublic)
		if (err != nil) != c.wantErr {
			t.Errorf("CheckBindAddress(%q, %v) error = %v, want error %v", c.bind, c.allowPublic, err, c.wantErr)
			continue
		}

		if err != nil {
			continue
		}

		if addr.String() != c.want || public != c.public {
			t.Errorf("CheckBindAddress(%q, %v) = %s public=%v, want %s public=%v", c.bind, c.allowPublic, addr, public, c.want, c.public)
		}
	}

	if _, _, err := CheckBindAddress("0.0.0.0", false); err == nil || !strings.Contains(err.Error(), "--allow-public") {
		t.Errorf("refusal should name --allow-public: %v", err)
	}
}

func TestExposeTargets(t *testing.T) {
	both, err := exposeTargets(ExposeOptions{})
	if err != nil || len(both) != 2 || both[0].local != 5052 || both[1].local != 8545 {
		t.Fatalf("default targets = %+v (%v)", both, err)
	}

	for _, tg := range both {
		if tg.remote == 8551 {
			t.Fatal("the engine API must never be exposed")
		}
	}

	beacon, _ := exposeTargets(ExposeOptions{Beacon: true, BeaconPort: 15052})
	if len(beacon) != 1 || beacon[0].service != beaconService || beacon[0].local != 15052 || beacon[0].remote != 5052 {
		t.Fatalf("beacon target = %+v", beacon)
	}

	if _, err := exposeTargets(ExposeOptions{BeaconPort: 9000, ExecutionPort: 9000}); err == nil {
		t.Fatal("same port for both should fail")
	}

	if _, err := exposeTargets(ExposeOptions{Beacon: true, BeaconPort: 70000}); err == nil {
		t.Fatal("port 70000 should fail")
	}
}

func installTestDeployment(t *testing.T, values string) *config.Config {
	t.Helper()

	cfg := &config.Config{ConfigDir: t.TempDir()}
	dir := filepath.Join(cfg.ConfigDir, "networks", "ethereum", "hoodi")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "values.yaml"), []byte(values), 0o600); err != nil {
		t.Fatal(err)
	}

	return cfg
}

func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}

// Expose proxies connections to the forward, and reopens a forward whose
// API stopped answering (the pod behind svc/… was replaced).
func TestExpose_ProxiesAndReconnects(t *testing.T) {
	cfg := installTestDeployment(t, "network: hoodi\nexecutionClient: reth\nconsensusClient: silver\n")

	var generation atomic.Int32

	newBeacon := func() *httptest.Server {
		gen := generation.Add(1)
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, fmt.Sprintf(`{"data":{"version":"silver/gen%d"}}`, gen))
		}))
	}

	var (
		mu      sync.Mutex
		current = newBeacon()
		opened  atomic.Int32
	)

	t.Cleanup(func() { mu.Lock(); current.Close(); mu.Unlock() })

	origEnsure, origForward := exposeEnsureClusterFn, exposeForwardFn
	t.Cleanup(func() { exposeEnsureClusterFn, exposeForwardFn = origEnsure, origForward })

	exposeEnsureClusterFn = func(*config.Config) error { return nil }
	exposeForwardFn = func(*config.Config) func(context.Context, string, string, int) (string, func(), error) {
		return func(_ context.Context, namespace, service string, port int) (string, func(), error) {
			if namespace != "ethereum-hoodi" || service != beaconService || port != 5052 {
				t.Errorf("forward to %s/%s:%d", namespace, service, port)
			}

			opened.Add(1)
			mu.Lock()
			defer mu.Unlock()

			return current.URL, func() {}, nil
		}
	}

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())

	var out bytes.Buffer

	done := make(chan error, 1)

	go func() {
		done <- Expose(ctx, cfg, ui.NewForTest(&out, &out), ExposeOptions{
			Identifier: "ethereum/hoodi", Beacon: true, BeaconPort: port, HealthInterval: 50 * time.Millisecond,
		})
	}()

	get := func() string {
		var lastErr error

		for range 100 {
			resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/eth/v1/node/version") //nolint:noctx // test
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()

				return string(body)
			}

			lastErr = err
			time.Sleep(20 * time.Millisecond)
		}

		t.Fatalf("no answer through expose: %v", lastErr)

		return ""
	}

	if got := get(); !strings.Contains(got, "silver/gen1") {
		t.Fatalf("first answer = %q", got)
	}

	// The pod is replaced: the old forward stops answering.
	mu.Lock()
	current.Close()
	current = newBeacon()
	mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := get(); strings.Contains(got, "silver/gen2") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("expose did not reconnect; forwards opened %d\n%s", opened.Load(), out.String())
		}

		time.Sleep(50 * time.Millisecond)
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Expose: %v", err)
	}

	if !strings.Contains(out.String(), "host.docker.internal") {
		t.Errorf("no Docker hint:\n%s", out.String())
	}
}

func TestExpose_Refusals(t *testing.T) {
	origEnsure := exposeEnsureClusterFn
	t.Cleanup(func() { exposeEnsureClusterFn = origEnsure })

	exposeEnsureClusterFn = func(*config.Config) error { return nil }

	var out bytes.Buffer

	u := ui.NewForTest(&out, &out)

	stopped := installTestDeployment(t, "network: hoodi\nexecutionClient: reth\nconsensusClient: lighthouse\nstopped: true\n")
	if err := Expose(context.Background(), stopped, u, ExposeOptions{Identifier: "ethereum/hoodi"}); err == nil || !strings.Contains(err.Error(), "obol network start") {
		t.Errorf("stopped node: %v", err)
	}

	running := installTestDeployment(t, "network: hoodi\nexecutionClient: reth\nconsensusClient: lighthouse\n")
	if err := Expose(context.Background(), running, u, ExposeOptions{Identifier: "ethereum/hoodi", Bind: "0.0.0.0"}); err == nil || !strings.Contains(err.Error(), "--allow-public") {
		t.Errorf("0.0.0.0 without --allow-public: %v", err)
	}

	if err := Expose(context.Background(), running, u, ExposeOptions{Identifier: "aztec/x"}); err == nil {
		t.Error("non-ethereum deployment should be refused")
	}
}
