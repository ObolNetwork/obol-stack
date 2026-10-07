package main

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/images"
)

// A recorded offer pinned to an old dev tag must be re-resolved on replay;
// non-managed images (user upstreams) must be left alone.
func TestRefreshManagedImages(t *testing.T) {
	manifest := map[string]any{
		"kind": "Deployment",
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{
			map[string]any{"name": "demo", "image": "ghcr.io/obolnetwork/demo-server:dev-36b3d1f16b5a"},
			map[string]any{"name": "user", "image": "docker.io/library/nginx:1.27"},
		}}}},
	}
	replacers := images.BuildReplacers(func(repo string) string { return repo + ":dev-new" })

	got, err := refreshManagedImages(manifest, replacers)
	if err != nil {
		t.Fatal(err)
	}

	containers := got["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)
	if img := containers[0].(map[string]any)["image"]; img != "ghcr.io/obolnetwork/demo-server:dev-new" {
		t.Errorf("managed image = %v, want re-resolved dev-new", img)
	}
	if img := containers[1].(map[string]any)["image"]; img != "docker.io/library/nginx:1.27" {
		t.Errorf("user image changed: %v", img)
	}
}
