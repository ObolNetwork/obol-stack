package serviceoffercontroller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// canonicalAddress is the one true (EIP-55) form used for panel/exclusion
// keys throughout the eval market.
func canonicalAddress(addr string) string {
	return common.HexToAddress(addr).Hex()
}

// failingSeedSource simulates an unreachable / lying drand relay set.
type failingSeedSource struct{ calls int }

func (f *failingSeedSource) Seed(context.Context, string, time.Time) ([32]byte, monetizeapi.ServiceBountyPanelSeed, error) {
	f.calls++
	return [32]byte{}, monetizeapi.ServiceBountyPanelSeed{}, errors.New("relay down")
}

// Real quicknet beacon, recorded once from
// https://api.drand.sh/52db9b…/public/1000 (2026-06-10) and BLS-verified at
// recording time. Round 1000 is emitted at genesis + 999×3s = 1692806364.
const (
	fixtureRound      = uint64(1000)
	fixtureSignature  = "b44679b9a59af2ec876b1a6b1ad52ea9b1615fc3982b19576350f93447cb1125e342b73a8dd2bacbe47e4b6b63ed5e39"
	fixtureRandomness = "fe290beca10872ef2fb164d2aa4442de4566183ec51c56ff3cd603d930e54fdd"
	// fixtureCreatedUnix +30s lag = 1692806361 → first round strictly after
	// is round 1000 (emitted at 1692806364).
	fixtureCreatedUnix = int64(1692806331)
)

func fixtureRelay(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	wantPath := fmt.Sprintf("/%s/public/%d", quicknetChainHash, fixtureRound)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			t.Errorf("relay fetched %s, want %s", r.URL.Path, wantPath)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func fixtureBody(round uint64, randomness, signature string) string {
	return fmt.Sprintf(`{"round":%d,"randomness":"%s","signature":"%s"}`, round, randomness, signature)
}

func TestLocalSeedSource_ProvenancePinned(t *testing.T) {
	seed, provenance, err := localSeedSource{}.Seed(context.Background(), "uid-42", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if want := sha256.Sum256([]byte("uid-42")); seed != want {
		t.Fatalf("local seed must be exactly sha256(uid): got %x want %x", seed, want)
	}
	if want := (monetizeapi.ServiceBountyPanelSeed{Source: "local"}); !reflect.DeepEqual(provenance, want) {
		t.Fatalf("local provenance = %+v, want %+v", provenance, want)
	}
}

func TestNewSeedSource_EnvModeSelection(t *testing.T) {
	t.Setenv(seedModeEnv, "drand")
	if _, ok := newSeedSource().(*drandSeedSource); !ok {
		t.Fatal("OBOL_BOUNTY_SEED=drand must select the drand source")
	}
	t.Setenv(seedModeEnv, "")
	if _, ok := newSeedSource().(localSeedSource); !ok {
		t.Fatal("unset/other OBOL_BOUNTY_SEED must select the local source")
	}
	t.Setenv(seedModeEnv, "anything-else")
	if _, ok := newSeedSource().(localSeedSource); !ok {
		t.Fatal("unrecognized OBOL_BOUNTY_SEED must select the local source")
	}
}

func TestNewDrandSeedSource_RelayEnvOverride(t *testing.T) {
	t.Setenv(drandRelaysEnv, " https://relay-a.example/ ,https://relay-b.example")
	src := newDrandSeedSource(nil)
	want := []string{"https://relay-a.example", "https://relay-b.example"}
	if !reflect.DeepEqual(src.relays, want) {
		t.Fatalf("relays = %v, want %v", src.relays, want)
	}
	t.Setenv(drandRelaysEnv, "")
	if src := newDrandSeedSource(nil); !reflect.DeepEqual(src.relays, defaultDrandRelays) {
		t.Fatalf("relays = %v, want defaults %v", src.relays, defaultDrandRelays)
	}
}

func TestDrandRoundAfter(t *testing.T) {
	genesis := time.Unix(quicknetGenesisUnix, 0)
	cases := []struct {
		t    time.Time
		want uint64
	}{
		{genesis.Add(-time.Hour), 1},                        // before genesis → first beacon
		{genesis, 2},                                        // round 1 is AT genesis, not strictly after
		{genesis.Add(1 * time.Second), 2},                   // round 2 at genesis+3s
		{genesis.Add(3 * time.Second), 3},                   // exactly on round 2 → next
		{time.Unix(fixtureCreatedUnix+30, 0), fixtureRound}, // the fixture anchor
	}
	for _, c := range cases {
		if got := drandRoundAfter(c.t); got != c.want {
			t.Errorf("drandRoundAfter(%s) = %d, want %d", c.t, got, c.want)
		}
	}
}

func TestDrandSeedSource_RealFixtureVerifies(t *testing.T) {
	server := fixtureRelay(t, fixtureBody(fixtureRound, fixtureRandomness, fixtureSignature), http.StatusOK)
	src := newDrandSeedSource([]string{server.URL})

	seed, provenance, err := src.Seed(context.Background(), "uid-7", time.Unix(fixtureCreatedUnix, 0))
	if err != nil {
		t.Fatalf("Seed on the recorded quicknet beacon must verify: %v", err)
	}
	if provenance.Source != "drand" || provenance.Round != fixtureRound ||
		provenance.Randomness != fixtureRandomness || provenance.Signature != fixtureSignature {
		t.Fatalf("provenance = %+v, want the recorded beacon", provenance)
	}
	randomness, _ := hex.DecodeString(fixtureRandomness)
	if want := sha256.Sum256(append([]byte("uid-7"), randomness...)); seed != want {
		t.Fatalf("seed = %x, want sha256(uid || randomness) = %x", seed, want)
	}
}

func TestDrandSeedSource_FlippedSignatureBitFails(t *testing.T) {
	// Flip one bit in the last signature byte: 0x39 → 0x38.
	tampered := fixtureSignature[:len(fixtureSignature)-1] + "8"
	tamperedRandomness := sha256.Sum256(mustHex(t, tampered))
	server := fixtureRelay(t, fixtureBody(fixtureRound, hex.EncodeToString(tamperedRandomness[:]), tampered), http.StatusOK)
	src := newDrandSeedSource([]string{server.URL})

	_, _, err := src.Seed(context.Background(), "uid-7", time.Unix(fixtureCreatedUnix, 0))
	if err == nil {
		t.Fatal("a flipped signature bit must fail BLS verification")
	}
	if !strings.Contains(err.Error(), "BLS verify") {
		t.Fatalf("error must come from BLS verification, got: %v", err)
	}
}

func TestDrandSeedSource_TamperedRandomnessFails(t *testing.T) {
	tampered := "ff" + fixtureRandomness[2:]
	server := fixtureRelay(t, fixtureBody(fixtureRound, tampered, fixtureSignature), http.StatusOK)
	src := newDrandSeedSource([]string{server.URL})

	if _, _, err := src.Seed(context.Background(), "uid-7", time.Unix(fixtureCreatedUnix, 0)); err == nil {
		t.Fatal("relay randomness that is not sha256(signature) must be rejected")
	}
}

func TestDrandSeedSource_WrongRoundFails(t *testing.T) {
	server := fixtureRelay(t, fixtureBody(fixtureRound+1, fixtureRandomness, fixtureSignature), http.StatusOK)
	src := newDrandSeedSource([]string{server.URL})

	if _, _, err := src.Seed(context.Background(), "uid-7", time.Unix(fixtureCreatedUnix, 0)); err == nil {
		t.Fatal("a relay answering with the wrong round must be rejected")
	}
}

func TestDrandSeedSource_AllRelaysDownErrorsNoLocalFallback(t *testing.T) {
	server := fixtureRelay(t, `{"error":"boom"}`, http.StatusInternalServerError)
	src := newDrandSeedSource([]string{server.URL, server.URL})

	seed, provenance, err := src.Seed(context.Background(), "uid-7", time.Unix(fixtureCreatedUnix, 0))
	if err == nil {
		t.Fatal("drand mode must surface relay failure — NEVER fall back to the local seed")
	}
	if seed != ([32]byte{}) || provenance.Source != "" {
		t.Fatalf("failure must return zero seed/provenance, got %x / %+v", seed, provenance)
	}
}

func TestDrandSeedSource_SecondRelayServes(t *testing.T) {
	down := fixtureRelay(t, "", http.StatusBadGateway)
	up := fixtureRelay(t, fixtureBody(fixtureRound, fixtureRandomness, fixtureSignature), http.StatusOK)
	src := newDrandSeedSource([]string{down.URL, up.URL})

	_, provenance, err := src.Seed(context.Background(), "uid-7", time.Unix(fixtureCreatedUnix, 0))
	if err != nil {
		t.Fatalf("second relay must serve when the first is down: %v", err)
	}
	if provenance.Round != fixtureRound {
		t.Fatalf("provenance round = %d, want %d", provenance.Round, fixtureRound)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
