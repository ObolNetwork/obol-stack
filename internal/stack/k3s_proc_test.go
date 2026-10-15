package stack

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// fakeProc lays out a /proc-like tree in a temp dir, so the real reader
// (sysProcFS) and its stat parsing are exercised.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	return &fakeProc{t: t, root: t.TempDir()}
}

func (f *fakeProc) add(pid, ppid int, start uint64, comm string, argv ...string) {
	f.t.Helper()
	dir := filepath.Join(f.root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	// Fields 3..22: state ppid pgrp session tty tpgid flags minflt cminflt
	// majflt cmajflt utime stime cutime cstime priority nice threads
	// itrealvalue starttime, then a few trailing fields.
	stat := fmt.Sprintf("%d (%s) S %d 1 1 0 -1 4194560 10 0 0 0 1 1 0 0 20 0 8 0 %d 123456 789 18446744073709551615\n",
		pid, comm, ppid, start)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeProc) fs() procFS { return sysProcFS{root: f.root} }

func TestParseProcStatCommWithSpacesAndParens(t *testing.T) {
	ppid, start, err := parseProcStat("42 (we (ird) name) S 7 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 98765 0 0\n")
	if err != nil {
		t.Fatal(err)
	}
	if ppid != 7 || start != 98765 {
		t.Fatalf("ppid=%d start=%d, want 7 98765", ppid, start)
	}
}

func TestIsK3sServerCmdline(t *testing.T) {
	bin := "/opt/obol/bin/k3s"
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{bin, "server", "--config", "/c/k3s-config.yaml"}, true},
		{[]string{"sudo", bin, "server", "--config", "/c"}, true},
		{[]string{"/usr/local/bin/k3s", "server"}, true},
		{[]string{"k3s server"}, true},
		{[]string{bin, "agent"}, false},
		{[]string{"/usr/bin/vim", "k3s", "server"}, false},
		{[]string{"/usr/sbin/sshd", "-D"}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isK3sServerCmdline(tc.argv, bin); got != tc.want {
			t.Errorf("isK3sServerCmdline(%q) = %v, want %v", tc.argv, got, tc.want)
		}
	}
}

func TestCheckK3sPid(t *testing.T) {
	bin := "/opt/obol/bin/k3s"
	p := newFakeProc(t)
	p.add(100, 1, 5000, "k3s-server", bin, "server", "--config", "/c")
	p.add(200, 1, 7000, "nginx", "/usr/sbin/nginx", "-g", "daemon off;")

	if st, _ := checkK3sPid(p.fs(), k3sPidRecord{PID: 100, StartTime: 5000}, bin); st != k3sPidVerified {
		t.Errorf("matching pid+start+cmdline: state %v, want verified", st)
	}
	if st, why := checkK3sPid(p.fs(), k3sPidRecord{PID: 100, StartTime: 4000}, bin); st != k3sPidReused || !strings.Contains(why, "start time") {
		t.Errorf("start-time mismatch: state %v (%s), want reused", st, why)
	}
	if st, _ := checkK3sPid(p.fs(), k3sPidRecord{PID: 200, StartTime: 7000}, bin); st != k3sPidReused {
		t.Errorf("PID reused by another program: state %v, want reused", st)
	}
	if st, _ := checkK3sPid(p.fs(), k3sPidRecord{PID: 300}, bin); st != k3sPidGone {
		t.Errorf("missing process: state %v, want gone", st)
	}
	// Legacy PID file (no start time) still requires a k3s cmdline.
	if st, _ := checkK3sPid(p.fs(), k3sPidRecord{PID: 100}, bin); st != k3sPidVerified {
		t.Errorf("legacy record for k3s: state %v, want verified", st)
	}
	if st, _ := checkK3sPid(p.fs(), k3sPidRecord{PID: 200}, bin); st != k3sPidReused {
		t.Errorf("legacy record for another program: state %v, want reused", st)
	}
}

func TestResolveK3sServerPidFindsChildOfSudo(t *testing.T) {
	bin := "/opt/obol/bin/k3s"
	p := newFakeProc(t)
	p.add(50, 1, 900, "sudo", "sudo", bin, "server", "--config", "/c")
	p.add(51, 50, 901, "sudo", "sudo", bin, "server", "--config", "/c") // use_pty monitor
	p.add(52, 51, 905, "k3s-server", bin, "server", "--config", "/c")
	p.add(60, 52, 950, "containerd", "containerd", "-c", "/x")

	rec, err := resolveK3sServerPid(p.fs(), 50, bin, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rec != (k3sPidRecord{PID: 52, StartTime: 905}) {
		t.Fatalf("resolved %+v, want k3s server pid 52 start 905", rec)
	}
	if _, err := resolveK3sServerPid(p.fs(), 60, bin, 0); err == nil {
		t.Error("expected an error when no k3s server descends from the given pid")
	}
}

func TestK3sReadPidRecordFormats(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	b := &K3sBackend{}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(cfg.ConfigDir, k3sPidFile), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("123 4567\n")
	if rec, err := b.readPidRecord(cfg); err != nil || rec != (k3sPidRecord{PID: 123, StartTime: 4567}) {
		t.Fatalf("readPidRecord = %+v, %v", rec, err)
	}
	write("123\n")
	if rec, err := b.readPidRecord(cfg); err != nil || rec != (k3sPidRecord{PID: 123}) {
		t.Fatalf("legacy readPidRecord = %+v, %v", rec, err)
	}
	write("123 abc\n")
	if _, err := b.readPidRecord(cfg); err == nil {
		t.Fatal("expected error for a malformed start time")
	}
}

func k3sDownTestBackend(t *testing.T, p *fakeProc) (*K3sBackend, *config.Config, *[]string) {
	t.Helper()
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	writeK3sTestExecutable(t, filepath.Join(binDir, k3sKillall), "#!/bin/sh\nexit 0\n")
	cfg := &config.Config{
		BinDir:    binDir,
		ConfigDir: filepath.Join(root, "config"),
		DataDir:   filepath.Join(root, "data"),
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var signals []string
	b := &K3sBackend{
		proc: p.fs(),
		signal: func(pid int, sig string) error {
			signals = append(signals, fmt.Sprintf("%s %d", sig, pid))
			// Simulate k3s exiting on SIGTERM.
			_ = os.RemoveAll(filepath.Join(p.root, strconv.Itoa(pid)))
			return nil
		},
	}
	return b, cfg, &signals
}

func TestK3sDownSignalsVerifiedK3s(t *testing.T) {
	p := newFakeProc(t)
	b, cfg, signals := k3sDownTestBackend(t, p)
	p.add(100, 1, 5000, "k3s-server", k3sBinaryPath(cfg), "server")
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, k3sPidFile), []byte("100 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := b.Down(cfg, ui.NewForTest(&out, &out), "test"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(*signals) != 1 || (*signals)[0] != "TERM 100" {
		t.Fatalf("signals = %v, want [TERM 100]", *signals)
	}
}

func TestK3sDownRefusesReusedPid(t *testing.T) {
	p := newFakeProc(t)
	b, cfg, signals := k3sDownTestBackend(t, p)
	// Same PID, but now an unrelated process started after a reboot.
	p.add(100, 1, 9999, "postgres", "/usr/lib/postgresql/bin/postgres", "-D", "/var/lib/pg")
	pidPath := filepath.Join(cfg.ConfigDir, k3sPidFile)
	if err := os.WriteFile(pidPath, []byte("100 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := b.Down(cfg, ui.NewForTest(&out, &out), "test"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(*signals) != 0 {
		t.Fatalf("a reused PID must never be signalled; sent %v", *signals)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("stale PID file should be removed")
	}
	text := out.String()
	if !strings.Contains(text, "Stale k3s PID file") || !strings.Contains(text, "stop it manually") {
		t.Errorf("expected stale-PID report with manual stop hint:\n%s", text)
	}
}

func TestK3sDownMissingProcessDoesNotSignal(t *testing.T) {
	p := newFakeProc(t)
	b, cfg, signals := k3sDownTestBackend(t, p)
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, k3sPidFile), []byte("100 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := b.Down(cfg, ui.NewForTest(&out, &out), "test"); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(*signals) != 0 {
		t.Fatalf("no process, no signal; sent %v", *signals)
	}
	if !strings.Contains(out.String(), "not running") {
		t.Errorf("expected not-running warning:\n%s", out.String())
	}
}

func TestK3sIsRunningRequiresVerifiedPid(t *testing.T) {
	p := newFakeProc(t)
	cfg := &config.Config{ConfigDir: t.TempDir(), BinDir: "/opt/obol/bin"}
	b := &K3sBackend{proc: p.fs()}
	p.add(100, 1, 9999, "bash", "/bin/bash")
	if err := os.WriteFile(filepath.Join(cfg.ConfigDir, k3sPidFile), []byte("100 5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if running, _ := b.IsRunning(cfg, "x"); running {
		t.Error("a reused PID must not count as k3s running")
	}
}
