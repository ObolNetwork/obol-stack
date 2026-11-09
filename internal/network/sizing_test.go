package network

import (
	"testing"
)

func TestEthereumSizing(t *testing.T) {
	cases := []struct {
		name                   string
		network, mode, el, cl  string
		elOverride, clOverride string
		wantEL, wantCL         MemorySizing
		wantErr                bool
	}{
		{"mainnet archive reth+silver", "mainnet", modeArchive, "reth", "silver", "", "", sz(6, 14), sz(4, 8), false},
		{"hoodi full reth+lighthouse", "hoodi", modeFull, "reth", "lighthouse", "", "", sz(1, 4), sz(2, 6), false},
		{"sepolia geth+teku", "sepolia", modeFull, "geth", "teku", "", "", sz(2, 6), sz(3, 6), false},
		{"unknown client falls back", "mainnet", modeFull, "ethrex", "newcl", "", "", defaultELSizing, defaultCLSizing, false},
		{"override raises the limit", "mainnet", modeFull, "reth", "lighthouse", "16Gi", "12Gi", sz(4, 16), sz(5, 12), false},
		{"override below the request caps it", "hoodi", modeFull, "geth", "lighthouse", "1536Mi", "", MemorySizing{1536, 1536}, sz(2, 6), false},
		{"bad override", "hoodi", modeFull, "reth", "lighthouse", "lots", "", MemorySizing{}, MemorySizing{}, true},
	}
	for _, c := range cases {
		el, cl, err := EthereumSizing(c.network, c.mode, c.el, c.cl, c.elOverride, c.clOverride)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err = %v, want error %v", c.name, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if el != c.wantEL || cl != c.wantCL {
			t.Errorf("%s: got EL %+v CL %+v, want EL %+v CL %+v", c.name, el, cl, c.wantEL, c.wantCL)
		}
	}
}

// Every table entry keeps the request at or below the limit, or the
// apiserver rejects the pod.
func TestSizingTablesRequestWithinLimit(t *testing.T) {
	for mainnet, modes := range elSizing {
		for archive, clients := range modes {
			for client, s := range clients {
				if s.RequestMi > s.LimitMi {
					t.Errorf("EL mainnet=%v archive=%v %s: request %d > limit %d", mainnet, archive, client, s.RequestMi, s.LimitMi)
				}
			}
		}
	}
	for network, clients := range clSizing {
		for client, s := range clients {
			if s.RequestMi > s.LimitMi {
				t.Errorf("CL %s %s: request %d > limit %d", network, client, s.RequestMi, s.LimitMi)
			}
		}
	}
}

// Every client the install flags offer has a row, so none silently gets
// the fallback.
func TestSizingTablesCoverClients(t *testing.T) {
	els := []string{"reth", "geth", "nethermind", "besu", "erigon"}
	cls := []string{"lighthouse", "prysm", "teku", "nimbus", "lodestar", "grandine", "silver"}
	for _, network := range []string{"mainnet", "hoodi", "sepolia"} {
		for _, el := range els {
			if _, ok := elSizing[network == "mainnet"][false][el]; !ok {
				t.Errorf("no EL sizing for %s on %s", el, network)
			}
		}
		for _, cl := range cls {
			if _, ok := clSizing[network][cl]; !ok {
				t.Errorf("no CL sizing for %s on %s", cl, network)
			}
		}
	}
}
