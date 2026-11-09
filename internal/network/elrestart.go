package network

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// liveMemoryFn reads the memory request and limit of one of a deployment's
// running StatefulSets ("" when unset). found is false when there is no
// StatefulSet or the cluster can't be asked. Swappable for tests.
var liveMemoryFn = func(cfg *config.Config, namespace, statefulSet string) (request, limit string, found bool) {
	out, err := stopKubectlFn(cfg, "get", "statefulset", statefulSet, "-n", namespace, "--ignore-not-found", "-o",
		`jsonpath={.metadata.name}|{.spec.template.spec.containers[0].resources.requests.memory}|{.spec.template.spec.containers[0].resources.limits.memory}`)
	if err != nil {
		return "", "", false
	}

	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) != 3 || parts[0] == "" {
		return "", "", false
	}

	return parts[1], parts[2], true
}

// memoryChanges reports whether a live request/limit differs from the
// sizing a sync will apply.
func memoryChanges(request, limit string, want MemorySizing) bool {
	req, err1 := parseMemoryMi(request)
	lim, err2 := parseMemoryMi(limit)

	return err1 != nil || err2 != nil || req != want.RequestMi || lim != want.LimitMi
}

// warnClientRestarts says when a sync will restart the clients for their
// memory alone, which the user didn't ask for: deployments made before obol
// set requests and limits get them on their next sync, and a new obol can
// resize them. Data is kept either way.
func warnClientRestarts(cfg *config.Config, u *ui.UI, d Deployment) {
	if d.Stopped {
		return
	}

	el, cl, err := deploymentSizing(d)
	if err != nil {
		return
	}

	var restarts []string
	for _, c := range []struct {
		sts, client string
		want        MemorySizing
	}{
		{executionService, d.ExecutionClient, el},
		{beaconService, d.ConsensusClient, cl},
	} {
		req, lim, found := liveMemoryFn(cfg, d.Namespace, c.sts)
		if found && memoryChanges(req, lim, c.want) {
			restarts = append(restarts, fmt.Sprintf("%s (limit %s)", c.client, formatMi(c.want.LimitMi)))
		}
	}

	if len(restarts) == 0 {
		return
	}

	u.Warnf("This sync restarts %s once to apply new memory requests and limits. Data is kept, and the clients resume where they stopped.",
		strings.Join(restarts, " and "))
}

// formatMi renders MiB as "10Gi", "1536Mi".
func formatMi(mi int64) string {
	if mi%1024 == 0 {
		return fmt.Sprintf("%dGi", mi/1024)
	}

	return fmt.Sprintf("%dMi", mi)
}
