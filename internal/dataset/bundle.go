package dataset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BundleManifest is the minimal shape read from a dataset bundle directory's
// manifest.json: a content-address hash and the list of artifact file names.
// (The bundle is produced by an external dataset exporter; only this
// generic envelope is consumed here.)
type BundleManifest struct {
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
}

// ReadBundle reads <dir>/manifest.json, resolves the primary training
// artifact (a .jsonl file, preferring an instruction/sft-style name), and
// returns the manifest hash (the content-address anchor), the artifact's
// absolute path, its whole-file SHA-256, and its byte size.
func ReadBundle(dir string) (manifestHash, artifactPath, fileHash string, size int64, err error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", "", "", 0, fmt.Errorf("dataset: read manifest: %w", err)
	}
	var m BundleManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", "", 0, fmt.Errorf("dataset: parse manifest: %w", err)
	}
	if len(m.Hash) != 64 {
		return "", "", "", 0, fmt.Errorf("dataset: manifest hash must be 64 hex chars, got %d", len(m.Hash))
	}
	artifact := pickArtifact(m.Files)
	if artifact == "" {
		return "", "", "", 0, fmt.Errorf("dataset: no .jsonl artifact listed in manifest")
	}
	artifactPath = filepath.Join(dir, artifact)
	fileHash, size, err = hashFile(artifactPath)
	if err != nil {
		return "", "", "", 0, err
	}
	return strings.ToLower(m.Hash), artifactPath, fileHash, size, nil
}

// pickArtifact chooses the training file: prefer one whose name signals an
// instruction/sft format, else the first .jsonl, else "".
func pickArtifact(files []string) string {
	var firstJSONL string
	for _, f := range files {
		lf := strings.ToLower(f)
		if !strings.HasSuffix(lf, ".jsonl") {
			continue
		}
		if firstJSONL == "" {
			firstJSONL = f
		}
		if strings.Contains(lf, "sft") || strings.Contains(lf, "instruct") {
			return f
		}
	}
	return firstJSONL
}

// hashFile returns the lowercase hex SHA-256 and byte size of a file.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("dataset: open artifact: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, fmt.Errorf("dataset: hash artifact: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
