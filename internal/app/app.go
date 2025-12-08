package app

import (
	"fmt"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// TODO: Application Installation System
//
// The applications system is being refactored to use a helmfile-based composition pattern.
//
// Current architecture:
//   - Root helmfile.yaml: $OBOL_CONFIG_DIR/helmfile.yaml
//   - Per-app helmfiles: $OBOL_CONFIG_DIR/applications/{repo}/{chart}/helmfile.yaml
//   - Per-app values: $OBOL_CONFIG_DIR/applications/{repo}/{chart}/values.yaml
//
// Implementation needed:
//   1. Install(cfg, chart, repo, valuesOverride) - Scaffold application directory
//   2. Edit(cfg, appPath) - Open helmfile or values.yaml in editor
//   3. Sync(cfg, appPath) - Deploy via helmfile
//   4. Delete(cfg, appPath) - Remove app and clean up cluster resources
//
// See: internal/embed/helmfile.yaml for root orchestration pattern

// Install scaffolds a new application directory
func Install(cfg *config.Config, chart string, repo string, valuesOverride string) error {
	fmt.Printf("Installing application: %s/%s\n", repo, chart)
	fmt.Println("TODO: Implement application scaffolding")
	fmt.Println("  1. Validate chart exists in repo")
	fmt.Println("  2. Create: $OBOL_CONFIG_DIR/applications/{repo}/{chart}/")
	fmt.Println("  3. Generate helmfile.yaml referencing chart")
	fmt.Println("  4. Generate values.yaml with sane defaults")

	return nil
}

// Edit opens an application file in the user's editor
func Edit(cfg *config.Config, appPath string) error {
	fmt.Printf("Editing application: %s\n", appPath)
	fmt.Println("TODO: Implement editor integration")

	return nil
}

// Sync deploys the application using helmfile
func Sync(cfg *config.Config, appPath string) error {
	fmt.Printf("Syncing application: %s\n", appPath)
	fmt.Println("TODO: Implement helmfile sync")
	fmt.Println("  1. Run: helmfile -f $OBOL_CONFIG_DIR/helmfile.yaml sync")
	fmt.Println("  2. Or: helmfile -f {appPath}/helmfile.yaml sync")

	return nil
}

// Delete removes the application and cluster resources
func Delete(cfg *config.Config, appPath string, force bool) error {
	fmt.Printf("Deleting application: %s\n", appPath)
	fmt.Println("TODO: Implement application deletion")
	fmt.Println("  1. Remove $OBOL_CONFIG_DIR/applications/{repo}/{chart}/")
	fmt.Println("  2. Run: kubectl delete namespace {chart}")

	return nil
}
