package stackbackup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
)

// listNodeNamesFn returns the space-separated node names of the cluster the
// stack's kubeconfig reaches. Swappable in tests.
var listNodeNamesFn = func(cfg *config.Config) (string, error) {
	bin, kubeconfig := kubectl.Paths(cfg)
	return kubectl.Output(bin, kubeconfig, "get", "nodes", "-o", "jsonpath={.items[*].metadata.name}")
}

// verifyClusterIdentity fails when the kubeconfig reaches a cluster other
// than this stack's. k3d API ports are reassigned across restarts, so with
// several stacks on one host (prod + a dev worktree) a stale kubeconfig can
// silently point at the other stack's cluster — export would then pause and
// harvest the wrong agents (labelled with this stack's ID) and import would
// apply resources into the wrong cluster.
//
// Only k3d is checked: its node names embed the stack ID
// (k3d-obol-stack-<id>-server-0). A k3s backend is one cluster per host and
// names nodes after the machine, so there is nothing to compare.
func verifyClusterIdentity(cfg *config.Config) error {
	stackID := readStackID(cfg.ConfigDir)
	if stackID == "" || readBackend(cfg.ConfigDir) != "k3d" {
		return nil
	}
	out, err := listNodeNamesFn(cfg)
	if err != nil {
		return fmt.Errorf("could not list cluster nodes to confirm stack identity: %w", err)
	}
	nodes := strings.Fields(out)
	// Exact node shape, not a prefix: stack "big-teal" must not match the
	// nodes of stack "big-teal-fish".
	own := regexp.MustCompile(`^k3d-obol-stack-` + regexp.QuoteMeta(stackID) + `-(server|agent)-[0-9]+$`)
	for _, n := range nodes {
		if own.MatchString(n) {
			return nil
		}
	}
	mismatch := &clusterMismatchError{
		StackID:    stackID,
		Nodes:      nodes,
		Kubeconfig: filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"),
	}
	for _, n := range nodes {
		if m := k3dNodeRe.FindStringSubmatch(n); m != nil {
			mismatch.OtherCluster = m[1]
			break
		}
	}
	return mismatch
}

// k3dNodeRe extracts the k3d cluster name from a node name.
var k3dNodeRe = regexp.MustCompile(`^k3d-(obol-stack-.+)-(server|agent)-[0-9]+$`)

// clusterMismatchError reports that the kubeconfig reaches a cluster other
// than this stack's. OtherCluster is the reached obol k3d cluster's name, or
// "" when it isn't one.
type clusterMismatchError struct {
	StackID      string
	OtherCluster string
	Nodes        []string
	Kubeconfig   string
}

func (e *clusterMismatchError) Error() string {
	return fmt.Sprintf("kubeconfig reaches a different cluster (nodes: %s), not stack %q; refresh it with: k3d kubeconfig write obol-stack-%s -o %s --overwrite",
		strings.Join(e.Nodes, ","), e.StackID, e.StackID, e.Kubeconfig)
}

// readBackend reads $OBOL_CONFIG_DIR/.stack-backend (owned by internal/stack;
// duplicated because stack imports this package). Missing file means k3d,
// matching stack.LoadBackend's default.
func readBackend(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, ".stack-backend"))
	if err != nil {
		return "k3d"
	}
	return strings.TrimSpace(string(data))
}
