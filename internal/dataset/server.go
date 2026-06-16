package dataset

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/research/groupauth"
)

// Membership modes (mirror internal/research/server).
const (
	MembershipOpen   = "open"
	MembershipInvite = "invite"
)

// Config builds a Server. Log/Ents/Store/Artifacts are owned by the caller so
// the CLI can rehydrate them from disk before serving.
type Config struct {
	ID          string // dataset id; appears in the route and as the default group id
	GroupID     string // membership group id (defaults to ID)
	Membership  string // open | invite (default invite)
	OwnerToken  string // gates owner-only routes; also a download superuser
	OwnerSigner string // 0x address that must have signed the version log (verify pins it)
	Log         *Log
	Ents        *Entitlements
	Store       *Store
	Artifacts   Artifacts
	// PaidJoin, when non-nil, wraps the /dataset/{id}/join/paid route with an
	// x402 payment gate (verify + in-process settle). Reaching the handler
	// therefore means a real on-chain payment occurred — the handler never
	// trusts client-supplied payment/version headers. nil disables paid join.
	PaidJoin func(http.Handler) http.Handler
	// JoinPriceAtomic is the atomic-unit join price recorded on a paid
	// entitlement (the gate enforces it; this value is for the ledger only).
	JoinPriceAtomic string
	Logger          *slog.Logger
}

// Server hosts one versioned dataset over an owner-run, membership-gated HTTP
// surface. Bytes never leave the owner machine un-gated.
type Server struct {
	id         string
	groupID    string
	membership string
	owner      string
	ownerSig   string
	auth       *groupauth.Authority
	log        *Log
	ents       *Entitlements
	store      *Store
	artifacts  Artifacts
	paidJoin   func(http.Handler) http.Handler
	joinAtomic string
	logger     *slog.Logger
}

// NewServer builds a Server from cfg, rehydrating the in-memory groupauth
// Authority from any persisted entitlements so paying members survive a
// restart.
func NewServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Membership == "" {
		cfg.Membership = MembershipInvite
	}
	if cfg.GroupID == "" {
		cfg.GroupID = cfg.ID
	}
	if cfg.Log == nil {
		cfg.Log = NewLog()
	}
	if cfg.Ents == nil {
		cfg.Ents = NewEntitlements()
	}
	s := &Server{
		id:         cfg.ID,
		groupID:    cfg.GroupID,
		membership: cfg.Membership,
		owner:      cfg.OwnerToken,
		ownerSig:   strings.ToLower(strings.TrimSpace(cfg.OwnerSigner)),
		auth:       groupauth.New(),
		log:        cfg.Log,
		ents:       cfg.Ents,
		store:      cfg.Store,
		artifacts:  cfg.Artifacts,
		paidJoin:   cfg.PaidJoin,
		joinAtomic: cfg.JoinPriceAtomic,
		logger:     cfg.Logger,
	}
	// Rehydrate groupauth from persisted entitlements (verified by hash; the
	// raw token is never needed).
	for _, ent := range s.ents.All() {
		s.auth.RegisterHash(ent.TokenHash, ent.GroupID, ent.Label)
	}
	return s
}

// Handler returns the HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	// Device-auth (public — the device_code is the secret, RFC 8628).
	mux.HandleFunc("POST /auth/device/code", s.handleDeviceCode)
	mux.HandleFunc("POST /auth/device/token", s.handleDeviceToken)
	mux.HandleFunc("POST /auth/device/approve", s.ownerOnly(s.handleApprove))

	// Paid join — the x402 gate proves (and settles) a real on-chain payment
	// before the handler mints a version-scoped member token. With no gate
	// configured the route is disabled, not open: paid join fails closed.
	if s.paidJoin != nil {
		mux.Handle("POST /dataset/{id}/join/paid", s.paidJoin(http.HandlerFunc(s.handleJoinPaid)))
	} else {
		mux.HandleFunc("POST /dataset/{id}/join/paid", func(w http.ResponseWriter, _ *http.Request) {
			writeErr(w, http.StatusServiceUnavailable, "paid_join_disabled", "paid join not configured (publish with --price)")
		})
	}

	// Member-gated reads.
	mux.HandleFunc("GET /dataset/{id}/versions", s.member(s.handleVersions))
	mux.HandleFunc("GET /dataset/{id}/verify", s.member(s.handleVerify))

	// Entitlement-gated (member + version) download with Range support.
	mux.HandleFunc("GET /dataset/{id}/download", s.downloadGate(s.handleDownload))

	// Owner-only operational view.
	mux.HandleFunc("GET /dataset/{id}/status", s.ownerOnly(s.handleStatus))
	return mux
}

// --- device-auth (mirrors internal/research/server) ---

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
	if s.membership == MembershipOpen {
		_ = s.auth.Approve(s.groupID, grant.UserCode)
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
		// A freshly-issued device-auth token is an owner-admitted worker:
		// grant full access (MaxVersion = head) and persist it.
		if res.Token != "" {
			head := 0
			if h, ok := s.log.Head(); ok {
				head = h.Seq
			}
			s.ents.Grant(Entitlement{
				TokenHash:  groupauth.HashToken(res.Token),
				GroupID:    s.groupID,
				MaxVersion: head,
				Label:      "worker",
			})
			s.persist()
		}
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
	switch err := s.auth.Approve(s.groupID, body.UserCode); err {
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

// --- paid join ---

func (s *Server) handleJoinPaid(w http.ResponseWriter, r *http.Request) {
	if !s.matchID(r) {
		writeErr(w, http.StatusNotFound, "unknown_dataset", "no such dataset on this host")
		return
	}
	// Reaching here means the x402 gate wrapping this route has already
	// verified (and is settling) a real on-chain payment for the join price.
	// The version to entitle is taken from the request URL, validated against
	// the published versions — NEVER from a client-supplied trust header.
	version, ok := s.resolveVersion(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown_version", "requested version is not published")
		return
	}
	raw, hash, err := s.auth.Mint(s.groupID, "paid-v"+strconv.Itoa(version))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "server_error", "failed to mint token")
		return
	}
	s.ents.Grant(Entitlement{
		TokenHash:  hash,
		GroupID:    s.groupID,
		MaxVersion: version,
		PaidAtomic: s.joinAtomic,
		Label:      "paid",
	})
	s.persist()
	s.logger.Info("paid join", "dataset", s.id, "version", version, "atomic", s.joinAtomic)
	writeJSON(w, http.StatusOK, map[string]any{"token": raw, "version": version})
}

// --- member-gated reads ---

func (s *Server) handleVersions(w http.ResponseWriter, r *http.Request) {
	if !s.matchID(r) {
		writeErr(w, http.StatusNotFound, "unknown_dataset", "no such dataset on this host")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       s.id,
		"versions": s.log.Versions(),
	})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.matchID(r) {
		writeErr(w, http.StatusNotFound, "unknown_dataset", "no such dataset on this host")
		return
	}
	err := s.log.Verify(EthVerifier{}, s.ownerSig)
	head := 0
	if h, ok := s.log.Head(); ok {
		head = h.Seq
	}
	resp := map[string]any{"valid": err == nil, "head": head, "owner": s.ownerSig}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- download ---

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, version int) {
	v, ok := s.log.Get(version)
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown_version", fmt.Sprintf("version %d not published", version))
		return
	}
	if s.artifacts == nil {
		writeErr(w, http.StatusServiceUnavailable, "no_artifacts", "artifact source not configured")
		return
	}
	content, modtime, closeFn, err := s.artifacts.Open(version)
	if err != nil {
		writeErr(w, http.StatusNotFound, "artifact_missing", err.Error())
		return
	}
	defer closeFn()

	// Whole-artifact commitments: sent on 200 AND 206 alike, so a resumed or
	// multi-connection download verifies against the full-file hash after
	// reassembly (the hash is of the whole file, never a chunk).
	w.Header().Set("X-Dataset-Version", strconv.Itoa(v.Seq))
	w.Header().Set("X-Dataset-Manifest-Hash", v.ManifestHash)
	w.Header().Set("X-Dataset-File-Hash", v.FileHash)
	w.Header().Set("Content-Type", "application/x-ndjson")

	// http.ServeContent handles Accept-Ranges, Range -> 206, Content-Range,
	// and conditional requests for us.
	http.ServeContent(w, r, fmt.Sprintf("%s-v%d.jsonl", s.id, v.Seq), modtime, content)
}

// downloadGate enforces group membership AND version entitlement before
// handing the resolved version to next. The owner token is a superuser.
func (s *Server) downloadGate(next func(http.ResponseWriter, *http.Request, int)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.matchID(r) {
			writeErr(w, http.StatusNotFound, "unknown_dataset", "no such dataset on this host")
			return
		}
		tok := bearer(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "auth_required", "member token required")
			return
		}
		version, ok := s.resolveVersion(r)
		if !ok {
			writeErr(w, http.StatusNotFound, "unknown_version", "requested version is not published")
			return
		}
		if s.isOwner(tok) {
			next(w, r, version)
			return
		}
		gid, ok := s.auth.VerifyToken(tok)
		if !ok || gid != s.groupID {
			writeErr(w, http.StatusForbidden, "not_a_member", "token is not a member of this dataset")
			return
		}
		if !s.ents.Allows(groupauth.HashToken(tok), version) {
			writeErr(w, http.StatusForbidden, "version_not_entitled",
				fmt.Sprintf("token is not entitled to version %d", version))
			return
		}
		next(w, r, version)
	}
}

// resolveVersion returns the requested ?version=N (default: head). ok==false
// when the log is empty or N is out of range.
func (s *Server) resolveVersion(r *http.Request) (int, bool) {
	head, ok := s.log.Head()
	if !ok {
		return 0, false
	}
	q := strings.TrimSpace(r.URL.Query().Get("version"))
	if q == "" {
		return head.Seq, true
	}
	n, err := strconv.Atoi(q)
	if err != nil || n < 1 || n > head.Seq {
		return 0, false
	}
	return n, true
}

// --- owner view ---

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.matchID(r) {
		writeErr(w, http.StatusNotFound, "unknown_dataset", "no such dataset on this host")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           s.id,
		"groupID":      s.groupID,
		"membership":   s.membership,
		"versions":     s.log.Versions(),
		"entitlements": len(s.ents.All()),
	})
}

// --- middleware + helpers ---

func (s *Server) member(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "auth_required", "member token required")
			return
		}
		if s.isOwner(tok) {
			next(w, r)
			return
		}
		gid, ok := s.auth.VerifyToken(tok)
		if !ok || gid != s.groupID {
			writeErr(w, http.StatusForbidden, "not_a_member", "token is not a member of this dataset")
			return
		}
		next(w, r)
	}
}

func (s *Server) ownerOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isOwner(bearer(r)) {
			writeErr(w, http.StatusUnauthorized, "owner_required", "owner token required")
			return
		}
		next(w, r)
	}
}

func (s *Server) isOwner(tok string) bool {
	return s.owner != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.owner)) == 1
}

// matchID returns true when the {id} path value addresses this dataset.
func (s *Server) matchID(r *http.Request) bool {
	id := r.PathValue("id")
	return id == "" || id == s.id
}

// persist snapshots the log + entitlements to the backing store (best-effort;
// a persistence failure is logged but does not fail the request — the
// in-memory state is authoritative for the live process).
func (s *Server) persist() {
	if s.store == nil {
		return
	}
	st := State{
		ID:           s.id,
		GroupID:      s.groupID,
		Versions:     s.log.Versions(),
		Entitlements: s.ents.All(),
	}
	if err := s.store.Save(st); err != nil {
		s.logger.Error("dataset persist failed", "dataset", s.id, "err", err)
	}
}

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
