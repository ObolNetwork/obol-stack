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

func TestNetworkStatusFormatting(t *testing.T) {
	if got := erpcPodLine("erpc-7c9cb85d44-5wbwt   Running   true   0", false); got != "pod Running · ready" {
		t.Errorf("erpcPodLine = %q", got)
	}

	if got := erpcPodLine("erpc-7c9cb85d44-5wbwt   Running   false   3", true); got != "pod Running · not ready · 3 restarts (erpc-7c9cb85d44-5wbwt)" {
		t.Errorf("erpcPodLine verbose = %q", got)
	}

	if got := shortClientVersion("reth/v2.7.0-3d592ec/x86_64-unknown-linux-gnu"); got != "reth/v2.7.0-3d592ec" {
		t.Errorf("shortClientVersion = %q", got)
	}

	if got := shortClientVersion("Lighthouse/v8.0.0"); got != "Lighthouse/v8.0.0" {
		t.Errorf("shortClientVersion short = %q", got)
	}

	ready := network.PodStatus{Component: "execution", Ready: true}
	oom := network.PodStatus{Component: "beacon", Reason: "OOMKilled", Restarts: 9}
	pending := network.PodStatus{Component: "beacon", Phase: "Pending"}

	if podsNeedLine([]network.PodStatus{ready, oom}) {
		t.Error("podsNeedLine: OOMKilled pod is already reported as a warning")
	}

	if !podsNeedLine([]network.PodStatus{ready, pending}) {
		t.Error("podsNeedLine: a pending pod with no reason has no warning")
	}
}
