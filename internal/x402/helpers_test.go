package x402

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// httpPost sends a POST request with JSON body and optional headers.
// Shared by both unit and integration tests.
func httpPost(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP POST %s: %v", url, err)
	}
	return resp
}

// mustQuoteJSON wraps a JSON string as a quoted JSON string value,
// suitable for embedding in a kubectl patch -p '{"data":{"key":<here>}}'.
func mustQuoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
