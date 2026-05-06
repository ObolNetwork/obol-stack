package buy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
)

func TestWellKnownURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"base URL", "https://demo.example", "https://demo.example/.well-known/agent-registration.json"},
		{"service path stripped", "https://demo.example/services/foo", "https://demo.example/.well-known/agent-registration.json"},
		{"trailing slash", "https://demo.example/", "https://demo.example/.well-known/agent-registration.json"},
		{"query dropped", "https://demo.example/services/foo?x=1", "https://demo.example/.well-known/agent-registration.json"},
		{"trim spaces", "  https://demo.example  ", "https://demo.example/.well-known/agent-registration.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := wellKnownURL(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("wellKnownURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWellKnownURL_Invalid(t *testing.T) {
	tests := []string{"", "not-a-url", "/relative/only"}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if _, err := wellKnownURL(in); err == nil {
				t.Fatalf("wellKnownURL(%q) returned nil err, want error", in)
			}
		})
	}
}

func TestVerifyAgentID(t *testing.T) {
	multi := &erc8004.AgentRegistration{
		Registrations: []erc8004.OnChainReg{
			{AgentID: 41, AgentRegistry: "eip155:1:0xabc"},
			{AgentID: 42, AgentRegistry: "eip155:84532:0xdef"},
		},
	}

	tests := []struct {
		name        string
		reg         *erc8004.AgentRegistration
		expected    int64
		wantErrSubs string
	}{
		{name: "match first", reg: multi, expected: 41},
		{name: "match second", reg: multi, expected: 42},
		{name: "mismatch", reg: multi, expected: 99, wantErrSubs: "expected 99"},
		{name: "empty registrations", reg: &erc8004.AgentRegistration{}, expected: 42, wantErrSubs: "no on-chain registrations"},
		{name: "expected zero", reg: multi, expected: 0, wantErrSubs: "expected agentId is 0"},
		{name: "nil registration", reg: nil, expected: 42, wantErrSubs: "nil registration"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := VerifyAgentID(tc.reg, tc.expected)
			if tc.wantErrSubs == "" {
				if err != nil {
					t.Fatalf("VerifyAgentID() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Fatalf("VerifyAgentID() err = %v, want substring %q", err, tc.wantErrSubs)
			}
		})
	}
}

func TestFetchSellerRegistration_Success(t *testing.T) {
	body := `{
		"type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
		"name": "demo",
		"description": "demo seller",
		"image": "https://demo.example/logo.png",
		"services": [{"name": "OASF"}],
		"x402Support": true,
		"active": true,
		"registrations": [{"agentId": 42, "agentRegistry": "eip155:84532:0xabc"}]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wellKnownPath {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	reg, err := FetchSellerRegistration(context.Background(), srv.URL+"/services/foo")
	if err != nil {
		t.Fatalf("FetchSellerRegistration: %v", err)
	}
	if got := reg.Registrations[0].AgentID; got != 42 {
		t.Fatalf("agentId = %d, want 42", got)
	}
}

func TestFetchSellerRegistration_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	_, err := FetchSellerRegistration(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("FetchSellerRegistration(404) err = %v, want 404 error", err)
	}
}

func TestFetchSellerRegistration_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	t.Cleanup(srv.Close)

	_, err := FetchSellerRegistration(context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "parse registration JSON") {
		t.Fatalf("FetchSellerRegistration(bad json) err = %v, want parse error", err)
	}
}

func TestFetchSellerRegistration_InvalidURL(t *testing.T) {
	_, err := FetchSellerRegistration(context.Background(), "not-a-url")
	if err == nil || !strings.Contains(err.Error(), "scheme and host") {
		t.Fatalf("FetchSellerRegistration(invalid url) err = %v, want scheme/host error", err)
	}
}
