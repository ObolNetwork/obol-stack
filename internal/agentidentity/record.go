// Package agentidentity keeps a host-side record of ERC-8004 AgentIdentity
// CRs. The CR (and in particular status.registrations — the per-chain
// agentId) lives only in etcd; losing it on cluster recreation means the
// next registration mints a NEW on-chain identity instead of updating the
// existing one. The record at $OBOL_CONFIG_DIR/identity/<ns>__<name>.json is
// the single source of truth across recreation: the CLI writes it on every
// AgentIdentity write, `obol stack export` refreshes it from the cluster
// (picking up controller-side registrations) and ships it inside the config
// tree, and record replay (internal/replay) re-applies it.
package agentidentity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

const resource = "agentidentities.obol.org"

// Metadata is the subset of ObjectMeta the CLI reads and writes.
type Metadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// Record is the JSON-shaped view of an AgentIdentity used both for kubectl
// round-trips and as the on-disk record.
type Record struct {
	APIVersion string                          `json:"apiVersion"`
	Kind       string                          `json:"kind"`
	Metadata   Metadata                        `json:"metadata"`
	Spec       monetizeapi.AgentIdentitySpec   `json:"spec"`
	Status     monetizeapi.AgentIdentityStatus `json:"status,omitempty"`
}

// New returns an empty record for ns/name.
func New(ns, name string) *Record {
	return &Record{
		APIVersion: monetizeapi.Group + "/" + monetizeapi.Version,
		Kind:       monetizeapi.AgentIdentityKind,
		Metadata:   Metadata{Namespace: ns, Name: name},
	}
}

func recordDir(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "identity")
}

// Path returns the record path for ns/name.
func Path(cfg *config.Config, ns, name string) string {
	return filepath.Join(recordDir(cfg), ns+"__"+name+".json")
}

// Save writes the record (spec + status only; no server-managed metadata).
func Save(cfg *config.Config, rec *Record) error {
	if rec == nil || rec.Metadata.Name == "" || rec.Metadata.Namespace == "" {
		return errors.New("agentidentity: namespace and name required")
	}
	clean := *rec
	clean.Metadata = Metadata{Name: rec.Metadata.Name, Namespace: rec.Metadata.Namespace}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(recordDir(cfg), 0o700); err != nil {
		return err
	}
	path := Path(cfg, rec.Metadata.Namespace, rec.Metadata.Name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads the record for ns/name; (nil, nil) when none exists.
func Load(cfg *config.Config, ns, name string) (*Record, error) {
	data, err := os.ReadFile(Path(cfg, ns, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse identity record %s/%s: %w", ns, name, err)
	}
	return &rec, nil
}

// List returns every recorded identity, sorted by file name.
func List(cfg *config.Config) ([]*Record, error) {
	entries, err := os.ReadDir(recordDir(cfg))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && strings.Contains(e.Name(), "__") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []*Record
	var errs []error
	for _, n := range names {
		ns, name, _ := strings.Cut(strings.TrimSuffix(n, ".json"), "__")
		rec, err := Load(cfg, ns, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if rec != nil {
			out = append(out, rec)
		}
	}
	return out, errors.Join(errs...)
}

// Kube is the slice of kubectl this package needs; swapped in tests.
type Kube interface {
	Output(args ...string) (string, error)
	Run(args ...string) error
	// Apply applies a JSON/YAML manifest.
	Apply(data []byte) error
}

type kubectlKube struct{ bin, kubeconfig string }

func (k kubectlKube) Output(args ...string) (string, error) {
	return kubectl.Output(k.bin, k.kubeconfig, args...)
}
func (k kubectlKube) Run(args ...string) error { return kubectl.Run(k.bin, k.kubeconfig, args...) }
func (k kubectlKube) Apply(data []byte) error  { return kubectl.Apply(k.bin, k.kubeconfig, data) }

// NewKube returns the kubectl-backed client for cfg's cluster.
var NewKube = func(cfg *config.Config) Kube {
	bin, kc := kubectl.Paths(cfg)
	return kubectlKube{bin: bin, kubeconfig: kc}
}

// Get reads the live CR; (nil, nil) when it does not exist.
func Get(k Kube, ns, name string) (*Record, error) {
	raw, err := k.Output("get", resource, name, "-n", ns, "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found") {
			return nil, nil
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("decode AgentIdentity %s/%s: %w", ns, name, err)
	}
	return &rec, nil
}

// ApplySpec creates/updates the CR from rec's spec (status is a subresource
// and is never persisted by apply). Stamps the obol managed-by label.
func ApplySpec(k Kube, rec *Record) error {
	if rec == nil || rec.Metadata.Name == "" || rec.Metadata.Namespace == "" {
		return errors.New("agentidentity: namespace and name required")
	}
	spec := struct {
		APIVersion string                        `json:"apiVersion"`
		Kind       string                        `json:"kind"`
		Metadata   Metadata                      `json:"metadata"`
		Spec       monetizeapi.AgentIdentitySpec `json:"spec"`
	}{
		APIVersion: rec.APIVersion,
		Kind:       rec.Kind,
		Metadata: Metadata{
			Name:      rec.Metadata.Name,
			Namespace: rec.Metadata.Namespace,
			Labels:    map[string]string{kubectl.ManagedByLabel: kubectl.ManagedByObol},
		},
		Spec: rec.Spec,
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return k.Apply(data)
}

// PatchStatus merge-patches status.registrations. An empty slice is sent
// explicitly so a patch can clear the field.
func PatchStatus(k Kube, rec *Record) error {
	regs := rec.Status.Registrations
	if regs == nil {
		regs = []monetizeapi.AgentIdentityRegistration{}
	}
	patch, err := json.Marshal(map[string]any{"status": map[string]any{"registrations": regs}})
	if err != nil {
		return err
	}
	return k.Run("patch", resource, rec.Metadata.Name, "-n", rec.Metadata.Namespace,
		"--subresource=status", "--type=merge", "-p", string(patch))
}

// Outcome describes what Reconcile did for one record.
type Outcome string

const (
	OutcomeCreated   Outcome = "created"   // CR was missing; spec applied + status seeded
	OutcomeSeeded    Outcome = "seeded"    // CR existed without registrations; status seeded
	OutcomeRefreshed Outcome = "refreshed" // cluster had newer registrations; record updated
	OutcomeUnchanged Outcome = "unchanged"
)

// ReconcileOne converges one record with the cluster:
//   - CR missing: apply spec, seed status from the record.
//   - CR present without registrations: seed status from the record.
//   - CR present with registrations: the cluster wins (the controller may
//     have registered on another chain since the last CLI write) and the
//     record is refreshed from it.
func ReconcileOne(cfg *config.Config, k Kube, rec *Record) (Outcome, error) {
	ns, name := rec.Metadata.Namespace, rec.Metadata.Name
	live, err := Get(k, ns, name)
	if err != nil {
		return "", err
	}
	hasRecorded := monetizeapi.HasAgentIdentityRegistrations(rec.Status)
	switch {
	case live == nil:
		if err := ApplySpec(k, rec); err != nil {
			return "", fmt.Errorf("apply AgentIdentity %s/%s: %w", ns, name, err)
		}
		if hasRecorded {
			if err := PatchStatus(k, rec); err != nil {
				return "", fmt.Errorf("seed AgentIdentity %s/%s status: %w", ns, name, err)
			}
		}
		return OutcomeCreated, nil
	case !monetizeapi.HasAgentIdentityRegistrations(live.Status):
		if !hasRecorded {
			return OutcomeUnchanged, nil
		}
		if err := PatchStatus(k, rec); err != nil {
			return "", fmt.Errorf("seed AgentIdentity %s/%s status: %w", ns, name, err)
		}
		return OutcomeSeeded, nil
	default:
		if sameRegistrations(live.Status, rec.Status) {
			return OutcomeUnchanged, nil
		}
		if err := Save(cfg, live); err != nil {
			return "", fmt.Errorf("refresh identity record %s/%s: %w", ns, name, err)
		}
		return OutcomeRefreshed, nil
	}
}

// Mirror copies the live CR ns/name to its record. A missing CR leaves any
// existing record untouched (the record is what restores it).
func Mirror(cfg *config.Config, k Kube, ns, name string) error {
	live, err := Get(k, ns, name)
	if err != nil || live == nil {
		return err
	}
	return Save(cfg, live)
}

// MirrorAll records every AgentIdentity in the cluster. Used by `obol stack
// export` so the archive's config tree carries registrations the
// serviceoffer-controller wrote since the last CLI write. CRD absent or no
// CRs => nothing recorded, no error.
func MirrorAll(cfg *config.Config, k Kube) (int, error) {
	raw, err := k.Output("get", resource, "-A", "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "the server doesn't have a resource type") {
			return 0, nil
		}
		return 0, err
	}
	var list struct {
		Items []Record `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return 0, fmt.Errorf("decode AgentIdentity list: %w", err)
	}
	n := 0
	var errs []error
	for i := range list.Items {
		if err := Save(cfg, &list.Items[i]); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

func sameRegistrations(a, b monetizeapi.AgentIdentityStatus) bool {
	if len(a.Registrations) != len(b.Registrations) {
		return false
	}
	key := func(r monetizeapi.AgentIdentityRegistration) string { return r.Chain + "=" + r.AgentID }
	set := map[string]int{}
	for _, r := range a.Registrations {
		set[key(r)]++
	}
	for _, r := range b.Registrations {
		set[key(r)]--
	}
	for _, v := range set {
		if v != 0 {
			return false
		}
	}
	return true
}
