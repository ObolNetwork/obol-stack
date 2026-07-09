// Package images centralises the policy for selecting Docker image references
// in embedded Kubernetes manifests and dynamically-created workloads.
//
// Design (post digests-in-git removal):
//
//  1. Embedded templates mark stack-owned images with the PlaceholderTag
//     (`:__OBOL_IMAGE__`). Digests are never committed to git.
//  2. At apply time (CopyInfrastructure / dynamic resource creation), Resolve
//     rewrites each managed image to a short-SHA tag from version.GitCommit.
//  3. For production security, Resolve also binds the multi-arch index digest
//     from the registry (repo:sha@sha256:…), so a retagged short-SHA cannot
//     silently substitute a different image. Digest fetch is best-effort:
//     if the registry is unreachable, the short-SHA tag alone is used (still
//     privileged over :latest).
//  4. Dev mode (OBOL_DEVELOPMENT=true) uses a caller-supplied dev tag
//     (dev-<sha>) with no digest, matching the local k3d import.
//
// This replaces the old continuous repin-PR machinery: the binary's GitCommit
// and the image tag always match by construction, so a release that ships
// source after a fix always asks the cluster for images of that same commit.
package images

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/version"
)

// PlaceholderTag is the sentinel tag embedded templates use for stack-owned
// images. CopyInfrastructure rewrites every occurrence before helmfile apply.
// Leaving it unreplaced would fail image pulls loudly (fail-closed).
const PlaceholderTag = "__OBOL_IMAGE__"

// ManagedRepos that go through Resolve / RewriteTree. Keep in lockstep with
// internal/stack baseLocalImages and docker-publish-x402.yml.
var Managed = []string{
	"ghcr.io/obolnetwork/x402-verifier",
	"ghcr.io/obolnetwork/serviceoffer-controller",
	"ghcr.io/obolnetwork/x402-buyer",
	"ghcr.io/obolnetwork/job-broker",
	"ghcr.io/obolnetwork/demo-server",
	"ghcr.io/obolnetwork/obol-stack-public-storefront",
}

// Resolve returns the fully-qualified image reference for a managed repo.
//
//	images.Resolve("ghcr.io/obolnetwork/demo-server")
//	// → "ghcr.io/obolnetwork/demo-server:abc1234@sha256:…"  (prod, registry up)
//	// → "ghcr.io/obolnetwork/demo-server:abc1234"           (prod, offline)
//	// → "ghcr.io/obolnetwork/demo-server:latest"            (dev / unknown commit)
//
// Prefer ResolveDev under OBOL_DEVELOPMENT so the local import tag matches.
func Resolve(repo string) string {
	if useLatest() {
		return repo + ":latest"
	}
	return resolvePinned(repo, version.GitCommit)
}

// ResolveDev returns repo:devTag with no digest — local builds are imported
// into k3d under that exact tag.
func ResolveDev(repo, devTag string) string {
	if devTag == "" {
		devTag = "latest"
	}
	return repo + ":" + devTag
}

// StampIdentity is included in the defaults-tree stamp so upgrading the
// binary (new GitCommit) or flipping dev mode re-renders image pins even
// when embedded template bytes are unchanged.
func StampIdentity() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OBOL_DEVELOPMENT")), "true") {
		return "dev"
	}
	commit := strings.TrimSpace(version.GitCommit)
	if commit == "" || commit == "unknown" || commit == "dev" {
		return "latest"
	}
	return "sha:" + commit
}

// RewriteTree walks a directory of YAML files and rewrites every managed
// image reference via resolve. resolve receives the bare repository
// (e.g. "ghcr.io/obolnetwork/x402-verifier") and returns the full ref.
func RewriteTree(root string, resolve func(repo string) string) error {
	if resolve == nil {
		return fmt.Errorf("images.RewriteTree: nil resolve func")
	}
	replacers := buildReplacers(resolve)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := RewriteBytes(data, replacers)
		if string(updated) == string(data) {
			return nil
		}
		return os.WriteFile(path, updated, 0o600)
	})
}

// RewriteBytes applies the pre-built replacers to a single YAML blob.
// Exported for tests.
func RewriteBytes(data []byte, replacers []replacer) []byte {
	updated := data
	for _, r := range replacers {
		updated = r.pattern.ReplaceAll(updated, []byte(r.replacement))
	}
	return updated
}

// BuildReplacers is exported for tests; production uses RewriteTree.
func BuildReplacers(resolve func(repo string) string) []replacer {
	return buildReplacers(resolve)
}

type replacer struct {
	pattern     *regexp.Regexp
	replacement string
}

func buildReplacers(resolve func(repo string) string) []replacer {
	out := make([]replacer, 0, len(Managed))
	for _, base := range Managed {
		// Left-to-right alternation: longest (tag+digest) first so we never
		// leave a stray @sha256: suffix (Docker would honor the digest over
		// the rewritten tag — pitfall #12).
		// Forms: :tag@sha256:d | @sha256:d | :tag | :__OBOL_IMAGE__ | :latest
		pat := regexp.MustCompile(
			regexp.QuoteMeta(base) +
				`(:[A-Za-z0-9._-]{1,128}@sha256:[0-9a-f]{64}|@sha256:[0-9a-f]{64}|:` +
				regexp.QuoteMeta(PlaceholderTag) +
				`|:[A-Za-z0-9._-]{1,128})`,
		)
		out = append(out, replacer{pattern: pat, replacement: resolve(base)})
	}
	return out
}

func resolvePinned(repo, tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" || tag == "unknown" || tag == "dev" {
		return repo + ":latest"
	}
	ref := repo + ":" + tag
	if skipDigest() {
		return ref
	}
	digest, err := FetchIndexDigest(repo, tag)
	if err != nil || digest == "" {
		// Short-SHA tag is still privileged over :latest. Digest is defense
		// in depth when the registry is reachable at apply time.
		return ref
	}
	return ref + "@" + digest
}

func useLatest() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("OBOL_DEVELOPMENT")), "true") {
		return true
	}
	commit := strings.TrimSpace(version.GitCommit)
	if commit == "" || commit == "unknown" || commit == "dev" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(version.GitDirty), "true") {
		return true
	}
	return false
}

func skipDigest() bool {
	v := strings.TrimSpace(os.Getenv("OBOL_SKIP_IMAGE_DIGEST"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

// ---------------------------------------------------------------------------
// Registry digest binding (GHCR multi-arch index digest)
// ---------------------------------------------------------------------------

var (
	digestCache   = map[string]string{}
	digestCacheMu sync.Mutex
	httpClient    = &http.Client{Timeout: 8 * time.Second}
)

// FetchIndexDigest returns the multi-arch index digest GHCR serves for
// repo:tag (e.g. "sha256:deadbeef…"). Matches
// `docker buildx imagetools inspect --format '{{ .Manifest.Digest }}'`.
func FetchIndexDigest(repo, tag string) (string, error) {
	cacheKey := repo + ":" + tag
	digestCacheMu.Lock()
	if d, ok := digestCache[cacheKey]; ok {
		digestCacheMu.Unlock()
		return d, nil
	}
	digestCacheMu.Unlock()

	// repo is "ghcr.io/obolnetwork/x402-verifier" → path "obolnetwork/x402-verifier"
	path := strings.TrimPrefix(repo, "ghcr.io/")
	if path == repo || path == "" {
		return "", fmt.Errorf("images: only ghcr.io repos supported for digest fetch, got %q", repo)
	}

	token, err := ghcrPullToken(path)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodHead, "https://ghcr.io/v2/"+path+"/manifests/"+tag, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("images: ghcr HEAD %s:%s → HTTP %d", path, tag, resp.StatusCode)
	}
	digest := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest"))
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return "", fmt.Errorf("images: missing/invalid Docker-Content-Digest for %s:%s (%q)", path, tag, digest)
	}

	digestCacheMu.Lock()
	digestCache[cacheKey] = digest
	digestCacheMu.Unlock()
	return digest, nil
}

// ClearDigestCache is for tests.
func ClearDigestCache() {
	digestCacheMu.Lock()
	digestCache = map[string]string{}
	digestCacheMu.Unlock()
}

func ghcrPullToken(repository string) (string, error) {
	url := "https://ghcr.io/token?scope=repository:" + repository + ":pull"
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("images: ghcr token HTTP %d", resp.StatusCode)
	}
	// Tiny JSON: {"token":"..."} — avoid importing encoding/json dependency
	// surface for a one-field parse by using a minimal scan.
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])
	const key = `"token":"`
	i := strings.Index(body, key)
	if i < 0 {
		return "", fmt.Errorf("images: ghcr token response missing token field")
	}
	rest := body[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", fmt.Errorf("images: ghcr token response truncated")
	}
	return rest[:j], nil
}
