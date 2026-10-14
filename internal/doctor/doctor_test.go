package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// fakeEnv answers commands from a table keyed by "binary-basename arg1 arg2…".
// Unknown commands fail, like a missing binary.
func fakeEnv(t *testing.T, cmds map[string]string) *Env {
	t.Helper()

	cfg := &config.Config{ConfigDir: t.TempDir(), DataDir: t.TempDir(), BinDir: t.TempDir(), StateDir: t.TempDir()}

	return &Env{
		Cfg: cfg,
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			key := filepath.Base(name) + " " + strings.Join(args, " ")
			for prefix, out := range cmds {
				if strings.HasPrefix(key, prefix) {
					if out == "ERR" {
						return nil, errors.New("exit status 1")
					}

					return []byte(out), nil
				}
			}

			return nil, errors.New("not found: " + key)
		},
		LookupHost: func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") },
		Listen:     func(string) error { return nil },
		FreeBytes:  func(string) (uint64, error) { return 100 * gib, nil },
	}
}

func TestCheckDocker_MemoryThresholds(t *testing.T) {
	for _, tc := range []struct {
		mem  int64
		want Status
	}{
		{2 * gib, StatusFail},
		{4 * gib, StatusWarn},
		{8 * gib, StatusOK},
	} {
		e := fakeEnv(t, map[string]string{
			"docker info": `{"ServerVersion":"28.1.1","OperatingSystem":"Docker Desktop","MemTotal":` + strconv.FormatInt(tc.mem, 10) + `,"NCPU":8}`,
		})

		r := checkDocker(context.Background(), e)
		if r.Status != tc.want {
			t.Errorf("mem %d GiB: status %s, want %s (%s)", tc.mem/gib, r.Status, tc.want, r.Message)
		}

		if tc.want != StatusOK && !strings.Contains(r.Fix, "Docker Desktop → Settings") {
			t.Errorf("fix should name the Docker Desktop setting, got %q", r.Fix)
		}
	}
}

func TestCheckDocker_Unreachable(t *testing.T) {
	r := checkDocker(context.Background(), fakeEnv(t, nil))
	if r.Status != StatusFail || r.Fix == "" {
		t.Fatalf("got %+v; want a fail with a fix", r)
	}
}

func TestCheckHostsEntry(t *testing.T) {
	e := fakeEnv(t, nil)
	if r := checkHostsEntry(context.Background(), e); r.Status != StatusFail || !strings.Contains(r.Fix, "/etc/hosts") {
		t.Fatalf("unresolved: got %+v", r)
	}

	e.LookupHost = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	if r := checkHostsEntry(context.Background(), e); r.Status != StatusOK {
		t.Fatalf("resolved: got %+v", r)
	}
}

func TestCheckPorts_BusyWhenStackDown(t *testing.T) {
	e := fakeEnv(t, nil)
	e.Listen = func(addr string) error {
		if strings.HasSuffix(addr, ":8080") {
			return errors.New("address already in use")
		}

		return nil
	}

	r := checkPorts(context.Background(), e)
	if r.Status != StatusFail || !strings.Contains(r.Message, ":8080") || !strings.Contains(r.Fix, "lsof") {
		t.Fatalf("got %+v", r)
	}
}

func writeStackID(t *testing.T, e *Env, id string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(e.Cfg.ConfigDir, ".stack-id"), []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckCluster(t *testing.T) {
	t.Run("not initialised", func(t *testing.T) {
		r := checkCluster(context.Background(), fakeEnv(t, nil))
		if r.Status != StatusFail || !strings.Contains(r.Fix, "obol stack init") {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("running", func(t *testing.T) {
		e := fakeEnv(t, map[string]string{"kubectl --kubeconfig": "ok"})
		writeStackID(t, e, "fitting-robin")

		if r := checkCluster(context.Background(), e); r.Status != StatusOK {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("stale kubeconfig is fixable", func(t *testing.T) {
		e := fakeEnv(t, map[string]string{
			"kubectl --kubeconfig": "ERR",
			"k3d cluster list":     `[{"name":"obol-stack-fitting-robin","serversRunning":1}]`,
		})
		writeStackID(t, e, "fitting-robin")

		r := checkCluster(context.Background(), e)
		if r.Status != StatusWarn || !r.Fixable {
			t.Fatalf("got %+v; want a fixable warning", r)
		}
	})

	t.Run("stopped is not fixable", func(t *testing.T) {
		e := fakeEnv(t, map[string]string{
			"kubectl --kubeconfig": "ERR",
			"k3d cluster list":     `[{"name":"obol-stack-fitting-robin","serversRunning":0}]`,
		})
		writeStackID(t, e, "fitting-robin")

		r := checkCluster(context.Background(), e)
		if r.Status != StatusWarn || r.Fixable || r.Fix != "obol stack up" {
			t.Fatalf("got %+v", r)
		}
	})
}

func TestPodProblems(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	mk := func(name, phase string, age time.Duration, ready bool, restarts int, waiting string) pod {
		var p pod

		p.Metadata.Name, p.Metadata.Namespace = name, "x402"
		p.Metadata.CreationTimestamp = now.Add(-age)
		p.Status.Phase = phase

		c := containerStatus{Ready: ready, RestartCount: restarts}
		if waiting != "" {
			c.State.Waiting = &struct {
				Reason string `json:"reason"`
			}{Reason: waiting}
		}

		p.Status.ContainerStatuses = []containerStatus{c}

		return p
	}

	problems, flapping := podProblems([]pod{
		mk("healthy", "Running", time.Hour, true, 0, ""),
		mk("crashing", "Running", time.Hour, false, 9, "CrashLoopBackOff"),
		mk("starting", "Pending", time.Minute, false, 0, ""),
		mk("stuck", "Pending", 10*time.Minute, false, 0, ""),
		mk("pull", "Pending", time.Minute, false, 0, "ImagePullBackOff"),
		mk("done", "Succeeded", time.Hour, false, 0, ""),
		mk("flappy", "Running", time.Hour, true, 7, ""),
	}, now)

	var names []string
	for _, p := range problems {
		names = append(names, p.name)
	}

	if got := strings.Join(names, ","); got != "crashing,stuck,pull" {
		t.Fatalf("problems = %s; want crashing,stuck,pull", got)
	}

	if len(flapping) != 1 || flapping[0].name != "flappy" {
		t.Fatalf("flapping = %v; want flappy", flapping)
	}
}

func TestCheckWallets_AddressOnly(t *testing.T) {
	e := fakeEnv(t, nil)
	dir := filepath.Join(e.Cfg.DataDir, "hermes-obol-agent", "remote-signer-keystores")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	ks := `{"address":"d0391eedc3268f3deef1f05fff5d7aef82f64ccf","crypto":{"ciphertext":"SECRET-MATERIAL"}}`
	if err := os.WriteFile(filepath.Join(dir, "k.json"), []byte(ks), 0o600); err != nil {
		t.Fatal(err)
	}

	r := checkWallets(context.Background(), e)
	if !strings.Contains(r.Message, "0xd039…4ccf") || strings.Contains(r.Message+r.Fix, "SECRET") {
		t.Fatalf("got %+v; want the short address and nothing else from the keystore", r)
	}
}

func TestRun_GatesClusterChecksAndRepairs(t *testing.T) {
	e := fakeEnv(t, nil)
	repaired := false
	healthy := false

	checks := []Check{
		{ID: "pods", Group: "Stack", Title: "Pods", NeedsCluster: true, Run: func(context.Context, *Env) Result { return ok("fine") }},
		{ID: "web", Group: "Host", Title: "Web", Online: true, Run: func(context.Context, *Env) Result { return ok("fine") }},
		{ID: ClusterCheckID, Group: "Stack", Title: "Cluster", Run: func(context.Context, *Env) Result { return fail("down", "obol stack up") }},
		{
			ID: "ca", Group: "Services", Title: "CA",
			Run: func(context.Context, *Env) Result {
				if healthy {
					return ok("populated")
				}

				return fixable(fail("empty", "obol doctor --fix"))
			},
			Repair: func(context.Context, *Env) error { repaired, healthy = true, true; return nil },
		},
		{
			ID: "slow", Group: "Host", Title: "Slow",
			Run: func(ctx context.Context, _ *Env) Result { <-ctx.Done(); return Result{} },
		},
	}

	res := Run(context.Background(), e, checks, Options{Offline: true, Fix: true, Timeout: 50 * time.Millisecond})

	byID := map[string]Result{}
	for _, r := range res {
		byID[r.ID] = r
	}

	if byID["pods"].Status != StatusSkip || byID["web"].Status != StatusSkip {
		t.Errorf("cluster/online checks should skip: pods=%s web=%s", byID["pods"].Status, byID["web"].Status)
	}

	if !repaired || !byID["ca"].Fixed || byID["ca"].Status != StatusOK {
		t.Errorf("repair not applied: %+v", byID["ca"])
	}

	if byID["slow"].Status != StatusWarn || !strings.Contains(byID["slow"].Message, "timed out") {
		t.Errorf("slow check: %+v", byID["slow"])
	}

	if res[0].Group != "Host" {
		t.Errorf("results not in group order: first is %s", res[0].Group)
	}

	if s := Summarize(res); s.Fail != 1 || s.Skip != 2 {
		t.Errorf("summary = %+v", s)
	}
}

func TestOllamaListensOnAllInterfaces(t *testing.T) {
	orig := ollamaGOOS
	t.Cleanup(func() { ollamaGOOS = orig })

	cases := []struct {
		name    string
		goos    string
		ss      string
		exposed bool
	}{
		{"linux all interfaces", "linux", "LISTEN 0 4096 0.0.0.0:11434 0.0.0.0:*\n", true},
		{"linux ipv6 any", "linux", "LISTEN 0 4096 *:11434 *:*\n", true},
		{"linux loopback", "linux", "LISTEN 0 4096 127.0.0.1:11434 0.0.0.0:*\n", false},
		{"linux bridge only", "linux", "LISTEN 0 4096 172.17.0.1:11434 0.0.0.0:*\n", false},
		{"linux ss missing", "linux", "ERR", false},
		{"darwin skipped", "darwin", "LISTEN 0 4096 0.0.0.0:11434 0.0.0.0:*\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ollamaGOOS = tc.goos
			e := fakeEnv(t, map[string]string{"ss -Hltn": tc.ss})
			r, exposed := ollamaListensOnAllInterfaces(context.Background(), e)
			if exposed != tc.exposed {
				t.Fatalf("exposed = %v, want %v (result %+v)", exposed, tc.exposed, r)
			}
		})
	}
}
