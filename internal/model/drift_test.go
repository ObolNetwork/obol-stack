package model

import (
	"reflect"
	"testing"
)

// DiffRouterModels is the drift safety net that replaced the Reloader
// annotation on litellm-config (issue #321): with hot-add/hot-delete as the
// only live-apply path, a silently-failed hot call must surface as drift in
// `obol model status` instead of a router that quietly disagrees with the
// ConfigMap.
func TestDiffRouterModels(t *testing.T) {
	tests := []struct {
		name        string
		configured  []string
		live        []string
		wantMissing []string
		wantExtra   []string
	}{
		{
			name:       "in sync",
			configured: []string{"anthropic/claude-sonnet-5", "qwen3.5:9b"},
			live:       []string{"anthropic/claude-sonnet-5", "qwen3.5:9b"},
		},
		{
			name:        "hot-add failed: configured model absent from router",
			configured:  []string{"qwen3.5:9b", "qwen3.5:4b"},
			live:        []string{"qwen3.5:9b"},
			wantMissing: []string{"qwen3.5:4b"},
		},
		{
			name:       "hot-delete failed: removed model still served",
			configured: []string{"qwen3.5:9b"},
			live:       []string{"qwen3.5:9b", "qwen3.5:4b"},
			wantExtra:  []string{"qwen3.5:4b"},
		},
		{
			name:       "configured wildcard is never missing",
			configured: []string{"paid/*"},
			live:       []string{},
		},
		{
			name:       "live model matched by configured wildcard is not extra",
			configured: []string{"paid/*"},
			live:       []string{"paid/qwen36-deep"},
		},
		{
			name:       "live wildcard group listed verbatim is not extra",
			configured: []string{"paid/*"},
			live:       []string{"paid/*"},
		},
		{
			name:        "wildcard does not cover other prefixes",
			configured:  []string{"paid/*", "qwen3.5:9b"},
			live:        []string{"paid/qwen36-deep", "stale-model"},
			wantMissing: []string{"qwen3.5:9b"},
			wantExtra:   []string{"stale-model"},
		},
		{
			name:       "both empty",
			configured: nil,
			live:       nil,
		},
		{
			name:      "empty config with live models",
			live:      []string{"orphan"},
			wantExtra: []string{"orphan"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DiffRouterModels(tt.configured, tt.live)

			if !reflect.DeepEqual(got.Missing, tt.wantMissing) {
				t.Errorf("Missing = %v; want %v", got.Missing, tt.wantMissing)
			}
			if !reflect.DeepEqual(got.Extra, tt.wantExtra) {
				t.Errorf("Extra = %v; want %v", got.Extra, tt.wantExtra)
			}

			wantEmpty := len(tt.wantMissing) == 0 && len(tt.wantExtra) == 0
			if got.Empty() != wantEmpty {
				t.Errorf("Empty() = %v; want %v", got.Empty(), wantEmpty)
			}
		})
	}
}
