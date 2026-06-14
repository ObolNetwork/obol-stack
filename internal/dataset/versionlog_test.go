package dataset

import (
	"strings"
	"testing"
	"time"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hashC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func newTestSigner(t *testing.T) *EthSigner {
	t.Helper()
	key, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return NewEthSigner(key)
}

var fixedTime = time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)

func TestCanonicalDigest_RejectsMalformedHashes(t *testing.T) {
	tests := []struct {
		name string
		v    DatasetVersion
	}{
		{"short manifest", DatasetVersion{Seq: 1, ManifestHash: "abcd", FileHash: hashB}},
		{"non-hex manifest", DatasetVersion{Seq: 1, ManifestHash: strings.Repeat("z", 64), FileHash: hashB}},
		{"short file", DatasetVersion{Seq: 1, ManifestHash: hashA, FileHash: "ff"}},
		{"empty file", DatasetVersion{Seq: 1, ManifestHash: hashA, FileHash: ""}},
		{"seq zero", DatasetVersion{Seq: 0, ManifestHash: hashA, FileHash: hashB}},
		{"negative size", DatasetVersion{Seq: 1, ManifestHash: hashA, FileHash: hashB, Size: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CanonicalDigest(tt.v); err == nil {
				t.Errorf("CanonicalDigest(%+v) = nil err, want rejection", tt.v)
			}
		})
	}
}

func TestCanonicalDigest_DeterministicAndFieldSensitive(t *testing.T) {
	base := DatasetVersion{Seq: 2, ManifestHash: hashA, FileHash: hashB, Size: 1048576, PrevSig: "deadbeef"}
	d1, err := CanonicalDigest(base)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	d2, _ := CanonicalDigest(base)
	if d1 != d2 {
		t.Fatal("digest not deterministic for identical input")
	}

	// Each field change must move the digest (no canonicalization collision).
	mutated := []DatasetVersion{
		{Seq: 3, ManifestHash: hashA, FileHash: hashB, Size: 1048576, PrevSig: "deadbeef"},
		{Seq: 2, ManifestHash: hashC, FileHash: hashB, Size: 1048576, PrevSig: "deadbeef"},
		{Seq: 2, ManifestHash: hashA, FileHash: hashC, Size: 1048576, PrevSig: "deadbeef"},
		{Seq: 2, ManifestHash: hashA, FileHash: hashB, Size: 1048577, PrevSig: "deadbeef"},
		{Seq: 2, ManifestHash: hashA, FileHash: hashB, Size: 1048576, PrevSig: "cafebabe"},
	}
	for i, m := range mutated {
		d, err := CanonicalDigest(m)
		if err != nil {
			t.Fatalf("mutated[%d] digest: %v", i, err)
		}
		if d == d1 {
			t.Errorf("mutated[%d] produced the same digest as base — field not bound", i)
		}
	}
}

// TestCanonicalDigest_NoConcatAmbiguity proves the length-prefix defeats the
// classic concat footgun: moving a char across the Size/PrevSig boundary must
// change the digest. (Two versions whose naive ":"-join would collide.)
func TestCanonicalDigest_NoConcatAmbiguity(t *testing.T) {
	a := DatasetVersion{Seq: 1, ManifestHash: hashA, FileHash: hashB, Size: 12, PrevSig: "3abc"}
	b := DatasetVersion{Seq: 1, ManifestHash: hashA, FileHash: hashB, Size: 123, PrevSig: "abc"}
	da, _ := CanonicalDigest(a)
	db, _ := CanonicalDigest(b)
	if da == db {
		t.Fatal("length-prefix failed: ambiguous Size/PrevSig boundary collided")
	}
}

func TestLog_AppendChainsAndVerifies(t *testing.T) {
	signer := newTestSigner(t)
	log := NewLog()

	v1, err := log.Append(hashA, hashA, 100, signer, fixedTime)
	if err != nil {
		t.Fatalf("append v1: %v", err)
	}
	if v1.Seq != 1 || v1.PrevSig != "" {
		t.Errorf("v1 seq=%d prevSig=%q, want 1 and empty", v1.Seq, v1.PrevSig)
	}
	v2, err := log.Append(hashB, hashB, 200, signer, fixedTime)
	if err != nil {
		t.Fatalf("append v2: %v", err)
	}
	if v2.Seq != 2 || v2.PrevSig != v1.Signature {
		t.Errorf("v2 not chained to v1: prevSig=%q want %q", v2.PrevSig, v1.Signature)
	}
	if _, err := log.Append(hashC, hashC, 300, signer, fixedTime); err != nil {
		t.Fatalf("append v3: %v", err)
	}

	if err := log.Verify(EthVerifier{}, signer.SignerID()); err != nil {
		t.Errorf("Verify on a clean chain failed: %v", err)
	}
	if head, ok := log.Head(); !ok || head.Seq != 3 {
		t.Errorf("Head seq = %v (ok=%v), want 3", head.Seq, ok)
	}
}

func TestLog_Verify_DetectsTamper(t *testing.T) {
	signer := newTestSigner(t)
	log := NewLog()
	_, _ = log.Append(hashA, hashA, 100, signer, fixedTime)
	_, _ = log.Append(hashB, hashB, 200, signer, fixedTime)

	// Mutate a field on the persisted copy and rebuild a log from it.
	versions := log.Versions()
	versions[1].Size = 999999 // attacker inflates the size after signing
	tampered := LogFromVersions(versions)
	if err := tampered.Verify(EthVerifier{}, signer.SignerID()); err == nil {
		t.Error("Verify accepted a tampered Size — signature not binding")
	}
}

func TestLog_Verify_DetectsReorderAndMiddleRemoval(t *testing.T) {
	signer := newTestSigner(t)
	log := NewLog()
	_, _ = log.Append(hashA, hashA, 100, signer, fixedTime)
	_, _ = log.Append(hashB, hashB, 200, signer, fixedTime)
	_, _ = log.Append(hashC, hashC, 300, signer, fixedTime)
	versions := log.Versions()

	t.Run("reorder", func(t *testing.T) {
		swapped := []DatasetVersion{versions[0], versions[2], versions[1]}
		if err := LogFromVersions(swapped).Verify(EthVerifier{}, signer.SignerID()); err == nil {
			t.Error("Verify accepted a reordered log")
		}
	})
	t.Run("middle removal", func(t *testing.T) {
		gapped := []DatasetVersion{versions[0], versions[2]}
		if err := LogFromVersions(gapped).Verify(EthVerifier{}, signer.SignerID()); err == nil {
			t.Error("Verify accepted a log with a removed middle entry")
		}
	})
}

func TestLog_Verify_RejectsWrongSigner(t *testing.T) {
	owner := newTestSigner(t)
	attacker := newTestSigner(t)
	log := NewLog()
	_, _ = log.Append(hashA, hashA, 100, attacker, fixedTime) // signed by the wrong key

	if err := log.Verify(EthVerifier{}, owner.SignerID()); err == nil {
		t.Error("Verify accepted a version signed by a non-owner key")
	}
	// ...but accepts it when we don't pin the owner (signature-validity only).
	if err := log.Verify(EthVerifier{}, ""); err != nil {
		t.Errorf("Verify with no owner pin rejected a structurally valid sig: %v", err)
	}
}

func TestLog_Verify_EmptyLog(t *testing.T) {
	if err := NewLog().Verify(EthVerifier{}, ""); err != ErrEmptyLog {
		t.Errorf("empty log Verify = %v, want ErrEmptyLog", err)
	}
}

// FuzzCanonicalDigest asserts the encoder never panics on arbitrary hash
// inputs and is total: valid 64-hex pairs always digest, everything else is a
// clean error.
func FuzzCanonicalDigest(f *testing.F) {
	f.Add(hashA, hashB, int64(100), "")
	f.Add("abcd", "", int64(0), "ff")
	f.Add(strings.Repeat("0", 64), strings.Repeat("f", 64), int64(1<<40), strings.Repeat("9", 130))
	f.Fuzz(func(t *testing.T, manifest, file string, size int64, prevSig string) {
		v := DatasetVersion{Seq: 1, ManifestHash: manifest, FileHash: file, Size: size, PrevSig: prevSig}
		_, err := CanonicalDigest(v)
		valid := len(manifest) == 64 && len(file) == 64 &&
			isHex(manifest) && isHex(file) && size >= 0 && len(prevSig) <= 255
		if valid && err != nil {
			t.Errorf("valid input rejected: %v", err)
		}
		if !valid && err == nil {
			t.Errorf("invalid input accepted: manifest=%q file=%q size=%d prevSigLen=%d", manifest, file, size, len(prevSig))
		}
	})
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
