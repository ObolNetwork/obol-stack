// Package kubectl provides helpers for running kubectl commands with the
// correct KUBECONFIG environment variable set. It centralises the pattern
// that was previously duplicated across network, x402, model, agent, and
// cmd/obol packages.
package kubectl

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// EnsureCluster checks that the kubeconfig file exists, returning a
// descriptive error when the cluster is not running.
func EnsureCluster(cfg *config.Config) error {
	kubeconfig := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	return nil
}

// ErrClusterDown indicates the Kubernetes API server is unreachable,
// typically because the k3d cluster is stopped.
var ErrClusterDown = errors.New("cluster appears to be stopped — run 'obol stack up' to start it")

// wrapClusterDown checks whether an error (or the accompanying stderr text)
// indicates the Kubernetes API server is unreachable and, if so, returns
// ErrClusterDown.  This catches the common case where the kubeconfig file
// still exists from a prior session but the k3d cluster has been stopped.
func wrapClusterDown(err error, stderr string) error {
	if err == nil {
		return nil
	}

	combined := err.Error() + " " + stderr
	if strings.Contains(combined, "connection refused") ||
		strings.Contains(combined, "connect: no route to host") ||
		strings.Contains(combined, "Unable to connect to the server") {
		return ErrClusterDown
	}

	return err
}

// clusterDownHints maps CLI subcommand paths to human-friendly descriptions
// of what the command was trying to do.  Used by FormatClusterDownError to
// produce contextual messages like "run 'obol stack up' before listing
// services for sale".
var clusterDownHints = map[string]string{
	"sell list":          "listing services for sale",
	"sell status":        "checking service status",
	"sell stop":          "stopping a service",
	"sell delete":        "deleting a service",
	"sell test":          "testing a service endpoint",
	"sell pricing":       "configuring pricing",
	"sell register":      "registering on the agent registry",
	"sell inference":     "starting the inference gateway",
	"sell http":          "creating an HTTP service offer",
	"network sync":       "syncing a network deployment",
	"network delete":     "deleting a network deployment",
	"network status":     "checking network status",
	"openclaw onboard":   "onboarding an OpenClaw instance",
	"openclaw sync":      "syncing an OpenClaw instance",
	"openclaw setup":     "configuring OpenClaw",
	"openclaw token":     "retrieving the gateway token",
	"openclaw delete":    "deleting an OpenClaw instance",
	"openclaw list":      "listing OpenClaw instances",
	"openclaw dashboard": "opening the dashboard",
	"model status":       "checking model status",
	"model list":         "listing models",
	"model setup":        "configuring a model",
	"model remove":       "removing a model",
	"app sync":           "syncing an app deployment",
	"app delete":         "deleting an app deployment",
	"tunnel status":      "checking tunnel status",
	"tunnel restart":     "restarting the tunnel",
	"tunnel logs":        "fetching tunnel logs",
	"tunnel login":       "configuring the tunnel",
	"tunnel provision":   "provisioning the tunnel",
	"agent init":         "initializing the agent",
}

// FormatClusterDownError returns a contextual message for a cluster-down
// error based on the CLI arguments.  Returns empty string if err is not
// ErrClusterDown.
func FormatClusterDownError(err error, args []string) string {
	if !errors.Is(err, ErrClusterDown) {
		return ""
	}

	// Try "subcmd subsubcmd" then "subcmd" to find a matching hint.
	if len(args) >= 3 {
		if hint, ok := clusterDownHints[args[1]+" "+args[2]]; ok {
			return fmt.Sprintf("cluster appears to be stopped — run 'obol stack up' before %s", hint)
		}
	}
	if len(args) >= 2 {
		if hint, ok := clusterDownHints[args[1]]; ok {
			return fmt.Sprintf("cluster appears to be stopped — run 'obol stack up' before %s", hint)
		}
	}

	return ErrClusterDown.Error()
}

// Paths returns the absolute paths to the kubectl binary and kubeconfig.
func Paths(cfg *config.Config) (binary, kubeconfig string) {
	return filepath.Join(cfg.BinDir, "kubectl"),
		filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
}

// Run executes kubectl with the given arguments, inheriting stdout and
// capturing stderr. The error message includes stderr output on failure.
func Run(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return wrapClusterDown(fmt.Errorf("%w: %s", err, errMsg), errMsg)
		}

		return wrapClusterDown(err, "")
	}

	return nil
}

// RunSilent executes kubectl without inheriting stdout. Stderr is captured
// and included in the returned error on failure.
func RunSilent(binary, kubeconfig string, args ...string) error {
	cmd := exec.Command(binary, args...)

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return wrapClusterDown(fmt.Errorf("%w: %s", err, errMsg), errMsg)
		}

		return wrapClusterDown(err, "")
	}

	return nil
}

// Output executes kubectl and returns the captured stdout. Stderr is
// captured and included in the returned error on failure.
func Output(binary, kubeconfig string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout

	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", wrapClusterDown(fmt.Errorf("%w: %s", err, errMsg), errMsg)
		}

		return "", wrapClusterDown(err, "")
	}

	return stdout.String(), nil
}

// Apply pipes the given data into kubectl apply -f -.
func Apply(binary, kubeconfig string, data []byte) error {
	_, err := ApplyOutput(binary, kubeconfig, data)
	return err
}

// ApplyServerSideForceConflicts pipes the given data into kubectl apply
// --server-side --force-conflicts -f -. Use it only for narrow compatibility
// migrations where the caller has already decided which manager must own the
// restored fields.
func ApplyServerSideForceConflicts(binary, kubeconfig string, data []byte, fieldManager string) error {
	args := []string{"apply", "--server-side", "--force-conflicts", "-f", "-"}
	if strings.TrimSpace(fieldManager) != "" {
		args = []string{"apply", "--server-side", "--force-conflicts", "--field-manager=" + fieldManager, "-f", "-"}
	}

	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	cmd.Stdin = bytes.NewReader(data)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return wrapClusterDown(fmt.Errorf("kubectl apply --server-side: %w: %s", err, errMsg), errMsg)
		}

		return wrapClusterDown(fmt.Errorf("kubectl apply --server-side: %w", err), "")
	}

	return nil
}

// ApplyOutput pipes the given data into kubectl apply -f - and returns stdout.
func ApplyOutput(binary, kubeconfig string, data []byte) (string, error) {
	cmd := exec.Command(binary, "apply", "-f", "-")

	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	cmd.Stdin = bytes.NewReader(data)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", wrapClusterDown(fmt.Errorf("kubectl apply: %w: %s", err, errMsg), errMsg)
		}

		return "", wrapClusterDown(fmt.Errorf("kubectl apply: %w", err), "")
	}

	out := strings.TrimSpace(stdout.String())
	if out != "" {
		fmt.Println(out)
	}

	return out, nil
}

// PipeCommands pipes the stdout of the first kubectl command into the stdin
// of the second. Both commands run with the correct KUBECONFIG. This is useful
// for patterns like "kubectl create --dry-run -o yaml | kubectl replace -f -"
// which avoid the 262KB annotation limit that kubectl apply imposes.
func PipeCommands(binary, kubeconfig string, args1, args2 []string) error {
	env := append(os.Environ(), "KUBECONFIG="+kubeconfig)

	cmd1 := exec.Command(binary, args1...)
	cmd1.Env = env

	cmd2 := exec.Command(binary, args2...)
	cmd2.Env = env

	pipe, err := cmd1.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	cmd2.Stdin = pipe

	var stderr1, stderr2 bytes.Buffer
	cmd1.Stderr = &stderr1
	cmd2.Stderr = &stderr2

	if err := cmd1.Start(); err != nil {
		return fmt.Errorf("cmd1 start: %w", err)
	}
	if err := cmd2.Start(); err != nil {
		_ = cmd1.Process.Kill()
		return fmt.Errorf("cmd2 start: %w", err)
	}

	err1 := cmd1.Wait()
	err2 := cmd2.Wait()

	if err1 != nil {
		s := strings.TrimSpace(stderr1.String())
		return wrapClusterDown(fmt.Errorf("cmd1: %w: %s", err1, s), s)
	}
	if err2 != nil {
		s := strings.TrimSpace(stderr2.String())
		return wrapClusterDown(fmt.Errorf("cmd2: %w: %s", err2, s), s)
	}

	return nil
}
