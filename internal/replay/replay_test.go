package replay

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func indexOf(t *testing.T, name string) int {
	t.Helper()
	for i, n := range Order {
		if n == name {
			return i
		}
	}
	t.Fatalf("step %q missing from replay Order", name)
	return -1
}

// TestOrder_Dependencies pins the ordering constraints documented on Order.
// Each pair is (must run first, must run later).
func TestOrder_Dependencies(t *testing.T) {
	before := [][2]string{
		{StepNetworks, StepRPCUpstreams},     // local upstreams before remote merge
		{StepRPCUpstreams, StepERPCOverlay},  // overlay merges onto recorded remotes (#763)
		{StepNetworks, StepERPCOverlay},      // ...and onto local upstreams
		{StepModels, StepAgentInstances},     // agents render model_list head
		{StepModels, StepAgentCRs},           //
		{StepX402Pricing, StepSellOffers},    // verifier needs payTo before offers
		{StepAgentIdentity, StepSellOffers},  // controller would re-mint without it
		{StepAgentCRs, StepSellOffers},       // agent-backed offers resolve agent.ref
		{StepStorefront, StepSellOffers},     // first catalog rebuild carries branding
		{StepApps, StepSellOffers},           // http offers gate app upstreams
		{StepAgentInstances, StepSellOffers}, //
		{StepERPCOverlay, StepSellOffers},    //
	}
	for _, p := range before {
		if indexOf(t, p[0]) >= indexOf(t, p[1]) {
			t.Errorf("%s must run before %s", p[0], p[1])
		}
	}
	if Order[len(Order)-1] != StepSellOffers {
		t.Errorf("sell-offers must be the last step, got %v", Order)
	}
	seen := map[string]bool{}
	for _, n := range Order {
		if seen[n] {
			t.Errorf("duplicate step %s", n)
		}
		seen[n] = true
	}
}

func TestSteps_FollowOrderAndAreComplete(t *testing.T) {
	steps := Steps(Options{})
	var names []string
	for _, s := range steps {
		if s.Run == nil {
			t.Errorf("step %s has no implementation", s.Name)
		}
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, Order) {
		t.Fatalf("Steps() = %v, want Order %v", names, Order)
	}
}

func TestSellOffersStep_CallsInjectedResume(t *testing.T) {
	called := false
	steps := Steps(Options{ResumeSellOffers: func(context.Context, *config.Config, *ui.UI) error {
		called = true
		return nil
	}})
	last := steps[len(steps)-1]
	if err := last.Run(context.Background(), &config.Config{}, ui.New(false)); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("sell-offers step must delegate to Options.ResumeSellOffers")
	}
}

func TestRun_BestEffortSummary(t *testing.T) {
	var ran []string
	step := func(name string, fn func(u *ui.UI) error) Step {
		return Step{Name: name, Run: func(_ context.Context, _ *config.Config, u *ui.UI) error {
			ran = append(ran, name)
			return fn(u)
		}}
	}
	boom := errors.New("boom")
	steps := []Step{
		step("a", func(*ui.UI) error { return nil }),
		step("b", func(*ui.UI) error { return boom }),
		step("c", func(u *ui.UI) error { u.Warn("internal warning"); return nil }),
		step("d", func(*ui.UI) error { return nil }),
	}
	sum := run(context.Background(), &config.Config{}, ui.New(false), steps)

	if !reflect.DeepEqual(ran, []string{"a", "b", "c", "d"}) {
		t.Fatalf("a failing step must not stop the replay; ran %v", ran)
	}
	if !reflect.DeepEqual(sum.OK, []string{"a", "d"}) {
		t.Errorf("OK = %v", sum.OK)
	}
	if !reflect.DeepEqual(sum.Warned, []string{"b", "c"}) {
		t.Errorf("Warned = %v (a step that only printed a warning counts as warned)", sum.Warned)
	}
	if !errors.Is(sum.Errors["b"], boom) || sum.Errors["c"] != nil {
		t.Errorf("Errors = %v", sum.Errors)
	}
}
