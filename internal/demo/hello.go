package demo

import "net/http"

// HelloHandler returns a proof-of-payment response echoing request details.
func HelloHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := make(map[string]string)
		for _, key := range []string{
			"X-Payment-Status",
			"X-Payment-Tx",
			"X-Forwarded-For",
			"X-Forwarded-Host",
			"X-Forwarded-Proto",
			"X-Forwarded-Uri",
			"User-Agent",
		} {
			if v := r.Header.Get(key); v != "" {
				headers[key] = v
			}
		}

		respond(w, r, "hello", map[string]any{
			"message": "You've successfully paid to access this service via x402 micropayments.",
			"echo": map[string]any{
				"method":  r.Method,
				"path":    r.URL.Path,
				"query":   r.URL.RawQuery,
				"headers": headers,
			},
		})
	}
}
