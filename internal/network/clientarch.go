package network

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
)

// amd64OnlyClients publish linux/amd64 images only. On a node of any other
// architecture their pod never starts.
var amd64OnlyClients = map[string]bool{"silver": true}

// nodeArchsFn returns the kubernetes.io/arch label of every cluster node.
// Swappable for tests.
var nodeArchsFn = func(cfg *config.Config) ([]string, error) {
	bin, kubeconfig := kubectl.Paths(cfg)
	if _, err := os.Stat(kubeconfig); err != nil {
		return nil, err
	}

	out, err := kubectl.Output(bin, kubeconfig, "get", "nodes", "--request-timeout=5s",
		"-o", `jsonpath={.items[*].metadata.labels.kubernetes\.io/arch}`)
	if err != nil {
		return nil, err
	}

	return strings.Fields(out), nil
}

// targetArch is the architecture the node pods will run on: the cluster
// nodes' when the cluster answers, else this machine's (k3d nodes run the
// Docker host's architecture). If nodes differ, a non-amd64 one wins, since
// nothing pins the beacon pod to an amd64 node.
func targetArch(cfg *config.Config) string {
	archs, err := nodeArchsFn(cfg)
	if err != nil || len(archs) == 0 {
		return runtime.GOARCH
	}

	for _, a := range archs {
		if a != "amd64" {
			return a
		}
	}

	return "amd64"
}

func checkClientArch(client, arch string) error {
	if !amd64OnlyClients[client] || arch == "amd64" {
		return nil
	}

	return fmt.Errorf("consensus client %s publishes linux/amd64 images only, and the stack runs on %s.\n"+
		"Choose another --consensus-client: lighthouse, prysm, teku, nimbus, lodestar or grandine", client, arch)
}

// checkClusterClientArch runs checkClientArch against targetArch, asking
// the cluster only when the client is amd64-only.
func checkClusterClientArch(cfg *config.Config, client string) error {
	if !amd64OnlyClients[client] {
		return nil
	}

	return checkClientArch(client, targetArch(cfg))
}
