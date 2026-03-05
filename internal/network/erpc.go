package network

import (
	"encoding/json"
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

// networkChainIDs maps network names to EVM chain IDs.
var networkChainIDs = map[string]int{
	"mainnet":      1,
	"hoodi":        560048,
	"sepolia":      11155111,
	"base":         8453,
	"base-sepolia": 84532,
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

	var values struct {
		Network string `yaml:"network"`
	}
	if err := yaml.Unmarshal(valuesContent, &values); err != nil {
		return fmt.Errorf("could not parse values.yaml: %w", err)
	}

	chainID, ok := networkChainIDs[values.Network]
	if !ok {
		return fmt.Errorf("unknown network %q — no chain ID mapping", values.Network)
	}

	// Build the internal RPC endpoint for this network's execution client
	namespace := fmt.Sprintf("%s-%s", networkType, id)
	endpoint := fmt.Sprintf("http://ethereum-execution.%s.svc.cluster.local:8545", namespace)
	upstreamID := fmt.Sprintf("local-%s-%s", networkType, id)

	return patchERPCUpstream(cfg, upstreamID, endpoint, chainID, values.Network, true)
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
	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		return fmt.Errorf("could not parse eRPC config: %w", err)
	}

	// Navigate to projects[0].upstreams
	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		return fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("eRPC config project[0] is not a map")
	}

	upstreams, _ := project["upstreams"].([]interface{})

	// Remove existing upstream with this ID (idempotent)
	filtered := make([]interface{}, 0, len(upstreams))
	for _, u := range upstreams {
		um, ok := u.(map[string]interface{})
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
		newUpstream := map[string]interface{}{
			"id":       upstreamID,
			"endpoint": endpoint,
			"evm": map[string]interface{}{
				"chainId": chainID,
			},
			"ignoreMethods": []interface{}{
				"eth_sendRawTransaction",
				"eth_sendTransaction",
			},
		}
		filtered = append([]interface{}{newUpstream}, filtered...)
	}

	project["upstreams"] = filtered

	// If no network entry exists for this chainID yet, add one so eRPC
	// knows how to route requests for this chain. We do NOT touch existing
	// selectionPolicy entries — they may contain method-specific routing
	// (e.g. write-only upstreams like blink). eRPC tries upstreams in
	// array order, so inserting the local node at position 0 is sufficient
	// for local-first routing with automatic remote fallback.
	if add {
		networks, _ := project["networks"].([]interface{})
		found := false
		for _, n := range networks {
			nm, ok := n.(map[string]interface{})
			if !ok {
				continue
			}
			evm, _ := nm["evm"].(map[string]interface{})
			if evm == nil {
				continue
			}
			if cid, _ := evm["chainId"].(int); cid == chainID {
				found = true
				break
			}
		}
		if !found {
			newNetwork := map[string]interface{}{
				"architecture": "evm",
				"evm":          map[string]interface{}{"chainId": chainID},
				"failsafe": map[string]interface{}{
					"timeout": map[string]interface{}{"duration": "30s"},
					"retry":   map[string]interface{}{"maxAttempts": 2, "delay": "100ms"},
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
	patchData := map[string]interface{}{
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
		"rollout", "restart", fmt.Sprintf("deployment/%s", erpcDeployment), "-n", erpcNamespace); err != nil {
		return fmt.Errorf("could not restart eRPC: %w", err)
	}

	return nil
}
