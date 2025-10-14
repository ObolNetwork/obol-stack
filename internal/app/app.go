package app

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/obol/obol-stack/internal/config"
	"github.com/obol/obol-stack/internal/executor"
	"github.com/obol/obol-stack/internal/logging"
)

const (
	defaultAppsDir = "default"
)

// List returns all available installable applications (excludes default/)
func List(cfg *config.Config, logger *logging.Logger, embedFS embed.FS) error {
	if logger != nil {
		logger.LogCommand("app list", []string{})
		defer func() {
			logger.LogCommandComplete("app list", 0, nil)
		}()
	}

	apps := []string{}

	// Walk the embedded applications directory
	err := fs.WalkDir(embedFS, "applications", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root applications directory
		if path == "applications" {
			return nil
		}

		// Only process directories that are direct children of applications/
		if d.IsDir() {
			relPath := strings.TrimPrefix(path, "applications/")
			// Skip if it has a slash (it's a subdirectory)
			if !strings.Contains(relPath, "/") && relPath != defaultAppsDir {
				apps = append(apps, relPath)
			}
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to list applications: %w", err)
	}

	if len(apps) == 0 {
		fmt.Println("No installable applications available")
		return nil
	}

	fmt.Println("Available applications:")
	for _, app := range apps {
		fmt.Printf("  - %s\n", app)
	}

	return nil
}

// Install copies an embedded application to the user's config directory
func Install(cfg *config.Config, logger *logging.Logger, embedFS embed.FS, appName string, force bool) error {
	if logger != nil {
		logger.LogCommand("app install", []string{appName, fmt.Sprintf("force=%v", force)})
		defer func() {
			logger.LogCommandComplete("app install", 0, nil)
		}()
	}

	// Check if app is "default" - not allowed
	if appName == defaultAppsDir {
		return fmt.Errorf("cannot install 'default' - default applications are automatically deployed")
	}

	// Check if application exists in embedded FS
	appPath := filepath.Join("applications", appName)
	appDir, err := fs.ReadDir(embedFS, appPath)
	if err != nil {
		return fmt.Errorf("application '%s' not found", appName)
	}
	if len(appDir) == 0 {
		return fmt.Errorf("application '%s' is empty", appName)
	}

	// Destination directory in user config
	destDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	// Check if already installed (force will allow overwrite)
	if _, err := os.Stat(destDir); err == nil {
		if !force {
			return fmt.Errorf("application '%s' already installed at %s\nUse --force to overwrite", appName, destDir)
		}
		// Force is true, so remove existing directory first
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("failed to remove existing installation: %w", err)
		}
	}

	// Create destination directory
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", destDir, err)
	}

	// Copy all files from embedded app to destination
	copied := 0
	err = fs.WalkDir(embedFS, appPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip root application directory
		if path == appPath {
			return nil
		}

		// Get relative path within the app
		relPath := strings.TrimPrefix(path, appPath+"/")
		destPath := filepath.Join(destDir, relPath)

		if d.IsDir() {
			return os.MkdirAll(destPath, 0755)
		}

		// Read embedded file
		data, err := embedFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Write to destination
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}

		copied++
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to copy application: %w", err)
	}

	fmt.Printf("✓ Installed application '%s' to %s\n", appName, destDir)
	fmt.Printf("  Copied %d files\n", copied)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Review configuration: obol app edit %s\n", appName)
	fmt.Printf("  2. Deploy to cluster: obol app sync %s\n", appName)

	return nil
}

// Edit opens the application's values.yaml in the user's editor
func Edit(cfg *config.Config, logger *logging.Logger, appName string) error {
	if logger != nil {
		logger.LogCommand("app edit", []string{appName})
		defer func() {
			logger.LogCommandComplete("app edit", 0, nil)
		}()
	}

	valuesPath := filepath.Join(cfg.ConfigDir, "applications", appName, "values.yaml")

	// Check if application is installed
	if _, err := os.Stat(valuesPath); os.IsNotExist(err) {
		return fmt.Errorf("application '%s' not installed\nRun: obol app install %s", appName, appName)
	}

	// Determine editor (respect EDITOR env var, fallback to vim)
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vim"
	}

	fmt.Printf("Opening %s in %s...\n", valuesPath, editor)

	cmd := exec.Command(editor, valuesPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open editor: %w", err)
	}

	return nil
}

// Sync applies the application to the cluster using helmfile with applyset tracking
func Sync(cfg *config.Config, logger *logging.Logger, appName string) error {
	var cmdErr error

	if logger != nil {
		logger.LogCommand("app sync", []string{appName})
		defer func() {
			exitCode := 0
			if cmdErr != nil {
				exitCode = 1
			}
			logger.LogCommandComplete("app sync", exitCode, cmdErr)
		}()
	}

	appDir := filepath.Join(cfg.ConfigDir, "applications", appName)
	helmfilePath := filepath.Join(appDir, "helmfile.yaml")

	// Check if application is installed
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("application '%s' not installed\nRun: obol app install %s", appName, appName)
		return cmdErr
	}

	// Get kubeconfig path
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("cluster not running, use 'obol cluster up' first")
		return cmdErr
	}

	// Create executor
	exec := executor.New(logger)

	helmfileBin := filepath.Join(cfg.BinDir, "helmfile")

	// Check if helmfile exists
	if _, err := os.Stat(helmfileBin); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("helmfile not found at %s\nPlease install helmfile", helmfileBin)
		return cmdErr
	}

	fmt.Printf("Syncing application '%s'...\n", appName)

	// Check if kubectl exists
	kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
	if _, err := os.Stat(kubectlBin); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("kubectl not found at %s\nPlease install kubectl", kubectlBin)
		return cmdErr
	}

	// Create temporary directory for rendered manifests
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("obol-app-%s-*", appName))
	if err != nil {
		cmdErr = fmt.Errorf("failed to create temp directory: %w", err)
		return cmdErr
	}
	defer os.RemoveAll(tmpDir)

	fmt.Printf("Rendering manifests...\n")

	// Step 1: Use helmfile template to generate all YAML manifests
	templateCmd := exec.CommandWithLogging(
		helmfileBin,
		"-f", helmfilePath,
		"template",
		"--output-dir", tmpDir,
	)
	templateCmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	)
	templateCmd.Dir = appDir

	if err := templateCmd.Run(); err != nil {
		cmdErr = fmt.Errorf("failed to render manifests: %w", err)
		return cmdErr
	}

	fmt.Printf("Applying manifests with applyset tracking...\n")

	// Find all YAML files in the rendered directory
	var yamlFiles []string
	err = filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml")) {
			yamlFiles = append(yamlFiles, path)
		}
		return nil
	})
	if err != nil {
		cmdErr = fmt.Errorf("failed to find YAML files: %w", err)
		return cmdErr
	}

	if len(yamlFiles) == 0 {
		cmdErr = fmt.Errorf("no YAML files found in rendered manifests")
		return cmdErr
	}

	fmt.Printf("Found %d manifest files\n", len(yamlFiles))

	// Step 2: Ensure namespace exists before applying
	fmt.Printf("Ensuring namespace '%s' exists...\n", appName)
	getNsCmd := exec.Command(
		kubectlBin,
		"get", "namespace", appName,
	)
	getNsCmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	)

	// Check if namespace exists
	if err := getNsCmd.Run(); err != nil {
		// Namespace doesn't exist, create it
		createNsCmd := exec.CommandWithLogging(
			kubectlBin,
			"create", "namespace", appName,
		)
		createNsCmd.Env = append(os.Environ(),
			fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
		)

		if err := createNsCmd.Run(); err != nil {
			cmdErr = fmt.Errorf("failed to create namespace: %w", err)
			return cmdErr
		}
	}

	// Step 3: Use kubectl apply with --prune and applyset for lifecycle management
	// ApplySet-based pruning automatically tracks and removes resources no longer in manifests
	// Requires KUBECTL_APPLYSET=true environment variable (alpha feature in k8s 1.27+)
	args := []string{
		"apply",
		"--prune",
		"--applyset", appName,
		"--namespace", appName,
	}
	// Add all YAML files
	for _, yamlFile := range yamlFiles {
		args = append(args, "-f", yamlFile)
	}

	applyCmd := exec.CommandWithLogging(
		kubectlBin,
		args...,
	)
	applyCmd.Env = append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
		"KUBECTL_APPLYSET=true", // Enable applyset feature
	)

	if err := applyCmd.Run(); err != nil {
		cmdErr = fmt.Errorf("failed to apply manifests: %w", err)
		return cmdErr
	}

	fmt.Printf("✓ Application '%s' synced successfully\n", appName)
	fmt.Printf("\nMonitor status:\n")
	fmt.Printf("  kubectl get pods -n %s\n", appName)

	return nil
}

// Delete removes the application from user config and prunes all cluster resources
func Delete(cfg *config.Config, logger *logging.Logger, appName string, force bool) error {
	var cmdErr error

	if logger != nil {
		logger.LogCommand("app delete", []string{appName, fmt.Sprintf("force=%v", force)})
		defer func() {
			exitCode := 0
			if cmdErr != nil {
				exitCode = 1
			}
			logger.LogCommandComplete("app delete", exitCode, cmdErr)
		}()
	}

	appDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	// Check if application is installed
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		cmdErr = fmt.Errorf("application '%s' not installed", appName)
		return cmdErr
	}

	// Confirm deletion unless force is used
	if !force {
		fmt.Printf("WARNING: This will delete the application configuration and remove all resources from the cluster.\n")
		fmt.Printf("Application: %s\n", appName)
		fmt.Printf("Location: %s\n", appDir)
		fmt.Printf("\nContinue? [y/N]: ")

		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			fmt.Println("Deletion cancelled")
			return nil
		}
	}

	// Get kubeconfig path
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "cluster", "kubeconfig", "kubeconfig.yaml")

	// Only attempt cluster cleanup if cluster is running
	if _, err := os.Stat(kubeconfigPath); err == nil {
		// Create executor
		exec := executor.New(logger)

		helmfileBin := filepath.Join(cfg.BinDir, "helmfile")

		// Check if helmfile exists
		if _, err := os.Stat(helmfileBin); err == nil {
			helmfilePath := filepath.Join(appDir, "helmfile.yaml")

			if _, err := os.Stat(helmfilePath); err == nil {
				fmt.Printf("Removing application resources from cluster...\n")

				// Run helmfile destroy to remove all resources
				destroyCmd := exec.CommandWithLogging(
					helmfileBin,
					"-f", helmfilePath,
					"destroy",
				)

				destroyCmd.Env = append(os.Environ(),
					fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
				)
				destroyCmd.Dir = appDir

				if err := destroyCmd.Run(); err != nil {
					fmt.Printf("Warning: failed to remove cluster resources: %v\n", err)
					fmt.Printf("You may need to manually clean up resources in namespace '%s'\n", appName)
				} else {
					fmt.Printf("✓ Cluster resources removed\n")
				}
			}
		}

		// Additionally, delete the namespace with applyset label
		kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
		if _, err := os.Stat(kubectlBin); err == nil {
			deleteNsCmd := exec.Command(
				kubectlBin,
				"delete", "namespace", appName,
				"--ignore-not-found",
			)
			deleteNsCmd.Env = append(os.Environ(),
				fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
			)

			if err := deleteNsCmd.Run(); err != nil {
				fmt.Printf("Warning: failed to delete namespace: %v\n", err)
			}
		}
	} else {
		fmt.Printf("Cluster not running, skipping cluster resource cleanup\n")
	}

	// Remove application directory
	if err := os.RemoveAll(appDir); err != nil {
		cmdErr = fmt.Errorf("failed to remove application directory: %w", err)
		return cmdErr
	}

	fmt.Printf("✓ Application '%s' deleted\n", appName)

	return nil
}
