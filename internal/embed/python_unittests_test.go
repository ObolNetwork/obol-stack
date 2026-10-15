package embed

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSkillPythonUnitTests runs the repo-level Python unit tests
// (tests/test_*.py: buy.py auth expiry, auto-refill, ...) so `go test ./...`
// covers the embedded skill scripts too. Equivalent to, from the repo root:
//
//	python3 -I -m unittest discover -s tests -p 'test_*.py'
//
// stdlib only, no network. Skipped when python3 is unavailable.
func TestSkillPythonUnitTests(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "tests")); err != nil {
		t.Skipf("no tests/ dir at %s: %v", repoRoot, err)
	}
	cmd := exec.Command(python, "-I", "-B", "-m", "unittest", "discover", "-s", "tests", "-p", "test_*.py")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python unit tests failed: %v\n%s", err, out)
	}
}
