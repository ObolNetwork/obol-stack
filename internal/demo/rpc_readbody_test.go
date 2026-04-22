package demo

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// errReader implements io.ReadCloser and always returns an error on Read.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("simulated read failure") }
func (errReader) Close() error               { return nil }

// failingBodyRoundTripper returns a 200 response whose Body fails on Read.
type failingBodyRoundTripper struct{}

func (failingBodyRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Body:       errReader{},
		Header:     make(http.Header),
	}, nil
}

// TestRPCCall_ReadBodyError covers blocks.go:83-85 — the io.ReadAll error
// branch inside rpcCall. A transport that returns a response whose Body reader
// errors out triggers the "read body" wrap without hitting the transport path.
func TestRPCCall_ReadBodyError(t *testing.T) {
	client := &http.Client{Transport: failingBodyRoundTripper{}}

	got, err := rpcCall(client, "http://unused.example/", "eth_blockNumber", "[]")
	if err == nil {
		t.Fatalf("expected error, got nil (result=%s)", string(got))
	}
	if !strings.Contains(err.Error(), "read body") {
		t.Fatalf("error = %q, want substring %q", err.Error(), "read body")
	}
	if got != nil {
		t.Errorf("expected nil result on read error, got %s", string(got))
	}

	// Sanity: the wrapped error should still surface the simulated cause.
	if !strings.Contains(err.Error(), "simulated read failure") {
		t.Errorf("error = %q should wrap underlying cause", err.Error())
	}
}

// Tiny compile-time assertion that we're using io.ReadCloser shape.
var _ io.ReadCloser = errReader{}
