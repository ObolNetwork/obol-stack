package agentskills

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// The default Hermes agent gets every built-in skill plus the opt-in ones
// recorded here by `obol agent skills add`, so they survive syncs and cluster
// recreation. (CRD sub-agents keep their list in Agent.spec.skills instead.)
type record struct {
	Version int      `yaml:"version"`
	Skills  []string `yaml:"skills"`
}

// RecordPath is $CONFIG_DIR/skills/<agent>.yaml.
func RecordPath(cfg *config.Config, agent string) string {
	return filepath.Join(cfg.ConfigDir, "skills", agent+".yaml")
}

// Added returns the opt-in skills recorded for agent, sorted.
func Added(cfg *config.Config, agent string) ([]string, error) {
	data, err := os.ReadFile(RecordPath(cfg, agent))
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var rec record
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", RecordPath(cfg, agent), err)
	}

	sort.Strings(rec.Skills)

	return rec.Skills, nil
}

// SetAdded records the opt-in skills for agent (deduplicated, sorted).
func SetAdded(cfg *config.Config, agent string, names []string) error {
	uniq := slices.Clone(names)
	sort.Strings(uniq)
	uniq = slices.Compact(uniq)

	path := RecordPath(cfg, agent)
	if len(uniq) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}

		return nil
	}

	data, err := yaml.Marshal(record{Version: 1, Skills: uniq})
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// DefaultAgentSet is the full skill set for the stack-managed default agent:
// every built-in skill plus its recorded opt-in skills.
func DefaultAgentSet(cfg *config.Config, agent string) ([]string, error) {
	added, err := Added(cfg, agent)
	if err != nil {
		return nil, err
	}

	set := append(Embedded(), added...)
	sort.Strings(set)

	return slices.Compact(set), nil
}
