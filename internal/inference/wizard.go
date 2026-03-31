package inference

// wizard.go — first-run inference wizard.
//
// Guides the user through discovering and selecting a local inference
// endpoint during initial setup. If inference is already configured,
// the wizard is a no-op.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// inferenceConfigFile is the filename within ConfigDir that marks inference as configured.
const inferenceConfigFile = "inference.json"

// RunInferenceWizard runs the first-run inference setup flow.
// It scans for local endpoints, displays them, and lets the user pick one.
func RunInferenceWizard(cfg *config.Config) error {
	return RunInferenceWizardIO(cfg, os.Stdin, os.Stdout)
}

// RunInferenceWizardIO is the testable version that accepts explicit I/O.
func RunInferenceWizardIO(cfg *config.Config, in io.Reader, out io.Writer) error {
	if IsInferenceConfigured(cfg) {
		fmt.Fprintln(out, "Inference already configured — skipping wizard.")
		return nil
	}

	fmt.Fprintln(out, "🔍 Scanning for local inference servers...")
	endpoints, err := ScanLocalEndpoints()
	if err != nil {
		return fmt.Errorf("scanning endpoints: %w", err)
	}

	if len(endpoints) == 0 {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "No local inference servers found.")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "You can still earn by selling compute on the network:")
		fmt.Fprintln(out, "  $ obol sell http")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Earn first, spend later — no wallet needed to start.")
		return nil
	}

	fmt.Fprintln(out, "")
	fmt.Fprint(out, FormatEndpointDisplay(endpoints))

	// Auto-select if only one endpoint with one model.
	if len(endpoints) == 1 && len(endpoints[0].Models) == 1 {
		ep := endpoints[0]
		m := ep.Models[0]
		fmt.Fprintf(out, "\nFound %s on %s (%s). Use this? [Y/n] ", m.ID, ep.BaseURL(), ep.ServerType)

		scanner := bufio.NewScanner(in)
		if scanner.Scan() {
			answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
			if answer != "" && answer != "y" && answer != "yes" {
				fmt.Fprintln(out, "Skipped. Run 'obol config inference' to set up later.")
				return nil
			}
		}
		fmt.Fprintf(out, "✅ Configured %s via %s\n", m.ID, ep.BaseURL())
		return nil
	}

	// Multiple endpoints or models — numbered list.
	fmt.Fprintln(out, "\nSelect an endpoint (enter number):")
	var choices []struct {
		ep    EndpointInfo
		model ModelInfo
	}
	idx := 1
	for _, ep := range endpoints {
		for _, m := range ep.Models {
			fmt.Fprintf(out, "  [%d] %s — %s (%s)\n", idx, m.ID, ep.BaseURL(), ep.ServerType)
			choices = append(choices, struct {
				ep    EndpointInfo
				model ModelInfo
			}{ep, m})
			idx++
		}
	}
	fmt.Fprint(out, "\n> ")

	scanner := bufio.NewScanner(in)
	if scanner.Scan() {
		n, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err != nil || n < 1 || n > len(choices) {
			fmt.Fprintln(out, "Invalid selection. Run 'obol config inference' to set up later.")
			return nil
		}
		choice := choices[n-1]
		fmt.Fprintf(out, "✅ Configured %s via %s\n", choice.model.ID, choice.ep.BaseURL())
	}
	return nil
}

// FormatEndpointDisplay pretty-prints a list of discovered endpoints.
func FormatEndpointDisplay(endpoints []EndpointInfo) string {
	var b strings.Builder
	b.WriteString("Discovered inference endpoints:\n")
	for _, ep := range endpoints {
		status := "✓ healthy"
		if !ep.Healthy {
			status = "✗ unhealthy"
		}
		fmt.Fprintf(&b, "\n  %s (%s) [%s]\n", ep.BaseURL(), ep.ServerType, status)
		if len(ep.Models) == 0 {
			b.WriteString("    (no models loaded)\n")
		}
		for _, m := range ep.Models {
			owner := m.OwnedBy
			if owner == "" {
				owner = "unknown"
			}
			fmt.Fprintf(&b, "    • %s (by %s)\n", m.ID, owner)
		}
	}
	return b.String()
}

// IsInferenceConfigured checks whether inference has already been set up.
func IsInferenceConfigured(cfg *config.Config) bool {
	if cfg == nil || cfg.ConfigDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(cfg.ConfigDir, inferenceConfigFile))
	if err != nil {
		return false
	}
	return info.Size() > 0
}
