package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/passthrough/stubtool"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

// TestMain lets this test binary act as the obol CLI (OBOL_TEST_MAIN=1) and
// as a stub kubectl/helm/helmfile (when started under that name), so
// passthrough behaviour is tested end to end on real processes.
func TestMain(m *testing.M) {
	if stubtool.IsStub() {
		stubtool.Main()
	}
	if os.Getenv("OBOL_TEST_MAIN") == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRenderEnv_Golden(t *testing.T) {
	const kc, bin = "/home/o b/.config/obol/kubeconfig.yaml", "/home/o b/.local/bin"
	curPath := strings.Join([]string{bin, "/usr/bin", "/bin"}, ":")

	for _, tc := range []struct {
		shell string
		unset bool
	}{
		{"sh", false},
		{"sh", true},
		{"fish", false},
		{"fish", true},
		{"pwsh", false},
		{"pwsh", true},
	} {
		name := "env_" + tc.shell
		if tc.unset {
			name += "_unset"
		}
		t.Run(name, func(t *testing.T) {
			got, err := renderEnv(tc.shell, kc, bin, tc.unset, curPath)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name+".golden")
			if *updateGolden {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if got != string(want) {
				t.Fatalf("output mismatch for %s:\n--- got\n%s--- want\n%s", name, got, want)
			}
		})
	}

	// bash/zsh share sh syntax; unknown shells are rejected.
	sh, _ := renderEnv("sh", kc, bin, false, curPath)
	for _, alias := range []string{"bash", "zsh"} {
		if got, _ := renderEnv(alias, kc, bin, false, curPath); got != sh {
			t.Errorf("%s output differs from sh", alias)
		}
	}
	if _, err := renderEnv("tcsh", kc, bin, false, curPath); err == nil {
		t.Error("tcsh: want error")
	}
}

func TestEnvCommand_NoCluster(t *testing.T) {
	cfg := newTestConfig(t)
	root := newRootCommand(cfg)
	var out bytes.Buffer
	root.Writer = &out
	if err := root.Run(context.Background(), []string{"obol", "env", "--shell", "fish"}); err != nil {
		t.Fatalf("obol env: %v", err)
	}
	if want := "set -gx KUBECONFIG '" + filepath.Join(cfg.ConfigDir, "kubeconfig.yaml") + "'"; !strings.Contains(out.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, out.String())
	}
}

func TestDetectShell(t *testing.T) {
	for shell, want := range map[string]string{
		"/usr/bin/fish": "fish", "/bin/zsh": "sh", "/bin/bash": "sh", "/usr/local/bin/pwsh": "pwsh",
	} {
		t.Setenv("SHELL", shell)
		if got := detectShell(); got != want {
			t.Errorf("SHELL=%s: detectShell() = %q, want %q", shell, got, want)
		}
	}
}

func TestParseCobraCompletion(t *testing.T) {
	raw := "pods\tPod resources\nservices\n_activeHelp_ ignore me\n:4\nafter-directive\n"
	got := parseCobraCompletion([]byte(raw))
	want := []string{"pods:Pod resources", "services"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGlobalFlagsBeforePassthrough_Rejected(t *testing.T) {
	for _, args := range [][]string{
		{"obol", "-o", "json", "kubectl", "get", "po"},
		{"obol", "--verbose", "helm", "list"},
		{"obol", "-q", "hermes", "chat"},
	} {
		root := newRootCommand(newTestConfig(t))
		root.Writer = &bytes.Buffer{}
		err := root.Run(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "must not come before") {
			t.Errorf("%v: err = %v, want global-flag error", args, err)
		}
	}
}
