package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"

	x402types "github.com/x402-foundation/x402/go/types"
)

// SignPaymentFunc signs an x402 payment for a requirement and returns the
// base64 X-PAYMENT header value. Injected so the dataset client stays decoupled
// from the concrete signer (the CLI passes x402.SignExactPayment).
type SignPaymentFunc func(req x402types.PaymentRequirements) (string, error)

// JoinOptions configures a paid join (pay the seller's x402 price to mint a
// version-scoped member token).
type JoinOptions struct {
	BaseURL   string
	ID        string
	Version   int    // 0 = head
	MaxAtomic string // optional safety cap on the join price, in atomic units
	Client    *http.Client
}

// JoinResult reports a completed paid join.
type JoinResult struct {
	Token   string
	Version int
	Amount  string // atomic units paid
	PayTo   string
	Network string
}

// JoinPaid pays the seller's x402 join price to mint a version-scoped member
// token: it probes the /join/paid 402 challenge, signs the advertised payment
// with sign, and POSTs it. Fully host-side and peer-to-peer — no cluster,
// sidecar, or remote signer needed.
func JoinPaid(ctx context.Context, opts JoinOptions, sign SignPaymentFunc) (JoinResult, error) {
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	url := strings.TrimSuffix(opts.BaseURL, "/") + "/dataset/" + opts.ID + "/join/paid"
	if opts.Version > 0 {
		url += "?version=" + strconv.Itoa(opts.Version)
	}

	// 1. Probe for the 402 challenge.
	probe, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return JoinResult{}, err
	}
	resp, err := opts.Client.Do(probe)
	if err != nil {
		return JoinResult{}, err
	}
	pr, err := decodeJoinChallenge(resp)
	if err != nil {
		return JoinResult{}, err
	}
	if opts.MaxAtomic != "" {
		limit, ok1 := new(big.Int).SetString(opts.MaxAtomic, 10)
		price, ok2 := new(big.Int).SetString(pr.Amount, 10)
		if ok1 && ok2 && price.Cmp(limit) > 0 {
			return JoinResult{}, fmt.Errorf("dataset: join price %s exceeds --max-price %s (atomic units)", pr.Amount, opts.MaxAtomic)
		}
	}

	// 2. Sign the advertised payment, then 3. POST it to mint the token.
	xpay, err := sign(pr)
	if err != nil {
		return JoinResult{}, fmt.Errorf("dataset: sign join payment: %w", err)
	}
	payReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return JoinResult{}, err
	}
	payReq.Header.Set("X-PAYMENT", xpay)
	payResp, err := opts.Client.Do(payReq)
	if err != nil {
		return JoinResult{}, err
	}
	defer payResp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(payResp.Body, 1<<16))
	if payResp.StatusCode != http.StatusOK {
		return JoinResult{}, fmt.Errorf("dataset: paid join %s -> %d: %s", url, payResp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Token   string `json:"token"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return JoinResult{}, fmt.Errorf("dataset: paid join returned no token: %s", strings.TrimSpace(string(body)))
	}
	return JoinResult{Token: out.Token, Version: out.Version, Amount: pr.Amount, PayTo: pr.PayTo, Network: pr.Network}, nil
}

// decodeJoinChallenge reads the seller's 402 paid-join challenge and returns
// the first advertised payment requirement.
func decodeJoinChallenge(resp *http.Response) (x402types.PaymentRequirements, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPaymentRequired {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return x402types.PaymentRequirements{}, fmt.Errorf("dataset: expected a 402 paid-join challenge, got %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var challenge struct {
		Accepts []x402types.PaymentRequirements `json:"accepts"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&challenge); err != nil {
		return x402types.PaymentRequirements{}, fmt.Errorf("dataset: decode 402 challenge: %w", err)
	}
	if len(challenge.Accepts) == 0 {
		return x402types.PaymentRequirements{}, fmt.Errorf("dataset: 402 challenge carried no accepts[]")
	}
	return challenge.Accepts[0], nil
}

// FetchResult reports what a verified download produced.
type FetchResult struct {
	Version      int
	ManifestHash string
	FileHash     string
	Bytes        int64
	Resumed      bool
}

// FetchOptions configures a verified, resumable dataset download.
type FetchOptions struct {
	BaseURL string // e.g. https://host (no trailing slash, no /dataset suffix)
	ID      string
	Version int // 0 = server head
	Token   string
	OutPath string
	Client  *http.Client
	// ExpectedOwner, when set, pins the 0x address that must have signed every
	// entry in the version log. Empty still verifies signatures + chain
	// linkage, but skips the owner-identity check (use it to defeat a seller
	// that swapped in a different signing key).
	ExpectedOwner string
}

// Fetch downloads a dataset version to OutPath with HTTP Range resume and
// verifies the whole-file SHA-256 against the OWNER-SIGNED version log — not a
// response header a malicious seller controls. It first fetches and verifies
// the signed chain (pinning ExpectedOwner when set), takes the authoritative
// file-hash commitment from it, then downloads and compares the reassembled
// whole file to that. A partial OutPath+".part" from an earlier interrupted
// run is resumed rather than restarted.
func Fetch(ctx context.Context, opts FetchOptions) (FetchResult, error) {
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}

	// Integrity is anchored in the signed log, so resolve the target version's
	// signed commitment BEFORE trusting any served bytes.
	want, err := resolveSignedVersion(ctx, opts)
	if err != nil {
		return FetchResult{}, err
	}
	expectedHash := strings.ToLower(want.FileHash)

	part := opts.OutPath + ".part"
	have := int64(0)
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	resumed := have > 0

	url := strings.TrimSuffix(opts.BaseURL, "/") + "/dataset/" + opts.ID + "/download?version=" + strconv.Itoa(want.Seq)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FetchResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+opts.Token)
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}

	resp, err := opts.Client.Do(req)
	if err != nil {
		return FetchResult{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored Range (or fresh start): rewrite from scratch.
		have = 0
		resumed = false
	case http.StatusPartialContent:
		// Append to the existing .part.
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return FetchResult{}, fmt.Errorf("dataset: download %s -> %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	flag := os.O_CREATE | os.O_WRONLY
	if have > 0 {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return FetchResult{}, err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return FetchResult{}, fmt.Errorf("dataset: stream body: %w", err)
	}
	if err := f.Close(); err != nil {
		return FetchResult{}, err
	}

	// Verify the reassembled whole file against the SIGNED commitment.
	got, size, err := hashFile(part)
	if err != nil {
		return FetchResult{}, err
	}
	if got != expectedHash {
		return FetchResult{}, fmt.Errorf("dataset: file hash mismatch: got %s, signed version log commits %s (corrupt or tampered)", got, expectedHash)
	}
	if err := os.Rename(part, opts.OutPath); err != nil {
		return FetchResult{}, fmt.Errorf("dataset: finalize download: %w", err)
	}
	return FetchResult{Version: want.Seq, ManifestHash: want.ManifestHash, FileHash: expectedHash, Bytes: size, Resumed: resumed}, nil
}

// resolveSignedVersion fetches the seller's version log, verifies the chain
// (signatures, linkage, and the pinned owner when set), and returns the
// requested version's signed entry — the authoritative file-hash commitment.
func resolveSignedVersion(ctx context.Context, opts FetchOptions) (DatasetVersion, error) {
	url := strings.TrimSuffix(opts.BaseURL, "/") + "/dataset/" + opts.ID + "/versions"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DatasetVersion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+opts.Token)

	resp, err := opts.Client.Do(req)
	if err != nil {
		return DatasetVersion{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return DatasetVersion{}, fmt.Errorf("dataset: versions %s -> %d: %s", url, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Versions []DatasetVersion `json:"versions"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return DatasetVersion{}, fmt.Errorf("dataset: decode versions: %w", err)
	}

	log := LogFromVersions(payload.Versions)
	if err := log.Verify(EthVerifier{}, opts.ExpectedOwner); err != nil {
		return DatasetVersion{}, fmt.Errorf("dataset: version log failed verification: %w", err)
	}

	if opts.Version > 0 {
		v, ok := log.Get(opts.Version)
		if !ok {
			return DatasetVersion{}, fmt.Errorf("dataset: version %d not present in signed log", opts.Version)
		}
		return v, nil
	}
	h, ok := log.Head()
	if !ok {
		return DatasetVersion{}, fmt.Errorf("dataset: signed version log is empty")
	}
	return h, nil
}

// VerifyFile recomputes a file's SHA-256 and compares it to want.
func VerifyFile(path, want string) error {
	got, _, err := hashFile(path)
	if err != nil {
		return err
	}
	if got != strings.ToLower(want) {
		return fmt.Errorf("dataset: hash mismatch: got %s, want %s", got, want)
	}
	return nil
}
