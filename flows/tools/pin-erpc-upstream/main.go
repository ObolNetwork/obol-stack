package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

func main() {
	chainID := flag.Int("chain-id", 0, "EVM chain ID to pin")
	upstreamID := flag.String("upstream-id", "", "eRPC upstream ID to keep for the chain")
	flag.Parse()

	if *chainID <= 0 {
		fatal(errors.New("--chain-id must be a positive integer"))
	}
	if *upstreamID == "" {
		fatal(errors.New("--upstream-id is required"))
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fatal(fmt.Errorf("read eRPC config: %w", err))
	}

	output, err := pinERPCConfigYAML(input, *chainID, *upstreamID)
	if err != nil {
		fatal(err)
	}

	if _, err := os.Stdout.Write(output); err != nil {
		fatal(fmt.Errorf("write patched eRPC config: %w", err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func pinERPCConfigYAML(configYAML []byte, chainID int, upstreamID string) ([]byte, error) {
	var erpcConfig map[string]any
	if err := yaml.Unmarshal(configYAML, &erpcConfig); err != nil {
		return nil, fmt.Errorf("parse eRPC config: %w", err)
	}

	projects, ok := erpcConfig["projects"].([]any)
	if !ok || len(projects) == 0 {
		return nil, errors.New("eRPC config has no projects")
	}

	project, ok := projects[0].(map[string]any)
	if !ok {
		return nil, errors.New("eRPC config project[0] is not a map")
	}

	upstreams, _ := project["upstreams"].([]any)
	filtered := make([]any, 0, len(upstreams))
	var selected any

	for _, upstream := range upstreams {
		um, ok := upstream.(map[string]any)
		if !ok {
			filtered = append(filtered, upstream)
			continue
		}

		evm, _ := um["evm"].(map[string]any)
		if yamlInt(evm["chainId"]) != chainID {
			filtered = append(filtered, upstream)
			continue
		}

		id, _ := um["id"].(string)
		if id == upstreamID {
			selected = upstream
		}
	}

	if selected == nil {
		return nil, fmt.Errorf("eRPC upstream %q for chain %d not found", upstreamID, chainID)
	}

	project["upstreams"] = append([]any{selected}, filtered...)

	updatedYAML, err := yaml.Marshal(erpcConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal pinned eRPC config: %w", err)
	}
	return updatedYAML, nil
}

func yamlInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}
