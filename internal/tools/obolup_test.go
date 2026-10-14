package tools

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// readObolup returns obolup.sh from the repository root.
func readObolup(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "obolup.sh"))
	if err != nil {
		t.Fatalf("read obolup.sh: %v", err)
	}

	return string(raw)
}

type shellFunc struct {
	name  string
	start int // 1-based line of the "name() {" line
	body  []string
}

var funcStartRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(\)\s*\{`)

// shellFuncs splits a bash script into its top-level functions (those that
// start in column 0 and end with a "}" in column 0). Lines outside any
// function are returned under the name "".
func shellFuncs(script string) []shellFunc {
	var (
		out []shellFunc
		cur = shellFunc{name: "", start: 1}
	)

	for i, line := range strings.Split(script, "\n") {
		if m := funcStartRE.FindStringSubmatch(line); m != nil && cur.name == "" {
			out = append(out, cur)
			cur = shellFunc{name: m[1], start: i + 1}

			continue
		}

		if cur.name != "" && line == "}" {
			out = append(out, cur)
			cur = shellFunc{name: "", start: i + 2}

			continue
		}

		cur.body = append(cur.body, line)
	}

	return append(out, cur)
}

// codeLines drops comments, blank lines and lines that only print text.
func codeLines(lines []string) []string {
	var out []string

	for _, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}

		if strings.HasPrefix(s, "echo ") || strings.HasPrefix(s, "printf ") ||
			strings.HasPrefix(s, "log_") {
			continue
		}

		out = append(out, l)
	}

	return out
}

// TestObolupDoesNotDownloadTools: the host tools are installed by the obol
// CLI (sha256-verified from manifest.yaml), never by obolup.sh itself.
func TestObolupDoesNotDownloadTools(t *testing.T) {
	script := readObolup(t)

	if !strings.Contains(script, "upgrade --tools-only") {
		t.Error("obolup.sh must install tools with 'obol upgrade --tools-only'")
	}

	forbidden := map[string]string{
		"--verify=false":      "helm plugin signature verification must not be disabled",
		"helm plugin install": "helm-diff is installed (sha256-verified) by the obol CLI",
	}

	// Every download origin in the manifest (e.g. dl.k8s.io,
	// github.com/k3d-io/k3d) must be absent from obolup.sh.
	for _, tool := range Default().Tools {
		for _, a := range tool.Assets {
			u, err := url.Parse(a.URL)
			if err != nil {
				t.Fatalf("%s: bad asset url %q: %v", tool.Name, a.URL, err)
			}

			origin := u.Host
			if u.Host == "github.com" {
				parts := strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 3)
				if len(parts) >= 2 {
					origin = u.Host + "/" + parts[0] + "/" + parts[1]
				}
			}

			forbidden[origin] = tool.Name + " is downloaded and verified by the obol CLI"
		}
	}

	for needle, why := range forbidden {
		if strings.Contains(script, needle) {
			t.Errorf("obolup.sh contains %q: %s", needle, why)
		}
	}
}

var readCmdRE = regexp.MustCompile(`(^|[\s;&|({!])read\s`)

// TestObolupReadsAreGuarded: user input is read in exactly one place,
// prompt_read, which reads /dev/tty only after can_prompt (which honours
// OBOL_NONINTERACTIVE and checks /dev/tty can actually be opened). Any other
// read must consume a here-string, never stdin (under curl | bash, stdin is
// the script).
func TestObolupReadsAreGuarded(t *testing.T) {
	funcs := shellFuncs(readObolup(t))
	found := map[string]shellFunc{}

	for _, f := range funcs {
		found[f.name] = f

		for _, l := range codeLines(f.body) {
			if !readCmdRE.MatchString(l) {
				continue
			}

			switch {
			case f.name == "prompt_read":
				if !strings.Contains(l, "</dev/tty") {
					t.Errorf("prompt_read must read from /dev/tty, got: %s", strings.TrimSpace(l))
				}
			case strings.Contains(l, "<<<"):
				// here-string: not user input
			case strings.Contains(l, "while ") && strings.Contains(strings.Join(f.body, "\n"), "done <<<"):
				// loop fed by a here-string: not user input
			default:
				t.Errorf("%s (line %d+): unguarded read, use prompt_read/confirm: %s", f.name, f.start, strings.TrimSpace(l))
			}
		}
	}

	pr, ok := found["prompt_read"]
	if !ok {
		t.Fatal("obolup.sh: prompt_read not found")
	}

	if !strings.Contains(strings.Join(pr.body, "\n"), "if can_prompt; then") {
		t.Error("prompt_read must only read after can_prompt succeeds")
	}

	cp, ok := found["can_prompt"]
	if !ok {
		t.Fatal("obolup.sh: can_prompt not found")
	}

	body := strings.Join(cp.body, "\n")
	for _, want := range []string{"NONINTERACTIVE", "{ true </dev/tty; } 2>/dev/null"} {
		if !strings.Contains(body, want) {
			t.Errorf("can_prompt must check %q", want)
		}
	}
}

var sudoCmdRE = regexp.MustCompile(`(^|[\s;&|({!])sudo(_cmd)?\s`)

// TestObolupSudoNeverHangs: sudo is only run through sudo_cmd, in functions
// that first check sudo_available (cached credentials or a terminal for the
// password prompt). `sudo -n` never prompts and is always allowed.
func TestObolupSudoNeverHangs(t *testing.T) {
	for _, f := range shellFuncs(readObolup(t)) {
		if f.name == "sudo_available" || f.name == "sudo_cmd" {
			continue
		}

		lines := codeLines(f.body)
		guarded := strings.Contains(strings.Join(lines, "\n"), "sudo_available")

		for _, l := range lines {
			m := sudoCmdRE.FindStringSubmatch(l)
			if m == nil {
				continue
			}

			if m[2] == "" {
				if !strings.Contains(l, "sudo -n ") {
					t.Errorf("%s (line %d+): call sudo through sudo_cmd: %s", f.name, f.start, strings.TrimSpace(l))
				}

				continue
			}

			if !guarded {
				t.Errorf("%s (line %d+): sudo_cmd without a sudo_available check: %s", f.name, f.start, strings.TrimSpace(l))
			}
		}
	}
}

// TestObolupOllamaNeedsConsent: Ollama's installer only runs after
// ollama_consent, which --yes alone does not satisfy.
func TestObolupOllamaNeedsConsent(t *testing.T) {
	found := map[string]shellFunc{}
	for _, f := range shellFuncs(readObolup(t)) {
		found[f.name] = f
	}

	consent, ok := found["ollama_consent"]
	if !ok {
		t.Fatal("obolup.sh: ollama_consent not found")
	}

	body := strings.Join(consent.body, "\n")
	for _, want := range []string{"OBOL_INSTALL_OLLAMA", `prompt_read choice "  $question [y/N]: " "n"`} {
		if !strings.Contains(body, want) {
			t.Errorf("ollama_consent must contain %q", want)
		}
	}

	for name, f := range found {
		if name == "run_ollama_installer" {
			continue
		}

		lines := strings.Join(codeLines(f.body), "\n")
		if strings.Contains(lines, "ollama.com/install.sh | sh") ||
			strings.Contains(lines, "$OLLAMA_INSTALL_URL | sh") {
			t.Errorf("%s runs the Ollama installer directly; use run_ollama_installer after ollama_consent", name)
		}

		if strings.Contains(lines, "run_ollama_installer") && !strings.Contains(lines, "ollama_consent") {
			t.Errorf("%s calls run_ollama_installer without ollama_consent", name)
		}
	}
}
