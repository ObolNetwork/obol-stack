package testutil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
)

// MockFacilitator wraps an httptest.Server that speaks the x402 facilitator protocol.
// Accessible from inside k3d cluster via http://host.k3d.internal:<port> (Linux)
// or http://host.docker.internal:<port> (macOS).
type MockFacilitator struct {
	Server     *httptest.Server
	Port       int
	ClusterURL string // e.g. "http://host.k3d.internal:54321"

	VerifyCalls atomic.Int32
	SettleCalls atomic.Int32
}

// ClusterHostAddress returns the hostname for reaching the host from inside k3d.
// On macOS: host.docker.internal. On Linux: host.k3d.internal.
func ClusterHostAddress() string { return clusterHostURL() }

// clusterHostURL returns the hostname for reaching the host from inside k3d.
// On macOS, Docker Desktop exposes the host as host.docker.internal.
// On Linux, k3d uses host.k3d.internal with a host-gateway entry.
func clusterHostURL() string {
	if runtime.GOOS == "darwin" {
		return "host.docker.internal"
	}
	return "host.k3d.internal"
}

// ClusterHostIP returns an IP address that k3d containers can use to reach the
// host machine. Used for creating EndpointSlice objects (which require IPs).
//
// On macOS: Docker Desktop VM gateway (192.168.65.254)
// On Linux: docker0 bridge interface IP (typically 172.17.0.1)
func ClusterHostIP(t *testing.T) string {
	t.Helper()

	hostname := clusterHostURL()

	// Try DNS first (works on some setups).
	addrs, err := net.LookupHost(hostname)
	if err == nil && len(addrs) > 0 {
		t.Logf("resolved %s → %s", hostname, addrs[0])
		return addrs[0]
	}

	// macOS: Docker Desktop VM gateway.
	if runtime.GOOS == "darwin" {
		const dockerDesktopGW = "192.168.65.254"
		t.Logf("%s not resolvable on host, using Docker Desktop gateway %s", hostname, dockerDesktopGW)
		return dockerDesktopGW
	}

	// Linux: docker0 bridge interface.
	iface, err := net.InterfaceByName("docker0")
	if err != nil {
		t.Fatalf("cannot resolve cluster host IP: DNS failed for %s and docker0 not found: %v", hostname, err)
	}
	ifAddrs, err := iface.Addrs()
	if err != nil {
		t.Fatalf("cannot get docker0 addresses: %v", err)
	}
	for _, addr := range ifAddrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			t.Logf("using docker0 bridge IP %s", ipNet.IP)
			return ipNet.IP.String()
		}
	}
	t.Fatalf("no IPv4 address on docker0 interface")
	return ""
}

// StartMockFacilitator starts a mock facilitator on a free port.
// Follows the pattern from x402/go/test/mocks/cash/cash.go.
// Handles: GET /supported, POST /verify, POST /settle.
// Registers t.Cleanup to stop the server.
func StartMockFacilitator(t *testing.T) *MockFacilitator {
	t.Helper()

	mf := &MockFacilitator{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /supported", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kinds":[{"x402Version":1,"scheme":"exact","network":"base-sepolia"}]}`)
	})

	mux.HandleFunc("POST /verify", func(w http.ResponseWriter, r *http.Request) {
		mf.VerifyCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"isValid":true,"invalidReason":"","payer":"0xmockpayer"}`)
	})

	mux.HandleFunc("POST /settle", func(w http.ResponseWriter, r *http.Request) {
		mf.SettleCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"transaction":"0xmocktxhash","network":"base-sepolia"}`)
	})

	// Find a free port.  Bind on 0.0.0.0 so k3d containers can reach us
	// via the docker0 bridge IP on Linux.
	l, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("find free port for mock facilitator: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	mf.Server = httptest.NewUnstartedServer(mux)
	mf.Server.Listener.Close()
	mf.Server.Listener = l
	mf.Server.Start()

	mf.Port = port
	mf.ClusterURL = fmt.Sprintf("http://%s:%d", clusterHostURL(), port)

	t.Cleanup(mf.Server.Close)
	return mf
}

// TestPaymentHeader constructs a base64-encoded x402 V1 payment header
// that the mock facilitator will accept.
func TestPaymentHeader(t *testing.T, payTo string) string {
	t.Helper()

	payload := map[string]interface{}{
		"x402Version": 1,
		"scheme":      "exact",
		"network":     "base-sepolia",
		"payload": map[string]interface{}{
			"signature": "0xmocksig",
			"authorization": map[string]interface{}{
				"from":        "0xmockpayer",
				"to":          payTo,
				"value":       "1000000",
				"validAfter":  0,
				"validBefore": 4294967295,
				"nonce":       "0x0",
			},
		},
		"resource": map[string]interface{}{
			"payTo":             payTo,
			"maxAmountRequired": "1000000",
			"asset":             "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
			"network":           "base-sepolia",
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payment header: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}
