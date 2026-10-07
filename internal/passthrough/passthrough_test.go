package passthrough

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/passthrough/stubtool"
)

// The test binary doubles as (a) the stub tool, when started under a stub
// name, and (b) a minimal driver calling Exec/Run/Handoff, when
// PASSTHROUGH_TEST_MODE is set — so process replacement, exit codes and
// signals are observed on real processes.
func TestMain(m *testing.M) {
	if stubtool.IsStub() {
		stubtool.Main()
	}
	if mode := os.Getenv("PASSTHROUGH_TEST_MODE"); mode != "" {
		driver(mode)
	}

	dir, err := os.MkdirTemp("", "passthrough-stub-")
	if err != nil {
		panic(err)
	}
	stubPath = filepath.Join(dir, "stubtool")
	if runtime.GOOS == "windows" {
		stubPath += ".exe"
	}
	if err := copyExecutable(stubPath); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var stubPath string

func driver(mode string) {
	stub := os.Getenv("STUB_PATH")
	env := os.Environ()
	var err error
	switch mode {
	case "exec":
		err = Exec(stub, os.Args[1:], env)
	case "run":
		var code int
		code, err = Run(stub, os.Args[1:], env)
		if err == nil {
			os.Exit(code)
		}
	case "handoff-hint":
		err = Handoff(stub, os.Args[1:], env, "HINT: is the stack up?")
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
	}
	if err != nil {
		os.Stderr.WriteString("driver: " + err.Error() + "\n")
		os.Exit(99)
	}
	os.Exit(0)
}

func copyExecutable(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// runDriver starts this test binary in driver mode and returns the stub
// report, stderr and the finished process state.
func runDriver(t *testing.T, mode string, extraEnv []string, stdin string, args ...string) (stubtool.Report, string, *os.ProcessState) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Env = append(os.Environ(), append([]string{"PASSTHROUGH_TEST_MODE=" + mode, "STUB_PATH=" + stubPath}, extraEnv...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()

	var rep stubtool.Report
	if line, _, _ := strings.Cut(stdout.String(), "\n"); line != "" {
		if err := json.Unmarshal([]byte(line), &rep); err != nil {
			t.Fatalf("stub output %q: %v", stdout.String(), err)
		}
	}
	return rep, stderr.String(), cmd.ProcessState
}

func TestStripSeparator(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{nil, nil},
		{[]string{"--"}, []string{}},
		{[]string{"--", "get", "po"}, []string{"get", "po"}},
		{[]string{"--", "--", "x"}, []string{"--", "x"}},
		{[]string{"exec", "p", "--", "ls"}, []string{"exec", "p", "--", "ls"}},
	} {
		if got := StripSeparator(tc.in); !reflect.DeepEqual(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
			t.Errorf("StripSeparator(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestKubeEnv(t *testing.T) {
	const stack = "/stack/kubeconfig.yaml"
	for _, tc := range []struct {
		name      string
		env, args []string
		want      string // expected effective KUBECONFIG ("" = unset)
		usesStack bool
	}{
		{"neither: stack default", []string{"PATH=/bin"}, []string{"get", "po"}, stack, true},
		{"ambient KUBECONFIG replaced", []string{"KUBECONFIG=/user/kc"}, []string{"get", "po"}, stack, true},
		{"empty KUBECONFIG replaced", []string{"KUBECONFIG="}, nil, stack, true},
		{"--context alone does not opt out", []string{"KUBECONFIG=/user/kc"}, []string{"--context", "work", "get"}, stack, true},
		{"--kubeconfig flag", nil, []string{"--kubeconfig", "/x", "get"}, "", false},
		{"--kubeconfig flag leaves env untouched", []string{"KUBECONFIG=/user/kc"}, []string{"--kubeconfig", "/x"}, "/user/kc", false},
		{"--kubeconfig= flag", nil, []string{"get", "--kubeconfig=/x"}, "", false},
		{"--kubeconfig after -- is not ours", nil, []string{"exec", "p", "--", "--kubeconfig=/x"}, stack, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, uses := KubeEnv(tc.env, tc.args, stack)
			got, _ := LookupEnv(env, "KUBECONFIG")
			if got != tc.want || uses != tc.usesStack {
				t.Fatalf("KUBECONFIG=%q usesStack=%v, want %q %v", got, uses, tc.want, tc.usesStack)
			}
			n := 0
			for _, kv := range env {
				if strings.HasPrefix(kv, "KUBECONFIG=") {
					n++
				}
			}
			if n > 1 {
				t.Fatalf("%d KUBECONFIG entries in %q: syscall.Exec would pass the first", n, env)
			}
		})
	}
}

func TestStackHint(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "kubeconfig.yaml")
	if StackHint(true, missing) != StackDownHint {
		t.Error("missing stack kubeconfig: want hint")
	}
	if StackHint(false, missing) != "" {
		t.Error("user-chosen kubeconfig: want no hint")
	}
	if err := os.WriteFile(missing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if StackHint(true, missing) != "" {
		t.Error("existing stack kubeconfig: want no hint")
	}
}

func TestResolveTool_EnvOverride(t *testing.T) {
	t.Setenv("OBOL_KUBECTL", stubPath)
	got, err := ResolveTool(t.TempDir(), "kubectl", nil)
	if err != nil || got != stubPath {
		t.Fatalf("ResolveTool = %q, %v; want %q", got, err, stubPath)
	}
}

func TestResolveTool_IncompatiblePathWarnsAndUses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}
	pathDir := t.TempDir()
	old := filepath.Join(pathDir, "helm")
	if err := os.WriteFile(old, []byte("#!/bin/sh\necho v2.17.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBOL_HELM", "")
	t.Setenv("PATH", pathDir)

	var warn bytes.Buffer
	got, err := ResolveTool(t.TempDir(), "helm", &warn)
	if err != nil || got != old {
		t.Fatalf("ResolveTool = %q, %v; want %q", got, err, old)
	}
	if !strings.Contains(warn.String(), "warning: using "+old) {
		t.Fatalf("warning = %q", warn.String())
	}

	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveTool(t.TempDir(), "helm", nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing tool: err = %v", err)
	}
}

func TestExec_ArgvEnvStdinAndExitCode(t *testing.T) {
	rep, _, ps := runDriver(t, "exec", []string{"KUBECONFIG=/user/kc", "STUB_EXIT=42"}, "piped input", "get", "--", "x y")
	if want := []string{"get", "--", "x y"}; !reflect.DeepEqual(rep.Argv, want) {
		t.Errorf("argv = %q, want %q", rep.Argv, want)
	}
	if rep.Kubeconfig == nil || *rep.Kubeconfig != "/user/kc" {
		t.Errorf("KUBECONFIG = %v", rep.Kubeconfig)
	}
	if rep.Stdin != "piped input" {
		t.Errorf("stdin = %q", rep.Stdin)
	}
	if ps.ExitCode() != 42 {
		t.Errorf("exit = %d, want 42", ps.ExitCode())
	}
	if runtime.GOOS != "windows" && rep.PID != ps.Pid() {
		t.Errorf("process not replaced: stub pid %d, driver pid %d", rep.PID, ps.Pid())
	}
}

func TestRun_ExitCode(t *testing.T) {
	_, _, ps := runDriver(t, "run", []string{"STUB_EXIT=42"}, "")
	if ps.ExitCode() != 42 {
		t.Fatalf("exit = %d, want 42", ps.ExitCode())
	}
}

func TestHandoff_HintOnlyOnFailure(t *testing.T) {
	_, stderr, ps := runDriver(t, "handoff-hint", nil, "", "version", "--client")
	if ps.ExitCode() != 0 || strings.Contains(stderr, "HINT") {
		t.Fatalf("success: exit=%d stderr=%q", ps.ExitCode(), stderr)
	}
	_, stderr, ps = runDriver(t, "handoff-hint", []string{"STUB_EXIT=1"}, "", "get", "po")
	if ps.ExitCode() != 1 || !strings.Contains(stderr, "HINT") {
		t.Fatalf("failure: exit=%d stderr=%q", ps.ExitCode(), stderr)
	}
}
