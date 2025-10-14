package app

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/executor"
	"github.com/ObolNetwork/obol-stack/internal/logging"
	"github.com/ObolNetwork/obol-stack/internal/stack"
)

const (
	defaultAppsDir = "default"
)

// List returns all available installable applications (excludes default/)
func List(cfg *config.Config, embedFS embed.FS) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info("Listing available applications")

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
		l.Info("No installable applications available")
		return nil
	}

	l.Info("Available applications:")
	for _, app := range apps {
		l.Info(fmt.Sprintf("  - %s", app))
	}

	return nil
}

// Install copies an embedded application to the user's config directory
func Install(cfg *config.Config, embedFS embed.FS, appName string, force bool) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Installing application: %s", appName))

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

	l.Success(fmt.Sprintf("Installed application '%s' to %s", appName, destDir))
	l.Info(fmt.Sprintf("  Copied %d files", copied))
	l.Info("Next steps:")
	l.Info(fmt.Sprintf("  1. Review configuration: obol app edit %s", appName))
	l.Info(fmt.Sprintf("  2. Deploy to stack: obol app sync %s", appName))

	return nil
}

// Edit opens the application's values.yaml in the user's editor
func Edit(cfg *config.Config, appName string) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Editing application: %s", appName))

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

	l.Info(fmt.Sprintf("Opening %s in %s", valuesPath, editor))

	// Create executor for editor command
	exec := executor.New(l.Logger)
	defer exec.Close()

	// Create editor command with stdin/stdout/stderr connected
	editorCmd := exec.CommandWithOutput(editor, valuesPath)
	editorCmd.SetStdin(os.Stdin)

	if err := editorCmd.Run(); err != nil {
		return fmt.Errorf("failed to open editor: %w", err)
	}

	return nil
}

// Sync applies the application to the cluster using helmfile with applyset tracking
func Sync(cfg *config.Config, appName string) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Syncing application: %s", appName))

	appDir := filepath.Join(cfg.ConfigDir, "applications", appName)
	helmfilePath := filepath.Join(appDir, "helmfile.yaml")

	// Check if application is installed
	if _, err := os.Stat(helmfilePath); os.IsNotExist(err) {
		return fmt.Errorf("application '%s' not installed\nRun: obol app install %s", appName, appName)
	}

	// Get kubeconfig path
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("stack not running, use 'obol stack up' first")
	}

	// Create executor
	exec := executor.New(l.Logger)
	defer exec.Close()

	helmfileBin := filepath.Join(cfg.BinDir, "helmfile")

	// Check if helmfile exists
	if _, err := os.Stat(helmfileBin); os.IsNotExist(err) {
		return fmt.Errorf("helmfile not found at %s\nPlease install helmfile", helmfileBin)
	}

	l.Info(fmt.Sprintf("Syncing application '%s' to cluster", appName))

	// Run helmfile sync with applyset support
	syncCmd := exec.CommandWithOutput(
		helmfileBin,
		"-f", helmfilePath,
		"sync",
		"--skip-deps", // Skip dependency updates for faster sync
	)

	// Set environment variables
	syncCmd.SetEnv(append(os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
	))

	if err := syncCmd.Run(); err != nil {
		return fmt.Errorf("failed to sync application: %w", err)
	}

	l.Success(fmt.Sprintf("Application '%s' synced successfully", appName))
	l.Info("Monitor status:")
	l.Info(fmt.Sprintf("  kubectl get pods -n %s", appName))

	return nil
}

// Delete removes the application from user config and prunes all cluster resources
func Delete(cfg *config.Config, appName string, force bool) error {
	// Get stack ID for logging
	stackID := stack.GetStackID(cfg)

	// Create logger
	l, cleanup := logging.NewSlogLogger(logging.LoggerConfig{
		StateDir: cfg.StateDir,
		StackID:  stackID,
	})
	defer cleanup()

	l.Info(fmt.Sprintf("Deleting application: %s", appName))

	appDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	// Check if application is installed
	if _, err := os.Stat(appDir); os.IsNotExist(err) {
		return fmt.Errorf("application '%s' not installed", appName)
	}

	// Confirm deletion unless force is used
	if !force {
		l.Warn("This will delete the application configuration and remove all resources from the cluster")
		l.Info(fmt.Sprintf("Application: %s", appName))
		l.Info(fmt.Sprintf("Location: %s", appDir))
		fmt.Printf("\nContinue? [y/N]: ")

		var response string
		fmt.Scanln(&response)
		if strings.ToLower(response) != "y" && strings.ToLower(response) != "yes" {
			l.Info("Deletion cancelled")
			return nil
		}
	}

	// Get kubeconfig path
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	// Only attempt cluster cleanup if cluster is running
	if _, err := os.Stat(kubeconfigPath); err == nil {
		// Create executor
		exec := executor.New(l.Logger)
		defer exec.Close()

		helmfileBin := filepath.Join(cfg.BinDir, "helmfile")

		// Check if helmfile exists
		if _, err := os.Stat(helmfileBin); err == nil {
			helmfilePath := filepath.Join(appDir, "helmfile.yaml")

			if _, err := os.Stat(helmfilePath); err == nil {
				l.Info("Removing application resources from cluster")

				// Run helmfile destroy to remove all resources
				destroyCmd := exec.CommandWithOutput(
					helmfileBin,
					"-f", helmfilePath,
					"destroy",
				)

				destroyCmd.SetEnv(append(os.Environ(),
					fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
				))

				if err := destroyCmd.Run(); err != nil {
					l.Warn(fmt.Sprintf("Failed to remove cluster resources: %v", err))
					l.Warn(fmt.Sprintf("You may need to manually clean up resources in namespace '%s'", appName))
				} else {
					l.Success("Cluster resources removed")
				}
			}
		}

		// Additionally, delete the namespace with applyset label
		kubectlBin := filepath.Join(cfg.BinDir, "kubectl")
		if _, err := os.Stat(kubectlBin); err == nil {
			deleteNsCmd := exec.CommandWithOutput(
				kubectlBin,
				"delete", "namespace", appName,
				"--ignore-not-found",
			)
			deleteNsCmd.SetEnv(append(os.Environ(),
				fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath),
			))

			if err := deleteNsCmd.Run(); err != nil {
				l.Warn(fmt.Sprintf("Failed to delete namespace: %v", err))
			}
		}
	} else {
		l.Info("Cluster not running, skipping cluster resource cleanup")
	}

	// Remove application directory
	if err := os.RemoveAll(appDir); err != nil {
		return fmt.Errorf("failed to remove application directory: %w", err)
	}

	l.Success(fmt.Sprintf("Application '%s' deleted", appName))

	return nil
}
