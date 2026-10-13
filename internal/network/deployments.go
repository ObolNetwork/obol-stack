package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"gopkg.in/yaml.v3"
)

// Deployment is one installed local node, read from its values.yaml under
// $CONFIG_DIR/networks/<type>/<id>/. Fields that a network type does not
// have (e.g. clients for aztec) stay empty.
type Deployment struct {
	Type            string `json:"type"`
	ID              string `json:"id"`
	Namespace       string `json:"namespace"`
	Network         string `json:"network,omitempty"`
	ExecutionClient string `json:"execution_client,omitempty"`
	ConsensusClient string `json:"consensus_client,omitempty"`
	Mode            string `json:"mode,omitempty"`
	// Error is set when values.yaml could not be read; the deployment
	// directory still exists, so it is listed rather than hidden.
	Error string `json:"error,omitempty"`
}

// Identifier is the "<type>/<id>" form every network subcommand accepts.
func (d Deployment) Identifier() string { return d.Type + "/" + d.ID }

// InstalledDeployments returns every installed network deployment (see
// ListDeployments) with the settings from its values.yaml. It only reads the
// config dir, so it works with the cluster down. Never returns nil.
func InstalledDeployments(cfg *config.Config) []Deployment {
	ids := ListDeployments(cfg)
	out := make([]Deployment, 0, len(ids))
	for _, ident := range ids {
		typ, id, _ := strings.Cut(ident, "/")
		out = append(out, readDeployment(cfg, typ, id))
	}

	return out
}

func readDeployment(cfg *config.Config, typ, id string) Deployment {
	d := Deployment{Type: typ, ID: id, Namespace: fmt.Sprintf("%s-%s", typ, id)}

	raw, err := os.ReadFile(filepath.Join(cfg.ConfigDir, "networks", typ, id, "values.yaml"))
	if err != nil {
		d.Error = "values.yaml unreadable: " + err.Error()
		return d
	}

	var values struct {
		Network         string `yaml:"network"`
		ExecutionClient string `yaml:"executionClient"`
		ConsensusClient string `yaml:"consensusClient"`
		Mode            string `yaml:"mode"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		d.Error = "values.yaml invalid: " + err.Error()
		return d
	}

	d.Network = values.Network
	d.ExecutionClient = values.ExecutionClient
	d.ConsensusClient = values.ConsensusClient
	d.Mode = values.Mode
	if d.Mode == "" && typ == "ethereum" {
		d.Mode = "full"
	}

	return d
}
