// Package server is the owner-hosted HTTP surface for a decentralized
// auto-research program: the device-auth (membership) endpoints plus the
// membership-gated knowledge base. One Server hosts one Program; the owner
// runs it on their machine and exposes it over a Cloudflare tunnel so
// workers on other obol-stacks reach it over the open internet — every KB
// route gated by a groupauth member token, never an open public route.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/research/groupauth"
	"github.com/ObolNetwork/obol-stack/internal/research/kb"
)

// Membership modes.
const (
	MembershipOpen   = "open"   // any worker is auto-approved on login
	MembershipInvite = "invite" // the owner must approve each worker
)

// Server hosts one program.
type Server struct {
	prog       kb.Program
	membership string
	owner      string // owner admin token (gates approve/status-admin)
	auth       *groupauth.Authority
	store      *kb.KB
	log        *slog.Logger
}

// New builds a Server for program p. ownerToken gates owner-only routes
// (approve). membership is MembershipOpen or MembershipInvite.
func New(p kb.Program, membership, ownerToken string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	if membership == "" {
		membership = MembershipInvite
	}
	return &Server{
		prog:       p,
		membership: membership,
		owner:      ownerToken,
		auth:       groupauth.New(),
		store:      kb.New(p),
		log:        log,
	}
}

// KB exposes the underlying store (for the CLI's local status/close).
func (s *Server) KB() *kb.KB { return s.store }

// Approve links a pending user_code to the group (owner action). Exposed
// so the owner CLI can approve locally without a round-trip token.
func (s *Server) Approve(userCode string) error {
	return s.auth.Approve(s.prog.ID, userCode)
}

// Handler returns the HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// Device-auth (public — the device_code is the secret, RFC 8628).
	mux.HandleFunc("POST /auth/device/code", s.handleDeviceCode)
	mux.HandleFunc("POST /auth/device/token", s.handleDeviceToken)
	// Owner-only approval.
	mux.HandleFunc("POST /auth/device/approve", s.ownerOnly(s.handleApprove))

	// Membership-gated KB.
	mux.HandleFunc("GET /task", s.member(s.handleTask))
	mux.HandleFunc("GET /champion", s.member(s.handleChampion))
	mux.HandleFunc("POST /results", s.member(s.handleResults))
	mux.HandleFunc("GET /status", s.member(s.handleStatus))
	return mux
}

// --- device-auth handlers ---

func (s *Server) handleDeviceCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Worker string `json:"worker"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	grant, err := s.auth.RequestCode(body.Worker)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "failed to create code")
		return
	}
	// Open membership: auto-approve so the worker is admitted without the
	// owner acting (the program chose to be public-join).
	if s.membership == MembershipOpen {
		_ = s.auth.Approve(s.prog.ID, grant.UserCode)
	}
	writeJSON(w, http.StatusOK, grant)
}

func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.DeviceCode == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "device_code required")
		return
	}
	res, err := s.auth.Poll(body.DeviceCode)
	switch err {
	case nil:
		// The roster is populated authoritatively on Submit (the token does
		// not carry the worker's self-declared id).
		writeJSON(w, http.StatusOK, res)
	case groupauth.ErrExpired:
		writeErr(w, http.StatusGone, "expired_token", "device code expired")
	default:
		writeErr(w, http.StatusNotFound, "invalid_grant", "device code not found")
	}
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserCode string `json:"user_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserCode == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "user_code required")
		return
	}
	switch err := s.auth.Approve(s.prog.ID, body.UserCode); err {
	case nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
	case groupauth.ErrExpired:
		writeErr(w, http.StatusGone, "expired_code", "code expired")
	case groupauth.ErrAlreadyUsed:
		writeErr(w, http.StatusConflict, "already_used", "code already used")
	default:
		writeErr(w, http.StatusNotFound, "invalid_code", "code not found")
	}
}

// --- KB handlers (member-gated) ---

func (s *Server) handleTask(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"program":  s.store.Program(),
		"champion": s.store.Champion(),
	})
}

func (s *Server) handleChampion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"champion": s.store.Champion()})
}

func (s *Server) handleResults(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Worker string  `json:"worker"`
		Value  float64 `json:"value"`
		Output string  `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "bad result body")
		return
	}
	res, err := s.store.Submit(body.Worker, body.Value, body.Output)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_result", err.Error())
		return
	}
	s.log.Info("result submitted",
		"worker", body.Worker, "value", body.Value,
		"accepted", res.Accepted, "champion", res.Champion, "impact", res.Impact)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"program":  s.store.Program(),
		"roster":   s.store.RosterSorted(),
		"results":  s.store.Results(),
		"champion": s.store.Champion(),
		"payouts":  s.store.Payouts(),
	})
}

// --- middleware ---

// member gates a handler on a valid member token for THIS program's group.
// The owner admin token is also accepted (the owner is a superuser, so the
// owner CLI can read status without minting itself a member token).
func (s *Server) member(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "auth_required", "member token required")
			return
		}
		if s.owner != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.owner)) == 1 {
			next(w, r)
			return
		}
		gid, ok := s.auth.VerifyToken(tok)
		if !ok || gid != s.prog.ID {
			writeErr(w, http.StatusForbidden, "not_a_member", "token is not a member of this program")
			return
		}
		next(w, r)
	}
}

// ownerOnly gates a handler on the owner admin token (constant-time).
func (s *Server) ownerOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if s.owner == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.owner)) != 1 {
			writeErr(w, http.StatusUnauthorized, "owner_required", "owner token required")
			return
		}
		next(w, r)
	}
}

// --- helpers ---

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if v, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
