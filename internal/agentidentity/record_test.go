package agentidentity

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
)

func regs(pairs ...string) monetizeapi.AgentIdentityStatus {
	var st monetizeapi.AgentIdentityStatus
	for i := 0; i+1 < len(pairs); i += 2 {
		st = monetizeapi.UpsertAgentIdentityRegistration(st, pairs[i], pairs[i+1])
	}
	return st
}

func TestRecord_RoundTrip(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if rec, err := Load(cfg, "x402", "default"); err != nil || rec != nil {
		t.Fatalf("missing record = (%v, %v)", rec, err)
	}
	in := New("x402", "default")
	in.Metadata.Labels = map[string]string{"server": "side"} // not persisted
	in.Status = regs("base", "42", "base-sepolia", "7")
	if err := Save(cfg, in); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(Path(cfg, "x402", "default"), "identity/x402__default.json") {
		t.Errorf("unexpected path %s", Path(cfg, "x402", "default"))
	}
	if info, err := os.Stat(Path(cfg, "x402", "default")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record perm: %v %v", info, err)
	}
	out, err := Load(cfg, "x402", "default")
	if err != nil {
		t.Fatal(err)
	}
	if out.Metadata.Labels != nil {
		t.Error("record must not persist live labels")
	}
	if !reflect.DeepEqual(out.Status, in.Status) || out.Kind != monetizeapi.AgentIdentityKind {
		t.Fatalf("round trip = %+v", out)
	}
	all, err := List(cfg)
	if err != nil || len(all) != 1 {
		t.Fatalf("List = %v, %v", all, err)
	}
}

type fakeKube struct {
	live    *Record
	applied []string
	patched []string
}

func (f *fakeKube) Output(args ...string) (string, error) {
	if f.live == nil {
		return "", errors.New(`Error from server (NotFound): agentidentities.obol.org "default" not found`)
	}
	data, _ := json.Marshal(f.live)
	return string(data), nil
}

func (f *fakeKube) Run(args ...string) error {
	f.patched = append(f.patched, args[len(args)-1])
	var p struct {
		Status monetizeapi.AgentIdentityStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(args[len(args)-1]), &p); err != nil {
		return err
	}
	f.live.Status = p.Status
	return nil
}

func (f *fakeKube) Apply(data []byte) error {
	f.applied = append(f.applied, string(data))
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return err
	}
	rec.Status = monetizeapi.AgentIdentityStatus{} // status is a subresource
	if f.live == nil {
		f.live = &rec
	}
	return nil
}

func TestReconcileOne(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	rec := New("x402", "default")
	rec.Status = regs("base", "42")

	// Fresh cluster: create + seed status.
	k := &fakeKube{}
	got, err := ReconcileOne(cfg, k, rec)
	if err != nil || got != OutcomeCreated {
		t.Fatalf("fresh = %v, %v", got, err)
	}
	if !strings.Contains(k.applied[0], `"obol.org/managed-by":"obol"`) {
		t.Errorf("applied spec missing managed-by label: %s", k.applied[0])
	}
	if monetizeapi.AgentIdentityAgentIDForChain(k.live.Status, "base") != "42" {
		t.Fatalf("status not seeded: %+v", k.live.Status)
	}

	// Second pass: idempotent.
	k.applied, k.patched = nil, nil
	if got, err := ReconcileOne(cfg, k, rec); err != nil || got != OutcomeUnchanged || len(k.applied)+len(k.patched) != 0 {
		t.Fatalf("second pass = %v, %v (applied=%d patched=%d)", got, err, len(k.applied), len(k.patched))
	}

	// CR exists but lost its status: re-seed.
	k.live.Status = monetizeapi.AgentIdentityStatus{}
	if got, err := ReconcileOne(cfg, k, rec); err != nil || got != OutcomeSeeded {
		t.Fatalf("empty status = %v, %v", got, err)
	}

	// Controller registered on another chain: cluster wins, record refreshed.
	k.live.Status = regs("base", "42", "base-sepolia", "9")
	if got, err := ReconcileOne(cfg, k, rec); err != nil || got != OutcomeRefreshed {
		t.Fatalf("newer cluster = %v, %v", got, err)
	}
	saved, _ := Load(cfg, "x402", "default")
	if monetizeapi.AgentIdentityAgentIDForChain(saved.Status, "base-sepolia") != "9" {
		t.Fatalf("record not refreshed from cluster: %+v", saved.Status)
	}
}
