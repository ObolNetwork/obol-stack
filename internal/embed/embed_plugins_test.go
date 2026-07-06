package embed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// payMCPFiles are the files the embedded pay_mcp plugin must ship. The plugin
// loads from a directory, so plugin.yaml + __init__.py are load-critical; the
// rest are imported relatively by __init__/register().
var payMCPFiles = []string{
	"plugin.yaml", "__init__.py", "x402.py", "rails.py", "payment.py", "recovery.py",
}

func TestGetEmbeddedPluginNames(t *testing.T) {
	names, err := GetEmbeddedPluginNames()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "pay_mcp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("embedded plugins %v missing pay_mcp", names)
	}
}

func TestCopyPlugins_SeedsPayMCP(t *testing.T) {
	dst := t.TempDir()
	if err := CopyPlugins(dst); err != nil {
		t.Fatalf("CopyPlugins: %v", err)
	}

	for _, f := range payMCPFiles {
		p := filepath.Join(dst, "pay_mcp", f)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("missing seeded file %s: %v", f, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("seeded file %s is empty", f)
		}
	}
}

func TestCopyPlugins_NoPycache(t *testing.T) {
	dst := t.TempDir()
	if err := CopyPlugins(dst); err != nil {
		t.Fatalf("CopyPlugins: %v", err)
	}
	err := filepath.Walk(dst, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == "__pycache__" {
			t.Errorf("__pycache__ leaked into seed: %s", path)
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".pyc") || strings.HasSuffix(path, ".pyo")) {
			t.Errorf("compiled python leaked into seed: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestCopyPlugins_RelativeImportsOnly is the inject-by-default invariant: a
// user-dir plugin is loaded under the synthetic package name
// hermes_plugins.pay_mcp, and a stock hermes image has no bundled
// plugins.pay_mcp to satisfy an absolute self-import. So the embedded copy must
// import its own modules relatively (`from . import x402`), never
// `from plugins.pay_mcp import x402`. Upstream locks this with
// tests/plugins/test_pay_mcp_userdir_load.py; we re-assert on the vendored copy
// because the two are synced by hand.
func TestCopyPlugins_RelativeImportsOnly(t *testing.T) {
	dst := t.TempDir()
	if err := CopyPlugins(dst); err != nil {
		t.Fatalf("CopyPlugins: %v", err)
	}
	pyFiles := []string{"__init__.py", "x402.py", "rails.py", "payment.py", "recovery.py"}
	for _, f := range pyFiles {
		data, err := os.ReadFile(filepath.Join(dst, "pay_mcp", f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if strings.Contains(string(data), "from plugins.pay_mcp") ||
			strings.Contains(string(data), "import plugins.pay_mcp") {
			t.Errorf("%s uses an absolute self-import; must be relative "+
				"(breaks load from the user-plugins dir on a stock image)", f)
		}
	}
}

func TestCopyPlugins_ManifestNamesPayMCP(t *testing.T) {
	dst := t.TempDir()
	if err := CopyPlugins(dst); err != nil {
		t.Fatalf("CopyPlugins: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "pay_mcp", "plugin.yaml"))
	if err != nil {
		t.Fatalf("read plugin.yaml: %v", err)
	}
	// The seeded manifest name must match the plugins.enabled entry the agent
	// configs write (pay_mcp), or the plugin is discovered-but-not-enabled.
	if !strings.Contains(string(data), "name: pay_mcp") {
		t.Errorf("plugin.yaml does not declare `name: pay_mcp`:\n%s", data)
	}
}

// TestCopyPlugins_PreservesUserPlugins mirrors the skills contract: re-seeding
// must not delete a user-added plugin with a different name.
func TestCopyPlugins_PreservesUserPlugins(t *testing.T) {
	dst := t.TempDir()
	custom := filepath.Join(dst, "my-own-plugin")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "plugin.yaml"), []byte("name: my-own-plugin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyPlugins(dst); err != nil {
		t.Fatalf("CopyPlugins: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "plugin.yaml")); err != nil {
		t.Errorf("user plugin was clobbered by re-seed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "pay_mcp", "__init__.py")); err != nil {
		t.Errorf("pay_mcp not seeded alongside user plugin: %v", err)
	}
}
