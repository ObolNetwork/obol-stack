// Package groupauth is the membership/OAuth layer for decentralized
// auto-research groups: it issues and verifies the tokens that gate a
// research program's PRIVATE collective knowledge base.
//
// It is an RFC 8628-style device-authorization flow adapted from the
// Darkbloom / d-inference coordinator (coordinator/api/device_auth.go) —
// same shape, research-group semantics:
//
//  1. A worker agent calls RequestCode()             → device_code + user_code
//  2. The PROGRAM OWNER approves the user_code        → links it to the group
//     (this is the membership decision — "coordination at the OAuth level")
//  3. The worker polls Poll(device_code)              → a member token
//  4. The worker presents the token to the program's  → read/write the
//     private knowledge-base service                      group-private KB
//
// The store is in-memory and dependency-free on purpose: a research
// program is a small, single-coordinator group, and keeping this package
// free of cluster/store deps lets the serviceoffer-controller embed it
// (or a sidecar mount it) without pulling the whole coordinator in. Tokens
// are persisted only as SHA-256 hashes; the raw token is returned once.
package groupauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"
)

const (
	// CodeExpiry is how long an unused device code stays valid.
	CodeExpiry = 15 * time.Minute
	// PollInterval is the minimum seconds a worker should wait between polls.
	PollInterval = 5
	// tokenPrefix namespaces member tokens (mirrors the d-inference prefix
	// convention so tokens are self-identifying in logs).
	tokenPrefix = "obol-research-mt-"
	// userCharset excludes ambiguous glyphs (0/O, 1/I/L).
	userCharset = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
)

// Status values for a device code.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
)

var (
	// ErrNotFound is returned when a device code or token is unknown.
	ErrNotFound = errors.New("groupauth: not found")
	// ErrExpired is returned when a device code has passed its expiry.
	ErrExpired = errors.New("groupauth: device code expired")
	// ErrAlreadyUsed is returned when approving a non-pending code.
	ErrAlreadyUsed = errors.New("groupauth: code already used")
)

// deviceCode is an in-flight authorization request.
type deviceCode struct {
	DeviceCode string
	UserCode   string
	Status     string
	GroupID    string // the ResearchProgram id, set on approval
	WorkerID   string // optional self-declared worker label
	ExpiresAt  time.Time
}

// memberToken is an issued, hashed membership credential.
type memberToken struct {
	TokenHash string
	GroupID   string
	Label     string
	IssuedAt  time.Time
	Active    bool
}

// CodeGrant is returned to a worker starting a login.
type CodeGrant struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	ExpiresIn  int    `json:"expires_in"`
	Interval   int    `json:"interval"`
}

// PollResult is returned while a worker polls for approval.
type PollResult struct {
	// Status is "authorization_pending" until approved, then "authorized".
	Status string `json:"status"`
	// Token is set only once, when Status first becomes "authorized".
	Token   string `json:"token,omitempty"`
	GroupID string `json:"group_id,omitempty"`
}

// Authority issues and verifies a single research group's membership
// tokens. One Authority per ResearchProgram; safe for concurrent use.
type Authority struct {
	mu       sync.Mutex
	byDevice map[string]*deviceCode
	byUser   map[string]*deviceCode
	tokens   map[string]*memberToken // keyed by token hash
	now      func() time.Time        // injectable clock for tests
}

// New returns an empty Authority.
func New() *Authority {
	return &Authority{
		byDevice: map[string]*deviceCode{},
		byUser:   map[string]*deviceCode{},
		tokens:   map[string]*memberToken{},
		now:      time.Now,
	}
}

// RequestCode starts a device login for a worker. workerID is an optional
// self-declared label (e.g. an address or agent name) recorded for the
// owner's approval decision; it is NOT trusted as identity.
func (a *Authority) RequestCode(workerID string) (CodeGrant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	dcVal, err := randomHex(32)
	if err != nil {
		return CodeGrant{}, err
	}
	userCode, err := generateUserCode()
	if err != nil {
		return CodeGrant{}, err
	}
	// Resolve the rare user-code collision before storing.
	for i := 0; i < 5; i++ {
		if _, taken := a.byUser[userCode]; !taken {
			break
		}
		if userCode, err = generateUserCode(); err != nil {
			return CodeGrant{}, err
		}
	}

	dc := &deviceCode{
		DeviceCode: dcVal,
		UserCode:   userCode,
		Status:     StatusPending,
		WorkerID:   strings.TrimSpace(workerID),
		ExpiresAt:  a.now().Add(CodeExpiry),
	}
	a.byDevice[dcVal] = dc
	a.byUser[userCode] = dc

	return CodeGrant{
		DeviceCode: dcVal,
		UserCode:   userCode,
		ExpiresIn:  int(CodeExpiry.Seconds()),
		Interval:   PollInterval,
	}, nil
}

// Approve is the membership decision: the program owner links a pending
// user_code to the group. Only the owner calls this (it is the gate that
// makes the knowledge base private to the group). user_code matching is
// case-insensitive and space-trimmed.
func (a *Authority) Approve(groupID, userCode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	dc, ok := a.byUser[normalizeUserCode(userCode)]
	if !ok {
		return ErrNotFound
	}
	if a.now().After(dc.ExpiresAt) {
		return ErrExpired
	}
	if dc.Status != StatusPending {
		return ErrAlreadyUsed
	}
	dc.Status = StatusApproved
	dc.GroupID = strings.TrimSpace(groupID)
	return nil
}

// Poll returns the authorization state for a device code. On the first
// poll after approval it mints and returns the raw member token exactly
// once; only its SHA-256 hash is retained.
func (a *Authority) Poll(deviceCode string) (PollResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	dc, ok := a.byDevice[deviceCode]
	if !ok {
		return PollResult{}, ErrNotFound
	}
	if a.now().After(dc.ExpiresAt) {
		return PollResult{}, ErrExpired
	}

	if dc.Status != StatusApproved {
		return PollResult{Status: "authorization_pending"}, nil
	}

	raw, err := randomHex(32)
	if err != nil {
		return PollResult{}, err
	}
	rawToken := tokenPrefix + raw
	a.tokens[hashToken(rawToken)] = &memberToken{
		TokenHash: hashToken(rawToken),
		GroupID:   dc.GroupID,
		Label:     "device-" + dc.UserCode,
		IssuedAt:  a.now(),
		Active:    true,
	}
	// Consume the code so the token is issued once.
	delete(a.byDevice, dc.DeviceCode)
	delete(a.byUser, dc.UserCode)

	return PollResult{Status: "authorized", Token: rawToken, GroupID: dc.GroupID}, nil
}

// VerifyToken reports whether rawToken is an active member token and, if
// so, the group it grants access to. This is what the private knowledge-
// base service calls on each request.
func (a *Authority) VerifyToken(rawToken string) (groupID string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	t, found := a.tokens[hashToken(rawToken)]
	if !found || !t.Active {
		return "", false
	}
	return t.GroupID, true
}

// Revoke deactivates a member token (owner removing a worker from the group).
func (a *Authority) Revoke(rawToken string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, found := a.tokens[hashToken(rawToken)]; found {
		t.Active = false
	}
}

// HashToken returns the SHA-256 hex hash of a raw member token. Exposed so a
// caller (e.g. a payment-gated service's entitlement map) can key off the
// same hash the Authority stores without ever holding the raw token.
func HashToken(rawToken string) string { return hashToken(rawToken) }

// Mint issues a member token for groupID WITHOUT the device-auth flow, for
// services where a settled payment — not an owner approval — is the
// membership decision. The raw token is returned exactly once; only its hash
// is retained (returned too, so the caller can persist it). label is a
// non-trusted descriptive tag.
func (a *Authority) Mint(groupID, label string) (rawToken, tokenHash string, err error) {
	raw, err := randomHex(32)
	if err != nil {
		return "", "", err
	}
	rawToken = tokenPrefix + raw
	tokenHash = hashToken(rawToken)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens[tokenHash] = &memberToken{
		TokenHash: tokenHash,
		GroupID:   strings.TrimSpace(groupID),
		Label:     strings.TrimSpace(label),
		IssuedAt:  a.now(),
		Active:    true,
	}
	return rawToken, tokenHash, nil
}

// RegisterHash re-registers a previously issued token by its hash, marking it
// active for groupID. It lets a persistent store rehydrate the in-memory
// Authority after a restart without ever seeing the raw token (the Authority
// verifies by hash). A blank hash is ignored; otherwise idempotent.
func (a *Authority) RegisterHash(tokenHash, groupID, label string) {
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens[tokenHash] = &memberToken{
		TokenHash: tokenHash,
		GroupID:   strings.TrimSpace(groupID),
		Label:     strings.TrimSpace(label),
		IssuedAt:  a.now(),
		Active:    true,
	}
}

// --- helpers ---

func generateUserCode() (string, error) {
	code := make([]byte, 8)
	for i := range code {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(userCharset))))
		if err != nil {
			return "", err
		}
		code[i] = userCharset[n.Int64()]
	}
	return string(code[:4]) + "-" + string(code[4:]), nil
}

func normalizeUserCode(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
