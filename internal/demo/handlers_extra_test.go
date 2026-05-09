package demo

import (
	"net/http/httptest"
	"net/http"
	"testing"
)

// TestExtractPayment_DefaultStatus verifies the status fallback when no
// X-Payment-Status header is present. This is the branch the existing
// TestHelloHandler skips (it always sets the header).
func TestExtractPayment_DefaultStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Intentionally no X-Payment-Status header.

	info := extractPayment(req)
	if info.Status != "paid" {
		t.Errorf("default Status = %q, want %q (firstNonEmpty fallback)", info.Status, "paid")
	}
	if info.Tx != "" {
		t.Errorf("Tx = %q, want empty", info.Tx)
	}
	if info.Payer != "" {
		t.Errorf("Payer = %q, want empty", info.Payer)
	}
}

func TestExtractPayment_PassesThroughHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Payment-Status", "settled")
	req.Header.Set("X-Payment-Tx", "0xdeadbeef")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")

	info := extractPayment(req)
	if info.Status != "settled" {
		t.Errorf("Status = %q, want %q", info.Status, "settled")
	}
	if info.Tx != "0xdeadbeef" {
		t.Errorf("Tx = %q, want 0xdeadbeef", info.Tx)
	}
	if info.Payer != "10.0.0.1" {
		t.Errorf("Payer = %q, want 10.0.0.1", info.Payer)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"first wins", []string{"a", "b"}, "a"},
		{"empty falls through", []string{"", "b"}, "b"},
		{"all empty", []string{"", "", ""}, ""},
		{"nil args", nil, ""},
		{"single", []string{"x"}, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonEmpty(tt.in...); got != tt.want {
				t.Errorf("firstNonEmpty(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
