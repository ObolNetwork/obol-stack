// mock-facilitator is a standalone x402 facilitator that always approves payments.
// Used for local testing when the real facilitator (x402.rs) is unavailable.
//
// Usage: go run ./cmd/mock-facilitator
// Listens on 0.0.0.0:4040
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("POST /verify from %s", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"valid":          true,
			"invalidReason":  "",
			"payer":          "0x0000000000000000000000000000000000000001",
			"isSettled":      false,
		})
	})

	mux.HandleFunc("/settle", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("POST /settle from %s", r.RemoteAddr)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"txHash":  "0x" + fmt.Sprintf("%064d", 1),
		})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := "0.0.0.0:4040"
	log.Printf("Mock x402 facilitator listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
