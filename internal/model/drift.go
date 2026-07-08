package model

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

// RouterDrift describes a divergence between the litellm-config ConfigMap
// (the persistence source of truth) and the live LiteLLM router.
//
// Since Reloader no longer watches litellm-config (issue #321: a
// ConfigMap-triggered rollout would gap inference on every model change),
// hot-add/hot-delete API calls are the only thing keeping the live router in
// sync between pod restarts. This check is the replacement safety net: it
// makes a silently-failed hot call visible in `obol model status` instead of
// leaving the operator with a router that quietly disagrees with the config.
type RouterDrift struct {
	// Missing entries exist in the ConfigMap model_list but not in the live
	// router — a hot-add failed or never ran. They will appear after the next
	// pod restart; until then requests to them 404.
	Missing []string `json:"missing,omitempty"`
	// Extra entries are served by the live router but absent from the
	// ConfigMap — a hot-delete failed or never ran. They disappear on the
	// next pod restart.
	Extra []string `json:"extra,omitempty"`
}

// Empty reports whether the live router and the ConfigMap agree.
func (d RouterDrift) Empty() bool {
	return len(d.Missing) == 0 && len(d.Extra) == 0
}

// DiffRouterModels is the pure core of the drift check. configured is the
// raw model_name list from the ConfigMap; live is the model id list reported
// by the running router (/v1/models).
//
// Wildcard entries (trailing "/*") are configuration for LiteLLM's router,
// not concrete serving targets: a configured wildcard is never "missing",
// and any live model matched by a configured wildcard prefix is not "extra".
func DiffRouterModels(configured, live []string) RouterDrift {
	liveSet := make(map[string]struct{}, len(live))
	for _, m := range live {
		liveSet[m] = struct{}{}
	}

	var wildcardPrefixes []string
	configuredSet := make(map[string]struct{}, len(configured))
	var drift RouterDrift

	for _, name := range configured {
		if prefix, ok := strings.CutSuffix(name, "*"); ok {
			wildcardPrefixes = append(wildcardPrefixes, prefix)
			configuredSet[name] = struct{}{}
			continue
		}
		configuredSet[name] = struct{}{}
		if _, ok := liveSet[name]; !ok {
			drift.Missing = append(drift.Missing, name)
		}
	}

	for _, name := range live {
		if _, ok := configuredSet[name]; ok {
			continue
		}
		if strings.HasSuffix(name, "*") {
			// The live router lists wildcard groups verbatim; they are
			// router config, not drift.
			continue
		}
		matched := false
		for _, prefix := range wildcardPrefixes {
			if strings.HasPrefix(name, prefix) {
				matched = true
				break
			}
		}
		if !matched {
			drift.Extra = append(drift.Extra, name)
		}
	}

	return drift
}

// CheckRouterDrift compares the litellm-config ConfigMap model_list against
// the live router's /v1/models. It returns an error when either side cannot
// be read (cluster down, pod not running) — callers should treat that as
// "check unavailable", not as drift.
func CheckRouterDrift(cfg *config.Config) (RouterDrift, error) {
	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")

	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return RouterDrift{}, errors.New("cluster not running")
	}

	raw, err := kubectl.Output(kubectlBinary, kubeconfigPath,
		"get", "configmap", configMapName, "-n", namespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return RouterDrift{}, fmt.Errorf("read LiteLLM config: %w", err)
	}

	var litellmConfig LiteLLMConfig
	if err := yaml.Unmarshal([]byte(raw), &litellmConfig); err != nil {
		return RouterDrift{}, fmt.Errorf("parse LiteLLM config: %w", err)
	}

	configured := make([]string, 0, len(litellmConfig.ModelList))
	for _, entry := range litellmConfig.ModelList {
		configured = append(configured, entry.ModelName)
	}

	masterKey, err := GetMasterKey(cfg)
	if err != nil {
		return RouterDrift{}, fmt.Errorf("read master key: %w", err)
	}

	body, err := litellmGETViaPortForward(kubectlBinary, kubeconfigPath, masterKey, "/v1/models")
	if err != nil {
		return RouterDrift{}, fmt.Errorf("query live router: %w", err)
	}

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return RouterDrift{}, fmt.Errorf("parse /v1/models response: %w", err)
	}

	live := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		live = append(live, m.ID)
	}

	return DiffRouterModels(configured, live), nil
}
