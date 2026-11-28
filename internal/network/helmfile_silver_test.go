package network

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// TestEthereumHelmfile_SilverCommand pins the shell-free Silver command:
// its images are distroless from alpha.21 (no sh), and the image's
// SILVER_BEACON_API_BIND=0.0.0.0:5051 beats the chart's config.toml, so only
// an explicit --beacon-api-bind keeps the API on the port the probes use.
func TestEthereumHelmfile_SilverCommand(t *testing.T) {
	for _, net := range []string{"hoodi", "sepolia", "mainnet"} {
		t.Run(net, func(t *testing.T) {
			id := net + "-node"
			state := renderEthereumHelmfile(t, map[string]any{
				"id": id, "network": net, "executionClient": "reth", "consensusClient": "silver", "mode": "full",
			})
			silver := clientValues(t, state, "silver")

			raw, ok := silver["customCommand"].([]any)
			if !ok || len(raw) == 0 {
				t.Fatalf("silver has no customCommand: %v", silver)
			}

			cmd := make([]string, len(raw))
			for i, a := range raw {
				cmd[i] = fmt.Sprint(a)
			}

			if cmd[0] != "silver" {
				t.Errorf("command starts with %q, want the silver binary", cmd[0])
			}

			for _, a := range cmd {
				if a == "sh" || a == "-ac" || a == "-c" || strings.Contains(a, "$") {
					t.Errorf("command needs a shell (%q): %v", a, cmd)
				}
			}

			httpPort, ok := silver["httpPort"].(int)
			if !ok || httpPort == 0 {
				t.Fatalf("silver httpPort = %v, want the beacon API port set explicitly", silver["httpPort"])
			}

			for _, want := range []string{
				"--config=/config/config.toml",
				"--jwt-secret=/data/jwt.hex",
				"--execution-endpoint=http://ethereum-execution:8551",
				"--network=" + net,
				fmt.Sprintf("--beacon-api-bind=0.0.0.0:%d", httpPort),
			} {
				if !slices.Contains(cmd, want) {
					t.Errorf("command lacks %q: %v", want, cmd)
				}
			}

			// The probes name the http-api container port, which the chart
			// sets from httpPort, i.e. the port bound above.
			for _, probe := range []string{"startupProbe", "livenessProbe", "readinessProbe"} {
				p, _ := silver[probe].(map[string]any)
				get, _ := p["httpGet"].(map[string]any)
				if get["port"] != "http-api" {
					t.Errorf("%s port = %v, want http-api", probe, get["port"])
				}
			}

			// alpha.23 refuses Host headers it does not know (403 "host
			// not allowed"); in-cluster callers use the service names.
			config, _ := silver["config"].(string)
			ns := "ethereum-" + id
			wantHosts := `beacon_api_allowed_hosts = ["ethereum-beacon", "` + "ethereum-beacon." + ns + `", "ethereum-beacon.` + ns + `.svc", "ethereum-beacon.` + ns + `.svc.cluster.local", "host.docker.internal"]`
			if !strings.Contains(config, wantHosts) {
				t.Errorf("silver config lacks the allowed hosts for %s:\n%s", id, config)
			}

			if strings.Contains(config, `"*"`) {
				t.Errorf("silver must not allow every Host:\n%s", config)
			}

			if !strings.Contains(config, "metrics.enabled = true") || !strings.Contains(config, `metrics.bind = "0.0.0.0:5054"`) {
				t.Errorf("silver metrics listener not enabled:\n%s", config)
			}

			if sm, _ := silver["serviceMonitor"].(map[string]any); sm["enabled"] != false {
				t.Errorf("silver serviceMonitor = %v; it scrapes the API port, which has no /metrics", sm)
			}

			// network status, expose and the metadata ConfigMap assume 5052.
			if httpPort != 5052 {
				t.Errorf("beacon API port = %d, want 5052", httpPort)
			}
		})
	}
}

// overrides.yaml is merged last into the ethereum release, only when sync
// says it exists.
func TestEthereumHelmfile_OverridesLayer(t *testing.T) {
	lastValues := func(state map[string]any) any {
		for _, r := range state["releases"].([]any) {
			rel := r.(map[string]any)
			if rel["name"] == "ethereum" {
				vals := rel["values"].([]any)
				return vals[len(vals)-1]
			}
		}

		t.Fatal("no ethereum release")

		return nil
	}

	base := map[string]any{"network": "hoodi", "executionClient": "reth", "consensusClient": "silver", "mode": "full"}
	if got := lastValues(renderEthereumHelmfile(t, base)); got == "overrides.yaml" {
		t.Fatal("overrides.yaml layered without hasOverrides")
	}

	base["hasOverrides"] = "true"
	if got := lastValues(renderEthereumHelmfile(t, base)); got != "overrides.yaml" {
		t.Fatalf("last ethereum values layer = %v, want overrides.yaml", got)
	}
}
