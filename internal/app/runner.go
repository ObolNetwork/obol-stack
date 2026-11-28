package app

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

// toolRunner runs an external tool (helm, kubectl) and returns its stdout.
// stdin, when non-nil, is fed to the process; it is how Secret manifests
// reach kubectl, so values never appear in argv. Errors carry stderr, never
// stdin.
type toolRunner func(name string, args, env []string, stdin []byte) ([]byte, error)

// runTool is the single seam every helm/kubectl call in this package goes
// through. Tests replace it with a fake so nothing touches a real cluster.
var runTool toolRunner = execTool

func execTool(name string, args, env []string, stdin []byte) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)

	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return stdout.Bytes(), err
		}

		return stdout.Bytes(), fmt.Errorf("%w: %s", err, msg)
	}

	return stdout.Bytes(), nil
}

// stackKubeconfig is the kubeconfig `obol stack up` writes.
func stackKubeconfig(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
}

// clusterConfigured reports whether a stack kubeconfig exists. Without it
// there is no cluster to query and cluster checks are skipped.
func clusterConfigured(cfg *config.Config) bool {
	_, err := os.Stat(stackKubeconfig(cfg))
	return err == nil
}

// errNoCluster is returned by cluster helpers when no stack kubeconfig exists.
var errNoCluster = errors.New("cluster not running (no stack kubeconfig); run 'obol stack up' first")

// kubectl runs kubectl against the stack kubeconfig (an ambient KUBECONFIG
// is always overridden).
func kubectl(cfg *config.Config, stdin []byte, args ...string) ([]byte, error) {
	if !clusterConfigured(cfg) {
		return nil, errNoCluster
	}

	return runTool(cfg.ToolPath("kubectl"), args, []string{"KUBECONFIG=" + stackKubeconfig(cfg)}, stdin)
}

// helmCluster runs helm against the stack kubeconfig.
func helmCluster(cfg *config.Config, args ...string) ([]byte, error) {
	if !clusterConfigured(cfg) {
		return nil, errNoCluster
	}

	return runTool(cfg.ToolPath("helm"), args, []string{"KUBECONFIG=" + stackKubeconfig(cfg)}, nil)
}

// helmLocal runs a helm command that needs no cluster (repo list, show,
// pull).
func helmLocal(cfg *config.Config, args ...string) ([]byte, error) {
	return runTool(cfg.ToolPath("helm"), args, nil, nil)
}
