package dataset

import "sync"

// Entitlement records what a single member token may download. A
// payment-minted token is scoped to the exact version it paid for; an
// owner-admitted worker gets MaxVersion = head (full access). The raw token
// is never stored — only its SHA-256 hash (the same hash groupauth keys on),
// so the entitlement map can be persisted without holding a credential.
type Entitlement struct {
	TokenHash  string `json:"tokenHash"`
	GroupID    string `json:"groupID"`
	MaxVersion int    `json:"maxVersion"`
	PaidAtomic string `json:"paidAtomic,omitempty"`
	Label      string `json:"label,omitempty"`
}

// Entitlements is the concurrent token-hash -> Entitlement map that enforces
// the version-scope invariant the bare groupauth member() gate cannot express
// (member() only proves group membership, not which version was paid for).
type Entitlements struct {
	mu     sync.Mutex
	byHash map[string]Entitlement
}

// NewEntitlements returns an empty map.
func NewEntitlements() *Entitlements {
	return &Entitlements{byHash: map[string]Entitlement{}}
}

// Grant records (or overwrites) an entitlement.
func (e *Entitlements) Grant(ent Entitlement) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.byHash[ent.TokenHash] = ent
}

// Lookup returns the entitlement for a token hash.
func (e *Entitlements) Lookup(tokenHash string) (Entitlement, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent, ok := e.byHash[tokenHash]
	return ent, ok
}

// Allows reports whether the token hash may download the given version
// (1 <= version <= MaxVersion). An unknown token is never allowed.
func (e *Entitlements) Allows(tokenHash string, version int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	ent, ok := e.byHash[tokenHash]
	if !ok {
		return false
	}
	return version >= 1 && version <= ent.MaxVersion
}

// All returns every entitlement (for persistence), in unspecified order.
func (e *Entitlements) All() []Entitlement {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Entitlement, 0, len(e.byHash))
	for _, v := range e.byHash {
		out = append(out, v)
	}
	return out
}

// Load replaces the map from persisted entitlements (rehydration after restart).
func (e *Entitlements) Load(ents []Entitlement) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.byHash = make(map[string]Entitlement, len(ents))
	for _, ent := range ents {
		e.byHash[ent.TokenHash] = ent
	}
}
