package x402mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Serve must reject bad inputs before it ever contacts a facilitator, so these
// run without one.
func TestServeValidation(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"missing pay-to", Options{Chain: "base-sepolia"}, "pay-to"},
		{"unsupported chain", Options{PayTo: "0xabc", Chain: "dogecoin"}, "unsupported chain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Serve(context.Background(), tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Serve(%+v) error = %v, want substring %q", tt.opts, err, tt.want)
			}
		})
	}
}

func TestCAIP2Networks(t *testing.T) {
	want := map[string]string{
		"base":         "eip155:8453",
		"base-sepolia": "eip155:84532",
		"ethereum":     "eip155:1",
		"polygon":      "eip155:137",
	}
	for chain, id := range want {
		if got := caip2[chain]; got != id {
			t.Errorf("caip2[%q] = %q, want %q", chain, got, id)
		}
	}
}

// TestProxyTool models the canonical paid-MCP use case: fronting a real backend
// service (here a weather API, mirroring the x402 repo's paid get_weather tool).
// The buyer's args are forwarded as the request body, the backend's auth header
// is injected server-side (never supplied by the buyer), and the service
// response is returned verbatim.
func TestProxyTool(t *testing.T) {
	t.Run("forwards args, injects the backend auth header, returns the service response", func(t *testing.T) {
		var got map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Backend auth is injected server-side; the buyer never supplies it.
			if r.Header.Get("X-Api-Key") != "weather-key" {
				t.Errorf("X-Api-Key = %q, want weather-key", r.Header.Get("X-Api-Key"))
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"city":"London","temp_c":12,"conditions":"Cloudy"}`))
		}))
		defer srv.Close()

		args := []byte(`{"city":"London"}`)
		out, err := proxyTool(context.Background(), srv.URL,
			map[string]string{"X-Api-Key": "weather-key"}, args)
		if err != nil {
			t.Fatalf("proxyTool: %v", err)
		}
		if got["city"] != "London" {
			t.Errorf("forwarded city = %v, want London", got["city"])
		}
		if !strings.Contains(out, "Cloudy") {
			t.Errorf("out = %q, want the weather service response", out)
		}
	})

	t.Run("propagates upstream error status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("invalid token"))
		}))
		defer srv.Close()

		_, err := proxyTool(context.Background(), srv.URL, nil, []byte(`{}`))
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("err = %v, want a 401 error", err)
		}
	})

	t.Run("requires an upstream", func(t *testing.T) {
		if _, err := proxyTool(context.Background(), "", nil, []byte(`{}`)); err == nil {
			t.Fatal("want an error when no upstream is configured")
		}
	})
}

func TestHelpers(t *testing.T) {
	if got := nonEmpty("", "fallback"); got != "fallback" {
		t.Errorf("nonEmpty(\"\") = %q, want fallback", got)
	}
	if got := nonEmpty("value", "fallback"); got != "value" {
		t.Errorf("nonEmpty(value) = %q, want value", got)
	}
	if textResult("ok").IsError {
		t.Error("textResult should not be an error result")
	}
	if !errResult("bad").IsError {
		t.Error("errResult should be an error result")
	}
}
