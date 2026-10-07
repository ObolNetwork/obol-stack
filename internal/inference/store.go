package inference

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
)

// ErrDeploymentNotFound is returned when a named inference deployment does
// not exist in the store.
var ErrDeploymentNotFound = errors.New("inference: deployment not found")

// ErrDeploymentExists is returned by Create when a deployment with the same
// name already exists and --force was not specified.
var ErrDeploymentExists = errors.New("inference: deployment already exists")

// Deployment is a named, persisted inference gateway configuration.
// Unknown JSON fields are ignored on load, so descriptors written by older
// releases (e.g. with the removed "enclave_tag" or "vm_*" fields) still load.
type Deployment struct {
	// Name is the human-readable identifier for this deployment.
	// Used as the directory name.
	Name string `json:"name"`

	// ListenAddr is the gateway listen address (default ":8402").
	ListenAddr string `json:"listen_addr"`

	// UpstreamURL is the inference backend URL (default "http://localhost:11434").
	UpstreamURL string `json:"upstream_url"`

	// WalletAddress is the USDC payment recipient.
	WalletAddress string `json:"wallet_address"`

	// PricePerRequest is the per-inference price denominated in AssetSymbol
	// units (default "0.001" USDC).
	PricePerRequest string `json:"price_per_request"`

	// AssetSymbol is the token symbol charged per request (default "USDC").
	// Used for human-readable log output and to label persisted state.
	AssetSymbol string `json:"asset_symbol,omitempty"`

	// PricePerMTok is the original per-million-token price when request pricing
	// was derived from the temporary phase-1 approximation.
	PricePerMTok string `json:"price_per_mtok,omitempty"`

	// ApproxTokensPerRequest records the fixed approximation used to derive the
	// charged request price from PricePerMTok.
	ApproxTokensPerRequest int `json:"approx_tokens_per_request,omitempty"`

	// Chain is the x402 payment chain name (e.g. "base", "base-sepolia").
	Chain string `json:"chain"`

	// FacilitatorURL is the x402 facilitator URL.
	FacilitatorURL string `json:"facilitator_url"`

	// LegacyVMMode and LegacyTEEType are read-only remnants of the removed
	// `--vm` (Apple Containerization) and `--tee` (TDX/SNP/Nitro attestation)
	// modes. The CLI never sets them; they are decoded only so resume can
	// refuse to silently downgrade such an offer to a plain gateway. See
	// RemovedIsolationMode.
	LegacyVMMode  bool   `json:"vm_mode,omitempty"`
	LegacyTEEType string `json:"tee_type,omitempty"`

	// NoPaymentGate disables the built-in x402 payment middleware when the
	// gateway runs behind the cluster's x402 verifier to avoid double-gating.
	NoPaymentGate bool `json:"no_payment_gate,omitempty"`

	// Provenance holds optional metadata about how the model was produced
	// (e.g. training or benchmark results). Stored alongside the deployment
	// config and passed to the registration document when selling.
	Provenance *Provenance `json:"provenance,omitempty"`

	// ModelName is the upstream model identifier the operator selected via
	// `obol sell inference --model <name>` (e.g. "aeon-ultimate"). Persisted
	// so `obol stack up` can rebuild the ServiceOffer's spec.model.name on
	// resume without re-prompting the operator.
	ModelName string `json:"model_name,omitempty"`

	// ServiceNamespace is the Kubernetes namespace the cluster-routed Service
	// + Endpoints + ServiceOffer live in (typically "llm" for inference
	// deployments co-located with LiteLLM). Persisted so the resume path
	// re-applies into the same namespace.
	ServiceNamespace string `json:"service_namespace,omitempty"`

	// Registration is the ERC-8004 registration block the operator passed at
	// sell time (--register-*). Persisted as map[string]any (the same shape
	// buildSellRegistrationConfig emits) so the resume path can rebuild the
	// ServiceOffer's spec.registration verbatim. Empty when the offer was
	// created with --no-register.
	Registration map[string]any `json:"registration,omitempty"`

	// CreatedAt is the RFC3339 timestamp of when this deployment was created.
	CreatedAt string `json:"created_at"`

	// UpdatedAt is the RFC3339 timestamp of the most recent update.
	UpdatedAt string `json:"updated_at,omitempty"`

	// DeletedAt is set when `obol sell delete` removes the offer from
	// the cluster. The descriptor stays on disk for `sell inference
	// list/status` history, but resume must not replay a tombstoned
	// offer (it would re-create the ServiceOffer and relaunch the host
	// gateway, undoing the delete). Re-creating the offer with
	// `obol sell inference <name>` rewrites the descriptor and clears
	// the tombstone.
	DeletedAt string `json:"deleted_at,omitempty"`
}

// Provenance tracks how a model or service was produced.
// JSON field names use camelCase so the same document can flow through
// publish.py -> --provenance-file -> ServiceOffer -> agent-registration.json.
type Provenance struct {
	Framework    string `json:"framework,omitempty"`    // e.g. "autoresearch"
	MetricName   string `json:"metricName,omitempty"`   // e.g. "val_bpb"
	MetricValue  string `json:"metricValue,omitempty"`  // e.g. "0.9973"
	ExperimentID string `json:"experimentId,omitempty"` // commit hash or UUID
	TrainHash    string `json:"trainHash,omitempty"`    // e.g. "sha256:..."
	ParamCount   string `json:"paramCount,omitempty"`   // e.g. "50000000"
}

// RemovedIsolationMode reports which removed isolation mode (if any) the
// persisted descriptor was created with: "vm", "tee (<backend>)", or "" for a
// plain gateway. Callers that replay descriptors use it to skip offers whose
// operator explicitly asked for isolation the CLI can no longer provide.
func (d *Deployment) RemovedIsolationMode() string {
	switch {
	case d.LegacyTEEType != "":
		return "tee (" + d.LegacyTEEType + ")"
	case d.LegacyVMMode:
		return "vm"
	}

	return ""
}

// validDeploymentName matches safe deployment names: alphanumeric, hyphens,
// underscores, 1-63 chars. No path separators, dots, or shell metacharacters.
var validDeploymentName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

// ValidateName checks that name is a safe deployment identifier.
// It rejects empty strings, path traversal attempts, and shell metacharacters.
func ValidateName(name string) error {
	if !validDeploymentName.MatchString(name) {
		return fmt.Errorf("invalid deployment name %q: must be 1-63 alphanumeric chars, hyphens, or underscores", name)
	}

	return nil
}

// Store manages named inference deployment configurations on disk.
// Layout: <configDir>/inference/<name>/config.json
type Store struct {
	root string // configDir/inference
}

// NewStore returns a Store rooted at configDir.
func NewStore(configDir string) *Store {
	return &Store{root: filepath.Join(configDir, "inference")}
}

// dir returns the directory path for a named deployment.
func (s *Store) dir(name string) string {
	return filepath.Join(s.root, name)
}

// configPath returns the config.json path for a named deployment.
func (s *Store) configPath(name string) string {
	return filepath.Join(s.dir(name), "config.json")
}

// Create persists a new Deployment.  Returns ErrDeploymentExists if a
// deployment with that name is already stored and force is false.
func (s *Store) Create(d *Deployment, force bool) error {
	if err := ValidateName(d.Name); err != nil {
		return err
	}

	if _, err := os.Stat(s.configPath(d.Name)); err == nil && !force {
		return fmt.Errorf("%w: %s", ErrDeploymentExists, d.Name)
	}

	// Apply defaults.
	if d.ListenAddr == "" {
		d.ListenAddr = ":8402"
	}

	if d.UpstreamURL == "" {
		d.UpstreamURL = "http://localhost:11434"
	}

	if d.PricePerRequest == "" {
		d.PricePerRequest = "0.001"
	}

	if d.Chain == "" {
		d.Chain = "base"
	}

	if d.FacilitatorURL == "" {
		d.FacilitatorURL = x402verifier.DefaultFacilitatorURL
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}

	d.UpdatedAt = now

	if err := os.MkdirAll(s.dir(d.Name), 0o700); err != nil {
		return fmt.Errorf("inference store: mkdir: %w", err)
	}

	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("inference store: marshal: %w", err)
	}

	if err := os.WriteFile(s.configPath(d.Name), data, 0o600); err != nil {
		return fmt.Errorf("inference store: write: %w", err)
	}

	return nil
}

// Get loads a Deployment by name.  Returns ErrDeploymentNotFound if missing.
func (s *Store) Get(name string) (*Deployment, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(s.configPath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrDeploymentNotFound, name)
		}

		return nil, fmt.Errorf("inference store: read %s: %w", name, err)
	}

	var d Deployment
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("inference store: parse %s: %w", name, err)
	}

	return &d, nil
}

// List returns all deployment names in alphabetical order.
func (s *Store) List() ([]*Deployment, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // empty — not an error
		}

		return nil, fmt.Errorf("inference store: list: %w", err)
	}

	var deployments []*Deployment

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		d, err := s.Get(e.Name())
		if err != nil {
			continue // skip malformed entries
		}

		deployments = append(deployments, d)
	}

	return deployments, nil
}

// Delete removes a deployment's config directory from disk.
func (s *Store) Delete(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}

	if _, err := s.Get(name); err != nil {
		return err
	}

	if err := os.RemoveAll(s.dir(name)); err != nil {
		return fmt.Errorf("inference store: delete %s: %w", name, err)
	}

	return nil
}

// Update persists changes to an existing Deployment.
func (s *Store) Update(d *Deployment) error {
	if _, err := s.Get(d.Name); err != nil {
		return err
	}

	d.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("inference store: marshal: %w", err)
	}

	return os.WriteFile(s.configPath(d.Name), data, 0o600)
}
