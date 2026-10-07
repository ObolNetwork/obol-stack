//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/passthrough/stubtool"
)

// obolHarness runs this test binary as `obol` with stub tools.
type obolHarness struct {
	configDir string
	env       []string
}

const stackKC = "<stack>" // placeholder for the harness' stack kubeconfig

func newObolHarness(t *testing.T, kubeconfigExists bool) *obolHarness {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	stubs := filepath.Join(root, "stubs")
	cfgDir := filepath.Join(root, "config")
	for _, d := range []string{stubs, cfgDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"kubectl", "helm", "helmfile"} {
		if err := os.Symlink(self, filepath.Join(stubs, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if kubeconfigExists {
		if err := os.WriteFile(filepath.Join(cfgDir, "kubeconfig.yaml"), []byte("apiVersion: v1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "KUBECONFIG" || k == "HELMFILE_FILE_PATH" || strings.HasPrefix(k, "OBOL_") || strings.HasPrefix(k, "STUB_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"OBOL_TEST_MAIN=1", "NO_COLOR=1",
		"OBOL_CONFIG_DIR="+cfgDir,
		"OBOL_BIN_DIR="+filepath.Join(root, "bin"),
		"OBOL_DATA_DIR="+filepath.Join(root, "data"),
		"OBOL_STATE_DIR="+filepath.Join(root, "state"),
		"OBOL_KUBECTL="+filepath.Join(stubs, "kubectl"),
		"OBOL_HELM="+filepath.Join(stubs, "helm"),
		"OBOL_HELMFILE="+filepath.Join(stubs, "helmfile"),
	)
	return &obolHarness{configDir: cfgDir, env: env}
}

func (h *obolHarness) command(extraEnv []string, args ...string) *exec.Cmd {
	self, _ := os.Executable()
	cmd := exec.Command(self, args...)
	cmd.Env = append(append([]string{}, h.env...), extraEnv...)
	return cmd
}

// run returns the stub's report (zero value if the stub did not run),
// stdout, stderr and the exit status.
func (h *obolHarness) run(t *testing.T, extraEnv []string, stdin string, args ...string) (stubtool.Report, string, string, *os.ProcessState) {
	t.Helper()
	cmd := h.command(extraEnv, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()

	var rep stubtool.Report
	if line, _, _ := strings.Cut(stdout.String(), "\n"); strings.HasPrefix(line, "{") {
		if err := json.Unmarshal([]byte(line), &rep); err != nil {
			t.Fatalf("stub output %q: %v", line, err)
		}
	}
	return rep, stdout.String(), stderr.String(), cmd.ProcessState
}

func TestPassthrough_Golden(t *testing.T) {
	for _, tc := range []struct {
		name         string
		noKubeconfig bool
		env          []string
		stdin        string
		args         []string
		wantArgv     []string
		wantKC       string // stackKC, "" = unset, or a literal path
		wantHelmfile string // for helmfile cases: stackKC-style placeholder "<helmfile>", "" = unset
		wantExit     int
		wantStderr   string
		noStderr     string
	}{
		{
			name: "stack KUBECONFIG set when user chose none",
			args: []string{"kubectl", "get", "po"}, wantArgv: []string{"get", "po"}, wantKC: stackKC,
		},
		{
			name: "ambient KUBECONFIG replaced by stack kubeconfig",
			env:  []string{"KUBECONFIG=/user/kc"},
			args: []string{"kubectl", "get", "po"}, wantArgv: []string{"get", "po"}, wantKC: stackKC,
		},
		{
			name: "--context alone keeps stack kubeconfig",
			env:  []string{"KUBECONFIG=/user/kc"},
			args: []string{"kubectl", "--context", "work", "get", "po"}, wantArgv: []string{"--context", "work", "get", "po"}, wantKC: stackKC,
		},
		{
			name: "explicit --kubeconfig leaves env untouched",
			env:  []string{"KUBECONFIG=/user/kc"},
			args: []string{"kubectl", "--kubeconfig=/x", "get", "po"}, wantArgv: []string{"--kubeconfig=/x", "get", "po"}, wantKC: "/user/kc",
		},
		{
			name: "--kubeconfig preserved, no env added",
			args: []string{"kubectl", "--kubeconfig", "/x", "get", "po"}, wantArgv: []string{"--kubeconfig", "/x", "get", "po"}, wantKC: "",
		},
		{
			name: "helm --kubeconfig= preserved",
			args: []string{"helm", "list", "--kubeconfig=/x"}, wantArgv: []string{"list", "--kubeconfig=/x"}, wantKC: "",
		},
		{
			name: "leading -- stripped, later -- kept",
			args: []string{"kubectl", "--", "exec", "p", "--", "ls"}, wantArgv: []string{"exec", "p", "--", "ls"}, wantKC: stackKC,
		},
		{
			name:  "stdin forwarded",
			stdin: "apiVersion: v1\nkind: List\n",
			args:  []string{"kubectl", "apply", "-f", "-"}, wantArgv: []string{"apply", "-f", "-"}, wantKC: stackKC,
		},
		{
			name: "exit code 42 propagated",
			env:  []string{"STUB_EXIT=42"},
			args: []string{"kubectl", "get", "po"}, wantArgv: []string{"get", "po"}, wantKC: stackKC, wantExit: 42,
		},
		{
			name:         "no kubeconfig: cluster-free command still runs, no hint",
			noKubeconfig: true,
			args:         []string{"kubectl", "version", "--client"}, wantArgv: []string{"version", "--client"}, wantKC: stackKC,
			noStderr: "obol stack up",
		},
		{
			name:         "no kubeconfig: failure prints stack hint and keeps exit code",
			noKubeconfig: true,
			env:          []string{"STUB_EXIT=1"},
			args:         []string{"kubectl", "get", "po"}, wantArgv: []string{"get", "po"}, wantKC: stackKC,
			wantExit: 1, wantStderr: "is the stack up? run 'obol stack up'",
		},
		{
			name:         "ambient KUBECONFIG and no stack kubeconfig: still stack, hint on failure",
			noKubeconfig: true,
			env:          []string{"KUBECONFIG=/user/kc", "STUB_EXIT=1"},
			args:         []string{"kubectl", "get", "po"}, wantArgv: []string{"get", "po"}, wantKC: stackKC,
			wantExit: 1, wantStderr: "obol stack up",
		},
		{
			name:         "explicit --kubeconfig and no stack kubeconfig: no hint",
			noKubeconfig: true,
			env:          []string{"STUB_EXIT=1"},
			args:         []string{"kubectl", "--kubeconfig", "/x", "get", "po"}, wantArgv: []string{"--kubeconfig", "/x", "get", "po"}, wantKC: "",
			wantExit: 1, noStderr: "obol stack up",
		},
		{
			name:         "no kubeconfig: child self-SIGTERM maps to 143",
			noKubeconfig: true,
			env:          []string{"STUB_SIGNAL=TERM"},
			args:         []string{"helm", "template", "x"}, wantArgv: []string{"template", "x"}, wantKC: stackKC, wantExit: 143,
		},
		{
			name: "helmfile gets stack helmfile path",
			args: []string{"helmfile", "list"}, wantArgv: []string{"list"}, wantKC: stackKC, wantHelmfile: "<helmfile>",
		},
		{
			name: "helmfile -f wins",
			args: []string{"helmfile", "-f", "my.yaml", "list"}, wantArgv: []string{"-f", "my.yaml", "list"}, wantKC: stackKC,
		},
		{
			name: "global flag via env is not an error",
			env:  []string{"OBOL_OUTPUT=json"},
			args: []string{"kubectl", "get", "po"}, wantArgv: []string{"get", "po"}, wantKC: stackKC,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newObolHarness(t, !tc.noKubeconfig)
			rep, stdout, stderr, ps := h.run(t, tc.env, tc.stdin, tc.args...)

			if rep.Argv == nil {
				t.Fatalf("stub did not run; stdout=%q stderr=%q", stdout, stderr)
			}
			if !reflect.DeepEqual(rep.Argv, tc.wantArgv) {
				t.Errorf("argv = %q, want %q", rep.Argv, tc.wantArgv)
			}
			wantKC := tc.wantKC
			if wantKC == stackKC {
				wantKC = filepath.Join(h.configDir, "kubeconfig.yaml")
			}
			if got := deref(rep.Kubeconfig); got != wantKC {
				t.Errorf("KUBECONFIG = %q, want %q", got, wantKC)
			}
			wantHF := tc.wantHelmfile
			if wantHF == "<helmfile>" {
				wantHF = filepath.Join(h.configDir, "helmfile.yaml")
			}
			if got := deref(rep.HelmfileFilePath); got != wantHF {
				t.Errorf("HELMFILE_FILE_PATH = %q, want %q", got, wantHF)
			}
			if rep.Stdin != tc.stdin {
				t.Errorf("stdin = %q, want %q", rep.Stdin, tc.stdin)
			}
			if code := ps.ExitCode(); code != tc.wantExit {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tc.wantExit, stderr)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("stderr %q missing %q", stderr, tc.wantStderr)
			}
			if tc.noStderr != "" && strings.Contains(stderr, tc.noStderr) {
				t.Errorf("stderr %q must not contain %q", stderr, tc.noStderr)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// With a stack kubeconfig, obol is replaced by the tool (same PID) and a
// signal death is the shell-visible status, exactly as running the tool.
func TestPassthrough_ExecReplacesProcess(t *testing.T) {
	h := newObolHarness(t, true)
	rep, _, _, ps := h.run(t, []string{"STUB_SIGNAL=TERM"}, "", "kubectl", "get", "po")
	if rep.PID != ps.Pid() {
		t.Errorf("stub pid %d != obol pid %d: process was not replaced", rep.PID, ps.Pid())
	}
	ws := ps.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("status = %v, want killed by SIGTERM", ps)
	}
}

// SIGTERM to obol on the child path (no stack kubeconfig yet) reaches the
// tool; obol exits 143 and leaves no orphan.
func TestPassthrough_SIGTERMNotOrphaned(t *testing.T) {
	h := newObolHarness(t, false)
	cmd := h.command([]string{"STUB_SLEEP=20s"}, "kubectl", "get", "po", "-w")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read stub report: %v", err)
	}
	var rep stubtool.Report
	if err := json.Unmarshal(line, &rep); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 143 {
		t.Errorf("obol exit = %d, want 143", code)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("tool was not terminated promptly")
	}
	if err := syscall.Kill(rep.PID, 0); err == nil {
		_ = syscall.Kill(rep.PID, syscall.SIGKILL)
		t.Fatal("tool still running after obol exited (orphaned)")
	}
}

func TestPassthrough_GlobalFlagBeforeToolIsError(t *testing.T) {
	h := newObolHarness(t, true)
	rep, _, stderr, ps := h.run(t, nil, "", "-o", "json", "kubectl", "get", "po")
	if rep.Argv != nil {
		t.Fatalf("tool ran with %q; want obol to refuse", rep.Argv)
	}
	if ps.ExitCode() != 1 || !strings.Contains(stderr, "put kubectl flags after the tool name") {
		t.Fatalf("exit=%d stderr=%q", ps.ExitCode(), stderr)
	}
}

// `obol kubectl … <TAB>` is answered by the tool's own cobra __complete.
func TestPassthrough_CompletionDelegation(t *testing.T) {
	h := newObolHarness(t, true)

	_, stdout, stderr, ps := h.run(t, nil, "", "kubectl", "get", "--generate-shell-completion")
	if ps.ExitCode() != 0 {
		t.Fatalf("exit=%d stderr=%q", ps.ExitCode(), stderr)
	}
	if want := "pods:Pod resources\nlast=EMPTY\n"; stdout != want {
		t.Fatalf("completion = %q, want %q", stdout, want)
	}

	// A trailing flag is ambiguous (partial flag vs. completing its value):
	// both are asked and merged.
	_, stdout, _, _ = h.run(t, nil, "", "kubectl", "get", "po", "-n", "--generate-shell-completion")
	if want := "pods:Pod resources\nlast=-n\nlast=EMPTY\n"; stdout != want {
		t.Fatalf("completion = %q, want %q", stdout, want)
	}
}
