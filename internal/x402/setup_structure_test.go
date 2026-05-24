package x402

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureVerifier_NoInlineRegex enforces CLAUDE.md pitfall #9 at the
// structural level: setup.go must not carry its own image-pin rewrite
// regex. The canonical rewrite lives in internal/defaults/defaults.go,
// applied to the helmfile-rendered tree under $OBOL_CONFIG_DIR/defaults.
// Driving the verifier deployment through helmfile (not kubectl apply
// of embed.FS) means any duplicated regex is dead code at best and a
// silent-bypass footgun at worst.
//
// If this test fires, either:
//   - delete the duplicate regex from internal/x402/setup.go, or
//   - if the duplicate is genuinely needed (it almost never is), move
//     it behind a shared helper in internal/defaults and call that.
func TestEnsureVerifier_NoInlineRegex(t *testing.T) {
	setupPath := mustResolveFile(t, "setup.go")

	data, err := os.ReadFile(setupPath)
	if err != nil {
		t.Fatalf("read setup.go: %v", err)
	}
	src := string(data)

	// Cheap textual guard first — surfaces a clear error message even when
	// the AST parse below would also catch it.
	if strings.Contains(src, `"regexp"`) {
		t.Fatalf("internal/x402/setup.go must not import the regexp package; " +
			"the image-pin rewrite belongs in internal/defaults (see CLAUDE.md pitfall #9)")
	}
	if strings.Contains(src, "regexp.MustCompile") || strings.Contains(src, "regexp.Compile") {
		t.Fatalf("internal/x402/setup.go must not compile regexes inline; " +
			"the duplicated rewrite was deleted in favor of helmfile-driven deploy")
	}

	// AST-level guard: catches aliased imports (e.g. `re "regexp"`) and is
	// resilient to comments that happen to contain the word "regexp".
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, setupPath, data, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse setup.go: %v", err)
	}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "regexp" {
			t.Fatalf("internal/x402/setup.go imports %q; remove the duplicated rewrite", path)
		}
	}
}

// mustResolveFile locates a source file relative to this test file. Works
// whether `go test` is run from the package directory or from the repo root.
func mustResolveFile(t *testing.T, name string) string {
	t.Helper()
	// First try working directory (default for `go test ./...`).
	if _, err := os.Stat(name); err == nil {
		abs, err := filepath.Abs(name)
		if err != nil {
			t.Fatalf("abs %q: %v", name, err)
		}
		return abs
	}
	t.Fatalf("could not locate %q from %q", name, mustGetwd(t))
	return ""
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return wd
}
