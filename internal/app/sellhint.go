package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// maxSellHints caps how many per-service sell suggestions are printed
// after a sync; charts with many Services (umbrella charts) would
// otherwise drown the success output.
const maxSellHints = 3

// unsellableChartNames are charts whose Services must never be put behind
// a public x402 route: distributed-validator and signer workloads.
var unsellableChartNames = map[string]bool{
	"dv-pod":         true,
	"charon":         true,
	"charon-cluster": true,
	"charon-relay":   true,
	"remote-signer":  true,
	"web3signer":     true,
}

// unsellableNameTokens match chart or Service names of validator/signer
// workloads not listed above.
var unsellableNameTokens = []string{"validator", "signer", "charon", "dv-pod", "keystore"}

// unsellablePorts are well-known ports of validator, signer and node
// internals. A Service exposing any of them is never suggested.
var unsellablePorts = map[int]string{
	3600:  "charon validator API (lets a caller make the DV sign)",
	3610:  "charon p2p",
	3620:  "charon monitoring",
	3630:  "charon reserved port",
	3640:  "charon relay HTTP",
	5051:  "validator client / beacon API",
	5052:  "beacon node API",
	5054:  "beacon node metrics",
	5062:  "validator client HTTP API",
	7500:  "validator client API",
	8545:  "execution JSON-RPC (sell RPC through obol network/eRPC instead)",
	8546:  "execution JSON-RPC websocket",
	8551:  "execution engine API",
	9000:  "remote signer / consensus p2p",
	9001:  "consensus p2p",
	30303: "execution p2p",
}

// unsellableChart reports whether a chart name is a validator or signer
// workload.
func unsellableChart(chartName string) bool {
	name := strings.ToLower(chartName)
	if unsellableChartNames[name] {
		return true
	}

	for _, tok := range unsellableNameTokens {
		if strings.Contains(name, tok) {
			return true
		}
	}

	return false
}

// sellableService decides whether to suggest selling a Service; when not,
// it returns the reason.
func sellableService(name string, ports []int) (bool, string) {
	lower := strings.ToLower(name)
	for _, tok := range unsellableNameTokens {
		if strings.Contains(lower, tok) {
			return false, "validator/signer service"
		}
	}

	for _, p := range ports {
		if why, ok := unsellablePorts[p]; ok {
			return false, fmt.Sprintf("port %d: %s", p, why)
		}
	}

	return true, ""
}

// PrintSellHint discovers the Services a just-synced app exposes and
// prints a ready-to-run `obol sell http` command for each, connecting app
// installs to the monetization flow. Best-effort: any discovery failure
// (no kubeconfig, kubectl missing, no Services yet) prints nothing.
//
// Distributed-validator and signer workloads (dv-pod, charon, remote
// signers) never get a hint, nor does any Service exposing a validator,
// signer or node-internal port: selling charon's validator API (3600)
// would let anyone get the DV to sign.
func PrintSellHint(cfg *config.Config, u *ui.UI, deploymentIdentifier string) {
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return
	}

	meta, err := loadAppMeta(deploymentDirFor(cfg, appName, id), appName, id)
	if err != nil {
		return
	}

	if unsellableChart(meta.ChartName) || unsellableChart(appName) {
		return
	}

	out, err := kubectl(cfg, nil, "get", "services", "-n", meta.Namespace,
		"-o", `jsonpath={range .items[*]}{.metadata.name}{" "}{range .spec.ports[*]}{.port}{","}{end}{"\n"}{end}`)
	if err != nil {
		return
	}

	printSellHints(u, meta.Namespace, string(out))
}

// printSellHints prints hints for `kubectl get services` jsonpath output
// ("<name> <port>,<port>,\n" per Service).
func printSellHints(u *ui.UI, namespace, out string) {
	printed := 0

	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		name, portList, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || name == "" {
			continue
		}

		var ports []int

		for p := range strings.SplitSeq(portList, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				ports = append(ports, n)
			}
		}

		if len(ports) == 0 {
			continue
		}

		if ok, _ := sellableService(name, ports); !ok {
			continue
		}

		if printed == 0 {
			u.Blank()
			u.Print("To put this app on sale behind x402 payments:")
		}

		if printed == maxSellHints {
			u.Printf("  (more services: obol kubectl get svc -n %s)", namespace)
			break
		}

		u.Printf("  obol sell http %s --upstream %s --port %d --namespace %s --per-request 0.001 --pay-to 0x<wallet>",
			name, name, ports[0], namespace)

		printed++
	}
}
