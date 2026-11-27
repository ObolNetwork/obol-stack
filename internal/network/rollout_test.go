package network

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

type fakePod struct {
	name, owner, revision, phase string
	ready, deleting              bool
}

func (p fakePod) json() string {
	ready := "False"
	if p.ready {
		ready = "True"
	}

	del := ""
	if p.deleting {
		del = `"deletionTimestamp":"2026-11-27T10:00:00Z",`
	}

	return fmt.Sprintf(`{"metadata":{"name":%q,%s"labels":{"controller-revision-hash":%q},"ownerReferences":[{"kind":"StatefulSet","name":%q}]},"status":{"phase":%q,"conditions":[{"type":"Ready","status":%q}]}}`,
		p.name, del, p.revision, p.owner, p.phase, ready)
}

// fakeRollout serves `kubectl get statefulset|pods -o json` and records
// deletes. pods is called on every get, so a test can change them over time.
func fakeRollout(t *testing.T, replicas int, pods func() []fakePod) (deleted *[]string) {
	t.Helper()

	origKubectl, origSleep, origNow := rolloutKubectlFn, rolloutSleepFn, rolloutNowFn
	t.Cleanup(func() { rolloutKubectlFn, rolloutSleepFn, rolloutNowFn = origKubectl, origSleep, origNow })

	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	rolloutNowFn = func() time.Time { return now }
	rolloutSleepFn = func(d time.Duration) { now = now.Add(d) }

	var dels []string

	rolloutKubectlFn = func(_ *config.Config, args ...string) (string, error) {
		switch {
		case args[0] == "get" && args[1] == "statefulset":
			return fmt.Sprintf(`{"items":[
			  {"metadata":{"name":"ethereum-beacon"},"spec":{"replicas":%d},"status":{"updateRevision":"ethereum-beacon-new"}},
			  {"metadata":{"name":"ethereum-execution"},"spec":{"replicas":%d},"status":{"updateRevision":"ethereum-execution-new"}}]}`, replicas, replicas), nil
		case args[0] == "get" && args[1] == "pods":
			var items []string
			for _, p := range pods() {
				items = append(items, p.json())
			}

			return `{"items":[` + strings.Join(items, ",") + `]}`, nil
		case args[0] == "delete" && args[1] == "pod":
			if args[2] != "-n" || args[3] != "ethereum-hoodi" {
				t.Errorf("delete in the wrong namespace: %v", args)
			}

			dels = append(dels, args[4])

			return "", nil
		}

		t.Fatalf("unexpected kubectl %v", args)

		return "", nil
	}

	return &dels
}

func TestUnstickRollouts_DeletesOnlyStuckPods(t *testing.T) {
	deleted := fakeRollout(t, 1, func() []fakePod {
		return []fakePod{
			// Crash-looping on the old spec: the StatefulSet will never roll it.
			{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-old", phase: "Running"},
			// Ready on the old spec: the controller rolls it itself.
			{name: "ethereum-execution-0", owner: "ethereum-execution", revision: "ethereum-execution-old", phase: "Running", ready: true},
		}
	})

	var out bytes.Buffer
	unstickRollouts(&config.Config{}, ui.NewForTest(&out, &out), "ethereum-hoodi")

	if !slices.Equal(*deleted, []string{"ethereum-beacon-0"}) {
		t.Fatalf("deleted %v, want only the stuck beacon pod\n%s", *deleted, out.String())
	}

	if !strings.Contains(out.String(), "its volume is kept") {
		t.Errorf("no notice:\n%s", out.String())
	}
}

func TestUnstickRollouts_LeavesHealthyAndRollingPods(t *testing.T) {
	cases := map[string][]fakePod{
		"on the new revision, not ready yet": {{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-new", phase: "Pending"}},
		"being deleted by the controller":    {{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-old", phase: "Running", deleting: true}},
		"ready on the old revision":          {{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-old", phase: "Running", ready: true}},
		"not a statefulset pod":              {{name: "dv-pod-0", owner: "other", revision: "x", phase: "Pending"}},
	}

	for name, pods := range cases {
		t.Run(name, func(t *testing.T) {
			deleted := fakeRollout(t, 1, func() []fakePod { return pods })

			var out bytes.Buffer
			unstickRollouts(&config.Config{}, ui.NewForTest(&out, &out), "ethereum-hoodi")

			if len(*deleted) != 0 {
				t.Fatalf("deleted %v", *deleted)
			}
		})
	}
}

// A stale pod that becomes Ready (or is rolled) during the wait is left to
// the controller.
func TestUnstickRollouts_WaitsForTheNormalRollout(t *testing.T) {
	gets := 0
	deleted := fakeRollout(t, 1, func() []fakePod {
		gets++
		if gets > 2 {
			return []fakePod{{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-new", phase: "Running", ready: true}}
		}

		return []fakePod{{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-old", phase: "Pending"}}
	})

	var out bytes.Buffer
	unstickRollouts(&config.Config{}, ui.NewForTest(&out, &out), "ethereum-hoodi")

	if len(*deleted) != 0 {
		t.Fatalf("deleted %v although the rollout went ahead", *deleted)
	}
}

// Right after helmfile sync the controller may not have computed the new
// updateRevision yet; wait for it instead of judging against the old one.
func TestUnstickRollouts_WaitsForTheControllerToObserve(t *testing.T) {
	deleted := fakeRollout(t, 1, func() []fakePod {
		return []fakePod{{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-old", phase: "Running"}}
	})

	inner := rolloutKubectlFn
	stsGets := 0
	rolloutKubectlFn = func(cfg *config.Config, args ...string) (string, error) {
		if args[0] == "get" && args[1] == "statefulset" {
			stsGets++
			if stsGets == 1 {
				return `{"items":[{"metadata":{"name":"ethereum-beacon","generation":3},"spec":{"replicas":1},"status":{"updateRevision":"ethereum-beacon-old","observedGeneration":2}}]}`, nil
			}
		}

		return inner(cfg, args...)
	}

	var out bytes.Buffer
	unstickRollouts(&config.Config{}, ui.NewForTest(&out, &out), "ethereum-hoodi")

	if !slices.Equal(*deleted, []string{"ethereum-beacon-0"}) || stsGets < 2 {
		t.Fatalf("deleted %v after %d statefulset reads", *deleted, stsGets)
	}
}

// Stopped deployments (0 replicas) are not touched.
func TestUnstickRollouts_SkipsScaledDown(t *testing.T) {
	deleted := fakeRollout(t, 0, func() []fakePod {
		return []fakePod{{name: "ethereum-beacon-0", owner: "ethereum-beacon", revision: "ethereum-beacon-old", phase: "Running"}}
	})

	var out bytes.Buffer
	unstickRollouts(&config.Config{}, ui.NewForTest(&out, &out), "ethereum-hoodi")

	if len(*deleted) != 0 {
		t.Fatalf("deleted %v", *deleted)
	}
}
