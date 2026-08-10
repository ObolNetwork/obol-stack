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

	// RateLimiters budgets are merged by id; other top-level keys are
	// shallow-replaced (see mergeRateLimiters).
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

	// ClusterSync is a best-effort comparison of ContentHash against the
	// live ConfigMap's erpcOverlayAnnotKey annotation:
	// "in-sync" | "drifted" | "not-applied" | "" (cluster unreachable/unknown).
	ClusterSync string
}

// ERPCSyncInSync / ERPCSyncDrifted / ERPCSyncNotApplied are the ClusterSync
// values StatusERPC can report; "" means the cluster couldn't be checked.
const (
	ERPCSyncInSync     = "in-sync"
	ERPCSyncDrifted    = "drifted"
	ERPCSyncNotApplied = "not-applied"
)

func erpcOverlayPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "rpc", "erpc-overlay.yaml")
}

// erpcProvenancePath is the host-side record of what mergeERPCOverlay
// replaced the FIRST time each overlay-owned key was applied, so ResetERPC
// can restore chart-base/recorded entries instead of deleting them.
func erpcProvenancePath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "rpc", "erpc-overlay-provenance.yaml")
}

// erpcProvenance maps each overlay-owned merge key to the entry it replaced.
// A key present with a nil *map value means the overlay ADDED that entry
// (no prior base entry to restore); a key absent means "not yet recorded".
// (A pointer, not a bare map, because a nil map[string]any round-trips
// through a YAML marshal/unmarshal as an empty map, not nil — see
// captureERPCProvenance.)
type erpcProvenance struct {
	Networks  map[string]*map[string]any `yaml:"networks,omitempty"`
	Upstreams map[string]*map[string]any `yaml:"upstreams,omitempty"`
}

func readERPCProvenance(cfg *config.Config) (*erpcProvenance, error) {
	data, err := os.ReadFile(erpcProvenancePath(cfg))
	if os.IsNotExist(err) {
		return &erpcProvenance{}, nil
	}
	if err != nil {
		return nil, err
	}
	var p erpcProvenance
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse eRPC overlay provenance: %w", err)
	}
	return &p, nil
}

func writeERPCProvenance(cfg *config.Config, p *erpcProvenance) error {
	path := erpcProvenancePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// captureERPCProvenance records, for every overlay-owned key not already
// tracked, whatever entry currently occupies that key in erpcConfig — the
// chart-base or recorded-RPC entry mergeERPCOverlay is about to replace (or
// nil if the key doesn't exist yet, meaning the overlay is adding it).
// Must be called BEFORE mergeERPCOverlay mutates erpcConfig.
func captureERPCProvenance(cfg *config.Config, erpcConfig map[string]any, ov *ERPCOverlay) error {
	if len(ov.Networks) == 0 && len(ov.Upstreams) == 0 {
		return nil
	}
	prov, err := readERPCProvenance(cfg)
	if err != nil {
		return err
	}
	if !captureERPCProvenanceEntries(prov, erpcConfig, ov) {
		return nil
	}
	return writeERPCProvenance(cfg, prov)
}

// captureERPCProvenanceEntries is the in-memory half of
// captureERPCProvenance. It records the original value for each newly-owned
// key after retired overlay entries have been stripped, so replacing one
// overlay with another never mistakes the previous overlay value for chart
// base state.
func captureERPCProvenanceEntries(prov *erpcProvenance, erpcConfig map[string]any, ov *ERPCOverlay) bool {
	if prov == nil || ov == nil {
		return false
	}
	project := erpcConfigProject(erpcConfig)
	if project == nil {
		return false
	}
	changed := false

	if len(ov.Networks) > 0 {
		if prov.Networks == nil {
			prov.Networks = map[string]*map[string]any{}
		}
		byKey := map[string]map[string]any{}
		for _, n := range asMapSlice(project["networks"]) {
			if k := networkMergeKey(n); k != "" {
				byKey[k] = n
			}
		}
		for _, n := range ov.Networks {
			k := networkMergeKey(n)
			if k == "" {
				continue
			}
			if _, tracked := prov.Networks[k]; tracked {
				continue
			}
			if orig, hit := byKey[k]; hit {
				clone := cloneMap(orig)
				prov.Networks[k] = &clone
			} else {
				prov.Networks[k] = nil // marker: overlay added this key
			}
			changed = true
		}
	}

	if len(ov.Upstreams) > 0 {
		if prov.Upstreams == nil {
			prov.Upstreams = map[string]*map[string]any{}
		}
		byID := map[string]map[string]any{}
		for _, u := range asMapSlice(project["upstreams"]) {
			if id, _ := u["id"].(string); id != "" {
				byID[id] = u
			}
		}
		for _, u := range ov.Upstreams {
			id, _ := u["id"].(string)
			if id == "" {
				continue
			}
			if _, tracked := prov.Upstreams[id]; tracked {
				continue
			}
			if orig, hit := byID[id]; hit {
				clone := cloneMap(orig)
				prov.Upstreams[id] = &clone
			} else {
				prov.Upstreams[id] = nil // marker: overlay added this key
			}
			changed = true
		}
	}

	return changed
}

// ensureERPCProvenanceTracksOverlay preserves ownership of an existing saved
// overlay before SetERPC replaces that document. Normally provenance already
// contains these keys. Missing entries can occur after upgrading an overlay
// created before provenance tracking; nil markers retain the legacy reset
// behavior (drop the owned key) when the original value is unknowable.
func ensureERPCProvenanceTracksOverlay(cfg *config.Config, ov *ERPCOverlay) error {
	if ov == nil {
		return nil
	}
	prov, err := readERPCProvenance(cfg)
	if err != nil {
		return err
	}
	changed := false
	if len(ov.Networks) > 0 && prov.Networks == nil {
		prov.Networks = make(map[string]*map[string]any)
	}
	for _, n := range ov.Networks {
		key := networkMergeKey(n)
		if key == "" {
			continue
		}
		if _, tracked := prov.Networks[key]; !tracked {
			prov.Networks[key] = nil
			changed = true
		}
	}
	if len(ov.Upstreams) > 0 && prov.Upstreams == nil {
		prov.Upstreams = make(map[string]*map[string]any)
	}
	for _, u := range ov.Upstreams {
		id, _ := u["id"].(string)
		if id == "" {
			continue
		}
		if _, tracked := prov.Upstreams[id]; !tracked {
			prov.Upstreams[id] = nil
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return writeERPCProvenance(cfg, prov)
}

// erpcConfigProject returns projects[0] as a map, or nil if the shape is
// unexpected (callers treat that as "nothing to capture/strip").
func erpcConfigProject(erpcConfig map[string]any) map[string]any {
	projects, ok := erpcConfig["projects"].([]any)
	if !ok || len(projects) == 0 {
		return nil
	}
	project, ok := projects[0].(map[string]any)
	if !ok {
		return nil
	}
	return project
}

// asMapSlice filters a []any (as decoded from YAML) down to its map[string]any
// entries, skipping anything malformed.
func asMapSlice(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
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

// SetERPC loads an overlay YAML from path, persists it under ConfigDir as the
// complete desired operator overlay, and reconciles the live eRPC ConfigMap to
// it. Entries owned by the previous overlay but omitted from the new document
// are removed (or restored from provenance) before the new entries are merged.
func SetERPC(cfg *config.Config, u *ui.UI, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read overlay file: %w", err)
	}
	ov, err := parseERPCOverlay(data)
	if err != nil {
		return err
	}
	previous, err := readERPCOverlay(cfg)
	if err != nil {
		return fmt.Errorf("read existing eRPC overlay: %w", err)
	}
	if err := ensureERPCProvenanceTracksOverlay(cfg, previous); err != nil {
		return fmt.Errorf("preserve existing eRPC overlay ownership: %w", err)
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

// ResetERPC removes overlay-owned fragments from the live ConfigMap, then
// deletes the host-side overlay and provenance files. If cluster cleanup fails,
// the files are deliberately retained so reset or stack-up reconciliation can
// retry without losing ownership information.
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
		return fmt.Errorf("strip eRPC config from live ConfigMap (durable overlay retained for retry): %w", err)
	}
	u.Success("Removed overlay-added networks/upstreams and restored any chart-base/recorded entries they replaced")

	path := erpcOverlayPath(cfg)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove overlay file: %w", err)
	}
	if err := os.Remove(erpcProvenancePath(cfg)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove overlay provenance file: %w", err)
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
	clusterHash, clusterErr := readERPCOverlayAnnotation(cfg)
	st.ClusterSync = erpcOverlayDriftStatus(st.ContentHash, clusterHash, clusterErr)
	return st, nil
}

// erpcOverlayDriftStatus compares the host-side overlay's content hash
// against the erpcOverlayAnnotKey annotation read from the live ConfigMap.
// Pure/no I/O so it's directly testable; clusterErr != nil (cluster
// unreachable, no permissions, etc.) yields "" — best-effort, not fatal.
func erpcOverlayDriftStatus(localHash, clusterHash string, clusterErr error) string {
	if clusterErr != nil {
		return ""
	}
	if clusterHash == "" {
		return ERPCSyncNotApplied
	}
	if clusterHash == localHash {
		return ERPCSyncInSync
	}
	return ERPCSyncDrifted
}

// readERPCOverlayAnnotation best-effort reads the erpcOverlayAnnotKey
// annotation applyOverlayToCluster stamps on the eRPC ConfigMap, so status
// can detect drift between the on-disk overlay and what's actually live.
func readERPCOverlayAnnotation(cfg *config.Config) (string, error) {
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return "", err
	}
	kubectlBin, kubeconfigPath := kubectl.Paths(cfg)
	out, err := kubectl.Output(kubectlBin, kubeconfigPath,
		"get", "configmap", erpcConfigMapName, "-n", erpcNamespace,
		"-o", fmt.Sprintf("jsonpath={.metadata.annotations.%s}", strings.ReplaceAll(erpcOverlayAnnotKey, ".", "\\.")))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
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
	// Networks must have a usable merge key (evm.chainId or alias) — an
	// entry with neither gets networkMergeKey "" and would be blindly
	// appended as a duplicate on every merge instead of being matched.
	for i, n := range ov.Networks {
		if err := validateNetworkEntry(n); err != nil {
			return nil, fmt.Errorf("networks[%d]: %w", i, err)
		}
	}
	return &ov, nil
}

// validateNetworkEntry checks the cheap-to-catch type errors and requires a
// usable merge key, so a bad entry fails `erpc set` instead of silently
// duplicating (or crash-looping eRPC) on every merge.
func validateNetworkEntry(n map[string]any) error {
	if raw, ok := n["evm"]; ok {
		evm, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("evm must be a mapping, got %T", raw)
		}
		if cid, ok := evm["chainId"]; ok {
			switch cid.(type) {
			case int, int64, float64:
			default:
				return fmt.Errorf("evm.chainId must be a number, got %T", cid)
			}
		}
	}
	if raw, ok := n["alias"]; ok {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("alias must be a string, got %T", raw)
		}
	}
	if networkMergeKey(n) == "" {
		return fmt.Errorf("missing usable merge key (need evm.chainId or alias)")
	}
	return nil
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
	prov, err := readERPCProvenance(cfg)
	if err != nil {
		return fmt.Errorf("read eRPC overlay provenance: %w", err)
	}
	if err := reconcileERPCOverlayConfig(erpcConfig, ov, prov); err != nil {
		return err
	}
	// Persist the union provenance before mutating the cluster. If the cluster
	// write fails, the saved desired overlay plus this record is sufficient for
	// the next reconcile to remove both old and partially-applied ownership.
	if err := writeERPCProvenance(cfg, prov); err != nil {
		return fmt.Errorf("write eRPC overlay provenance: %w", err)
	}
	if err := writeERPCConfig(cfg, erpcConfig); err != nil {
		return err
	}
	pruneERPCProvenance(prov, ov)
	if err := writeERPCProvenance(cfg, prov); err != nil {
		return fmt.Errorf("prune eRPC overlay provenance: %w", err)
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
	prov, err := readERPCProvenance(cfg)
	if err != nil {
		return err
	}
	if provenanceHasEntries(prov) {
		if err := stripERPCProvenanceKeys(erpcConfig, prov, nil, nil); err != nil {
			return err
		}
	} else if err := stripERPCOverlay(erpcConfig, ov, prov); err != nil {
		return err
	}
	return writeERPCConfig(cfg, erpcConfig)
}

// reconcileERPCOverlayConfig treats ov as desired state for overlay-owned
// networks and upstreams. Provenance may temporarily contain a union of old
// and new keys to make replacement crash-safe; keys not present in ov are
// stripped before provenance for new keys is captured and ov is merged.
func reconcileERPCOverlayConfig(erpcConfig map[string]any, ov *ERPCOverlay, prov *erpcProvenance) error {
	if ov == nil {
		return nil
	}
	if prov == nil {
		return fmt.Errorf("eRPC overlay provenance is nil")
	}
	desiredNetworks, desiredUpstreams := erpcOverlayKeySets(ov)
	if err := stripERPCProvenanceKeys(erpcConfig, prov, desiredNetworks, desiredUpstreams); err != nil {
		return err
	}
	captureERPCProvenanceEntries(prov, erpcConfig, ov)
	return mergeERPCOverlay(erpcConfig, ov)
}

func erpcOverlayKeySets(ov *ERPCOverlay) (map[string]struct{}, map[string]struct{}) {
	networks := make(map[string]struct{})
	upstreams := make(map[string]struct{})
	if ov == nil {
		return networks, upstreams
	}
	for _, n := range ov.Networks {
		if key := networkMergeKey(n); key != "" {
			networks[key] = struct{}{}
		}
	}
	for _, u := range ov.Upstreams {
		if id, _ := u["id"].(string); id != "" {
			upstreams[id] = struct{}{}
		}
	}
	return networks, upstreams
}

func pruneERPCProvenance(prov *erpcProvenance, ov *ERPCOverlay) {
	if prov == nil {
		return
	}
	keepNetworks, keepUpstreams := erpcOverlayKeySets(ov)
	for key := range prov.Networks {
		if _, keep := keepNetworks[key]; !keep {
			delete(prov.Networks, key)
		}
	}
	for id := range prov.Upstreams {
		if _, keep := keepUpstreams[id]; !keep {
			delete(prov.Upstreams, id)
		}
	}
}

func provenanceHasEntries(prov *erpcProvenance) bool {
	return prov != nil && (len(prov.Networks) > 0 || len(prov.Upstreams) > 0)
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

// stripERPCOverlay removes overlay-owned entries from erpcConfig. Per key,
// prov says whether the overlay ADDED it (no prior entry — drop it) or
// REPLACED a chart-base/recorded entry (restore that entry instead of
// deleting it, so the chain doesn't go unroutable). A key with no
// provenance record at all (nil prov, or overlay applied before provenance
// tracking existed) falls back to the old drop-only behavior.
func stripERPCOverlay(erpcConfig map[string]any, ov *ERPCOverlay, prov *erpcProvenance) error {
	networks, upstreams := erpcOverlayKeySets(ov)
	return stripERPCKeys(erpcConfig, networks, upstreams, prov)
}

// stripERPCProvenanceKeys strips every provenance-tracked key that is not in
// the corresponding keep set. A nil keep set means keep nothing. This is what
// lets a newly persisted overlay remove keys owned by its predecessor even
// though the predecessor document is no longer on disk.
func stripERPCProvenanceKeys(erpcConfig map[string]any, prov *erpcProvenance, keepNetworks, keepUpstreams map[string]struct{}) error {
	networks := make(map[string]struct{})
	upstreams := make(map[string]struct{})
	if prov != nil {
		for key := range prov.Networks {
			if _, keep := keepNetworks[key]; !keep {
				networks[key] = struct{}{}
			}
		}
		for id := range prov.Upstreams {
			if _, keep := keepUpstreams[id]; !keep {
				upstreams[id] = struct{}{}
			}
		}
	}
	return stripERPCKeys(erpcConfig, networks, upstreams, prov)
}

func stripERPCKeys(erpcConfig map[string]any, networkKeys, upstreamIDs map[string]struct{}, prov *erpcProvenance) error {
	project := erpcConfigProject(erpcConfig)
	if project == nil {
		return fmt.Errorf("eRPC config has no projects")
	}
	if prov == nil {
		prov = &erpcProvenance{}
	}

	if len(upstreamIDs) > 0 {
		drop := map[string]struct{}{}
		restore := map[string]map[string]any{}
		for id := range upstreamIDs {
			if orig, tracked := prov.Upstreams[id]; tracked && orig != nil {
				restore[id] = *orig
			} else {
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
			if orig, hit := restore[id]; hit {
				kept = append(kept, orig)
				continue
			}
			if _, hit := drop[id]; hit {
				continue
			}
			kept = append(kept, u)
		}
		project["upstreams"] = kept
	}

	if len(networkKeys) > 0 {
		drop := map[string]struct{}{}
		restore := map[string]map[string]any{}
		for k := range networkKeys {
			if orig, tracked := prov.Networks[k]; tracked && orig != nil {
				restore[k] = *orig
			} else {
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
			k := networkMergeKey(nm)
			if orig, hit := restore[k]; hit {
				kept = append(kept, orig)
				continue
			}
			if _, hit := drop[k]; hit {
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
	out := make([]any, 0)     // no capacity hint: len(a)+len(b) trips CodeQL allocation-size-overflow
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
	out := make([]any, 0) // no capacity hint: len(a)+len(b) trips CodeQL allocation-size-overflow
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
