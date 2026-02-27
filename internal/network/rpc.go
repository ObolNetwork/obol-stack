package network

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"gopkg.in/yaml.v3"
)

// RPCUpstreamInfo represents an upstream in the eRPC config for display.
type RPCUpstreamInfo struct {
	ID       string
	Endpoint string
	ChainID  int
}

// RPCNetworkInfo represents a network (chain) configured in eRPC.
type RPCNetworkInfo struct {
	ChainID   int
	Alias     string
	Upstreams []RPCUpstreamInfo
}

// ListRPCNetworks reads the eRPC ConfigMap and returns configured networks with their upstreams.
func ListRPCNetworks(cfg *config.Config) ([]RPCNetworkInfo, error) {
	erpcConfig, err := readERPCConfig(cfg)
	if err != nil {
		return nil, err
	}

	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		return nil, fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("eRPC config project[0] is not a map")
	}

	// Build upstream lookup by chain ID.
	upstreams, _ := project["upstreams"].([]interface{})
	upstreamsByChain := make(map[int][]RPCUpstreamInfo)
	for _, u := range upstreams {
		um, ok := u.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := um["id"].(string)
		endpoint, _ := um["endpoint"].(string)
		var chainID int
		if evm, ok := um["evm"].(map[string]interface{}); ok {
			chainID = yamlInt(evm["chainId"])
		}
		if chainID > 0 {
			upstreamsByChain[chainID] = append(upstreamsByChain[chainID], RPCUpstreamInfo{
				ID:       id,
				Endpoint: endpoint,
				ChainID:  chainID,
			})
		}
	}

	// Build network list.
	networks, _ := project["networks"].([]interface{})
	var result []RPCNetworkInfo
	for _, n := range networks {
		nm, ok := n.(map[string]interface{})
		if !ok {
			continue
		}
		var chainID int
		if evm, ok := nm["evm"].(map[string]interface{}); ok {
			chainID = yamlInt(evm["chainId"])
		}
		alias, _ := nm["alias"].(string)
		if chainID > 0 {
			result = append(result, RPCNetworkInfo{
				ChainID:   chainID,
				Alias:     alias,
				Upstreams: upstreamsByChain[chainID],
			})
		}
	}

	return result, nil
}

// AddCustomRPC adds a single custom RPC endpoint for a chain to the eRPC ConfigMap.
// Uses the "custom-" prefix to distinguish from ChainList-sourced upstreams.
func AddCustomRPC(cfg *config.Config, chainID int, chainName, endpoint string) error {
	erpcConfig, err := readERPCConfig(cfg)
	if err != nil {
		return err
	}

	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		return fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("eRPC config project[0] is not a map")
	}

	// Remove any existing custom upstream for this chain ID.
	existingUpstreams, _ := project["upstreams"].([]interface{})
	filtered := make([]interface{}, 0, len(existingUpstreams))
	for _, u := range existingUpstreams {
		um, ok := u.(map[string]interface{})
		if !ok {
			filtered = append(filtered, u)
			continue
		}
		id, _ := um["id"].(string)
		if strings.HasPrefix(id, fmt.Sprintf("custom-%d-", chainID)) {
			continue
		}
		filtered = append(filtered, u)
	}

	// Add the custom upstream.
	filtered = append(filtered, map[string]interface{}{
		"id":       fmt.Sprintf("custom-%d-0", chainID),
		"endpoint": endpoint,
		"evm": map[string]interface{}{
			"chainId": chainID,
		},
	})
	project["upstreams"] = filtered

	// Ensure a network entry exists for this chain ID.
	networksList, _ := project["networks"].([]interface{})
	found := false
	for _, n := range networksList {
		nm, ok := n.(map[string]interface{})
		if !ok {
			continue
		}
		if evm, ok := nm["evm"].(map[string]interface{}); ok {
			if yamlInt(evm["chainId"]) == chainID {
				found = true
				break
			}
		}
	}
	if !found {
		networksList = append(networksList, map[string]interface{}{
			"architecture": "evm",
			"evm":          map[string]interface{}{"chainId": chainID},
			"alias":        chainName,
			"failsafe": map[string]interface{}{
				"timeout": map[string]interface{}{"duration": "30s"},
				"retry":   map[string]interface{}{"maxAttempts": 2, "delay": "100ms"},
			},
		})
		project["networks"] = networksList
	}

	return writeERPCConfig(cfg, erpcConfig)
}

// AddPublicRPCs adds ChainList RPCs for a chain to the eRPC ConfigMap.
func AddPublicRPCs(cfg *config.Config, chainID int, chainName string, endpoints []RPCEndpoint) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)

	// Read current eRPC config from ConfigMap.
	configYAML, err := kubectl.Output(kubectlBin, kubeconfigPath,
		"get", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"-o", fmt.Sprintf("jsonpath={.data.%s}", strings.ReplaceAll(erpcConfigKey, ".", "\\.")))
	if err != nil {
		return fmt.Errorf("could not read eRPC config: %w", err)
	}

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		return fmt.Errorf("could not parse eRPC config: %w", err)
	}

	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		return fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("eRPC config project[0] is not a map")
	}

	// Remove any existing chainlist- upstreams for this chain ID.
	existingUpstreams, _ := project["upstreams"].([]interface{})
	filtered := make([]interface{}, 0, len(existingUpstreams))
	for _, u := range existingUpstreams {
		um, ok := u.(map[string]interface{})
		if !ok {
			filtered = append(filtered, u)
			continue
		}
		id, _ := um["id"].(string)
		if strings.HasPrefix(id, fmt.Sprintf("chainlist-%d-", chainID)) {
			continue // remove old chainlist entries for this chain
		}
		filtered = append(filtered, u)
	}

	// Add new ChainList upstreams.
	for i, ep := range endpoints {
		newUpstream := map[string]interface{}{
			"id":       fmt.Sprintf("chainlist-%d-%d", chainID, i),
			"endpoint": ep.URL,
			"evm": map[string]interface{}{
				"chainId": chainID,
			},
		}
		filtered = append(filtered, newUpstream)
	}
	project["upstreams"] = filtered

	// Ensure a network entry exists for this chain ID.
	networksList, _ := project["networks"].([]interface{})
	found := false
	for _, n := range networksList {
		nm, ok := n.(map[string]interface{})
		if !ok {
			continue
		}
		if evm, ok := nm["evm"].(map[string]interface{}); ok {
			if yamlInt(evm["chainId"]) == chainID {
				found = true
				break
			}
		}
	}
	if !found {
		newNetwork := map[string]interface{}{
			"architecture": "evm",
			"evm":          map[string]interface{}{"chainId": chainID},
			"alias":        chainName,
			"failsafe": map[string]interface{}{
				"timeout": map[string]interface{}{"duration": "30s"},
				"retry":   map[string]interface{}{"maxAttempts": 2, "delay": "100ms"},
			},
		}
		networksList = append(networksList, newNetwork)
		project["networks"] = networksList
	}

	// Write back.
	return writeERPCConfig(cfg, erpcConfig)
}

// RemovePublicRPCs removes all ChainList RPCs for a chain from the eRPC ConfigMap.
func RemovePublicRPCs(cfg *config.Config, chainID int) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)

	// Read current eRPC config from ConfigMap.
	configYAML, err := kubectl.Output(kubectlBin, kubeconfigPath,
		"get", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"-o", fmt.Sprintf("jsonpath={.data.%s}", strings.ReplaceAll(erpcConfigKey, ".", "\\.")))
	if err != nil {
		return fmt.Errorf("could not read eRPC config: %w", err)
	}

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		return fmt.Errorf("could not parse eRPC config: %w", err)
	}

	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		return fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("eRPC config project[0] is not a map")
	}

	// Remove chainlist- upstreams for this chain ID.
	existingUpstreams, _ := project["upstreams"].([]interface{})
	filtered := make([]interface{}, 0, len(existingUpstreams))
	removed := 0
	for _, u := range existingUpstreams {
		um, ok := u.(map[string]interface{})
		if !ok {
			filtered = append(filtered, u)
			continue
		}
		id, _ := um["id"].(string)
		if strings.HasPrefix(id, fmt.Sprintf("chainlist-%d-", chainID)) {
			removed++
			continue
		}
		filtered = append(filtered, u)
	}

	if removed == 0 {
		return fmt.Errorf("no ChainList RPCs found for chain ID %d", chainID)
	}

	project["upstreams"] = filtered

	return writeERPCConfig(cfg, erpcConfig)
}

// GetERPCStatus returns eRPC pod status and upstream counts.
func GetERPCStatus(cfg *config.Config) (podStatus string, upstreamCounts map[int]int, err error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", nil, err
	}
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)

	// Get pod status.
	podStatus, err = kubectl.Output(kubectlBin, kubeconfigPath,
		"get", "pods", "-n", erpcNamespace, "-l", "app.kubernetes.io/name=erpc",
		"-o", "custom-columns=NAME:.metadata.name,STATUS:.status.phase,READY:.status.containerStatuses[0].ready,RESTARTS:.status.containerStatuses[0].restartCount",
		"--no-headers")
	if err != nil {
		podStatus = "(unable to fetch pod status)"
	}

	// Read config for upstream counts.
	erpcConfig, readErr := readERPCConfig(cfg)
	if readErr != nil {
		return podStatus, nil, nil
	}

	upstreamCounts = make(map[int]int)
	projects, ok := erpcConfig["projects"].([]interface{})
	if !ok || len(projects) == 0 {
		return podStatus, upstreamCounts, nil
	}
	project, ok := projects[0].(map[string]interface{})
	if !ok {
		return podStatus, upstreamCounts, nil
	}

	upstreams, _ := project["upstreams"].([]interface{})
	for _, u := range upstreams {
		um, ok := u.(map[string]interface{})
		if !ok {
			continue
		}
		if evm, ok := um["evm"].(map[string]interface{}); ok {
			chainID := yamlInt(evm["chainId"])
			if chainID > 0 {
				upstreamCounts[chainID]++
			}
		}
	}

	return podStatus, upstreamCounts, nil
}

// readERPCConfig reads and parses the eRPC ConfigMap YAML.
func readERPCConfig(cfg *config.Config) (map[string]interface{}, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return nil, err
	}
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)

	configYAML, err := kubectl.Output(kubectlBin, kubeconfigPath,
		"get", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"-o", fmt.Sprintf("jsonpath={.data.%s}", strings.ReplaceAll(erpcConfigKey, ".", "\\.")))
	if err != nil {
		return nil, fmt.Errorf("could not read eRPC config: %w", err)
	}

	var erpcConfig map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &erpcConfig); err != nil {
		return nil, fmt.Errorf("could not parse eRPC config: %w", err)
	}

	return erpcConfig, nil
}

// writeERPCConfig serializes the eRPC config and patches the ConfigMap, then restarts eRPC.
func writeERPCConfig(cfg *config.Config, erpcConfig map[string]interface{}) error {
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)

	updatedYAML, err := yaml.Marshal(erpcConfig)
	if err != nil {
		return fmt.Errorf("could not serialize eRPC config: %w", err)
	}

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

	// Restart eRPC to pick up new config.
	if err := kubectl.RunSilent(kubectlBin, kubeconfigPath,
		"rollout", "restart", fmt.Sprintf("deployment/%s", erpcDeployment), "-n", erpcNamespace); err != nil {
		return fmt.Errorf("could not restart eRPC: %w", err)
	}

	return nil
}

// yamlInt extracts an int from a YAML-parsed interface{} value,
// handling both int and float64 (JSON numbers).
func yamlInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
