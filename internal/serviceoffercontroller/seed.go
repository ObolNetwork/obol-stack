package serviceoffercontroller

// Panel-draw randomness sources (design doc §11.4). The evaluator panel is a
// weighted lottery; whoever controls the lottery seed controls the panel, so
// the seed's provenance is recorded in status.panelSeed for auditability.
//
//   - local: sha256(bounty UID) — deterministic, free, fine for local-first
//     single-operator stacks (exactly the historical behavior).
//   - drand: the quicknet beacon FIRST round strictly after the bounty's
//     creation +30s, fetched over public HTTP relays and BLS-verified against
//     the quicknet group key. The poster cannot know the randomness when the
//     bounty is created, and the operator cannot grind it: a fetch or verify
//     failure returns an error and the panel stays unselected (requeue) — it
//     NEVER silently falls back to the local seed, because "break the relay,
//     get the predictable seed" would hand the operator a grinding lever.
//
// Mode is selected once at controller construction from OBOL_BOUNTY_SEED
// ("drand" → drand, anything else → local); relays are overridable via
// OBOL_BOUNTY_DRAND_URLS (comma-separated).

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	bls12381 "github.com/drand/kyber-bls12381"
	"github.com/drand/kyber/sign/bdn"

	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

// seedSource produces the 32-byte panel-lottery seed for a bounty plus the
// provenance record persisted into status.panelSeed.
type seedSource interface {
	Seed(ctx context.Context, uid string, created time.Time) ([32]byte, monetizeapi.ServiceBountyPanelSeed, error)
}

const (
	seedModeEnv     = "OBOL_BOUNTY_SEED"
	drandRelaysEnv  = "OBOL_BOUNTY_DRAND_URLS"
	seedSourceLocal = "local"
	seedSourceDrand = "drand"
	// seedRetryDelay is how long ensurePanel waits before re-trying a bounty
	// whose beacon fetch/verify failed.
	seedRetryDelay = 15 * time.Second
)

// newSeedSource picks the seed source from the environment. Called once at
// controller construction.
func newSeedSource() seedSource {
	if os.Getenv(seedModeEnv) == seedSourceDrand {
		return newDrandSeedSource(nil)
	}
	return localSeedSource{}
}

// localSeedSource is the historical deterministic seed: sha256(bounty UID).
type localSeedSource struct{}

func (localSeedSource) Seed(_ context.Context, uid string, _ time.Time) ([32]byte, monetizeapi.ServiceBountyPanelSeed, error) {
	return sha256.Sum256([]byte(uid)), monetizeapi.ServiceBountyPanelSeed{Source: seedSourceLocal}, nil
}

// ── drand quicknet ──────────────────────────────────────────────────────────
//
// Chain parameters verified live against https://api.drand.sh/v2/beacons/quicknet/info
// (2026-06-10): scheme bls-unchained-g1-rfc9380 — signatures on G1, group
// public key on G2, signed message = sha256(8-byte big-endian round number)
// (drand/drand crypto/schemes.go, "unchained means we're only hashing the
// round number"). randomness = sha256(signature).
//
// Relay paths: api.drand.sh serves both /v2/beacons/quicknet/rounds/<n> and
// the chain-hash path /<chain-hash>/public/<n>; drand.cloudflare.com serves
// ONLY the chain-hash path (v2 404s, verified live). The chain-hash path is
// therefore what we fetch — it works on every default relay and pins the
// chain hash into the URL itself.
const (
	quicknetChainHash    = "52db9ba70e0cc0f6eaf7803dd07447a1f5477735fd3f661792ba94600c84e971"
	quicknetGenesisUnix  = int64(1692803367)
	quicknetPeriodSec    = int64(3)
	quicknetPublicKeyHex = "83cf0f2896adee7eb8b5f01fcad3912212c437e0073e911fb90022d3e760183c8c4b450b6a0a6c3ac6a5776a2d1064510d1fec758c921cc22b0e17e63aaf4bcb5ed66304de9cf809bd274ca73bab4af5a6e9c76a4bc09e76eae8991ef5ece45a"

	// drandSeedLag: the panel draws from the first beacon strictly after
	// created+lag, so the randomness provably does not exist yet when the
	// bounty is posted.
	drandSeedLag = 30 * time.Second
)

var defaultDrandRelays = []string{"https://api.drand.sh", "https://drand.cloudflare.com"}

type drandSeedSource struct {
	relays []string
	client *http.Client
}

// newDrandSeedSource builds the quicknet-backed source. relays == nil reads
// OBOL_BOUNTY_DRAND_URLS, then falls back to the public defaults.
func newDrandSeedSource(relays []string) *drandSeedSource {
	if len(relays) == 0 {
		if env := os.Getenv(drandRelaysEnv); env != "" {
			for _, u := range strings.Split(env, ",") {
				if u = strings.TrimSpace(u); u != "" {
					relays = append(relays, strings.TrimRight(u, "/"))
				}
			}
		}
	}
	if len(relays) == 0 {
		relays = defaultDrandRelays
	}
	return &drandSeedSource{
		relays: relays,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// drandBeacon is the relay response on the chain-hash path
// (/<chain-hash>/public/<round>). Randomness is present on this path, but we
// recompute and cross-check it from the signature anyway.
type drandBeacon struct {
	Round      uint64 `json:"round"`
	Randomness string `json:"randomness"`
	Signature  string `json:"signature"`
}

// drandRoundAfter returns the first quicknet round emitted STRICTLY after t.
// Round r is emitted at genesis + (r-1)×period.
func drandRoundAfter(t time.Time) uint64 {
	d := t.Unix() - quicknetGenesisUnix
	if d < 0 {
		return 1
	}
	return uint64(d/quicknetPeriodSec) + 2
}

func (s *drandSeedSource) Seed(ctx context.Context, uid string, created time.Time) ([32]byte, monetizeapi.ServiceBountyPanelSeed, error) {
	round := drandRoundAfter(created.Add(drandSeedLag))

	var errs []error
	for _, relay := range s.relays {
		beacon, err := s.fetch(ctx, relay, round)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", relay, err))
			continue
		}
		randomness, err := verifyQuicknetBeacon(beacon, round)
		if err != nil {
			// A relay serving a beacon that fails BLS verification is lying
			// or corrupted — surface it, never trust it.
			errs = append(errs, fmt.Errorf("%s: %w", relay, err))
			continue
		}
		seed := sha256.Sum256(append([]byte(uid), randomness...))
		return seed, monetizeapi.ServiceBountyPanelSeed{
			Source:     seedSourceDrand,
			Round:      round,
			Randomness: hex.EncodeToString(randomness),
			Signature:  beacon.Signature,
		}, nil
	}
	// No silent fallback to the local seed — a broken relay must never become
	// a seed-grinding lever. The caller leaves the panel unselected and the
	// controller requeues.
	return [32]byte{}, monetizeapi.ServiceBountyPanelSeed{}, fmt.Errorf("drand round %d unavailable from all relays: %w", round, errors.Join(errs...))
}

func (s *drandSeedSource) fetch(ctx context.Context, relay string, round uint64) (*drandBeacon, error) {
	url := fmt.Sprintf("%s/%s/public/%d", strings.TrimRight(relay, "/"), quicknetChainHash, round)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var beacon drandBeacon
	if err := json.NewDecoder(resp.Body).Decode(&beacon); err != nil {
		return nil, fmt.Errorf("decode beacon: %w", err)
	}
	return &beacon, nil
}

// verifyQuicknetBeacon BLS-verifies the beacon signature against the quicknet
// group key (scheme bls-unchained-g1-rfc9380: signature on G1, key on G2,
// message = sha256(big-endian round)) and returns the verified randomness
// (sha256 of the signature).
func verifyQuicknetBeacon(beacon *drandBeacon, wantRound uint64) ([]byte, error) {
	if beacon.Round != wantRound {
		return nil, fmt.Errorf("relay returned round %d, want %d", beacon.Round, wantRound)
	}
	sig, err := hex.DecodeString(beacon.Signature)
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	suite := bls12381.NewBLS12381Suite()
	pubBytes, err := hex.DecodeString(quicknetPublicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode quicknet group key: %w", err)
	}
	pub := suite.G2().Point()
	if err := pub.UnmarshalBinary(pubBytes); err != nil {
		return nil, fmt.Errorf("unmarshal quicknet group key: %w", err)
	}

	var roundBytes [8]byte
	binary.BigEndian.PutUint64(roundBytes[:], beacon.Round)
	msg := sha256.Sum256(roundBytes[:])
	// bdn over the deprecated sign/bls: identical single-signature Verify;
	// the bls deprecation concerns rogue-key attacks on AGGREGATION, which a
	// fixed group key + single beacon signature never exercises.
	if err := bdn.NewSchemeOnG1(suite).Verify(pub, msg[:], sig); err != nil {
		return nil, fmt.Errorf("BLS verify round %d: %w", beacon.Round, err)
	}

	randomness := sha256.Sum256(sig)
	if beacon.Randomness != "" && !strings.EqualFold(beacon.Randomness, hex.EncodeToString(randomness[:])) {
		return nil, fmt.Errorf("relay randomness does not match sha256(signature) for round %d", beacon.Round)
	}
	return randomness[:], nil
}
