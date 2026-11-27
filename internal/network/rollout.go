package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Swappable for tests.
var (
	rolloutKubectlFn = func(cfg *config.Config, args ...string) (string, error) {
		bin, kubeconfig := kubectl.Paths(cfg)
		return kubectl.Output(bin, kubeconfig, args...)
	}
	rolloutSleepFn = time.Sleep
	rolloutNowFn   = time.Now
)

const (
	// rolloutWait is how long sync lets the StatefulSet controller roll
	// stale pods itself before replacing the stuck ones.
	rolloutWait  = 60 * time.Second
	rolloutPoll  = 5 * time.Second
	revisionHash = "controller-revision-hash"
)

type stsList struct {
	Items []struct {
		Metadata struct {
			Name       string `json:"name"`
			Generation int64  `json:"generation"`
		} `json:"metadata"`
		Spec struct {
			Replicas *int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			UpdateRevision     string `json:"updateRevision"`
			ObservedGeneration int64  `json:"observedGeneration"`
		} `json:"status"`
	} `json:"items"`
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name              string            `json:"name"`
			Labels            map[string]string `json:"labels"`
			DeletionTimestamp *string           `json:"deletionTimestamp"`
			OwnerReferences   []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"ownerReferences"`
		} `json:"metadata"`
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

// stuckPod is a StatefulSet pod still on an old revision that is not Ready.
type stuckPod struct {
	Name, StatefulSet, Phase string
}

// errRolloutPending: a StatefulSet controller has not caught up with the
// spec just applied, so its updateRevision is not the new one yet.
var errRolloutPending = errors.New("statefulset controller has not observed the new spec yet")

// stuckStatefulSetPods returns the pods in namespace that a RollingUpdate
// will never replace: on an old revision and not Ready (the controller only
// moves on from Ready pods). Pods already being deleted are left alone.
func stuckStatefulSetPods(cfg *config.Config, namespace string) ([]stuckPod, error) {
	raw, err := rolloutKubectlFn(cfg, "get", "statefulset", "-n", namespace, "--request-timeout=10s", "-o", "json")
	if err != nil {
		return nil, err
	}

	var sets stsList
	if err := json.Unmarshal([]byte(raw), &sets); err != nil {
		return nil, fmt.Errorf("parse statefulsets: %w", err)
	}

	updateRevision := map[string]string{}

	for _, s := range sets.Items {
		if s.Spec.Replicas != nil && *s.Spec.Replicas == 0 {
			continue
		}

		if s.Status.ObservedGeneration < s.Metadata.Generation {
			return nil, errRolloutPending
		}

		if s.Status.UpdateRevision != "" {
			updateRevision[s.Metadata.Name] = s.Status.UpdateRevision
		}
	}

	if len(updateRevision) == 0 {
		return nil, nil
	}

	raw, err = rolloutKubectlFn(cfg, "get", "pods", "-n", namespace, "--request-timeout=10s", "-o", "json")
	if err != nil {
		return nil, err
	}

	var pods podList
	if err := json.Unmarshal([]byte(raw), &pods); err != nil {
		return nil, fmt.Errorf("parse pods: %w", err)
	}

	var stuck []stuckPod

	for _, p := range pods.Items {
		if p.Metadata.DeletionTimestamp != nil {
			continue
		}

		owner := ""

		for _, o := range p.Metadata.OwnerReferences {
			if o.Kind == "StatefulSet" {
				owner = o.Name
			}
		}

		want, ok := updateRevision[owner]
		if !ok || p.Metadata.Labels[revisionHash] == "" || p.Metadata.Labels[revisionHash] == want {
			continue
		}

		ready := false

		for _, c := range p.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
		}

		if !ready {
			stuck = append(stuck, stuckPod{Name: p.Metadata.Name, StatefulSet: owner, Phase: p.Status.Phase})
		}
	}

	return stuck, nil
}

// unstickRollouts runs after a sync: a StatefulSet's RollingUpdate never
// replaces a pod that is not Ready (crash-looping, stuck in Init, Pending),
// so a fix for exactly that pod would never apply. Give the controller
// rolloutWait to roll on its own, then delete the pods still stuck on an
// old revision; the StatefulSet recreates them from the new spec and keeps
// their PVCs. A Ready pod is never deleted. Best-effort: errors are
// warnings, the sync itself already succeeded.
func unstickRollouts(cfg *config.Config, u *ui.UI, namespace string) {
	deadline := rolloutNowFn().Add(rolloutWait)

	stuck, err := stuckStatefulSetPods(cfg, namespace)
	for errors.Is(err, errRolloutPending) && rolloutNowFn().Before(deadline) {
		rolloutSleepFn(time.Second)
		stuck, err = stuckStatefulSetPods(cfg, namespace)
	}

	if err != nil {
		u.Dim(fmt.Sprintf("  Could not check %s for stuck rollouts: %v", namespace, err))
		return
	}

	if len(stuck) == 0 {
		return
	}

	u.Infof("Waiting up to %s for %d pod(s) on the old spec that are not Ready to roll", rolloutWait, len(stuck))

	deadline = rolloutNowFn().Add(rolloutWait)
	for len(stuck) > 0 && rolloutNowFn().Before(deadline) {
		rolloutSleepFn(rolloutPoll)

		if stuck, err = stuckStatefulSetPods(cfg, namespace); errors.Is(err, errRolloutPending) {
			continue
		} else if err != nil {
			u.Warnf("Could not check %s for stuck rollouts: %v", namespace, err)
			return
		}
	}

	for _, p := range stuck {
		u.Warnf("%s/%s is not Ready and still runs the old spec (%s), so the StatefulSet will not replace it; deleting it so it is recreated from the new one (its volume is kept)",
			namespace, p.Name, p.Phase)

		if _, err := rolloutKubectlFn(cfg, "delete", "pod", "-n", namespace, p.Name, "--wait=false"); err != nil {
			u.Warnf("Could not delete %s/%s: %v\n  Run: obol kubectl -n %s delete pod %s", namespace, p.Name, err, namespace, p.Name)
		}
	}
}
