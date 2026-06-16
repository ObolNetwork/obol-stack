package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

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
