package embed

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestEmbeddedImages_NoNewLatestTags guards against `image: foo:latest` drift
// in the embedded infrastructure templates. The `/v1` saga in PR #343 (add,
// revert, re-add) was consistent with a deployed `x402-buyer:latest` that
// lagged behind the source mux, and the workaround cemented `/v1` in the
// template instead of pinning the image.
//
// This test enumerates every `image: …:latest` occurrence under
// internal/embed/infrastructure/base/templates/ and fails when a new one
// appears. The allowlist below names every currently-unpinned image with the
// rationale; to add an entry, add the image and a reason. To remove an entry,
// replace `:latest` with a digest or immutable tag in the template and shrink
// the allowlist.
//
// Corresponds to W4 in the PR #343 review.
func TestEmbeddedImages_NoNewLatestTags(t *testing.T) {
	type latestHit struct {
		file string
		line int
		img  string
	}

	// Known unpinned images as of PR #343 follow-up. Each entry MUST have a
	// TODO in the template body explaining the pin-by-digest policy.
	allowed := map[string]string{
		"base/templates/llm.yaml:ghcr.io/obolnetwork/x402-buyer:latest": "x402-buyer: pin by digest once CI publishes a stable tag",
	}

	files := []string{
		"base/templates/llm.yaml",
		"base/templates/x402.yaml",
		"base/templates/local-path.yaml",
		"base/templates/obol-agent.yaml",
		"base/templates/obol-agent-monetize-rbac.yaml",
		"base/templates/obol-agent-admission-policy.yaml",
		"base/templates/serviceoffer-crd.yaml",
		"base/templates/registrationrequest-crd.yaml",
		"base/templates/purchaserequest-crd.yaml",
	}

	var hits []latestHit

	for _, f := range files {
		data, err := ReadInfrastructureFile(f)
		if err != nil {
			// Some files may not exist in every branch; skip gracefully.
			continue
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()

			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			// Match "image:" key in Pod/Deployment container specs.
			_, after, found := strings.Cut(trimmed, "image:")
			if !found {
				continue
			}
			// Extract the image reference (strip surrounding quotes, comments).
			after = strings.TrimSpace(after)

			after = strings.Trim(after, `"'`)
			if i := strings.IndexAny(after, " \t#"); i >= 0 {
				after = after[:i]
			}

			if after == "" {
				continue
			}

			if !strings.HasSuffix(after, ":latest") {
				continue
			}

			hits = append(hits, latestHit{file: f, line: lineNum, img: after})
		}

		if err := scanner.Err(); err != nil {
			t.Fatalf("scan %s: %v", f, err)
		}
	}

	var offending []string

	seen := map[string]bool{}

	for _, h := range hits {
		key := fmt.Sprintf("%s:%s", h.file, h.img)

		seen[key] = true
		if _, ok := allowed[key]; !ok {
			offending = append(offending, fmt.Sprintf("%s:%d uses %q but has no pin-exception entry", h.file, h.line, h.img))
		}
	}

	// Also enforce the allowlist is not stale: if an allowed image has been
	// pinned and removed from the YAML, the entry should be dropped from the
	// allowlist so a future drift does not go unnoticed.
	var stale []string

	for key := range allowed {
		if !seen[key] {
			stale = append(stale, key)
		}
	}

	sort.Strings(stale)

	if len(offending) > 0 {
		sort.Strings(offending)
		t.Fatalf("embedded templates use :latest without a pin exception:\n  %s\n\n"+
			"Pin by digest (preferred) or add to the allowlist in %s with a reason.",
			strings.Join(offending, "\n  "),
			"internal/embed/embed_image_pin_test.go")
	}

	if len(stale) > 0 {
		t.Fatalf("allowlist entries no longer match any :latest in templates (tighten the test):\n  %s",
			strings.Join(stale, "\n  "))
	}
}
