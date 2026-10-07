package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// runCapturingStdio runs the CLI with os.Stdout/os.Stderr redirected (the
// root Before hook builds its UI from the process streams).
func runCapturingStdio(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW

	var outBuf, errBuf bytes.Buffer
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(&outBuf, outR); done <- struct{}{} }()
	go func() { _, _ = io.Copy(&errBuf, errR); done <- struct{}{} }()

	err = newRootCommand(newTestConfig(t)).Run(context.Background(), append([]string{"obol"}, args...))

	_ = outW.Close()
	_ = errW.Close()
	<-done
	<-done
	os.Stdout, os.Stderr = oldOut, oldErr

	return outBuf.String(), errBuf.String(), err
}

func TestDeprecation_OpenClawRuntimeWarnsOnceOnStderr(t *testing.T) {
	stdout, stderr, err := runCapturingStdio(t, "-o", "json", "agent", "list", "--runtime", "openclaw")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := strings.Count(stderr, "Deprecated: OpenClaw"); got != 1 {
		t.Fatalf("OpenClaw deprecation printed %d times on stderr, want 1:\n%s", got, stderr)
	}

	if !strings.Contains(stderr, deprecationRemovalVersion) || !strings.Contains(stderr, "agent wallet restore --runtime hermes") {
		t.Fatalf("warning must name the removal version and the Hermes migration:\n%s", stderr)
	}

	if strings.Contains(stdout, "Deprecated") {
		t.Fatalf("deprecation leaked to stdout in JSON mode: %q", stdout)
	}

	var v any
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("stdout is not clean JSON: %v\n%s", err, stdout)
	}
}

func TestDeprecation_HermesRuntimeDoesNotWarn(t *testing.T) {
	_, stderr, err := runCapturingStdio(t, "agent", "list", "--runtime", "hermes")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if strings.Contains(stderr, "Deprecated") {
		t.Fatalf("unexpected deprecation warning: %s", stderr)
	}
}

func TestDeprecation_CommandsWired(t *testing.T) {
	root := newRootCommand(newTestConfig(t))

	for _, path := range [][]string{{"openclaw"}, {"domain"}, {"tunnel", "login"}} {
		c := root
		for _, name := range path {
			c = c.Command(name)
			if c == nil {
				t.Fatalf("command %v not found", path)
			}
		}

		if c.Before == nil {
			t.Errorf("obol %s: missing deprecation Before hook", strings.Join(path, " "))
		}

		if !strings.HasSuffix(c.Usage, deprecatedUsageSuffix) {
			t.Errorf("obol %s: Usage %q should end with %q", strings.Join(path, " "), c.Usage, deprecatedUsageSuffix)
		}
	}
}

func TestDeprecation_LocalTunnelManagementWarnsOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer

	root := &cli.Command{Name: "obol"}
	sub := &cli.Command{Name: "setup"}
	root.Commands = []*cli.Command{sub}

	if err := root.Run(context.Background(), []string{"obol"}); err != nil {
		t.Fatal(err)
	}

	root.Metadata = map[string]any{"ui": ui.NewForTest(&stdout, &stderr)}

	warnIfLocalTunnelManagement(sub, "connector")

	if stderr.Len() != 0 {
		t.Fatalf("connector management must not warn: %s", stderr.String())
	}

	warnIfLocalTunnelManagement(sub, "local")
	warnIfLocalTunnelManagement(sub, "Local")

	if got := strings.Count(stderr.String(), "Deprecated:"); got != 1 {
		t.Fatalf("local management warned %d times, want 1:\n%s", got, stderr.String())
	}

	if stdout.Len() != 0 {
		t.Fatalf("warning leaked to stdout: %s", stdout.String())
	}
}
