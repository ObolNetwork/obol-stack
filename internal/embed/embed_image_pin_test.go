package embed

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/images"
)

// TestEmbeddedImages_ManagedUsePlaceholder guards the post-repin model:
// stack-owned images in embedded templates MUST use :__OBOL_IMAGE__, which
// CopyInfrastructure rewrites to short-SHA (+ registry digest) at apply time.
// Digests are never committed to git.
func TestEmbeddedImages_ManagedUsePlaceholder(t *testing.T) {
	files := []string{
		"base/templates/x402.yaml",
		"base/templates/llm.yaml",
	}
	placeholder := ":" + images.PlaceholderTag
	for _, repo := range images.Managed {
		// Only assert on images that actually appear in these templates.
		// demo-server / storefront are created dynamically via images.Resolve.
		found := false
		for _, f := range files {
			data, err := ReadInfrastructureFile(f)
			if err != nil {
				continue
			}
			if !bytes.Contains(data, []byte(repo)) {
				continue
			}
			found = true
			re := regexp.MustCompile(`image:\s*` + regexp.QuoteMeta(repo) + `:\S+`)
			matches := re.FindAll(data, -1)
			if len(matches) == 0 {
				t.Errorf("%s: found bare %s but no image: line", f, repo)
				continue
			}
			for _, m := range matches {
				if !bytes.Contains(m, []byte(placeholder)) {
					t.Errorf("%s: managed image must use placeholder %s, got %s", f, placeholder, m)
				}
				if bytes.Contains(m, []byte("@sha256:")) {
					t.Errorf("%s: managed image must not embed a digest in git, got %s", f, m)
				}
			}
		}
		_ = found
	}
}

// TestEmbeddedImages_NoNewLatestTags guards against floating :latest on
// embedded infrastructure — except the empty allowlist (managed images use
// the placeholder; third-party images use digests).
func TestEmbeddedImages_NoNewLatestTags(t *testing.T) {
	type latestHit struct {
		file string
		line int
		img  string
	}

	// Empty: managed images use :__OBOL_IMAGE__; third-party images use digests.
	allowed := map[string]string{}

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
		"base/templates/agent-crd.yaml",
	}

	var hits []latestHit
	for _, f := range files {
		data, err := ReadInfrastructureFile(f)
		if err != nil {
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
			_, after, found := strings.Cut(trimmed, "image:")
			if !found {
				continue
			}
			after = strings.TrimSpace(after)
			after = strings.Trim(after, `"'`)
			if i := strings.IndexAny(after, " \t#"); i >= 0 {
				after = after[:i]
			}
			if after == "" || !strings.HasSuffix(after, ":latest") {
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
			offending = append(offending, fmt.Sprintf("%s:%d uses %q — use :%s for stack-owned images or a digest pin for third-party",
				h.file, h.line, h.img, images.PlaceholderTag))
		}
	}
	var stale []string
	for key := range allowed {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	if len(offending) > 0 {
		sort.Strings(offending)
		t.Fatalf("embedded templates use :latest without exception:\n  %s", strings.Join(offending, "\n  "))
	}
	if len(stale) > 0 {
		t.Fatalf("stale allowlist entries:\n  %s", strings.Join(stale, "\n  "))
	}
}

// TestEmbeddedImages_ThirdPartyAreDigestPinned: non-managed images that we
// ship still need @sha256: in git (Renovate / manual bumps). Stack-owned
// managed images are excluded — they use the placeholder + apply-time bind.
func TestEmbeddedImages_ThirdPartyAreDigestPinned(t *testing.T) {
	cases := []struct {
		file string
		repo string
	}{
		{file: "base/templates/llm.yaml", repo: "ghcr.io/obolnetwork/litellm"},
	}

	managed := map[string]bool{}
	for _, r := range images.Managed {
		managed[r] = true
	}

	for _, tc := range cases {
		t.Run(tc.repo, func(t *testing.T) {
			if managed[tc.repo] {
				t.Fatalf("%s is stack-managed; it must use :%s, not a git digest", tc.repo, images.PlaceholderTag)
			}
			data, err := ReadInfrastructureFile(tc.file)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			var found bool
			var offenders []string
			scanner := bufio.NewScanner(bytes.NewReader(data))
			scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") || !strings.Contains(trimmed, "image:") {
					continue
				}
				if !strings.Contains(line, tc.repo) {
					continue
				}
				found = true
				if !strings.Contains(line, "@sha256:") {
					offenders = append(offenders, fmt.Sprintf("%s:%d → %q lacks @sha256:", tc.file, lineNum, strings.TrimSpace(line)))
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("scan %s: %v", tc.file, err)
			}
			if !found {
				t.Fatalf("no image: line for %s in %s", tc.repo, tc.file)
			}
			if len(offenders) > 0 {
				t.Fatalf("third-party digest pin missing:\n  %s", strings.Join(offenders, "\n  "))
			}
		})
	}
}

// TestEmbeddedImages_CloudflaredHelmTagIsDigestPinned covers the cloudflared
// chart (Helm image.repository + image.tag idiom).
func TestEmbeddedImages_CloudflaredHelmTagIsDigestPinned(t *testing.T) {
	data, err := ReadInfrastructureFile("cloudflared/values.yaml")
	if err != nil {
		t.Fatalf("read cloudflared/values.yaml: %v", err)
	}
	var tagLine string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "tag:") {
			tagLine = line
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if tagLine == "" {
		t.Fatal("no tag: field in cloudflared/values.yaml")
	}
	if !strings.Contains(tagLine, "@sha256:") {
		t.Fatalf("cloudflared image tag is not digest-pinned: %q", strings.TrimSpace(tagLine))
	}
}

func TestEmbeddedLiteLLMConfigUsesWritableRuntimeCopy(t *testing.T) {
	data, err := ReadInfrastructureFile("base/templates/llm.yaml")
	if err != nil {
		t.Fatalf("read llm.yaml: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "mountPath: /etc/litellm/config.yaml") {
		t.Fatalf("LiteLLM still mounts the ConfigMap directly at /etc/litellm/config.yaml; /model/new must write to a writable runtime copy")
	}
	for _, want := range []string{
		"initContainers:",
		"name: prepare-litellm-config",
		"name: litellm-config-source",
		"name: litellm-config-work",
		"mountPath: /etc/litellm",
		"emptyDir:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("LiteLLM writable config pattern missing %q", want)
		}
	}
}
