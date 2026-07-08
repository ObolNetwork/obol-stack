package embed

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
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
		// job-broker ships first in this branch — no published release to
		// pin against yet. The release workflow pins it by digest on the
		// first tagged build (same lifecycle the other obolnetwork images
		// followed); under OBOL_DEVELOPMENT the :latest tag is what the
		// local dev build produces.
		"base/templates/x402.yaml:ghcr.io/obolnetwork/job-broker:latest": "pending first release pin",
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

// extractEmbeddedImagePin locates the single `image:` line for repo in the
// given embedded template and returns its (tag, digest). Fails the test when
// the ref is missing or not of the `<repo>:<short-sha>@sha256:<digest>` form
// the repin automation maintains (.github/scripts/repin-x402-images.sh).
func extractEmbeddedImagePin(t *testing.T, file, repo string) (tag, digest string) {
	t.Helper()

	data, err := ReadInfrastructureFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}

	re := regexp.MustCompile(`image: ` + regexp.QuoteMeta(repo) + `:([0-9a-f]{7,40})@(sha256:[0-9a-f]{64})`)

	m := re.FindSubmatch(data)
	if m == nil {
		t.Fatalf("%s: no commit-tagged, digest-pinned image ref for %s — the pin must look like "+
			"%s:<short-sha>@sha256:<digest> (run .github/scripts/repin-x402-images.sh)", file, repo, repo)
	}

	return string(m[1]), string(m[2])
}

// requireGitAncestor asserts that ancestor is reachable from the commit the
// pin tag names. Skips (not fails) when git or either commit is unavailable —
// shallow clones and source tarballs can't answer ancestry questions; the
// release workflow's verify-image-pins gate enforces freshness with full
// history.
func requireGitAncestor(t *testing.T, ancestor, pinTag string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; ancestry check skipped")
	}

	for _, c := range []string{ancestor, pinTag} {
		if err := exec.Command("git", "rev-parse", "--verify", "--quiet", c+"^{commit}").Run(); err != nil {
			t.Skipf("commit %s not resolvable in this clone (shallow checkout?); ancestry check skipped", c)
		}
	}

	if err := exec.Command("git", "merge-base", "--is-ancestor", ancestor, pinTag).Run(); err != nil {
		t.Fatalf("pinned image build commit %s does not descend from required fix commit %s", pinTag, ancestor)
	}
}

// TestEmbeddedImages_X402PinsShareOneBuildCommit guards the consistency
// invariant behind the repin automation: x402-verifier,
// serviceoffer-controller, and x402-buyer are built together by
// docker-publish-x402.yml, so their embedded pins must reference one build
// commit. Mixed tags mean a partial/manual repin — exactly the state that
// shipped a controller/buyer skew before rc11 (cf. 8fb1553) and the stale
// base pins before rc14 (cf. 2db429b).
func TestEmbeddedImages_X402PinsShareOneBuildCommit(t *testing.T) {
	verifierTag, _ := extractEmbeddedImagePin(t, "base/templates/x402.yaml", "ghcr.io/obolnetwork/x402-verifier")
	controllerTag, _ := extractEmbeddedImagePin(t, "base/templates/x402.yaml", "ghcr.io/obolnetwork/serviceoffer-controller")
	buyerTag, _ := extractEmbeddedImagePin(t, "base/templates/llm.yaml", "ghcr.io/obolnetwork/x402-buyer")

	if verifierTag != controllerTag || verifierTag != buyerTag {
		t.Fatalf("embedded x402 pins must share one build commit; got verifier=%s controller=%s buyer=%s "+
			"(repin all three: .github/scripts/repin-x402-images.sh <commit>)",
			verifierTag, controllerTag, buyerTag)
	}
}

// TestEmbeddedImages_X402PinsCarryRequiredFixes replaces the old
// exact-ref equality tests so the repin automation can bump pins without
// editing this file, while the "image carries fix X" guarantees get
// stronger: ancestry-verified against git history instead of trusted via a
// hand-maintained string. Each entry names a commit whose absence shipped a
// real bug; bumping a pin BACKWARD past any of them fails here.
func TestEmbeddedImages_X402PinsCarryRequiredFixes(t *testing.T) {
	// All three images share one build commit (asserted above), so checking
	// the controller tag covers the set.
	tag, _ := extractEmbeddedImagePin(t, "base/templates/x402.yaml", "ghcr.io/obolnetwork/serviceoffer-controller")

	// Only commits reachable from main belong here. A release-branch commit
	// (e.g. c19ffaf, the rc14 train) stops being an ancestor of future main
	// pins the moment the train squash-merges — the entry would hard-fail
	// every clone that still has the branch ref and skip everywhere else.
	// Train freshness is the release gate's job (verify-x402-pins.sh).
	requiredFixes := []struct {
		commit string
		why    string
	}{
		{"b39bcaa", "Secret-create-only reconciler — without it per-agent provisioning 403s under the no-update/patch Secret RBAC"},
		{"ab71481", "suppress verifyOnly=false warning on the in-process settle path (per-request log spam for sell-agent buyers)"},
		{"86b8c9f", "buyer drops expired pre-signed auths before signing (long-running paid inference)"},
	}

	for _, fix := range requiredFixes {
		t.Run(fix.commit, func(t *testing.T) {
			requireGitAncestor(t, fix.commit, tag)
			_ = fix.why // documentation; the failure message names the commit
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
