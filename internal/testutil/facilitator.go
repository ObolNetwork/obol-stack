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

// clusterHostURL returns the URL prefix for reaching the host from inside k3d.
// On macOS, Docker Desktop exposes the host as host.docker.internal.
// On Linux, k3d uses host.k3d.internal with a host-gateway entry.
func clusterHostURL() string {
	if runtime.GOOS == "darwin" {
		return "host.docker.internal"
	}
	return "host.k3d.internal"
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

	// Find a free port and create a listener so we know the port before starting.
	l, err := net.Listen("tcp", "127.0.0.1:0")
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
