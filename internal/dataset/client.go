package dataset

import (
	"context"
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
}

// Fetch downloads a dataset version to OutPath with HTTP Range resume and
// verifies the whole-file SHA-256 against the X-Dataset-File-Hash header the
// server commits on every response. A partial OutPath+".part" from an earlier
// interrupted run is resumed rather than restarted. The verification is done
// once over the reassembled whole file (the hash is of the whole artifact,
// never a chunk).
func Fetch(ctx context.Context, opts FetchOptions) (FetchResult, error) {
	if opts.Client == nil {
		opts.Client = http.DefaultClient
	}
	part := opts.OutPath + ".part"

	have := int64(0)
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	resumed := have > 0

	url := strings.TrimSuffix(opts.BaseURL, "/") + "/dataset/" + opts.ID + "/download"
	if opts.Version > 0 {
		url += "?version=" + strconv.Itoa(opts.Version)
	}

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

	fileHash := strings.ToLower(resp.Header.Get("X-Dataset-File-Hash"))
	manifestHash := strings.ToLower(resp.Header.Get("X-Dataset-Manifest-Hash"))
	version, _ := strconv.Atoi(resp.Header.Get("X-Dataset-Version"))
	if fileHash == "" {
		return FetchResult{}, fmt.Errorf("dataset: server did not advertise X-Dataset-File-Hash; refusing unverifiable download")
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

	// Verify the reassembled whole file against the committed hash.
	got, size, err := hashFile(part)
	if err != nil {
		return FetchResult{}, err
	}
	if got != fileHash {
		return FetchResult{}, fmt.Errorf("dataset: file hash mismatch: got %s, advertised %s (corrupt or tampered)", got, fileHash)
	}
	if err := os.Rename(part, opts.OutPath); err != nil {
		return FetchResult{}, fmt.Errorf("dataset: finalize download: %w", err)
	}
	return FetchResult{Version: version, ManifestHash: manifestHash, FileHash: fileHash, Bytes: size, Resumed: resumed}, nil
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
