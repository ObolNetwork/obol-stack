package stack

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestWarnLeftoverOpenClawNamespaces(t *testing.T) {
	orig := listNamespacesFn

	t.Cleanup(func() { listNamespacesFn = orig })

	tests := []struct {
		name     string
		names    []string
		err      error
		wantWarn bool
	}{
		{name: "openclaw instance present", names: []string{"kube-system", "hermes-obol-agent", "openclaw-legacy"}, err: nil, wantWarn: true},
		{name: "hermes only", names: []string{"kube-system", "hermes-obol-agent", "x402"}, err: nil, wantWarn: false},
		{name: "lookup fails", names: nil, err: errors.New("cluster unreachable"), wantWarn: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listNamespacesFn = func(*config.Config) ([]string, error) { return tt.names, tt.err }

			var stdout, stderr bytes.Buffer

			cfg := &config.Config{ConfigDir: "/cfg", DataDir: "/data", BinDir: "", StateDir: ""}
			warnLeftoverOpenClawNamespaces(cfg, ui.NewForTest(&stdout, &stderr))

			got := stderr.String()

			if tt.wantWarn {
				for _, want := range []string{
					"openclaw-legacy",
					"removed in v0.16",
					"/data/openclaw-legacy/remote-signer-keystores",
					"/cfg/applications/openclaw/legacy/values-remote-signer.yaml",
					"BEFORE deleting the namespace",
					"agent wallet restore --input",
				} {
					if !strings.Contains(got, want) {
						t.Fatalf("warning missing %q:\n%s", want, got)
					}
				}

				return
			}

			if got != "" {
				t.Fatalf("unexpected warning: %s", got)
			}
		})
	}
}
