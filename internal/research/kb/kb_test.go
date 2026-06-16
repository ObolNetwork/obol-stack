package kb

import (
	"math"
	"testing"
)

func f(v float64) *float64 { return &v }

func minimizeProg(split SplitMode, baseline *float64) Program {
	return Program{
		ID:       "nanogpt-valbpb",
		Criteria: Criteria{Metric: "val_bpb", Direction: Minimize, Accept: BeatsChampion},
		Baseline: baseline,
		Pool:     100,
		Split:    split,
	}
}

func thresholdProg(split SplitMode, threshold *float64) Program {
	return Program{
		ID:       "threshold-prog",
		Criteria: Criteria{Metric: "val_bpb", Direction: Minimize, Accept: Threshold, Threshold: threshold},
		Pool:     100,
		Split:    split,
	}
}

// TestPayouts_ThresholdResubmitCannotInflate is the H4 regression: under
// Threshold + ByImpact, resubmitting the same passing result must NOT inflate a
// worker's share. Each worker is credited their BEST accepted impact, not the
// sum of duplicates.
func TestPayouts_ThresholdResubmitCannotInflate(t *testing.T) {
	k := New(thresholdProg(ByImpact, f(1.20))) // minimize: value <= 1.20 passes

	a1, _ := k.Submit("spark1", 1.10, "") // spark1 clears it...
	a2, _ := k.Submit("spark1", 1.10, "") // ...and resubmits the SAME result.
	b1, _ := k.Submit("spark2", 1.10, "") // spark2 clears it once, equal impact.
	if !a1.Accepted || !a2.Accepted || !b1.Accepted {
		t.Fatalf("threshold submissions = %+v %+v %+v, want all accepted", a1, a2, b1)
	}

	pay := k.Payouts()
	if math.Abs(pay["spark1"]-50) > 1e-3 || math.Abs(pay["spark2"]-50) > 1e-3 {
		t.Fatalf("payouts = %+v, want 50/50 — resubmission must not inflate (was 66.67/33.33)", pay)
	}
}

func TestSubmit_BeatsChampion_Minimize(t *testing.T) {
	k := New(minimizeProg(ByImpact, f(1.20))) // baseline val_bpb 1.20

	// spark1 improves on baseline → accepted, champion, impact 1.20-1.10.
	r1, err := k.Submit("spark1", 1.10, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Accepted || !r1.Champion || math.Abs(r1.Impact-0.10) > 1e-9 {
		t.Fatalf("r1 = %+v, want accepted champion impact 0.10", r1)
	}

	// spark2 improves further → accepted, new champion, impact 1.10-1.05.
	r2, _ := k.Submit("spark2", 1.05, "")
	if !r2.Accepted || !r2.Champion || math.Abs(r2.Impact-0.05) > 1e-9 {
		t.Fatalf("r2 = %+v, want accepted champion impact 0.05", r2)
	}

	// spark1 submits a worse value → rejected, not champion, no impact.
	r3, _ := k.Submit("spark1", 1.30, "")
	if r3.Accepted || r3.Champion || r3.Impact != 0 {
		t.Fatalf("r3 = %+v, want rejected", r3)
	}

	if c := k.Champion(); c == nil || c.Worker != "spark2" || c.Value != 1.05 {
		t.Fatalf("champion = %+v, want spark2 @1.05", c)
	}

	// By-impact payout: spark1 0.10, spark2 0.05 → 2:1 of the 100 pool.
	pay := k.Payouts()
	if math.Abs(pay["spark1"]-66.666667) > 1e-3 || math.Abs(pay["spark2"]-33.333333) > 1e-3 {
		t.Fatalf("payouts = %+v, want ~66.67/33.33", pay)
	}
}

func TestSubmit_FirstVerifiedWins(t *testing.T) {
	k := New(minimizeProg(ByImpact, f(1.20)))
	// Two workers submit the SAME improvement; first one in wins the champion.
	a, _ := k.Submit("spark1", 1.10, "")
	b, _ := k.Submit("spark2", 1.10, "")
	if !a.Champion {
		t.Error("first identical submission must take the champion")
	}
	if b.Accepted || b.Champion {
		t.Errorf("second identical submission must not beat the champion: %+v", b)
	}
}

func TestSubmit_FirstResultNoBaselineSetsBaseline(t *testing.T) {
	k := New(minimizeProg(ByImpact, nil)) // no baseline
	r, _ := k.Submit("spark1", 1.10, "")
	if !r.Accepted || !r.Champion || r.Impact != 0 {
		t.Fatalf("first result w/o baseline = %+v, want champion impact 0", r)
	}
	// No positive impact yet → no payouts.
	if len(k.Payouts()) != 0 {
		t.Errorf("payouts before any improvement = %+v, want empty", k.Payouts())
	}
}

func TestSubmit_ThresholdMode(t *testing.T) {
	p := minimizeProg(ByImpact, nil)
	p.Criteria.Accept = Threshold
	p.Criteria.Threshold = f(1.00)
	k := New(p)

	miss, _ := k.Submit("spark1", 1.05, "") // above threshold → reject
	if miss.Accepted {
		t.Error("1.05 must miss threshold 1.00 (minimize)")
	}
	hit, _ := k.Submit("spark2", 0.90, "") // clears threshold → accept, impact 0.10
	if !hit.Accepted || math.Abs(hit.Impact-0.10) > 1e-9 {
		t.Fatalf("hit = %+v, want accepted impact 0.10", hit)
	}
}

func TestSubmit_MaximizeDirection(t *testing.T) {
	p := minimizeProg(ByImpact, f(0.80))
	p.Criteria.Direction = Maximize
	p.Criteria.Metric = "auc"
	k := New(p)
	r, _ := k.Submit("spark1", 0.90, "") // higher is better → +0.10
	if !r.Accepted || math.Abs(r.Impact-0.10) > 1e-9 {
		t.Fatalf("maximize r = %+v, want impact 0.10", r)
	}
	worse, _ := k.Submit("spark2", 0.85, "")
	if worse.Accepted {
		t.Error("0.85 < champion 0.90 must be rejected under maximize")
	}
}

func TestPayouts_ChampionTakesAll(t *testing.T) {
	k := New(minimizeProg(ChampionTakesAll, f(1.20)))
	k.Submit("spark1", 1.10, "")
	k.Submit("spark2", 1.05, "")
	pay := k.Payouts()
	if pay["spark2"] != 100 || len(pay) != 1 {
		t.Fatalf("champion-takes-all = %+v, want spark2:100", pay)
	}
}

func TestSubmit_RejectsNonFinite(t *testing.T) {
	k := New(minimizeProg(ByImpact, nil))
	if _, err := k.Submit("spark1", math.Inf(1), ""); err == nil {
		t.Error("Inf value must be rejected")
	}
	if _, err := k.Submit("", 1.0, ""); err == nil {
		t.Error("empty worker must be rejected")
	}
}
