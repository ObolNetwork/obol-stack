package helmcmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseMajor(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"v4.1.3+gc94d381", 4, true},
		{"v3.20.1+g4d04eef", 3, true},
		{"v3.20.1\n", 3, true},
		{"v10.0.0", 10, true},
		{"helm v4.0.0", 0, false},
		{"", 0, false},
		{"v.1.2", 0, false},
	}
	for _, tc := range cases {
		got, err := parseMajor(tc.in)
		if tc.ok && err != nil {
			t.Errorf("parseMajor(%q) errored: %v", tc.in, err)
			continue
		}
		if !tc.ok && err == nil {
			t.Errorf("parseMajor(%q) = %d, want error", tc.in, got)
			continue
		}
		if got != tc.want {
			t.Errorf("parseMajor(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestParseHelmfileReposBytes is the table-driven happy/edge path for the
// helmfile repo extractor that the tolerant-update preflight depends on.
func TestParseHelmfileReposBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []helmfileRepo
	}{
		{
			name: "minimal valid helmfile with multiple repos",
			in: `
repositories:
  - name: traefik
    url: https://traefik.github.io/charts
  - name: prometheus-community
    url: https://prometheus-community.github.io/helm-charts
releases:
  - name: ignored
    chart: ./ignored
`,
			want: []helmfileRepo{
				{Name: "traefik", URL: "https://traefik.github.io/charts"},
				{Name: "prometheus-community", URL: "https://prometheus-community.github.io/helm-charts"},
			},
		},
		{
			name: "entries missing name or url are skipped (OCI-only refs etc.)",
			in: `
repositories:
  - name: traefik
    url: https://traefik.github.io/charts
  - name: oci-only
  - url: https://nameless.example/charts
`,
			want: []helmfileRepo{
				{Name: "traefik", URL: "https://traefik.github.io/charts"},
			},
		},
		{
			name: "no repositories key returns empty slice (not nil-failure)",
			in: `
releases: []
`,
			want: []helmfileRepo{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHelmfileReposBytes([]byte(tc.in))
			if err != nil {
				t.Fatalf("parseHelmfileReposBytes: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("repos = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseHelmfileRepos_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "helmfile.yaml")
	body := `
repositories:
  - name: obol
    url: https://obolnetwork.github.io/helm-charts/
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write helmfile: %v", err)
	}

	repos, err := ParseHelmfileRepos(path)
	if err != nil {
		t.Fatalf("ParseHelmfileRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "obol" {
		t.Fatalf("unexpected repos: %#v", repos)
	}

	names, err := ManagedRepoNames(path)
	if err != nil {
		t.Fatalf("ManagedRepoNames: %v", err)
	}
	if !reflect.DeepEqual(names, []string{"obol"}) {
		t.Fatalf("names = %#v, want [obol]", names)
	}
}

// TestUpdateRepos_NoNamesIsNoop ensures we don't shell out for an empty repo
// list — the preflight should silently do nothing rather than running
// `helm repo update` with no args (which would update every globally
// registered repo, defeating the whole point of the fix).
func TestUpdateRepos_NoNamesIsNoop(t *testing.T) {
	out, err := UpdateRepos("/nonexistent/helm", nil)
	if err != nil {
		t.Fatalf("UpdateRepos(nil) returned err: %v", err)
	}
	if out != nil {
		t.Fatalf("UpdateRepos(nil) returned output: %q", out)
	}
}

// TestUpdateRepos_TolerantArgsConstructed verifies that the tolerant flag is
// passed when invoking against a real helm-3-style version response. We use a
// fake helm binary written into the temp dir so the test stays hermetic. The
// fake records its argv to a sentinel file so we can assert on it.
func TestUpdateRepos_TolerantArgsConstructed(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("shell-script fake binary not supported on windows")
	}

	dir := t.TempDir()
	helm := filepath.Join(dir, "helm")
	argLog := filepath.Join(dir, "args.log")

	// Fake helm:
	//   - `repo update --help` → advertises the tolerant flag
	//   - any other args       → append to args.log and exit 0
	script := `#!/bin/sh
if [ "$1" = "repo" ] && [ "$2" = "update" ] && [ "$3" = "--help" ]; then
  echo "      --fail-on-repo-update-fail=false   tolerate individual repo failures"
  exit 0
fi
echo "$@" >> "` + argLog + `"
exit 0
`
	if err := os.WriteFile(helm, []byte(script), 0o755); err != nil { //nolint:gosec // test binary
		t.Fatalf("write fake helm: %v", err)
	}

	if _, err := UpdateRepos(helm, []string{"traefik", "obol"}); err != nil {
		t.Fatalf("UpdateRepos: %v", err)
	}

	logged, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	got := string(logged)
	for _, want := range []string{"repo", "update", "--fail-on-repo-update-fail=false", "traefik", "obol"} {
		if !contains(got, want) {
			t.Fatalf("expected %q in fake helm argv, got: %s", want, got)
		}
	}
}

// TestUpdateRepos_OmitsUnsupportedTolerantFlag covers Helm builds whose
// `repo update` command no longer accepts --fail-on-repo-update-fail. The
// preflight must still run a targeted repo update instead of failing before
// any repo is refreshed.
func TestUpdateRepos_OmitsUnsupportedTolerantFlag(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("shell-script fake binary not supported on windows")
	}

	dir := t.TempDir()
	helm := filepath.Join(dir, "helm")
	argLog := filepath.Join(dir, "args.log")

	script := `#!/bin/sh
if [ "$1" = "repo" ] && [ "$2" = "update" ] && [ "$3" = "--help" ]; then
  echo "Usage: helm repo update [REPO1 [REPO2 ...]] [flags]"
  exit 0
fi
echo "$@" >> "` + argLog + `"
exit 0
`
	if err := os.WriteFile(helm, []byte(script), 0o755); err != nil { //nolint:gosec // test binary
		t.Fatalf("write fake helm: %v", err)
	}

	if _, err := UpdateRepos(helm, []string{"traefik", "obol"}); err != nil {
		t.Fatalf("UpdateRepos: %v", err)
	}

	logged, err := os.ReadFile(argLog)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	got := string(logged)
	if contains(got, "--fail-on-repo-update-fail=false") {
		t.Fatalf("unsupported tolerant flag was passed: %s", got)
	}
	for _, want := range []string{"repo", "update", "traefik", "obol"} {
		if !contains(got, want) {
			t.Fatalf("expected %q in fake helm argv, got: %s", want, got)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestParseHelmRepoList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "two repos",
			in:   `[{"name":"obol","url":"https://obolnetwork.github.io/helm-charts/"},{"name":"ethpandaops","url":"https://ethpandaops.github.io/ethereum-helm-charts"}]`,
			want: map[string]string{
				"obol":        "https://obolnetwork.github.io/helm-charts/",
				"ethpandaops": "https://ethpandaops.github.io/ethereum-helm-charts",
			},
		},
		{
			name: "empty array",
			in:   `[]`,
			want: map[string]string{},
		},
		{
			name: "entry without url skipped",
			in:   `[{"name":"broken","url":""},{"name":"obol","url":"https://example.com"}]`,
			want: map[string]string{"obol": "https://example.com"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseHelmRepoList([]byte(tc.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
