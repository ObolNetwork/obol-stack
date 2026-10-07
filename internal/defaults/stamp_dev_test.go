package defaults

import (
	"os/exec"
	"testing"
)

// A new commit must change the dev stamp, or `stack up` keeps the previous
// dev-<sha> images (the infrastructure is only re-copied on stamp change).
func TestInfrastructureStampTracksDevCommit(t *testing.T) {
	t.Setenv("OBOL_DEVELOPMENT", "true")
	t.Chdir(t.TempDir())

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "one")

	first, err := infrastructureStamp("k3d", "id")
	if err != nil {
		t.Fatal(err)
	}

	git("commit", "-q", "--allow-empty", "-m", "two")

	second, err := infrastructureStamp("k3d", "id")
	if err != nil {
		t.Fatal(err)
	}

	if first == second {
		t.Fatalf("stamp unchanged across commits:\n%s", first)
	}
}
