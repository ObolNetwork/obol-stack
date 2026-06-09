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

// TestEmbeddedImages_NamedImagesAreDigestPinned guards the @sha256: discipline
// for the cluster-side container images that ship as part of the embedded
// infrastructure. Tag-only refs (e.g. `:b13254e`) are vulnerable to mutable-tag
// rewrites — the class of supply-chain bug CLAUDE.md pitfall #12 documented
// after a real local-cluster incident.
//
// Adding a new image to this list MUST be accompanied by an `@sha256:<digest>`
// suffix on the `image:` line (or, for Helm value files, on the `tag:` field
// such that the rendered manifest produces `<repo>:<tag>@sha256:<digest>`).
//
// To regenerate a digest:
//
//	docker buildx imagetools inspect <repo>:<tag> --format '{{ .Manifest.Digest }}'
func TestEmbeddedImages_NamedImagesAreDigestPinned(t *testing.T) {
	cases := []struct {
		file string
		// repo is the substring used to locate the relevant line. The match
		// is line-scoped — the line must also contain @sha256: to pass.
		repo string
	}{
		// internal/embed/infrastructure/base/templates/x402.yaml
		{file: "base/templates/x402.yaml", repo: "ghcr.io/obolnetwork/x402-verifier"},
		{file: "base/templates/x402.yaml", repo: "ghcr.io/obolnetwork/serviceoffer-controller"},
		// internal/embed/infrastructure/base/templates/llm.yaml
		{file: "base/templates/llm.yaml", repo: "ghcr.io/obolnetwork/litellm"},
		{file: "base/templates/llm.yaml", repo: "ghcr.io/obolnetwork/x402-buyer"},
	}

	for _, tc := range cases {
		t.Run(tc.repo, func(t *testing.T) {
			data, err := ReadInfrastructureFile(tc.file)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}

			var (
				found     bool
				offenders []string
			)

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
				// Must look like a Kubernetes container `image:` field, not a
				// random doc-comment or env var.
				if !strings.Contains(trimmed, "image:") {
					continue
				}
				if !strings.Contains(line, tc.repo) {
					continue
				}

				found = true
				if !strings.Contains(line, "@sha256:") {
					offenders = append(offenders,
						fmt.Sprintf("%s:%d → %q lacks @sha256: digest pin", tc.file, lineNum, strings.TrimSpace(line)))
				}
			}

			if err := scanner.Err(); err != nil {
				t.Fatalf("scan %s: %v", tc.file, err)
			}

			if !found {
				t.Fatalf("no image: line containing %q found in %s — has the image been renamed or moved? "+
					"Update this test alongside the manifest change.", tc.repo, tc.file)
			}

			if len(offenders) > 0 {
				t.Fatalf("digest-pin discipline broken in %s:\n  %s\n\n"+
					"Pin the image as `<repo>:<tag>@sha256:<digest>`. Resolve with:\n"+
					"  docker buildx imagetools inspect %s:<tag> --format '{{ .Manifest.Digest }}'",
					tc.file, strings.Join(offenders, "\n  "), tc.repo)
			}
		})
	}
}

func TestEmbeddedImages_X402ControllerAndBuyerUseFixPins(t *testing.T) {
	cases := []struct {
		file string
		ref  string
	}{
		{
			// Repinned to 04bebbc (current main HEAD as of rc13) to pick up:
			//   - ab71481 fix(x402): suppress verifyOnly=false warning on the
			//     in-process settle path — covers the per-request log spam
			//     seen by sell-agent buyers on the prior pin.
			//   - 86b8c9f fix(x402-buyer): drop expired pre-signed auths
			//     before signing — affects long-running paid inference.
			// Still carries the Secret-create-only reconciler change from
			// b39bcaa. See TestServiceOfferControllerImage_CarriesSecretCreateOnlyFix.
			//
			// FOLLOW-UP REQUIRED after this PR merges: 04bebbc is this PR's
			// own merge base, so these images do NOT contain this PR's source
			// changes — the controller still renders sub-agents with Hermes
			// v2026.5.28 (compiled-in agent_render.go default), the verifier
			// still hardcodes maxTimeoutSeconds=60 and drops the settle tx
			// hash on facilitator errors, and the buyer lacks the
			// settled-but-failed ConfirmSpend branch. Rebuild all three from
			// the merge commit and repin (the rc11 pattern, cf. 8fb1553)
			// before cutting v0.10.0 final. OBOL_DEVELOPMENT=true masks this
			// locally because dev clusters rebuild from worktree source.
			file: "base/templates/x402.yaml",
			ref:  "ghcr.io/obolnetwork/serviceoffer-controller:04bebbc@sha256:286d07604c001006d54a5f89ef854210ab805859c072e7b8dd89fe0c6f130d7d",
		},
		{
			file: "base/templates/llm.yaml",
			ref:  "ghcr.io/obolnetwork/x402-buyer:04bebbc@sha256:1c2bb19824bae2caf4b305a495b6686ff6e973b378c2b88fc89d73a06265aaf7",
		},
	}

	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			data, err := ReadInfrastructureFile(tc.file)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}
			if !strings.Contains(string(data), "image: "+tc.ref) {
				t.Fatalf("%s must pin current x402 bundle image %q", tc.file, tc.ref)
			}
		})
	}
}

// TestEmbeddedImages_CloudflaredHelmTagIsDigestPinned covers the cloudflared
// chart, which uses the Helm idiom `image.repository` + `image.tag` rather
// than a literal `image:` line. The chart template renders
// `<repository>:<tag>`; embedding `@sha256:<digest>` inside `.tag` produces
// a valid digest-pinned ref at render time and preserves the same
// mutable-tag protection.
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
		t.Fatalf("scan cloudflared/values.yaml: %v", err)
	}

	if tagLine == "" {
		t.Fatal("no `tag:` field found in cloudflared/values.yaml — chart layout changed; update this test.")
	}

	if !strings.Contains(tagLine, "@sha256:") {
		t.Fatalf("cloudflared image tag is not digest-pinned: %q\n\n"+
			"Pin it as `tag: \"<tag>@sha256:<digest>\"`. Resolve with:\n"+
			"  docker buildx imagetools inspect cloudflare/cloudflared:<tag> --format '{{ .Manifest.Digest }}'",
			strings.TrimSpace(tagLine))
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
