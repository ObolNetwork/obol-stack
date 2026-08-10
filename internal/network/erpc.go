package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

const (
	erpcNamespace     = "erpc"
	erpcConfigMapName = "erpc-config"
	erpcConfigKey     = "erpc.yaml"
	erpcDeployment    = "erpc"
)

var errNoERPCRegistration = errors.New("network does not expose an eRPC upstream")

// networkChainIDs maps network names to EVM chain IDs.
var networkChainIDs = map[string]int{
	"mainnet":      1,
	"hoodi":        560048,
	"sepolia":      11155111,
	"base":         8453,
	"base-sepolia": 84532,
}

type localERPCRegistration struct {
	ChainID  int
	Alias    string
	Endpoint string
}

type localERPCValues struct {
	Network string `yaml:"network"`
	Chain   string `yaml:"chain"`
}

func resolveLocalERPCRegistration(networkType, id string, values localERPCValues) (localERPCRegistration, error) {
	namespace := fmt.Sprintf("%s-%s", networkType, id)

	switch networkType {
	case "ethereum":
		chainID, ok := networkChainIDs[values.Network]
		if !ok {
			return localERPCRegistration{}, fmt.Errorf("unknown network %q — no chain ID mapping", values.Network)
		}

		return localERPCRegistration{
			ChainID:  chainID,
			Alias:    values.Network,
			Endpoint: fmt.Sprintf("http://ethereum-execution.%s.svc.cluster.local:8545", namespace),
		}, nil
	case "hl-node":
		chain := strings.TrimSpace(values.Chain)
		if chain == "" {
			chain = values.Network
		}

		switch strings.ToLower(strings.TrimSpace(chain)) {
		case "mainnet":
			return localERPCRegistration{
				ChainID:  999,
				Alias:    "hyperevm",
				Endpoint: fmt.Sprintf("http://hl-node.%s.svc.cluster.local:3001/evm", namespace),
			}, nil
		case "testnet":
			return localERPCRegistration{
				ChainID:  998,
				Alias:    "hyperevm-testnet",
				Endpoint: fmt.Sprintf("http://hl-node.%s.svc.cluster.local:3001/evm", namespace),
			}, nil
		default:
			return localERPCRegistration{}, fmt.Errorf("unknown hl-node chain %q — expected mainnet or testnet", chain)
		}
	default:
		return localERPCRegistration{}, errNoERPCRegistration
	}
}

// RegisterERPCUpstream reads the deployed network's RPC endpoint and adds
// it as an upstream in the eRPC ConfigMap. The local node becomes the
// primary upstream (group: "primary") with automatic fallback to existing
// remote upstreams.
func RegisterERPCUpstream(cfg *config.Config, networkType, id string) error {
	// Read values.yaml to get the network name (mainnet, hoodi, etc.)
	deploymentDir := filepath.Join(cfg.ConfigDir, "networks", networkType, id)
	valuesPath := filepath.Join(deploymentDir, "values.yaml")

	valuesContent, err := os.ReadFile(valuesPath)
	if err != nil {
		return fmt.Errorf("could not read values.yaml: %w", err)
	}

	var values localERPCValues
	if err := yaml.Unmarshal(valuesContent, &values); err != nil {
		return fmt.Errorf("could not parse values.yaml: %w", err)
	}

	reg, err := resolveLocalERPCRegistration(networkType, id, values)
	if err != nil {
		return err
	}
	upstreamID := fmt.Sprintf("local-%s-%s", networkType, id)

	return patchERPCUpstream(cfg, upstreamID, reg.Endpoint, reg.ChainID, reg.Alias, true)
}

// DeregisterERPCUpstream removes a previously registered local upstream
// from the eRPC ConfigMap.
func DeregisterERPCUpstream(cfg *config.Config, networkType, id string) error {
	upstreamID := fmt.Sprintf("local-%s-%s", networkType, id)
	return patchERPCUpstream(cfg, upstreamID, "", 0, "", false)
}

// patchERPCUpstream adds or removes an upstream in the eRPC ConfigMap and
// restarts the eRPC deployment. When add is true, it adds/updates the
// upstream. When false, it removes it.
func patchERPCUpstream(cfg *config.Config, upstreamID, endpoint string, chainID int, networkAlias string, add bool) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}

	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)

	// Read current eRPC config from ConfigMap
	configYAML, err := kubectl.Output(kubectlBin, kubeconfigPath,
		"get", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"-o", fmt.Sprintf("jsonpath={.data.%s}", strings.ReplaceAll(erpcConfigKey, ".", "\\.")))
	if err != nil {
		return fmt.Errorf("could not read eRPC config: %w", err)
	}

	// Parse the YAML config
	var erpcConfig map[string]any
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		return fmt.Errorf("could not parse eRPC config: %w", err)
	}

	// Navigate to projects[0].upstreams
	projects, ok := erpcConfig["projects"].([]any)
	if !ok || len(projects) == 0 {
		return errors.New("eRPC config has no projects")
	}

	project, ok := projects[0].(map[string]any)
	if !ok {
		return errors.New("eRPC config project[0] is not a map")
	}

	upstreams, _ := project["upstreams"].([]any)

	// Remove existing upstream with this ID (idempotent)
	filtered := make([]any, 0, len(upstreams))
	for _, u := range upstreams {
		um, ok := u.(map[string]any)
		if !ok {
			filtered = append(filtered, u)
			continue
		}

		if um["id"] == upstreamID {
			continue // remove it
		}

		filtered = append(filtered, u)
	}

	if add {
		// Add the new upstream at the front of the array. eRPC tries
		// upstreams in order, so position 0 = highest priority. This gives
		// local-first routing with automatic fallback to remote RPCs.
		//
		// Write methods are blocked on local nodes so transactions are
		// always routed through the designated write upstream (e.g.
		// obol-rpc-mainnet) rather than leaking to the public mempool.
		newUpstream := map[string]any{
			"id":       upstreamID,
			"endpoint": endpoint,
			"evm": map[string]any{
				"chainId": chainID,
			},
			"ignoreMethods": []any{
				"eth_sendRawTransaction",
				"eth_sendTransaction",
			},
		}
		filtered = append([]any{newUpstream}, filtered...)
	}

	project["upstreams"] = filtered

	// If no network entry exists for this chainID yet, add one so eRPC
	// knows how to route requests for this chain. We do NOT touch existing
	// selectionPolicy entries — they may contain method-specific routing
	// (e.g. write-only upstreams like blink). eRPC tries upstreams in
	// array order, so inserting the local node at position 0 is sufficient
	// for local-first routing with automatic remote fallback.
	if add {
		networks, _ := project["networks"].([]any)
		found := false

		for _, n := range networks {
			nm, ok := n.(map[string]any)
			if !ok {
				continue
			}

			evm, _ := nm["evm"].(map[string]any)
			if evm == nil {
				continue
			}

			if cid, _ := evm["chainId"].(int); cid == chainID {
				found = true
				break
			}
		}

		if !found {
			newNetwork := map[string]any{
				"architecture": "evm",
				"evm":          map[string]any{"chainId": chainID},
				"failsafe": map[string]any{
					"timeout": map[string]any{"duration": "30s"},
					"retry":   map[string]any{"maxAttempts": 2, "delay": "100ms"},
				},
			}
			if networkAlias != "" {
				newNetwork["alias"] = networkAlias
			}

			networks = append(networks, newNetwork)
			project["networks"] = networks
		}
	}

	// Serialize back to YAML
	updatedYAML, err := yaml.Marshal(erpcConfig)
	if err != nil {
		return fmt.Errorf("could not serialize eRPC config: %w", err)
	}

	// Patch the ConfigMap
	patchData := map[string]any{
		"data": map[string]string{
			erpcConfigKey: string(updatedYAML),
		},
	}

	patchJSON, err := json.Marshal(patchData)
	if err != nil {
		return fmt.Errorf("could not marshal patch: %w", err)
	}

	if err := kubectl.RunSilent(kubectlBin, kubeconfigPath,
		"patch", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"-p", string(patchJSON), "--type=merge"); err != nil {
		return fmt.Errorf("could not patch eRPC ConfigMap: %w", err)
	}

	// Restart eRPC to pick up new config
	if err := kubectl.RunSilent(kubectlBin, kubeconfigPath,
		"rollout", "restart", "deployment/"+erpcDeployment, "-n", erpcNamespace); err != nil {
		return fmt.Errorf("could not restart eRPC: %w", err)
	}

	return nil
}
