package kubectl

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseForwardedPort(t *testing.T) {
	out := "Forwarding from 127.0.0.1:41234 -> 8545\nHandling connection for 41234\n"
	port, err := ParseForwardedPort(strings.NewReader(out), time.Second)
	if err != nil || port != 41234 {
		t.Fatalf("port=%d err=%v", port, err)
	}

	if _, err := ParseForwardedPort(strings.NewReader("error: something\n"), time.Second); err == nil {
		t.Fatal("expected error when kubectl exits without a Forwarding line")
	}
}

// fakeKubectl writes a shell script standing in for kubectl.
func fakeKubectl(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	path := filepath.Join(t.TempDir(), "kubectl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPortForward_PinsKubeconfigAndReportsPort(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	bin := fakeKubectl(t, `echo "$KUBECONFIG $@" > `+argsFile+`
echo "Forwarding from 127.0.0.1:45678 -> 5052"
exec sleep 30`)

	t.Setenv("KUBECONFIG", "/home/user/.kube/config")
	fwd, err := PortForward(context.Background(), bin, "/stack/kubeconfig.yaml", "ethereum-x", "svc/ethereum-beacon", 5052, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer fwd.Close()

	if fwd.URL() != "http://127.0.0.1:45678" {
		t.Fatalf("URL = %s", fwd.URL())
	}
	got, _ := os.ReadFile(argsFile)
	want := "/stack/kubeconfig.yaml port-forward --address 127.0.0.1 -n ethereum-x svc/ethereum-beacon :5052"
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("kubectl invoked as %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func TestPortForward_SurfacesKubectlError(t *testing.T) {
	bin := fakeKubectl(t, `echo "error: unable to forward port because pod is not running. Current status=Pending" >&2
exit 1`)

	_, err := PortForward(context.Background(), bin, "/k", "ns", "svc/x", 1, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "pod is not running") {
		t.Fatalf("err = %v, want kubectl's stderr", err)
	}
}

func TestPortForward_TimesOut(t *testing.T) {
	bin := fakeKubectl(t, `exec sleep 30`)

	start := time.Now()
	_, err := PortForward(context.Background(), bin, "/k", "ns", "svc/x", 1, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("timeout took %s", time.Since(start))
	}
}
