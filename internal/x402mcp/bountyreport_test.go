package x402mcp

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	basicCatalogID  = "https://a2ui.org/specification/v1_0/catalogs/basic/catalog.json"
	mcpAppCatalogID = "obol.org:mcp-app/v1"
)

func writeReportFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "hermes-obol-agent", "smoke-bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"report.a2ui.json": `{"messages":[{"version":"v1.0"}]}`,
		"report.app.html":  "<html><body>score & verdict</body></html>",
		"task.json":        `{"typeRef":"benchmark@v1"}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBountyReport_DeclarativeByDefault(t *testing.T) {
	root := writeReportFixture(t)

	out, err := renderBountyReport(root, bountyReportArgs{Name: "smoke-bench"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `"version":"v1.0"`) {
		t.Errorf("default render should be the raw declarative A2UI JSON, got %q", out)
	}
}

func TestBountyReport_NegotiatesMcpApp(t *testing.T) {
	root := writeReportFixture(t)

	out, err := renderBountyReport(root, bountyReportArgs{
		Name:                "smoke-bench",
		SupportedCatalogIDs: []string{mcpAppCatalogID, basicCatalogID},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	var node struct {
		Type       string `json:"type"`
		Name       string `json:"name"`
		Properties struct {
			Content string `json:"content"`
			Title   string `json:"title"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(out), &node); err != nil {
		t.Fatalf("mcp-app render is not a JSON node: %v", err)
	}
	if node.Type != "custom" || node.Name != "McpApp" {
		t.Errorf("node = %s/%s, want custom/McpApp", node.Type, node.Name)
	}
	if !strings.HasPrefix(node.Properties.Content, "url_encoded:") {
		t.Fatalf("content must be url_encoded:-prefixed, got %q", node.Properties.Content[:20])
	}
	decoded, err := url.QueryUnescape(strings.TrimPrefix(node.Properties.Content, "url_encoded:"))
	if err != nil {
		t.Fatalf("content does not decode: %v", err)
	}
	if decoded != "<html><body>score & verdict</body></html>" {
		t.Errorf("decoded content = %q (encoding must be decodeURIComponent-safe)", decoded)
	}
}

func TestBountyReport_PrefersClientOrder(t *testing.T) {
	root := writeReportFixture(t)

	out, err := renderBountyReport(root, bountyReportArgs{
		Name:                "smoke-bench",
		SupportedCatalogIDs: []string{basicCatalogID, mcpAppCatalogID},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(out, "McpApp") {
		t.Error("client preferred the basic catalog; declarative variant must win")
	}
}

func TestBountyReport_NoCatalogMatch(t *testing.T) {
	root := writeReportFixture(t)

	_, err := renderBountyReport(root, bountyReportArgs{
		Name:                "smoke-bench",
		SupportedCatalogIDs: []string{"example.com:unknown/v9"},
	})
	if err == nil || !strings.Contains(err.Error(), "supportedCatalogIds") {
		t.Errorf("no-match must error with the available catalogs, got %v", err)
	}
}

func TestBountyReport_InferenceWithoutSidecar(t *testing.T) {
	root := writeReportFixture(t)
	if err := os.Remove(filepath.Join(root, "hermes-obol-agent", "smoke-bench", "task.json")); err != nil {
		t.Fatal(err)
	}

	out, err := renderBountyReport(root, bountyReportArgs{Name: "smoke-bench"})
	if err != nil {
		t.Fatalf("inference from report files should work: %v", err)
	}
	if !strings.Contains(out, `"version":"v1.0"`) {
		t.Errorf("unexpected render: %q", out)
	}
}

func TestBountyReport_RejectsPathTraversal(t *testing.T) {
	root := writeReportFixture(t)

	for _, name := range []string{"../smoke-bench", "..", "a/b"} {
		if _, err := renderBountyReport(root, bountyReportArgs{Name: name}); err == nil {
			t.Errorf("name %q must be rejected (path traversal)", name)
		}
	}
	if _, err := renderBountyReport(root, bountyReportArgs{Name: "smoke-bench", Namespace: "../hermes-obol-agent"}); err == nil {
		t.Error("namespace traversal must be rejected")
	}
}

func TestBountyReport_MissingBounty(t *testing.T) {
	root := writeReportFixture(t)
	if _, err := renderBountyReport(root, bountyReportArgs{Name: "nonexistent"}); err == nil {
		t.Error("missing report dir must error")
	}
}
