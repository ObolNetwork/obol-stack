//go:build !windows

package agentruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/passthrough/stubtool"
)

// The test binary doubles as a stub kubectl (argv[0] "kubectl") and as a
// driver calling ExecInPod (EXECINPOD_TEST_CONFIG_DIR set), because ExecInPod
// replaces the calling process.
func TestMain(m *testing.M) {
	if stubtool.IsStub() {
		stubtool.Main()
	}
	if dir := os.Getenv("EXECINPOD_TEST_CONFIG_DIR"); dir != "" {
		cfg := &config.Config{ConfigDir: dir, BinDir: filepath.Join(dir, "bin")}
		err := ExecInPod(cfg, Hermes, DefaultInstanceID, []string{"hermes", "chat", "--", "-q"})
		os.Stderr.WriteString("ExecInPod returned: " + err.Error() + "\n")
		os.Exit(99)
	}
	os.Exit(m.Run())
}

func TestExecInPod_ExecsKubectlWithArgsEnvAndExitCode(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stub := filepath.Join(dir, "kubectl")
	if err := os.Symlink(self, stub); err != nil {
		t.Fatal(err)
	}
	kubeconfig := filepath.Join(dir, "kubeconfig.yaml")
	if err := os.WriteFile(kubeconfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		userKC string
		wantKC string
	}{
		{"stack kubeconfig", "", kubeconfig},
		{"ambient KUBECONFIG ignored", "/user/kc", kubeconfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(self)
			cmd.Env = append(os.Environ(), "EXECINPOD_TEST_CONFIG_DIR="+dir, "OBOL_KUBECTL="+stub, "STUB_EXIT=3", "KUBECONFIG="+tc.userKC)
			var out, errOut bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &errOut // not terminals → no -t
			_ = cmd.Run()

			if code := cmd.ProcessState.ExitCode(); code != 3 {
				t.Fatalf("exit = %d, want 3 (stderr %q)", code, errOut.String())
			}
			var rep stubtool.Report
			if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
				t.Fatalf("stub output %q: %v", out.String(), err)
			}
			want := []string{"exec", "-i", "-c", "hermes", "-n", "hermes-obol-agent", "deploy/hermes", "--", "hermes", "chat", "--", "-q"}
			if !reflect.DeepEqual(rep.Argv, want) {
				t.Errorf("argv = %q, want %q", rep.Argv, want)
			}
			if rep.Kubeconfig == nil || *rep.Kubeconfig != tc.wantKC {
				t.Errorf("KUBECONFIG = %v, want %q", rep.Kubeconfig, tc.wantKC)
			}
			if rep.PID != cmd.ProcessState.Pid() {
				t.Errorf("kubectl did not replace the obol process")
			}
			if strings.Contains(errOut.String(), "ExecInPod returned") {
				t.Errorf("unexpected: %s", errOut.String())
			}
		})
	}
}
