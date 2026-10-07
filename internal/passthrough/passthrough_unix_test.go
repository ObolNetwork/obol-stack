//go:build !windows

package passthrough

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/passthrough/stubtool"
)

// Exec path: the stub's self-SIGTERM kills the (replaced) process itself, so
// the parent sees a native signal death, not an exit code.
func TestExec_SignalDeathIsNative(t *testing.T) {
	_, _, ps := runDriver(t, "exec", []string{"STUB_SIGNAL=TERM"}, "")
	ws := ps.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Fatalf("status = %v, want killed by SIGTERM", ps)
	}
}

// Child path: a child killed by signal n maps to exit status 128+n.
func TestRun_ChildSignalMapsTo128PlusN(t *testing.T) {
	_, _, ps := runDriver(t, "run", []string{"STUB_SIGNAL=TERM"}, "")
	if ps.ExitCode() != 143 {
		t.Fatalf("exit = %d, want 143", ps.ExitCode())
	}
}

// Child path: SIGTERM to the parent is forwarded, the parent exits 143 and
// the child is not orphaned.
func TestRun_ForwardsSIGTERM(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), "PASSTHROUGH_TEST_MODE=run", "STUB_PATH="+stubPath, "STUB_SLEEP=20s")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read stub report: %v", err)
	}
	var rep stubtool.Report
	if err := json.Unmarshal(line, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.PID == cmd.Process.Pid {
		t.Fatal("run mode must use a child process")
	}

	start := time.Now()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 143 {
		t.Fatalf("exit = %d, want 143", code)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("child was not terminated promptly")
	}
	if err := syscall.Kill(rep.PID, 0); err == nil {
		_ = syscall.Kill(rep.PID, syscall.SIGKILL)
		t.Fatal("child still running after parent exited (orphaned)")
	}
}
