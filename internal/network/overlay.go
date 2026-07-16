package network

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

// Host-side eRPC operator overlay. Complements recorded-upstreams.yaml
// (simple remote remotes) with durable multi-upstream baskets, scoring,
// rate-limit budgets, and cache policy fragments that survive
// `obol stack up` / helmfile re-renders of the eRPC chart.
//
// Lifecycle (mirrors Hermes mergePreservedHermesConfigKeys + recorded RPCs):
//   1. Operator writes overlay via `obol network overlay apply -f`
//   2. Overlay is stored at $CONFIG_DIR/rpc/erpc-overlay.yaml (0600)
//   3. applyOverlayToCluster deep-merges into the live eRPC ConfigMap
//   4. ReconcileERPCOverlay re-applies after stack up (after ReconcileRecordedRPCs)
//
// See ObolNetwork/obol-stack#763.

const (
	erpcOverlayVersion     = 1
	erpcOverlayAnnotKey    = "obol.stack/erpc-overlay-hash"
	erpcOverlayAnnotSource = "obol.stack/erpc-overlay-source"
)

// ERPCOverlay is the durable operator overlay document.
// Fragments are free-form maps so operators can pass full eRPC YAML objects
// (scoreMultipliers, failsafe, rateLimitBudget, …) without a rigid schema.
type ERPCOverlay struct {
	Version int `yaml:"version"`

	// Networks are merged into projects[0].networks by chainId (preferred)
	// or alias. Matching entries are replaced; others are appended.
	Networks []map[string]any `yaml:"networks,omitempty"`

	// Upstreams are merged into projects[0].upstreams by id. Matching
	// entries are replaced; others are appended (after existing).
	Upstreams []map[string]any `yaml:"upstreams,omitempty"`

	// RateLimiters is deep-merged at the top level. Budget entries under
	// rateLimiters.budgets are merged by id.
	RateLimiters map[string]any `yaml:"rateLimiters,omitempty"`

	// CachePoliciesAdd are appended to database.evmJsonRpcCache.policies
	// when no existing policy has the same network+method+finality triple.
	CachePoliciesAdd []map[string]any `yaml:"cachePoliciesAdd,omitempty"`
}

// ERPCOverlayStatus is a read-only summary for CLI status.
type ERPCOverlayStatus struct {
	Path           string
	Present        bool
	Version        int
	NetworkCount   int
	UpstreamCount  int
	BudgetCount    int
	CachePolicyAdd int
	NetworkKeys    []string
	UpstreamIDs    []string
	ContentHash    string
}

func erpcOverlayPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "rpc", "erpc-overlay.yaml")
}

// ReconcileERPCOverlay re-applies the host-side overlay into the live eRPC
// ConfigMap. Called after ReconcileRecordedRPCs on stack up. Best-effort:
// missing overlay is a silent no-op; apply errors are warned, not fatal.
func ReconcileERPCOverlay(cfg *config.Config, u *ui.UI) {
	ov, err := readERPCOverlay(cfg)
	if err != nil {
		u.Warnf("Could not read durable eRPC config: %v", err)
		return
	}
	if ov == nil {
		return
	}
	if err := applyOverlayToCluster(cfg, ov, "reconcile"); err != nil {
		u.Warnf("Could not restore durable eRPC operator config: %v", err)
		return
	}
	u.Successf("Restored durable eRPC operator config (%d network(s), %d upstream(s))",
		len(ov.Networks), len(ov.Upstreams))
}

// ApplyERPCOverlayFile loads an overlay YAML from path, persists it under
// ConfigDir, and merges it into the live eRPC ConfigMap.
func SetERPC(cfg *config.Config, u *ui.UI, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read overlay file: %w", err)
	}
	ov, err := parseERPCOverlay(data)
	if err != nil {
		return err
	}
	if err := writeERPCOverlay(cfg, ov); err != nil {
		return err
	}
	if err := applyOverlayToCluster(cfg, ov, filepath.Base(path)); err != nil {
		return err
	}
	u.Successf("eRPC config set and saved to %s", erpcOverlayPath(cfg))
	u.Infof("  networks=%d upstreams=%d cachePoliciesAdd=%d",
		len(ov.Networks), len(ov.Upstreams), len(ov.CachePoliciesAdd))
	return nil
}

// ClearERPCOverlay removes overlay-owned fragments from the live ConfigMap
// (best-effort), then deletes the host-side overlay file.
func ResetERPC(cfg *config.Config, u *ui.UI) error {
	ov, err := readERPCOverlay(cfg)
	if err != nil {
		return err
	}
	if ov == nil {
		u.Info("No durable eRPC config on disk")
		return nil
	}

	if err := removeOverlayFromCluster(cfg, ov); err != nil {
		u.Warnf("Could not strip eRPC config from live ConfigMap (will still reset host file): %v", err)
	} else {
		u.Success("Removed operator eRPC networks/upstreams from live ConfigMap")
	}

	path := erpcOverlayPath(cfg)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove overlay file: %w", err)
	}
	// Best-effort annotation clear
	_ = annotateERPCOverlay(cfg, "", "")
	u.Successf("Reset eRPC config at %s", path)
	u.Info("Chart base + recorded remotes remain; re-run `obol stack up` if you need a full eRPC re-render")
	return nil
}

// StatusERPCOverlay returns a summary of the on-disk overlay (no cluster I/O).
func StatusERPC(cfg *config.Config) (*ERPCOverlayStatus, error) {
	path := erpcOverlayPath(cfg)
	st := &ERPCOverlayStatus{Path: path}
	ov, err := readERPCOverlay(cfg)
	if err != nil {
		return nil, err
	}
	if ov == nil {
		return st, nil
	}
	st.Present = true
	st.Version = ov.Version
	st.NetworkCount = len(ov.Networks)
	st.UpstreamCount = len(ov.Upstreams)
	st.CachePolicyAdd = len(ov.CachePoliciesAdd)
	st.BudgetCount = countRateLimitBudgets(ov.RateLimiters)
	for _, n := range ov.Networks {
		st.NetworkKeys = append(st.NetworkKeys, describeNetwork(n))
	}
	for _, u := range ov.Upstreams {
		if id, _ := u["id"].(string); id != "" {
			st.UpstreamIDs = append(st.UpstreamIDs, id)
		}
	}
	if data, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(data)
		st.ContentHash = hex.EncodeToString(sum[:8])
	}
	return st, nil
}

// --- persistence ---

func readERPCOverlay(cfg *config.Config) (*ERPCOverlay, error) {
	data, err := os.ReadFile(erpcOverlayPath(cfg))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseERPCOverlay(data)
}

func parseERPCOverlay(data []byte) (*ERPCOverlay, error) {
	var ov ERPCOverlay
	if err := yaml.Unmarshal(data, &ov); err != nil {
		return nil, fmt.Errorf("parse eRPC overlay: %w", err)
	}
	if ov.Version == 0 {
		ov.Version = erpcOverlayVersion
	}
	if ov.Version != erpcOverlayVersion {
		return nil, fmt.Errorf("unsupported erpc-overlay version %d (want %d)", ov.Version, erpcOverlayVersion)
	}
	if len(ov.Networks) == 0 && len(ov.Upstreams) == 0 &&
		len(ov.RateLimiters) == 0 && len(ov.CachePoliciesAdd) == 0 {
		return nil, fmt.Errorf("eRPC overlay is empty (need networks, upstreams, rateLimiters, and/or cachePoliciesAdd)")
	}
	// Upstreams must have ids for merge keys
	for i, u := range ov.Upstreams {
		id, _ := u["id"].(string)
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("upstreams[%d]: missing id", i)
		}
	}
	return &ov, nil
}

func writeERPCOverlay(cfg *config.Config, ov *ERPCOverlay) error {
	if ov.Version == 0 {
		ov.Version = erpcOverlayVersion
	}
	path := erpcOverlayPath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(ov)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- cluster apply / merge ---

func applyOverlayToCluster(cfg *config.Config, ov *ERPCOverlay, source string) error {
	erpcConfig, err := readERPCConfig(cfg)
	if err != nil {
		return err
	}
	if err := mergeERPCOverlay(erpcConfig, ov); err != nil {
		return err
	}
	if err := writeERPCConfig(cfg, erpcConfig); err != nil {
		return err
	}
	hash := ""
	if data, err := os.ReadFile(erpcOverlayPath(cfg)); err == nil {
		sum := sha256.Sum256(data)
		hash = hex.EncodeToString(sum[:8])
	}
	_ = annotateERPCOverlay(cfg, hash, source)
	return nil
}

func removeOverlayFromCluster(cfg *config.Config, ov *ERPCOverlay) error {
	erpcConfig, err := readERPCConfig(cfg)
	if err != nil {
		return err
	}
	if err := stripERPCOverlay(erpcConfig, ov); err != nil {
		return err
	}
	return writeERPCConfig(cfg, erpcConfig)
}

// mergeERPCOverlay mutates erpcConfig in place. Exported for tests via
// the unexported name used from overlay_test.go (same package).
func mergeERPCOverlay(erpcConfig map[string]any, ov *ERPCOverlay) error {
	if ov == nil {
		return nil
	}
	projects, ok := erpcConfig["projects"].([]any)
	if !ok || len(projects) == 0 {
		return fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]any)
	if !ok {
		return fmt.Errorf("eRPC config project[0] is not a map")
	}

	if len(ov.Networks) > 0 {
		networks, _ := project["networks"].([]any)
		project["networks"] = mergeNetworksByKey(networks, ov.Networks)
	}
	if len(ov.Upstreams) > 0 {
		upstreams, _ := project["upstreams"].([]any)
		project["upstreams"] = mergeUpstreamsByID(upstreams, ov.Upstreams)
	}
	if len(ov.RateLimiters) > 0 {
		existing, _ := erpcConfig["rateLimiters"].(map[string]any)
		erpcConfig["rateLimiters"] = mergeRateLimiters(existing, ov.RateLimiters)
	}
	if len(ov.CachePoliciesAdd) > 0 {
		if err := mergeCachePolicies(erpcConfig, ov.CachePoliciesAdd); err != nil {
			return err
		}
	}
	return nil
}

func stripERPCOverlay(erpcConfig map[string]any, ov *ERPCOverlay) error {
	projects, ok := erpcConfig["projects"].([]any)
	if !ok || len(projects) == 0 {
		return fmt.Errorf("eRPC config has no projects")
	}
	project, ok := projects[0].(map[string]any)
	if !ok {
		return fmt.Errorf("eRPC config project[0] is not a map")
	}

	if len(ov.Upstreams) > 0 {
		drop := map[string]struct{}{}
		for _, u := range ov.Upstreams {
			if id, _ := u["id"].(string); id != "" {
				drop[id] = struct{}{}
			}
		}
		upstreams, _ := project["upstreams"].([]any)
		kept := make([]any, 0, len(upstreams))
		for _, u := range upstreams {
			um, ok := u.(map[string]any)
			if !ok {
				kept = append(kept, u)
				continue
			}
			id, _ := um["id"].(string)
			if _, hit := drop[id]; hit {
				continue
			}
			kept = append(kept, u)
		}
		project["upstreams"] = kept
	}

	if len(ov.Networks) > 0 {
		drop := map[string]struct{}{}
		for _, n := range ov.Networks {
			if k := networkMergeKey(n); k != "" {
				drop[k] = struct{}{}
			}
		}
		networks, _ := project["networks"].([]any)
		kept := make([]any, 0, len(networks))
		for _, n := range networks {
			nm, ok := n.(map[string]any)
			if !ok {
				kept = append(kept, n)
				continue
			}
			if _, hit := drop[networkMergeKey(nm)]; hit {
				continue
			}
			kept = append(kept, n)
		}
		project["networks"] = kept
	}
	// Leave rateLimiters / cache policies in place — removing budgets that
	// other upstreams still reference is riskier than leaving unused ones.
	return nil
}

func mergeNetworksByKey(existing []any, overlay []map[string]any) []any {
	out := make([]any, 0, len(existing)+len(overlay))
	index := map[string]int{} // mergeKey → index in out
	for _, n := range existing {
		nm, ok := n.(map[string]any)
		if !ok {
			out = append(out, n)
			continue
		}
		k := networkMergeKey(nm)
		if k != "" {
			index[k] = len(out)
		}
		out = append(out, cloneMap(nm))
	}
	for _, n := range overlay {
		// Deep-ish copy so later mutations don't touch the overlay struct
		entry := cloneMap(n)
		k := networkMergeKey(entry)
		if k != "" {
			if i, ok := index[k]; ok {
				out[i] = entry
				continue
			}
			index[k] = len(out)
		}
		out = append(out, entry)
	}
	return out
}

func mergeUpstreamsByID(existing []any, overlay []map[string]any) []any {
	out := make([]any, 0, len(existing)+len(overlay))
	index := map[string]int{}
	for _, u := range existing {
		um, ok := u.(map[string]any)
		if !ok {
			out = append(out, u)
			continue
		}
		id, _ := um["id"].(string)
		if id != "" {
			index[id] = len(out)
		}
		out = append(out, cloneMap(um))
	}
	for _, u := range overlay {
		entry := cloneMap(u)
		id, _ := entry["id"].(string)
		if id != "" {
			if i, ok := index[id]; ok {
				out[i] = entry
				continue
			}
			index[id] = len(out)
		}
		out = append(out, entry)
	}
	return out
}

func mergeRateLimiters(existing, overlay map[string]any) map[string]any {
	if existing == nil {
		existing = map[string]any{}
	}
	out := cloneMap(existing)
	// budgets: merge by id
	if ob, ok := overlay["budgets"]; ok {
		out["budgets"] = mergeBudgetList(out["budgets"], ob)
	}
	for k, v := range overlay {
		if k == "budgets" {
			continue
		}
		out[k] = v
	}
	return out
}

func mergeBudgetList(existing any, overlay any) []any {
	var out []any
	index := map[string]int{}
	appendBudget := func(b any) {
		bm, ok := b.(map[string]any)
		if !ok {
			out = append(out, b)
			return
		}
		id, _ := bm["id"].(string)
		entry := cloneMap(bm)
		if id != "" {
			if i, ok := index[id]; ok {
				out[i] = entry
				return
			}
			index[id] = len(out)
		}
		out = append(out, entry)
	}
	switch e := existing.(type) {
	case []any:
		for _, b := range e {
			appendBudget(b)
		}
	}
	switch o := overlay.(type) {
	case []any:
		for _, b := range o {
			appendBudget(b)
		}
	case []map[string]any:
		for _, b := range o {
			appendBudget(b)
		}
	}
	return out
}

func mergeCachePolicies(erpcConfig map[string]any, add []map[string]any) error {
	db, _ := erpcConfig["database"].(map[string]any)
	if db == nil {
		db = map[string]any{}
		erpcConfig["database"] = db
	}
	cache, _ := db["evmJsonRpcCache"].(map[string]any)
	if cache == nil {
		cache = map[string]any{}
		db["evmJsonRpcCache"] = cache
	}
	policies, _ := cache["policies"].([]any)
	seen := map[string]struct{}{}
	for _, p := range policies {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		seen[cachePolicyKey(pm)] = struct{}{}
	}
	for _, p := range add {
		entry := cloneMap(p)
		k := cachePolicyKey(entry)
		if _, ok := seen[k]; ok {
			// Replace existing policy with same key
			for i, cur := range policies {
				cm, ok := cur.(map[string]any)
				if ok && cachePolicyKey(cm) == k {
					policies[i] = entry
					break
				}
			}
			continue
		}
		policies = append(policies, entry)
		seen[k] = struct{}{}
	}
	cache["policies"] = policies
	return nil
}

func cachePolicyKey(p map[string]any) string {
	return fmt.Sprintf("%v|%v|%v", p["network"], p["method"], p["finality"])
}

func networkMergeKey(n map[string]any) string {
	if evm, ok := n["evm"].(map[string]any); ok {
		if cid := yamlInt(evm["chainId"]); cid != 0 {
			return fmt.Sprintf("chain:%d", cid)
		}
	}
	if alias, ok := n["alias"].(string); ok && alias != "" {
		return "alias:" + alias
	}
	return ""
}

func describeNetwork(n map[string]any) string {
	alias, _ := n["alias"].(string)
	cid := 0
	if evm, ok := n["evm"].(map[string]any); ok {
		cid = yamlInt(evm["chainId"])
	}
	switch {
	case alias != "" && cid != 0:
		return fmt.Sprintf("%s (chainId %d)", alias, cid)
	case alias != "":
		return alias
	case cid != 0:
		return fmt.Sprintf("chainId %d", cid)
	default:
		return "(unnamed network)"
	}
}

func countRateLimitBudgets(rl map[string]any) int {
	if rl == nil {
		return 0
	}
	switch b := rl["budgets"].(type) {
	case []any:
		return len(b)
	case []map[string]any:
		return len(b)
	default:
		return 0
	}
}

func cloneMap(m map[string]any) map[string]any {
	// YAML round-trip keeps nested maps as map[string]any for our purposes
	// without needing a full deep-copy library.
	b, err := yaml.Marshal(m)
	if err != nil {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		out = make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func annotateERPCOverlay(cfg *config.Config, hash, source string) error {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return err
	}
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)
	// Clear when both empty
	if hash == "" && source == "" {
		_ = kubectl.RunSilent(kubectlBin, kubeconfigPath,
			"annotate", "configmap", erpcConfigMapName, "-n", erpcNamespace,
			erpcOverlayAnnotKey+"-", erpcOverlayAnnotSource+"-", "--overwrite")
		return nil
	}
	args := []string{
		"annotate", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"--overwrite",
	}
	if hash != "" {
		args = append(args, erpcOverlayAnnotKey+"="+hash)
	}
	if source != "" {
		args = append(args, erpcOverlayAnnotSource+"="+source)
	}
	return kubectl.RunSilent(kubectlBin, kubeconfigPath, args...)
}
