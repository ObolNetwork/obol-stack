package escrow

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// idPattern bounds escrow ids to safe filenames (the store keys files by id).
// ServiceBounty UIDs (RFC-4122) always match.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ServerOptions configures the escrow facilitator HTTP surface.
type ServerOptions struct {
	// Token is the bearer credential required on every /escrow/{op} POST.
	// Empty disables auth, mirroring HTTPGateway, which omits the
	// Authorization header entirely when its Token is empty.
	Token string
	// Spender is the facilitator's settlement address — the only executor
	// vouchers may name. Zero means no signing identity is configured:
	// voucher-less reserves still work (with an empty spender hint) but
	// voucher verification and capture are refused.
	Spender common.Address
	// Networks are the chain aliases this facilitator settles on, surfaced
	// via GET /escrow/info. When non-empty, vouchers on other chains are
	// rejected at reserve time.
	Networks []string
	// Submitter executes captures on-chain. Nil disables capture (503) while
	// reserve/void keep working.
	Submitter Submitter
}

// Server implements the facilitator side of the escrow wire protocol that
// HTTPGateway speaks: POST /escrow/{reserve,capture,void}/{id} plus
// GET /escrow/info and GET /healthz. The server holds no nonce-invalidation
// logic: Void is store-only, because the voucher deadline is the hard
// on-chain guarantee (v1 — no invalidateUnorderedNonces call).
type Server struct {
	store      *Store
	token      string
	spender    common.Address
	networks   []string
	networkIDs []*big.Int
	submitter  Submitter

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewServer builds a Server over a Store.
func NewServer(store *Store, opts ServerOptions) *Server {
	s := &Server{
		store:     store,
		token:     opts.Token,
		spender:   opts.Spender,
		networks:  opts.Networks,
		submitter: opts.Submitter,
		locks:     make(map[string]*sync.Mutex),
	}
	for _, n := range opts.Networks {
		if id, err := ChainIDForNetwork(n); err == nil {
			s.networkIDs = append(s.networkIDs, id)
		}
	}
	return s
}

// Handler returns the HTTP mux for the escrow surface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /escrow/reserve/{id}", s.auth(s.handleReserve))
	mux.HandleFunc("POST /escrow/capture/{id}", s.auth(s.handleCapture))
	mux.HandleFunc("POST /escrow/void/{id}", s.auth(s.handleVoid))
	mux.HandleFunc("GET /escrow/info", s.handleInfo)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	return mux
}

// auth enforces the bearer token with a constant-time comparison. An empty
// configured token disables auth (the HTTPGateway client omits the
// Authorization header when its token is empty, so this is the symmetric
// choice for local-first dev; production must set OBOL_ESCROW_TOKEN).
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// lockID serializes operations per escrow id (captures can take minutes on
// chain; a global lock would head-of-line-block every other escrow).
func (s *Server) lockID(id string) func() {
	s.mu.Lock()
	m, ok := s.locks[id]
	if !ok {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func (s *Server) spenderHex() string {
	if s.spender == (common.Address{}) {
		return ""
	}
	return s.spender.Hex()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !idPattern.MatchString(id) {
		http.Error(w, "invalid escrow id", http.StatusBadRequest)
		return "", false
	}
	return id, true
}

// networkAllowed checks the voucher's chain against the configured networks.
func (s *Server) networkAllowed(chainID *big.Int) bool {
	if len(s.networkIDs) == 0 {
		return true
	}
	for _, id := range s.networkIDs {
		if id.Cmp(chainID) == 0 {
			return true
		}
	}
	return false
}

func (s *Server) handleReserve(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var req ReserveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "decode reserve request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.ID != "" && req.ID != id {
		http.Error(w, fmt.Sprintf("body id %q does not match path id %q", req.ID, id), http.StatusBadRequest)
		return
	}
	req.ID = id

	unlock := s.lockID(id)
	defer unlock()

	entry, exists, err := s.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Already settled: idempotent success with the stored receipt.
	if exists && entry.State == StateCaptured {
		writeJSON(w, entry.Receipt)
		return
	}
	// A voucher-less re-reserve never downgrades a held voucher.
	if exists && entry.State == StateReserved && req.Voucher == nil {
		writeJSON(w, entry.Receipt)
		return
	}
	// A re-reserve of an already-held id must not silently REPLACE the signed
	// settlement terms: an identical request is idempotent success, but a
	// different voucher/amount under the same id is a conflict, not an
	// overwrite (Reserve is specified idempotent).
	if exists && entry.State == StateReserved {
		if sameReserveRequest(entry.Request, &req) {
			writeJSON(w, entry.Receipt)
			return
		}
		http.Error(w, "escrow id already reserved with different settlement terms", http.StatusConflict)
		return
	}

	var receipt Receipt
	if req.Voucher == nil {
		receipt = Receipt{State: StateAwaitingVoucher, Spender: s.spenderHex()}
	} else {
		if s.spender == (common.Address{}) {
			http.Error(w, "facilitator signing address not configured; cannot verify voucher spender binding", http.StatusServiceUnavailable)
			return
		}
		chainID, err := ChainIDForNetwork(req.Voucher.Network)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !s.networkAllowed(chainID) {
			http.Error(w, fmt.Sprintf("voucher network %q is not served by this facilitator (networks: %s)", req.Voucher.Network, strings.Join(s.networks, ", ")), http.StatusBadRequest)
			return
		}
		// Guard alias drift between the reserve leg and the voucher.
		if req.Network != "" {
			if reqChain, err := ChainIDForNetwork(req.Network); err == nil && reqChain.Cmp(chainID) != 0 {
				http.Error(w, fmt.Sprintf("reserve network %q and voucher network %q resolve to different chains", req.Network, req.Voucher.Network), http.StatusBadRequest)
				return
			}
		}
		if err := VerifyVoucher(*req.Voucher, chainID, s.spender); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		receipt = Receipt{State: StateReserved, Spender: s.spenderHex()}
	}

	if err := s.store.Put(Entry{ID: id, State: receipt.State, Request: &req, Receipt: receipt, UpdatedAt: time.Now().UTC()}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, receipt)
}

// sameReserveRequest reports whether two reserve requests carry identical
// settlement terms, so a retry is treated as idempotent success rather than a
// silent overwrite of the signed voucher. Compared by canonical JSON (struct
// fields marshal in declaration order, so the encoding is deterministic).
func sameReserveRequest(a, b *ReserveRequest) bool {
	if a == nil || b == nil {
		return a == b
	}
	ja, err1 := json.Marshal(a)
	jb, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && string(ja) == string(jb)
}

// captureRequest is the optional capture body. HTTPGateway.Capture sends no
// body (capture every voucher seat); HTTPGateway.CaptureBatch sends
// {"recipients":[...]} (capture the subset, omitted seats unpaid).
type captureRequest struct {
	Recipients []BatchRecipient `json:"recipients"`
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read capture request: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req captureRequest
	if body := strings.TrimSpace(string(raw)); body != "" {
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, "decode capture request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Recipients != nil && len(req.Recipients) == 0 {
			http.Error(w, "capture requested an explicitly empty recipient set", http.StatusBadRequest)
			return
		}
	}

	unlock := s.lockID(id)
	defer unlock()

	entry, exists, err := s.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "no such escrow", http.StatusNotFound)
		return
	}

	switch entry.State {
	case StateCaptured:
		// Idempotent: the stored receipt, no second settlement.
		writeJSON(w, entry.Receipt)
		return
	case StateVoided:
		http.Error(w, "escrow already voided", http.StatusConflict)
		return
	case StateAwaitingVoucher:
		http.Error(w, "no voucher attached; re-reserve with a signed voucher first", http.StatusConflict)
		return
	case StateReserved:
		// fall through to settle
	default:
		http.Error(w, "unknown escrow state "+entry.State, http.StatusInternalServerError)
		return
	}
	if entry.Request == nil || entry.Request.Voucher == nil {
		http.Error(w, "reserved escrow has no voucher", http.StatusConflict)
		return
	}
	if s.submitter == nil {
		http.Error(w, "settlement unavailable: facilitator has no signing key (set OBOL_ESCROW_KEY or OBOL_ESCROW_SIGNER_URL)", http.StatusServiceUnavailable)
		return
	}

	voucher := *entry.Request.Voucher
	requested := req.Recipients
	if requested == nil {
		requested = voucher.Recipients
	}
	details, err := BuildTransferDetails(voucher, requested)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	txHash, err := s.submitter.Submit(r.Context(), voucher, details)
	if err != nil {
		http.Error(w, "settlement failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	receipt := Receipt{State: StateCaptured, TxHash: txHash, Spender: s.spenderHex()}
	entry.State = StateCaptured
	entry.Receipt = receipt
	entry.UpdatedAt = time.Now().UTC()
	if err := s.store.Put(entry); err != nil {
		// The transfer settled; surface the receipt anyway and log loudly via
		// the error body on the next idempotent call.
		http.Error(w, fmt.Sprintf("settled in %s but failed to persist receipt: %v", txHash, err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, receipt)
}

func (s *Server) handleVoid(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	unlock := s.lockID(id)
	defer unlock()

	entry, exists, err := s.store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		// Re-runnable refunds: voiding the unknown is a no-op success
		// (mirrors LedgerGateway). Nothing is persisted for unknown ids.
		writeJSON(w, Receipt{State: StateVoided})
		return
	}
	if entry.State == StateCaptured {
		http.Error(w, "escrow already captured", http.StatusConflict)
		return
	}

	receipt := Receipt{State: StateVoided, Spender: s.spenderHex()}
	entry.State = StateVoided
	entry.Receipt = receipt
	entry.UpdatedAt = time.Now().UTC()
	if err := s.store.Put(entry); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, receipt)
}

// infoResponse is the GET /escrow/info body: the facilitator settlement
// address signers must bind as the voucher spender, and the chains served.
type infoResponse struct {
	Address  string   `json:"address"`
	Networks []string `json:"networks"`
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	networks := s.networks
	if networks == nil {
		networks = []string{}
	}
	writeJSON(w, infoResponse{Address: s.spenderHex(), Networks: networks})
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
