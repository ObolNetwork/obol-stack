package stack

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestWarnLegacyOpenClawNamespaces(t *testing.T) {
	orig := listNamespacesFn
	t.Cleanup(func() { listNamespacesFn = orig })

	tests := []struct {
		name     string
		names    []string
		err      error
		wantWarn bool
	}{
		{name: "openclaw instance present", names: []string{"kube-system", "hermes-obol-agent", "openclaw-legacy"}, wantWarn: true},
		{name: "hermes only", names: []string{"kube-system", "hermes-obol-agent", "x402"}},
		{name: "lookup fails", err: errors.New("cluster unreachable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listNamespacesFn = func(*config.Config) ([]string, error) { return tt.names, tt.err }

			var stdout, stderr bytes.Buffer
			warnLegacyOpenClawNamespaces(&config.Config{}, ui.NewForTest(&stdout, &stderr))

			got := stderr.String()
			if tt.wantWarn {
				if !strings.Contains(got, "openclaw-legacy") || strings.Count(got, "OpenClaw is deprecated") != 1 ||
					!strings.Contains(got, "agent wallet restore --runtime hermes") {
					t.Fatalf("missing deprecation/migration notice:\n%s", got)
				}

				return
			}

			if got != "" {
				t.Fatalf("unexpected warning: %s", got)
			}
		})
	}
}
