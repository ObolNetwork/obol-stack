package embed

import (
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"
)

// TestInternalHTTPRoutes_HostnameRestricted guards the tunnel-exposure
// invariant (CLAUDE.md "Security: Tunnel Exposure"): internal services —
// the frontend UI, eRPC and Grafana — must only match Host obol.stack, so
// the public Cloudflare tunnel hostname can never reach them. Removing the
// hostnames restriction from any of these routes is a critical security
// regression, not a refactor.
func TestInternalHTTPRoutes_HostnameRestricted(t *testing.T) {
	want := []any{"obol.stack"}
	required := []struct {
		file, namespace, name, path string
	}{
		{"base/templates/obol-frontend.yaml", "obol-frontend", "obol-frontend", "/"},
		{"base/templates/erpc.yaml", "erpc", "erpc", "/rpc"},
		{"base/templates/grafana.yaml", "monitoring", "grafana", "/grafana"},
	}
	for _, r := range required {
		t.Run(r.name, func(t *testing.T) {
			data, err := ReadInfrastructureFile(r.file)
			if err != nil {
				t.Fatal(err)
			}
			route := findDocByName(multiDoc(data), "HTTPRoute", r.name)
			if route == nil {
				t.Fatalf("HTTPRoute %q missing from %s", r.name, r.file)
			}
			if ns := nested(route, "metadata", "namespace"); ns != r.namespace {
				t.Errorf("namespace = %v, want %s", ns, r.namespace)
			}
			if got := nested(route, "spec", "hostnames"); !reflect.DeepEqual(got, want) {
				t.Errorf("hostnames = %v, want %v: internal routes must stay off the public tunnel", got, want)
			}
			rules, _ := nested(route, "spec", "rules").([]any)
			if len(rules) == 0 {
				t.Fatal("route has no rules")
			}
			matches, _ := rules[0].(map[string]any)["matches"].([]any)
			if len(matches) == 0 {
				t.Fatal("route has no matches")
			}
			if p := nested(matches[0].(map[string]any), "path", "value"); p != r.path {
				t.Errorf("path = %v, want %s", p, r.path)
			}
		})
	}
}

// TestBaseHTTPRoutes_AllHostnameRestricted makes "local only" the default for
// every HTTPRoute the base chart ships: a new route without
// hostnames: ["obol.stack"] fails here. Public routes are published by the
// serviceoffer-controller, not by static templates; if a static public route
// is ever genuinely needed, add it to publicRoutes with a justification.
func TestBaseHTTPRoutes_AllHostnameRestricted(t *testing.T) {
	publicRoutes := map[string]bool{}
	want := []any{"obol.stack"}

	err := fs.WalkDir(infrastructureFS, "infrastructure/base/templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".yaml" {
			return err
		}
		data, err := infrastructureFS.ReadFile(p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "kind: HTTPRoute") {
			return nil
		}
		parsed := 0
		for _, doc := range multiDoc(data) {
			if doc["kind"] != "HTTPRoute" {
				continue
			}
			parsed++
			name, _ := nested(doc, "metadata", "name").(string)
			if publicRoutes[name] {
				continue
			}
			if got := nested(doc, "spec", "hostnames"); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: HTTPRoute %q hostnames = %v, want %v", p, name, got, want)
			}
		}
		// multiDoc skips documents it cannot parse; don't let a route
		// escape the check that way.
		if declared := strings.Count(string(data), "\nkind: HTTPRoute\n"); parsed != declared {
			t.Errorf("%s: parsed %d HTTPRoutes but the file declares %d", p, parsed, declared)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
