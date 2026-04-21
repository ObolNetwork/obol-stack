// Package demo implements HTTP handlers for obol sell demo services.
//
// Each demo type returns a JSON response with a standard envelope:
//
//	{
//	  "demo": "<type>",
//	  "timestamp": "...",
//	  "payment": { "status": "...", "tx": "...", "payer": "..." },
//	  "data": { ... }
//	}
//
// The payment block is populated from x402 ForwardAuth headers injected
// by Traefik after successful payment verification.
package demo

import (
	"encoding/json"
	"net/http"
	"time"
)

// Response is the standard envelope for all demo responses.
type Response struct {
	Demo      string      `json:"demo"`
	Timestamp string      `json:"timestamp"`
	Payment   PaymentInfo `json:"payment"`
	Data      any         `json:"data"`
}

// PaymentInfo captures x402 payment metadata from ForwardAuth headers.
type PaymentInfo struct {
	Status string `json:"status"`
	Tx     string `json:"tx,omitempty"`
	Payer  string `json:"payer,omitempty"`
}

// extractPayment reads x402 headers set by the ForwardAuth middleware.
func extractPayment(r *http.Request) PaymentInfo {
	return PaymentInfo{
		Status: firstNonEmpty(r.Header.Get("X-Payment-Status"), "paid"),
		Tx:     r.Header.Get("X-Payment-Tx"),
		Payer:  r.Header.Get("X-Forwarded-For"),
	}
}

// writeJSON marshals v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// respond builds and writes the standard demo response envelope.
func respond(w http.ResponseWriter, r *http.Request, demoType string, data any) {
	writeJSON(w, http.StatusOK, Response{
		Demo:      demoType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payment:   extractPayment(r),
		Data:      data,
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
