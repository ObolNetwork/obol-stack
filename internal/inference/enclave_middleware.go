package inference

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/enclave"
)

const (
	// contentTypeEncrypted is the Content-Type used for SE-encrypted request/response bodies.
	// The body is a binary blob produced by enclave.Encrypt.
	contentTypeEncrypted = "application/x-obol-encrypted"

	// headerReplyPubkey, when present on an encrypted request, contains the
	// hex-encoded 65-byte uncompressed SEC1 client ephemeral public key.
	// The gateway will encrypt the upstream response to that key before
	// returning it, providing end-to-end confidentiality.
	headerReplyPubkey = "X-Obol-Reply-Pubkey"

	// maxResponseCapture is the maximum size of an upstream response body that
	// will be buffered for re-encryption. Prevents OOM from unbounded responses.
	maxResponseCapture = 64 << 20 // 64 MiB
)

// enclaveMiddleware wraps inference HTTP handlers to support SE-encrypted
// request bodies.  It exposes GET /v1/enclave/pubkey so that clients can
// discover the Secure Enclave public key needed to encrypt requests.
//
// Encryption protocol (mirrors ecloud's JWE approach but uses ECIES over
// SE-backed P-256 instead of RSA-OAEP-256):
//
//	Client → GET /v1/enclave/pubkey
//	      ← {"pubkey":"<hex65>","algorithm":"ECIES-P256-HKDF-SHA256-AES256GCM",...}
//
//	Client encrypts JSON body:
//	  ciphertext = enclave.Encrypt(sePubKey, requestJSON)
//
//	Client → POST /v1/chat/completions
//	         Content-Type: application/x-obol-encrypted
//	         X-Obol-Reply-Pubkey: <hex65 of client ephemeral pubkey>  (optional)
//	         Body: <ciphertext>
//
//	Gateway → decrypts body via SE key
//	        → forwards plaintext JSON to upstream (Ollama)
//	        → if X-Obol-Reply-Pubkey present: encrypts response back to client
//	        ← Content-Type: application/x-obol-encrypted  (if reply pubkey provided)
//	        ← Body: enclave.Encrypt(clientPubKey, upstreamResponseJSON)
type enclaveMiddleware struct {
	key enclave.Key
}

// newEnclaveMiddleware loads or generates the SE key identified by tag and
// returns middleware ready to serve.
func newEnclaveMiddleware(tag string) (*enclaveMiddleware, error) {
	k, err := enclave.NewKey(tag)
	if err != nil {
		return nil, fmt.Errorf("enclave middleware: %w", err)
	}
	return &enclaveMiddleware{key: k}, nil
}

// pubkeyJSON is the response body for GET /v1/enclave/pubkey.
type pubkeyJSON struct {
	// Pubkey is the hex-encoded 65-byte uncompressed SEC1 public key.
	// Clients MUST use this key with enclave.Encrypt to address requests
	// to this gateway.
	Pubkey string `json:"pubkey"`

	// Tag is the keychain application tag used to identify this key.
	Tag string `json:"tag"`

	// Persistent indicates whether the key is durably stored in the macOS
	// keychain.  false means the key is ephemeral (e.g. unsigned binary in
	// development) and will not survive a process restart.
	Persistent bool `json:"persistent"`

	// Algorithm describes the ECIES construction used by enclave.Encrypt.
	// Fixed value: "ECIES-P256-HKDF-SHA256-AES256GCM"
	Algorithm string `json:"algorithm"`
}

// handlePubkey serves GET /v1/enclave/pubkey.
func (m *enclaveMiddleware) handlePubkey(w http.ResponseWriter, _ *http.Request) {
	body := pubkeyJSON{
		Pubkey:     hex.EncodeToString(m.key.PublicKeyBytes()),
		Tag:        m.key.Tag(),
		Persistent: m.key.Persistent(),
		Algorithm:  "ECIES-P256-HKDF-SHA256-AES256GCM",
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("enclave pubkey handler: encode error: %v", err)
	}
}

// wrap returns a handler that transparently decrypts SE-encrypted request
// bodies before passing them to next, and optionally encrypts responses.
//
// Plaintext requests (any Content-Type other than application/x-obol-encrypted)
// pass through unchanged.
func (m *enclaveMiddleware) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		// Only intercept requests that declare the encrypted content type.
		// All other requests are forwarded as-is (backward compatible).
		if !strings.EqualFold(strings.TrimSpace(ct), contentTypeEncrypted) {
			next.ServeHTTP(w, r)
			return
		}

		// Read the encrypted body (cap at 10 MiB to guard against runaway uploads).
		enc, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		// Decrypt via the SE private key.
		plaintext, err := m.key.Decrypt(enc)
		if err != nil {
			log.Printf("enclave middleware: decrypt error: %v", err)
			http.Error(w, "decryption failed", http.StatusBadRequest)
			return
		}

		// Reconstruct the request with the decrypted body and standard JSON
		// content type so the upstream proxy sees a normal OpenAI request.
		r = r.Clone(r.Context())
		r.Body = io.NopCloser(bytes.NewReader(plaintext))
		r.ContentLength = int64(len(plaintext))
		r.Header.Set("Content-Type", "application/json")

		// Check whether the client wants the response encrypted in return.
		replyPubkeyHex := r.Header.Get(headerReplyPubkey)
		r.Header.Del(headerReplyPubkey) // strip before forwarding upstream

		if replyPubkeyHex == "" {
			// No reply encryption requested — forward normally.
			next.ServeHTTP(w, r)
			return
		}

		replyPubkey, err := hex.DecodeString(replyPubkeyHex)
		if err != nil || len(replyPubkey) != 65 {
			http.Error(w,
				fmt.Sprintf("invalid %s header: must be hex-encoded 65-byte uncompressed P-256 public key", headerReplyPubkey),
				http.StatusBadRequest,
			)
			return
		}

		// Capture the upstream response so we can encrypt it.
		rec := &responseCapture{header: w.Header()}
		next.ServeHTTP(rec, r)

		if rec.overflow {
			log.Printf("enclave middleware: upstream response exceeded %d byte capture limit", maxResponseCapture)
			http.Error(w, "response too large to encrypt", http.StatusBadGateway)
			return
		}

		encResp, err := enclave.Encrypt(replyPubkey, rec.body.Bytes())
		if err != nil {
			log.Printf("enclave middleware: response encrypt error: %v", err)
			http.Error(w, "response encryption failed", http.StatusInternalServerError)
			return
		}

		// The encrypted response body differs from upstream plaintext, so refresh
		// body-derived headers before writing.
		w.Header().Set("Content-Type", contentTypeEncrypted)
		w.Header().Set("Content-Length", strconv.Itoa(len(encResp)))
		w.Header().Del("Content-Encoding")
		w.Header().Del("ETag")
		w.WriteHeader(rec.code())
		if _, err := w.Write(encResp); err != nil {
			log.Printf("enclave middleware: write encrypted response: %v", err)
		}
	})
}

// responseCapture is a minimal http.ResponseWriter that buffers the body
// for post-processing while writing headers directly to the underlying writer.
type responseCapture struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	overflow   bool // true if body exceeded maxResponseCapture
}

func (c *responseCapture) Header() http.Header { return c.header }

func (c *responseCapture) Write(b []byte) (int, error) {
	if c.overflow {
		return 0, fmt.Errorf("response body exceeds %d byte limit", maxResponseCapture)
	}
	if c.body.Len()+len(b) > maxResponseCapture {
		c.overflow = true
		return 0, fmt.Errorf("response body exceeds %d byte limit", maxResponseCapture)
	}
	return c.body.Write(b)
}

func (c *responseCapture) WriteHeader(code int) {
	if c.statusCode == 0 {
		c.statusCode = code
	}
}

// statusCode returns the captured status code, defaulting to 200 OK.
func (c *responseCapture) code() int {
	if c.statusCode == 0 {
		return http.StatusOK
	}
	return c.statusCode
}
