package dataset

import (
	"reflect"
	"sort"
	"testing"
)

func TestEntitlements_Allows(t *testing.T) {
	e := NewEntitlements()
	e.Grant(Entitlement{TokenHash: "v2tok", GroupID: "g", MaxVersion: 2})
	e.Grant(Entitlement{TokenHash: "headtok", GroupID: "g", MaxVersion: 5})

	tests := []struct {
		name      string
		tokenHash string
		version   int
		want      bool
	}{
		{"paid v2 can fetch v1", "v2tok", 1, true},
		{"paid v2 can fetch v2", "v2tok", 2, true},
		{"paid v2 CANNOT fetch v3", "v2tok", 3, false},
		{"worker head can fetch v5", "headtok", 5, true},
		{"unknown token denied", "ghost", 1, false},
		{"version zero denied", "headtok", 0, false},
		{"negative version denied", "headtok", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.Allows(tt.tokenHash, tt.version); got != tt.want {
				t.Errorf("Allows(%q, %d) = %v, want %v", tt.tokenHash, tt.version, got, tt.want)
			}
		})
	}
}

func TestEntitlements_LoadAllRoundTrip(t *testing.T) {
	e := NewEntitlements()
	in := []Entitlement{
		{TokenHash: "a", GroupID: "g", MaxVersion: 1, PaidAtomic: "1000"},
		{TokenHash: "b", GroupID: "g", MaxVersion: 3},
	}
	e.Load(in)

	if ent, ok := e.Lookup("a"); !ok || ent.PaidAtomic != "1000" {
		t.Errorf("Lookup(a) = %+v, %v", ent, ok)
	}
	got := e.All()
	sort.Slice(got, func(i, j int) bool { return got[i].TokenHash < got[j].TokenHash })
	if !reflect.DeepEqual(got, in) {
		t.Errorf("All() = %+v, want %+v", got, in)
	}
}

func TestEntitlements_GrantOverwrites(t *testing.T) {
	e := NewEntitlements()
	e.Grant(Entitlement{TokenHash: "a", MaxVersion: 1})
	e.Grant(Entitlement{TokenHash: "a", MaxVersion: 9}) // top-up to a newer version
	if !e.Allows("a", 9) {
		t.Error("Grant did not overwrite MaxVersion")
	}
}
