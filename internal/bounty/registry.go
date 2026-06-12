// Package bounty loads the embedded, versioned ServiceBounty task-type
// packages (internal/embed/bountytasks/<id>/task.yaml). A task type is a
// self-describing unit — param schema, eval method + tolerance, OBOL eval
// pricing, hardware-proof policy, and the A2UI report schema — discovered
// dynamically the same way networks are (internal/embed/networks). Adding a
// task type is dropping in a directory; the CRD and controller never change.
package bounty

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// Param is one knob in a task type's schema. It generates a CLI flag for
// `obol bounty post <type>` and is validated against spec.task.params.
type Param struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"` // string | int | enum
	Default     string   `yaml:"default"`
	Enum        []string `yaml:"enum"`
	Required    bool     `yaml:"required"` // missing/empty value rejects the bounty at admission
	Description string   `yaml:"description"`
}

// EvalPayment is the OBOL-denominated evaluator payment leg (separate from the
// reward — x402 cannot splice a fee out of the reward auth).
type EvalPayment struct {
	Asset        string `yaml:"asset"`
	PerEvaluator string `yaml:"perEvaluator"`
	FundedBy     string `yaml:"fundedBy"`
	Settle       string `yaml:"settle"`
}

// Ladder is the evaluator cold-start ladder (design doc §11.4): Shadow (free,
// randomly assigned, graded against the quorum median but never counted) →
// Probation (one reserved quorum seat at reduced pay, value-capped bounties
// only) → Full. Thresholds are per-task-type constants, not protocol globals.
type Ladder struct {
	// ShadowAgreements within tolerance of the quorum median promote a
	// shadow evaluator to Probation.
	ShadowAgreements int `yaml:"shadowAgreements"`

	// ProbationEvals without divergence promote a probationer to Full.
	ProbationEvals int `yaml:"probationEvals"`

	// ProbationValueCap is the reward (human units) above which no probation
	// seat is offered — high-value bounties get an all-Full quorum.
	ProbationValueCap string `yaml:"probationValueCap"`

	// RevealWindow is the commit→reveal duration; every commit closes before
	// any reveal opens (selective-revelation guard).
	RevealWindow string `yaml:"revealWindow"`

	// NonRevealPenalty grades a missing reveal; "outlier" treats it as a
	// worst-case divergence so silent abstention is never the cheap exit.
	NonRevealPenalty string `yaml:"nonRevealPenalty"`

	// DecayHalfLife is the reputation half-life: ladder weight earned by an
	// evaluator halves every window of inactivity past lastEvalAt.
	DecayHalfLife string `yaml:"decayHalfLife"`

	// EscalationWindow is the second-round commit→reveal duration when a
	// diverged quorum escalates to a fresh, larger panel.
	EscalationWindow string `yaml:"escalationWindow"`

	// EscalationEpsilon is the knife-edge band: when the quorum median lands
	// within epsilon score points of the pass threshold, the verdict
	// escalates to a fresh 2k+1 panel instead of settling. 0 means "unset"
	// and backfills to the default (5); use a NEGATIVE value to disable the
	// knife-edge trigger for a task package.
	EscalationEpsilon int `yaml:"escalationEpsilon"`
}

type Eval struct {
	DefaultK  int         `yaml:"defaultK"`
	Selection string      `yaml:"selection"`
	Payment   EvalPayment `yaml:"payment"`
	Ladder    Ladder      `yaml:"ladder"`
}

type Acceptance struct {
	Method       string            `yaml:"method"`
	CommitReveal bool              `yaml:"commitReveal"`
	Tolerance    map[string]string `yaml:"tolerance"`
}

type Artifact struct {
	Name     string `yaml:"name"`
	Kind     string `yaml:"kind"`
	Required bool   `yaml:"required"`
}

// ReportVariant is one A2UI rendering of the deliverable's result data.
// kind=declarative is the lean default: an operations file (create_surface →
// update_components → update_data_model) the client renders natively from its
// compiled-in catalog — no custom code, no iframes. kind=mcp-app is the
// MCP-Apps escape hatch: `surface` is self-contained HTML served url_encoded
// inside an A2UI `custom` McpApp node's properties.content; the CLIENT
// supplies the double-iframe isolation (sandbox proxy + srcdoc inner frame,
// never allow-same-origin) — the server only ever returns JSON.
type ReportVariant struct {
	Kind      string `yaml:"kind"`      // declarative | mcp-app
	Surface   string `yaml:"surface"`   // file in the task package
	CatalogID string `yaml:"catalogId"` // stable id negotiated against the client's supportedCatalogIds
}

// Report carries the variants in preference order. The serving side (FE
// locally, the stack MCP server cross-party) picks the first variant whose
// catalogId the client advertises (a2ui catalog negotiation, locked per
// surface); a client matching nothing falls back to the raw artifacts.
type Report struct {
	Variants []ReportVariant `yaml:"variants"`
}

type Deliverable struct {
	Report    Report     `yaml:"report"`
	Gate      string     `yaml:"gate"` // local | mcp-x402 | sign-in-with-x
	Artifacts []Artifact `yaml:"artifacts"`
}

// TaskType is a parsed task-type package.
type TaskType struct {
	ID            string      `yaml:"id"`
	Version       int         `yaml:"version"`
	Runner        string      `yaml:"runner"`
	Enabled       bool        `yaml:"enabled"`
	Summary       string      `yaml:"summary"`
	Requires      []string    `yaml:"requires"`
	Params        []Param     `yaml:"params"`
	Acceptance    Acceptance  `yaml:"acceptance"`
	Eval          Eval        `yaml:"eval"`
	HardwareProof string      `yaml:"hardwareProof"`
	Deliverable   Deliverable `yaml:"deliverable"`
}

// Ref is the portable, versioned reference written into
// ServiceBounty.spec.task.typeRef, e.g. "benchmark@v1".
func (t TaskType) Ref() string {
	return fmt.Sprintf("%s@v%d", t.ID, t.Version)
}

// Load reads and parses a single embedded task-type package by directory name.
func Load(name string) (TaskType, error) {
	raw, err := embed.ReadEmbeddedBountyTaskFile(name, "task.yaml")
	if err != nil {
		return TaskType{}, err
	}

	var t TaskType
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return TaskType{}, fmt.Errorf("parse task type %q: %w", name, err)
	}

	if t.ID == "" {
		return TaskType{}, fmt.Errorf("task type %q: missing id", name)
	}

	applyLadderDefaults(&t.Eval.Ladder)

	return t, nil
}

// applyLadderDefaults backfills ladder knobs a task package omits, so older
// packages keep working when the ladder grows a field.
func applyLadderDefaults(l *Ladder) {
	if l.DecayHalfLife == "" {
		l.DecayHalfLife = "720h"
	}
	if l.EscalationWindow == "" {
		l.EscalationWindow = "30m"
	}
	if l.EscalationEpsilon == 0 {
		l.EscalationEpsilon = 5
	}
}

// Available returns every embedded task type (enabled or not), sorted by id.
func Available() ([]TaskType, error) {
	names, err := embed.GetAvailableBountyTasks()
	if err != nil {
		return nil, err
	}

	tasks := make([]TaskType, 0, len(names))
	for _, name := range names {
		t, err := Load(name)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })

	return tasks, nil
}

// Enabled returns only the task types live in this release. Shipping a type
// with enabled:false stages it (e.g. finetune) before it is turned on.
func Enabled() ([]TaskType, error) {
	all, err := Available()
	if err != nil {
		return nil, err
	}

	enabled := make([]TaskType, 0, len(all))
	for _, t := range all {
		if t.Enabled {
			enabled = append(enabled, t)
		}
	}

	return enabled, nil
}

// Resolve resolves an `id` ("benchmark") or a versioned ref ("benchmark@v1")
// to its task type. It errors if the type is unknown or disabled.
func Resolve(ref string) (TaskType, error) {
	id := ref
	for i := 0; i < len(ref); i++ {
		if ref[i] == '@' {
			id = ref[:i]
			break
		}
	}

	all, err := Available()
	if err != nil {
		return TaskType{}, err
	}

	for _, t := range all {
		if t.ID == id {
			if !t.Enabled {
				return TaskType{}, fmt.Errorf("task type %q is not enabled in this release", id)
			}
			return t, nil
		}
	}

	return TaskType{}, fmt.Errorf("unknown task type %q", ref)
}
