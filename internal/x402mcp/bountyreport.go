package x402mcp

// bounty_report — a FREE companion tool on the MCP server: serves the A2UI
// report deliverable of a settled ServiceBounty. Reports are gate:local in v1
// (the fulfiller's runner persists them on disk under the agent hierarchy);
// the cross-party paid gate (gate: mcp-x402) is this same tool wrapped with
// the existing payment wrapper — no new machinery.
//
// Variant selection is a2ui catalog negotiation: the caller passes its
// supportedCatalogIds in preference order and the first task-package variant
// whose catalogId matches wins. kind=declarative returns the raw A2UI
// v1.0-candidate message-list JSON (native render, no iframes); kind=mcp-app
// wraps the self-contained HTML into a `custom` McpApp node with url_encoded
// content — the CLIENT supplies double-iframe isolation (sandbox proxy +
// srcdoc inner frame, never allow-same-origin); this server only returns JSON.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ObolNetwork/obol-stack/internal/bounty"
)

type bountyReportArgs struct {
	Name                string   `json:"name"`
	Namespace           string   `json:"namespace"`
	TaskType            string   `json:"taskType"`
	SupportedCatalogIDs []string `json:"supportedCatalogIds"`
}

// bountyReportMeta is the optional task.json sidecar the runner writes next to
// the report files, removing task-type inference ambiguity.
type bountyReportMeta struct {
	TypeRef string `json:"typeRef"`
}

// AddBountyReportTool registers the free bounty_report tool. reportsDir layout:
// <reportsDir>/<namespace>/<name>/<variant surface file> (+ optional task.json
// sidecar {"typeRef":"benchmark@v1"}).
func AddBountyReportTool(server *mcpsdk.Server, reportsDir string) {
	server.AddTool(&mcpsdk.Tool{
		Name: "bounty_report",
		Description: "Fetch a ServiceBounty's A2UI report. Pass supportedCatalogIds in preference " +
			"order (a2ui catalog negotiation): a declarative match returns the A2UI v1.0 message list; " +
			"obol.org:mcp-app/v1 returns a custom McpApp node (self-contained HTML, render in the " +
			"double-iframe sandbox). Args: {name, namespace?, taskType?, supportedCatalogIds?}.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name":                map[string]any{"type": "string", "description": "Bounty name."},
				"namespace":           map[string]any{"type": "string", "description": "Bounty namespace (default hermes-obol-agent)."},
				"taskType":            map[string]any{"type": "string", "description": "Task type ref (e.g. benchmark@v1); inferred from the task.json sidecar or the report files when omitted."},
				"supportedCatalogIds": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Client-supported catalog ids in preference order."},
			},
		},
	}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var args bountyReportArgs
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return errResult(fmt.Sprintf("bad arguments: %v", err)), nil
			}
		}
		out, err := renderBountyReport(reportsDir, args)
		if err != nil {
			return errResult(err.Error()), nil
		}
		return textResult(out), nil
	})
}

// renderBountyReport resolves the report directory, negotiates the variant,
// and renders it. Exposed for tests.
func renderBountyReport(reportsDir string, args bountyReportArgs) (string, error) {
	if strings.TrimSpace(args.Name) == "" {
		return "", errors.New("name is required")
	}
	if args.Namespace == "" {
		args.Namespace = "hermes-obol-agent"
	}
	// The two path segments come from the caller — never let them escape the
	// reports root.
	for _, segment := range []string{args.Name, args.Namespace} {
		if segment != filepath.Base(segment) || segment == ".." || segment == "." {
			return "", fmt.Errorf("invalid path segment %q", segment)
		}
	}

	dir := filepath.Join(reportsDir, args.Namespace, args.Name)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("no report found for %s/%s", args.Namespace, args.Name)
	}

	t, err := resolveReportTaskType(dir, args.TaskType)
	if err != nil {
		return "", err
	}

	variant, raw, err := negotiateReportVariant(dir, t, args.SupportedCatalogIDs)
	if err != nil {
		return "", err
	}

	if variant.Kind == "mcp-app" {
		node := map[string]any{
			"type": "custom",
			"name": "McpApp",
			"properties": map[string]any{
				"title": fmt.Sprintf("%s — %s report", args.Name, t.Ref()),
				// decodeURIComponent-safe percent encoding (QueryEscape's '+'
				// for space would corrupt the HTML on decode).
				"content": "url_encoded:" + strings.ReplaceAll(url.QueryEscape(string(raw)), "+", "%20"),
			},
		}
		encoded, err := json.Marshal(node)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	return string(raw), nil
}

// resolveReportTaskType picks the task type: explicit arg > task.json sidecar >
// first enabled type with a variant surface present in dir.
func resolveReportTaskType(dir, explicit string) (bounty.TaskType, error) {
	if explicit != "" {
		return bounty.Resolve(explicit)
	}

	if raw, err := os.ReadFile(filepath.Join(dir, "task.json")); err == nil {
		var meta bountyReportMeta
		if err := json.Unmarshal(raw, &meta); err == nil && meta.TypeRef != "" {
			return bounty.Resolve(meta.TypeRef)
		}
	}

	enabled, err := bounty.Enabled()
	if err != nil {
		return bounty.TaskType{}, err
	}
	for _, t := range enabled {
		for _, v := range t.Deliverable.Report.Variants {
			if _, err := os.Stat(filepath.Join(dir, filepath.Base(v.Surface))); err == nil {
				return t, nil
			}
		}
	}
	return bounty.TaskType{}, fmt.Errorf("cannot infer task type for %s (write a task.json sidecar or pass taskType)", dir)
}

// negotiateReportVariant applies a2ui catalog negotiation: walk the caller's
// supportedCatalogIds in preference order, return the first variant that
// matches AND whose surface file exists. No ids → first variant present.
func negotiateReportVariant(dir string, t bounty.TaskType, supported []string) (bounty.ReportVariant, []byte, error) {
	variants := t.Deliverable.Report.Variants
	if len(variants) == 0 {
		return bounty.ReportVariant{}, nil, fmt.Errorf("task type %s declares no report variants", t.Ref())
	}

	read := func(v bounty.ReportVariant) []byte {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.Base(v.Surface)))
		if err != nil {
			return nil
		}
		return raw
	}

	if len(supported) > 0 {
		for _, id := range supported {
			for _, v := range variants {
				if v.CatalogID == id {
					if raw := read(v); raw != nil {
						return v, raw, nil
					}
				}
			}
		}
		return bounty.ReportVariant{}, nil, fmt.Errorf(
			"no variant of %s matches supportedCatalogIds %v (available: %s)", t.Ref(), supported, variantCatalogs(variants))
	}

	for _, v := range variants {
		if raw := read(v); raw != nil {
			return v, raw, nil
		}
	}
	return bounty.ReportVariant{}, nil, fmt.Errorf("no report files present in %s", dir)
}

func variantCatalogs(variants []bounty.ReportVariant) string {
	ids := make([]string, 0, len(variants))
	for _, v := range variants {
		ids = append(ids, v.CatalogID)
	}
	return strings.Join(ids, ", ")
}
