package network

import (
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// liveELMemoryFn reads the memory request of the deployment's running EL
// StatefulSet. found is false when there is no StatefulSet or the cluster
// can't be asked. Swappable for tests.
var liveELMemoryFn = func(cfg *config.Config, namespace string) (memory string, found bool) {
	out, err := stopKubectlFn(cfg, "get", "statefulset", executionService, "-n", namespace,
		"--ignore-not-found", "-o", "jsonpath={.metadata.name} {.spec.template.spec.containers[*].resources.requests.memory}")
	if err != nil {
		return "", false
	}

	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", false
	}

	return strings.Join(fields[1:], " "), true
}

// warnELRestart says when a sync will also restart the execution client.
// Deployments made before obol set per-network memory requests run the EL
// with none; the next sync adds them, which changes the pod template and
// restarts the EL, whatever the user meant to change (e.g. the CL).
func warnELRestart(cfg *config.Config, u *ui.UI, d Deployment) {
	memory, found := liveELMemoryFn(cfg, d.Namespace)
	if d.Stopped || !found || memory != "" {
		return
	}

	u.Warnf("This sync also restarts %s once: it was deployed before obol set memory requests per network, "+
		"and now gets them. Its data is kept, and it resumes where it stopped.", elName(d.ExecutionClient))
}

func elName(client string) string {
	if client == "" {
		return "the execution client"
	}

	return client + " (the execution client)"
}
