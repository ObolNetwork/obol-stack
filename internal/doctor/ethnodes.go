package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Ethereum node clients that keep restarting. A client killed by its
// liveness probe exits cleanly and is restarted at once, so the pod looks
// Running and Ready most of the time; only the restart count shows it
// (e.g. Silver serving its API on another port than the probe asked: 20
// restarts in 10 h, each "Completed").
const (
	ethRestartThreshold = 3
	ethRestartWindow    = 24 * time.Hour
)

type ethPod struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Status struct {
		ContainerStatuses []struct {
			Name         string `json:"name"`
			RestartCount int    `json:"restartCount"`
			LastState    struct {
				Terminated *struct {
					Reason     string    `json:"reason"`
					ExitCode   int       `json:"exitCode"`
					FinishedAt time.Time `json:"finishedAt"`
				} `json:"terminated"`
			} `json:"lastState"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type ethRestart struct {
	ns, pod, container, reason string
	restarts, exitCode         int
	ago                        time.Duration
}

func (r ethRestart) String() string {
	return fmt.Sprintf("%s/%s %s: %d restarts, last %s (exit %d) %s ago",
		r.ns, r.pod, r.container, r.restarts, r.reason, r.exitCode, roundAgo(r.ago))
}

func roundAgo(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

func checkEthereumRestarts(ctx context.Context, e *Env) Result {
	out, err := e.Kubectl(ctx, "get", "pods", "-A", "-o", "json")
	if err != nil {
		return warn("couldn't list pods", "obol kubectl get pods -A")
	}

	var pl struct {
		Items []ethPod `json:"items"`
	}

	if err := json.Unmarshal(out, &pl); err != nil {
		return warn("couldn't read pod list", "obol kubectl get pods -A")
	}

	restarts, nodes := ethereumRestarts(pl.Items, time.Now())
	if nodes == 0 {
		return skip("no local ethereum nodes")
	}

	if len(restarts) == 0 {
		return ok(fmt.Sprintf("no client restarting repeatedly (%d pods)", nodes))
	}

	first := restarts[0]

	return warn("restarting repeatedly: "+joinMax(restarts, 3),
		fmt.Sprintf("obol kubectl -n %s logs --previous %s -c %s  (and: obol network status)", first.ns, first.pod, first.container))
}

// ethereumRestarts returns the containers of pods in ethereum-* namespaces
// that restarted at least ethRestartThreshold times, the last time within
// ethRestartWindow, most restarts first; and how many such pods it saw.
func ethereumRestarts(pods []ethPod, now time.Time) ([]ethRestart, int) {
	var (
		out   []ethRestart
		nodes int
	)

	for _, p := range pods {
		if !strings.HasPrefix(p.Metadata.Namespace, "ethereum-") {
			continue
		}

		nodes++

		for _, c := range p.Status.ContainerStatuses {
			t := c.LastState.Terminated
			if c.RestartCount < ethRestartThreshold || t == nil || now.Sub(t.FinishedAt) > ethRestartWindow {
				continue
			}

			out = append(out, ethRestart{
				ns: p.Metadata.Namespace, pod: p.Metadata.Name, container: c.Name,
				reason: t.Reason, exitCode: t.ExitCode, restarts: c.RestartCount, ago: now.Sub(t.FinishedAt),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].restarts > out[j].restarts })

	return out, nodes
}
