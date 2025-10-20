package embed

import (
	_ "embed"
)

// K3dConfig contains the default k3d cluster configuration template
//
//go:embed k3d-config.yaml
var K3dConfig string
