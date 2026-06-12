package bounty

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

func TestEnabled_IncludesBenchmark(t *testing.T) {
	types, err := Enabled()
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}

	var bench *TaskType
	for i := range types {
		if types[i].ID == "benchmark" {
			bench = &types[i]
			break
		}
	}
	if bench == nil {
		t.Fatalf("benchmark task type not enabled; got %d types", len(types))
	}

	if got := bench.Ref(); got != "benchmark@v1" {
		t.Errorf("Ref() = %q, want benchmark@v1", got)
	}
	if bench.Acceptance.Method != "rerun-tolerance" {
		t.Errorf("acceptance.method = %q, want rerun-tolerance (benchmarks are not bit-exact)", bench.Acceptance.Method)
	}
	if bench.Eval.Payment.Asset != "OBOL" {
		t.Errorf("eval paid in %q, want OBOL (separate eval leg)", bench.Eval.Payment.Asset)
	}
	if bench.Eval.Payment.Settle != "batch-settlement" {
		t.Errorf("eval settle = %q, want batch-settlement", bench.Eval.Payment.Settle)
	}
	if len(bench.Params) == 0 {
		t.Error("benchmark has no params; CLI flags would be empty")
	}

	// Median-of-k quorum: k must be >=3 whenever a probation seat can be
	// occupied — the median absorbing one outlier is what makes the newcomer
	// seat verdict-safe (design doc §11.4).
	if bench.Eval.DefaultK < 3 {
		t.Errorf("eval.defaultK = %d, want >=3 (median-of-k with a probation seat)", bench.Eval.DefaultK)
	}

	// Ladder thresholds are per-type constants; zero values would make the
	// cold-start ladder unclimbable (no promotions) or the reveal window
	// degenerate (no selective-revelation guard).
	ladder := bench.Eval.Ladder
	if ladder.ShadowAgreements <= 0 {
		t.Errorf("ladder.shadowAgreements = %d, want >0", ladder.ShadowAgreements)
	}
	if ladder.ProbationEvals <= 0 {
		t.Errorf("ladder.probationEvals = %d, want >0", ladder.ProbationEvals)
	}
	if ladder.ProbationValueCap == "" {
		t.Error("ladder.probationValueCap is empty; probation seats would be unbounded by value")
	}
	if ladder.RevealWindow == "" {
		t.Error("ladder.revealWindow is empty; commits and reveals would not be separated")
	}
	if ladder.NonRevealPenalty != "outlier" {
		t.Errorf("ladder.nonRevealPenalty = %q, want outlier (non-reveal must cost >= divergence)", ladder.NonRevealPenalty)
	}

	// Report variants drive a2ui catalog negotiation: the first variant whose
	// catalogId the client advertises wins. The lean default is declarative;
	// the mcp-app variant is what generic MCP-Apps hosts render (the server
	// only serves JSON — double-iframe isolation is the client's job).
	variants := bench.Deliverable.Report.Variants
	if len(variants) < 2 {
		t.Fatalf("report has %d variants, want >=2 (declarative + mcp-app)", len(variants))
	}
	if variants[0].Kind != "declarative" {
		t.Errorf("first variant kind = %q, want declarative (the lean default must win negotiation)", variants[0].Kind)
	}
	hasMCPApp := false
	for _, v := range variants {
		if v.Kind == "mcp-app" {
			hasMCPApp = true
		}
		if v.CatalogID == "" {
			t.Errorf("variant %s/%s has empty catalogId; negotiation would never select it", v.Kind, v.Surface)
		}
		if _, err := embed.ReadEmbeddedBountyTaskFile("benchmark", v.Surface); err != nil {
			t.Errorf("variant surface %q is not in the embedded package: %v", v.Surface, err)
		}
	}
	if !hasMCPApp {
		t.Error("no mcp-app variant; generic MCP-Apps clients would have no rendering")
	}
}

func TestResolve(t *testing.T) {
	for _, ref := range []string{"benchmark", "benchmark@v1"} {
		got, err := Resolve(ref)
		if err != nil {
			t.Errorf("Resolve(%q): %v", ref, err)
			continue
		}
		if got.ID != "benchmark" {
			t.Errorf("Resolve(%q).ID = %q", ref, got.ID)
		}
	}

	if _, err := Resolve("does-not-exist"); err == nil {
		t.Error("Resolve(unknown) should error")
	}
}

// benchlocal@v1 wraps third-party BenchLocal packs — pack code IS the scorer
// and the BenchLocal registry has no checksums, so packCommit MUST be a
// required param: without a byte pin, rerun-tolerance verification is theater.
func TestEnabled_BenchlocalRequiresPackCommit(t *testing.T) {
	bl, err := Resolve("benchlocal@v1")
	if err != nil {
		t.Fatalf("Resolve(benchlocal@v1): %v", err)
	}

	required := map[string]bool{}
	for _, p := range bl.Params {
		if p.Required {
			required[p.Name] = true
		}
	}
	for _, name := range []string{"pack", "packVersion", "packCommit"} {
		if !required[name] {
			t.Errorf("param %s must be required (pack bytes are unpinned without it)", name)
		}
	}
	if _, ok := bl.Acceptance.Tolerance["totalScore"]; !ok {
		t.Error("benchlocal tolerance must key on totalScore (the BenchmarkScore primary metric)")
	}
}

// finetune@v1 ships staged: present in Available (schema reviewable), absent
// from Enabled (not postable), refused by Resolve (not claimable/admittable).
// This is the registry's whole staging mechanism — pin it.
func TestStaging_FinetuneShippedButDisabled(t *testing.T) {
	all, err := Available()
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	var staged *TaskType
	for i := range all {
		if all[i].ID == "finetune" {
			staged = &all[i]
		}
	}
	if staged == nil {
		t.Fatal("finetune package missing from Available — staging mechanism has nothing staged")
	}
	if staged.Enabled {
		t.Fatal("finetune must ship enabled:false until the MLX-LoRA runner + held-out re-eval land")
	}

	enabled, err := Enabled()
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	for _, e := range enabled {
		if e.ID == "finetune" {
			t.Error("Enabled() must exclude disabled packages")
		}
	}

	if _, err := Resolve("finetune"); err == nil {
		t.Error("Resolve(finetune) must refuse disabled types at admission")
	}
}
