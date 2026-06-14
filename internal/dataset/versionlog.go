// Package dataset implements the owner-hosted, membership-gated serving of
// versioned dataset artifacts: a signed, hash-chained version log
// (content-addressed by manifestHash), a token -> version entitlement map,
// and an HTTP server that streams artifacts to paying/approved members.
//
// The version log is a money/integrity primitive: each published version is
// signed by the owner's secp256k1 key (the same key used for on-chain
// registration) over a canonical, length-prefixed, domain-separated digest,
// and chained to its predecessor's signature so a third party replaying or
// reordering a fetched log is detectable offline.
package dataset

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// digestDST is the 32-byte domain-separation tag mixed into every version
// digest. It prevents a signature minted here from being replayed as a
// signature in any other obol protocol. The string is zero-padded to 32 bytes.
const digestDST = "obol/dataset/versionlog/v1"

// DatasetVersion is one immutable, signed entry in a dataset's version log.
// The human version tag is Seq (v1, v2, ...). ManifestHash is the
// content-address anchor; FileHash is the SHA-256 of the whole served
// artifact; Size binds the per-version price. PrevSig chains to the
// predecessor's Signature ("" at Seq==1).
type DatasetVersion struct {
	Seq          int       `json:"seq"`
	ManifestHash string    `json:"manifestHash"`
	FileHash     string    `json:"fileHash"`
	Size         int64     `json:"size"`
	PrevSig      string    `json:"prevSig"`
	Timestamp    time.Time `json:"timestamp"`
	Signature    string    `json:"signature"`
}

// Signer signs a 32-byte digest with the owner's key. SignerID is the
// signer's lowercased 0x EVM address (recoverable from the signature).
type Signer interface {
	SignerID() string
	SignDigest(digest [32]byte) (sigHex string, err error)
}

// Verifier recovers the signer's EVM address from a signature over a digest.
type Verifier interface {
	RecoverSigner(digest [32]byte, sigHex string) (signerID string, err error)
}

// CanonicalDigest computes the SHA-256 digest the owner signs for v, using a
// fixed-width, length-prefixed, domain-separated encoding (NOT fmt/concat):
//
//	SHA-256( DST[32] ‖ u64be(Seq) ‖ manifest[32] ‖ file[32] ‖ u64be(Size)
//	         ‖ u8(len(PrevSig)) ‖ PrevSig )
//
// ManifestHash and FileHash MUST be exactly 64 lowercase-or-mixed hex chars;
// anything else is rejected so attacker-controlled width can never shift the
// encoding.
func CanonicalDigest(v DatasetVersion) ([32]byte, error) {
	var zero [32]byte
	if v.Seq < 1 {
		return zero, fmt.Errorf("dataset: seq must be >= 1, got %d", v.Seq)
	}
	if v.Size < 0 {
		return zero, fmt.Errorf("dataset: size must be >= 0, got %d", v.Size)
	}
	mh, err := decodeSHA256Hex(v.ManifestHash)
	if err != nil {
		return zero, fmt.Errorf("dataset: manifestHash: %w", err)
	}
	fh, err := decodeSHA256Hex(v.FileHash)
	if err != nil {
		return zero, fmt.Errorf("dataset: fileHash: %w", err)
	}
	if len(v.PrevSig) > 255 {
		return zero, fmt.Errorf("dataset: prevSig too long (%d > 255)", len(v.PrevSig))
	}

	var dst [32]byte
	copy(dst[:], digestDST)

	h := sha256.New()
	h.Write(dst[:])
	var u64 [8]byte
	binary.BigEndian.PutUint64(u64[:], uint64(v.Seq))
	h.Write(u64[:])
	h.Write(mh)
	h.Write(fh)
	binary.BigEndian.PutUint64(u64[:], uint64(v.Size))
	h.Write(u64[:])
	h.Write([]byte{byte(len(v.PrevSig))})
	h.Write([]byte(v.PrevSig))

	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

// decodeSHA256Hex requires exactly 64 hex chars (a SHA-256) and returns the
// 32 raw bytes.
func decodeSHA256Hex(s string) ([]byte, error) {
	if len(s) != 64 {
		return nil, fmt.Errorf("want 64 hex chars, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Log is an append-only, in-memory sequence of signed dataset versions.
// Safe for concurrent use.
type Log struct {
	mu       sync.Mutex
	versions []DatasetVersion
}

// NewLog returns an empty log.
func NewLog() *Log { return &Log{} }

// LogFromVersions rebuilds a log from persisted versions (e.g. loaded from
// disk). The versions are NOT re-verified here; call Verify for that.
func LogFromVersions(versions []DatasetVersion) *Log {
	cp := make([]DatasetVersion, len(versions))
	copy(cp, versions)
	return &Log{versions: cp}
}

// Len returns the number of versions.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.versions)
}

// Versions returns a copy of every version in sequence order.
func (l *Log) Versions() []DatasetVersion {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DatasetVersion, len(l.versions))
	copy(out, l.versions)
	return out
}

// Head returns the latest version, or ok==false when the log is empty.
func (l *Log) Head() (DatasetVersion, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.versions) == 0 {
		return DatasetVersion{}, false
	}
	return l.versions[len(l.versions)-1], true
}

// Get returns the version with the given Seq (1-based), or ok==false.
func (l *Log) Get(seq int) (DatasetVersion, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq < 1 || seq > len(l.versions) {
		return DatasetVersion{}, false
	}
	return l.versions[seq-1], true
}

// Append builds the next version (Seq = len+1, chained to the prior
// signature), signs it with signer, and appends it. The hex digests are
// lowercased for stable byte-comparison downstream.
func (l *Log) Append(manifestHash, fileHash string, size int64, signer Signer, now time.Time) (DatasetVersion, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	prevSig := ""
	if n := len(l.versions); n > 0 {
		prevSig = l.versions[n-1].Signature
	}
	v := DatasetVersion{
		Seq:          len(l.versions) + 1,
		ManifestHash: strings.ToLower(strings.TrimSpace(manifestHash)),
		FileHash:     strings.ToLower(strings.TrimSpace(fileHash)),
		Size:         size,
		PrevSig:      prevSig,
		Timestamp:    now.UTC(),
	}
	digest, err := CanonicalDigest(v)
	if err != nil {
		return DatasetVersion{}, err
	}
	sig, err := signer.SignDigest(digest)
	if err != nil {
		return DatasetVersion{}, fmt.Errorf("dataset: sign version %d: %w", v.Seq, err)
	}
	v.Signature = sig
	l.versions = append(l.versions, v)
	return v, nil
}

// ErrEmptyLog is returned by Verify when there are no versions.
var ErrEmptyLog = errors.New("dataset: version log is empty")

// Verify walks the log v1..head and rejects it unless every entry: has a
// monotonic Seq starting at 1, chains correctly to its predecessor's
// Signature, carries a signature recoverable to expectedSigner, and has a
// valid canonical digest. This detects offline any reorder, middle removal,
// or tamper of a fetched log. (Tail truncation yields a valid shorter chain
// and is caught instead by comparing the advertised head version, not here.)
// expectedSigner == "" skips the owner-identity check (signature validity
// only).
func (l *Log) Verify(verifier Verifier, expectedSigner string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.versions) == 0 {
		return ErrEmptyLog
	}
	want := strings.ToLower(strings.TrimSpace(expectedSigner))
	prevSig := ""
	for i, v := range l.versions {
		if v.Seq != i+1 {
			return fmt.Errorf("dataset: version at index %d has non-monotonic seq %d", i, v.Seq)
		}
		if v.PrevSig != prevSig {
			return fmt.Errorf("dataset: version %d: chain break (prevSig does not match predecessor)", v.Seq)
		}
		digest, err := CanonicalDigest(v)
		if err != nil {
			return fmt.Errorf("dataset: version %d: %w", v.Seq, err)
		}
		signer, err := verifier.RecoverSigner(digest, v.Signature)
		if err != nil {
			return fmt.Errorf("dataset: version %d: bad signature: %w", v.Seq, err)
		}
		if want != "" && strings.ToLower(signer) != want {
			return fmt.Errorf("dataset: version %d: signed by %s, want owner %s", v.Seq, signer, want)
		}
		prevSig = v.Signature
	}
	return nil
}
