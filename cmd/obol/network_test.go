package main

import (
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/network"
)

func TestSelectDeployments(t *testing.T) {
	all := []network.Deployment{
		{Type: "aztec", ID: "seq", Namespace: "aztec-seq"},
		{Type: "ethereum", ID: "hoodi", Namespace: "ethereum-hoodi"},
		{Type: "ethereum", ID: "mainnet", Namespace: "ethereum-mainnet"},
	}

	cases := map[string]string{
		"ethereum/hoodi":   "ethereum/hoodi",
		"ethereum-mainnet": "ethereum/mainnet",
		"aztec":            "aztec/seq",
	}
	for arg, want := range cases {
		got, err := selectDeployments(all, arg)
		if err != nil || len(got) != 1 || got[0].Identifier() != want {
			t.Errorf("selectDeployments(%q) = %v, %v; want %s", arg, got, err, want)
		}
	}

	if got, err := selectDeployments(all, ""); err != nil || len(got) != 3 {
		t.Errorf("empty arg: got %d, %v", len(got), err)
	}

	for _, arg := range []string{"ethereum", "ethereum/nope"} {
		if _, err := selectDeployments(all, arg); err == nil {
			t.Errorf("selectDeployments(%q) should be ambiguous/unknown", arg)
		}
	}

	if _, err := selectDeployments([]network.Deployment{}, "ethereum"); err == nil {
		t.Error("no deployments: want error")
	}
}
