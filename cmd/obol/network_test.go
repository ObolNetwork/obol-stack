package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/ui"
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

func TestNetworkArchiveScopeDisplay(t *testing.T) {
	d := network.Deployment{
		Type: "ethereum", ID: "x", Network: "mainnet",
		ExecutionClient: "reth", ConsensusClient: "lighthouse", Mode: "archive",
		ArchiveScope: "archive since cancun (block 19,426,587)",
	}

	var out bytes.Buffer
	u := ui.NewForTest(&out, &out)
	printDeployments(u, []network.Deployment{d})
	out.WriteString(renderStatus(network.StatusReport{Nodes: []network.NodeStatus{
		network.Unprobed(d, "skipped"),
	}}, renderOptions{}))

	if got := strings.Count(out.String(), "archive since cancun (block 19,426,587)"); got != 2 {
		t.Fatalf("want the archive scope in list and status output, got %d:\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), "ethereum/x  ·  reth + lighthouse  ·  archive since cancun (block 19,426,587)  ·  mainnet") {
		t.Errorf("status header:\n%s", out.String())
	}

	// A stopped deployment says so in network list.
	out.Reset()
	d.Stopped = true
	printDeployments(u, []network.Deployment{d})
	if !strings.Contains(out.String(), "◼ stopped (obol network start ethereum/x)") {
		t.Errorf("list of a stopped node:\n%s", out.String())
	}

	// Deployments without a computed scope (non-ethereum) fall back to mode.
	if got := modeLabel(network.Deployment{Mode: "full"}); got != "full" {
		t.Errorf("modeLabel fallback = %q", got)
	}
}
