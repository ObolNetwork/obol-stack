package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// listedRelease is one entry of `helmfile list --output json`.
type listedRelease struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Chart   string `json:"chart"`
	Version string `json:"version"`
}

// applyChartCRDs server-side applies the CRDs shipped in each enabled
// release's chart before `helmfile sync`. Helm installs crds/ on first
// install only and never upgrades them, so without this an existing cluster
// keeps old CRD schemas after a chart bump (e.g. Traefik, prometheus-operator).
func applyChartCRDs(cfg *config.Config, u *ui.UI, helmfilePath, kubeconfigPath string, selectors []string) error {
	args := []string{"--file", helmfilePath, "--kubeconfig", kubeconfigPath}
	for _, s := range selectors {
		args = append(args, "--selector", s)
	}

	args = append(args, "list", "--output", "json")

	out, err := u.ExecOutput(ui.ExecConfig{
		Name: "Listing releases",
		Cmd:  helmcmd.Helmfile(cfg.ToolPath("helmfile"), cfg.ToolPath("helm"), args...),
	})
	if err != nil {
		return fmt.Errorf("helmfile list: %w", err)
	}

	var releases []listedRelease
	if err := json.Unmarshal(out, &releases); err != nil {
		return fmt.Errorf("parse helmfile list: %w", err)
	}

	for _, r := range releases {
		if !r.Enabled {
			continue
		}

		chart, showArgs := crdShowArgs(r, filepath.Dir(helmfilePath))

		crds, err := u.ExecOutput(ui.ExecConfig{
			Name: "Reading CRDs for " + chart,
			Cmd:  exec.Command(cfg.ToolPath("helm"), showArgs...),
		})
		if err != nil {
			return fmt.Errorf("helm show crds %s: %w", chart, err)
		}

		if len(bytes.TrimSpace(crds)) == 0 {
			continue
		}

		apply := exec.Command(cfg.ToolPath("kubectl"),
			"apply", "--server-side", "--force-conflicts",
			"--field-manager=obol-upgrade", "-f", "-")
		apply.Stdin = bytes.NewReader(crds)
		apply.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

		if err := u.Exec(ui.ExecConfig{Name: "Updating CRDs for " + r.Name, Cmd: apply}); err != nil {
			return fmt.Errorf("apply CRDs for %s: %w", r.Name, err)
		}
	}

	return nil
}

// crdShowArgs returns the chart reference and `helm show crds` arguments for
// a release. Local charts (./base) resolve against the helmfile's directory
// and take no --version.
func crdShowArgs(r listedRelease, helmfileDir string) (string, []string) {
	chart := r.Chart
	if strings.HasPrefix(chart, "./") || strings.HasPrefix(chart, "../") {
		chart = filepath.Join(helmfileDir, chart)
		return chart, []string{"show", "crds", chart}
	}

	args := []string{"show", "crds", chart}
	if r.Version != "" {
		args = append(args, "--version", r.Version)
	}

	return chart, args
}
