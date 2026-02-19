package openclaw

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// Skills management for OpenClaw instances.
//
// OpenClaw has built-in skill support (list, info, check) but the obol CLI
// only exposed "skills sync". This file fills that gap by:
//
//   - Surfacing the in-pod openclaw CLI commands (list, info, check) via
//     kubectl exec passthrough — zero reimplementation.
//   - Adding host-side skill management (add, remove) that uses clawhub
//     to install skills from the registry, then syncs to the pod.
//
// Skill hierarchy (OpenClaw loading precedence, later overrides earlier):
//
//   1. openclaw-bundled   — built into the container image
//   2. openclaw-managed   — ~/.config/obol/skills/ (inside container)
//   3. agents-skills      — .agents/skills/ (project-local)
//   4. openclaw-workspace — skills/ (workspace, from ConfigMap mount)
//
// The managed skills directory (<deployment>/skills/) on the host is
// synced to the pod's ConfigMap. Skills installed via "add" or pushed
// via "sync --from" end up in the workspace tier, which has highest
// precedence and cleanly overrides bundled defaults.

// SkillsCLI delegates a skills subcommand to the in-pod openclaw binary
// via kubectl exec. OpenClaw's built-in "skills list|info|check" commands
// are exposed without reimplementing any logic.
func SkillsCLI(cfg *config.Config, id string, args []string) error {
	return CLI(cfg, id, append([]string{"skills"}, args...))
}

// SkillsDir returns the managed skills directory for an instance.
// Skills installed via SkillsAdd are stored here and synced to the pod.
func SkillsDir(cfg *config.Config, id string) string {
	return filepath.Join(deploymentPath(cfg, id), "skills")
}

// findClawHub locates the clawhub CLI. Returns the binary path and any
// prefix args (e.g., ["clawhub"] when using npx as the runner).
func findClawHub() (binary string, prefixArgs []string, err error) {
	if p, lookErr := exec.LookPath("clawhub"); lookErr == nil {
		return p, nil, nil
	}
	if p, lookErr := exec.LookPath("npx"); lookErr == nil {
		return p, []string{"clawhub"}, nil
	}
	return "", nil, fmt.Errorf("clawhub not found.\n\nInstall with:\n  npm install -g clawhub\n\nOr ensure npx is available (Node.js 18+)")
}

// SkillsAdd installs a skill from the clawhub registry into the
// instance's managed skills directory, then syncs to the pod.
//
// Usage:
//
//	obol openclaw skills add <id> <slug>
//	obol openclaw skills add default austintgriffith/ethereum-wingman
func SkillsAdd(cfg *config.Config, id, slug string) error {
	dir := deploymentPath(cfg, id)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw onboard' first", appName, id)
	}

	skillsDir := SkillsDir(cfg, id)
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return fmt.Errorf("failed to create skills directory: %w", err)
	}

	binary, prefixArgs, err := findClawHub()
	if err != nil {
		return err
	}

	fmt.Printf("Installing skill %s...\n", slug)
	args := append(prefixArgs, "install", slug, "--dir", skillsDir)
	cmd := exec.Command(binary, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clawhub install failed: %w", err)
	}

	fmt.Printf("\nSyncing skills to instance %s...\n", id)
	return SkillsSync(cfg, id, skillsDir)
}

// SkillsRemove removes a skill from the managed directory and re-syncs
// remaining skills to the pod.
func SkillsRemove(cfg *config.Config, id, name string) error {
	dir := deploymentPath(cfg, id)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("deployment not found: %s/%s\nRun 'obol openclaw onboard' first", appName, id)
	}

	skillsDir := SkillsDir(cfg, id)
	skillPath := filepath.Join(skillsDir, name)

	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		return fmt.Errorf("skill %q not found in %s", name, skillsDir)
	}

	if err := os.RemoveAll(skillPath); err != nil {
		return fmt.Errorf("failed to remove skill: %w", err)
	}
	fmt.Printf("Removed skill %s\n", name)

	// Re-sync remaining skills to pod, or reset if none left
	entries, _ := os.ReadDir(skillsDir)
	if len(entries) == 0 {
		fmt.Println("No managed skills remaining; resetting ConfigMap...")
		return skillsReset(cfg, id)
	}

	fmt.Printf("Syncing skills to instance %s...\n", id)
	return SkillsSync(cfg, id, skillsDir)
}

// skillsReset creates a ConfigMap with an empty tar archive and restarts
// the deployment. The init container sees a valid skills.tgz, clears the
// old extracted skills directory, and extracts nothing — effectively
// removing all managed skills from the pod.
func skillsReset(cfg *config.Config, id string) error {
	namespace := fmt.Sprintf("%s-%s", appName, id)
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	configMapName := "openclaw-skills"

	// Create an empty tar.gz archive so the init container's "rm -rf + extract"
	// path runs and cleans the directory (an empty ConfigMap would just skip).
	emptyTgz, err := os.CreateTemp("", "openclaw-empty-skills-*.tgz")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(emptyTgz.Name())

	emptyDir, err := os.MkdirTemp("", "openclaw-empty-skills-dir-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(emptyDir)

	tarCmd := exec.Command("tar", "-czf", emptyTgz.Name(), "-C", emptyDir, ".")
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("failed to create empty archive: %w", err)
	}
	emptyTgz.Close()

	kubeEnv := append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))

	delCmd := exec.Command(kubectlBinary, "delete", "configmap", configMapName,
		"-n", namespace, "--ignore-not-found")
	delCmd.Env = kubeEnv
	delCmd.Run()

	createCmd := exec.Command(kubectlBinary, "create", "configmap", configMapName,
		"-n", namespace,
		fmt.Sprintf("--from-file=skills.tgz=%s", emptyTgz.Name()))
	createCmd.Env = kubeEnv
	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to reset ConfigMap: %w", err)
	}

	restartCmd := exec.Command(kubectlBinary, "rollout", "restart",
		"deployment/openclaw", "-n", namespace)
	restartCmd.Env = kubeEnv
	if err := restartCmd.Run(); err != nil {
		return fmt.Errorf("failed to restart deployment: %w", err)
	}

	waitCmd := exec.Command(kubectlBinary, "rollout", "status",
		"deployment/openclaw", "-n", namespace, "--timeout=60s")
	waitCmd.Env = kubeEnv
	waitCmd.Stdout = os.Stdout
	if err := waitCmd.Run(); err != nil {
		return fmt.Errorf("rollout did not complete: %w", err)
	}

	fmt.Printf("✓ Managed skills cleared from instance %s\n", id)
	return nil
}
