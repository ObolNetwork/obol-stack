package embed

import (
	"embed"
)

// Embedded file systems
// Note: embed paths are relative to this file's directory (internal/embed/)
//
//go:embed k3d-config.yaml
var K3dConfig string

//go:embed helmfile.yaml
var HelmfileTemplate string

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
//   1. Copy helmfile.yaml to config directory during cluster init
//   2. Implement 'obol app install <chart>' command to scaffold application directories
//   3. Implement 'obol app list' to show available charts from repos
//   4. Implement 'obol app sync' to deploy applications via helmfile
//
// See: internal/embed/helmfile.yaml for root orchestration pattern
