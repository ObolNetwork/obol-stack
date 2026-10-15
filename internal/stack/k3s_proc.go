package stack

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// procFS reads the bits of /proc the k3s backend needs to tell its own k3s
// server process apart from an unrelated process that later reused the PID
// (after a reboot or crash). Injectable for tests.
type procFS interface {
	// Cmdline returns the process argv.
	Cmdline(pid int) ([]string, error)
	// Stat returns the parent PID and the start time (field 22 of
	// /proc/<pid>/stat, clock ticks since boot).
	Stat(pid int) (ppid int, startTime uint64, err error)
	// Pids lists every visible process.
	Pids() ([]int, error)
}

// sysProcFS reads a /proc-layout directory (root "/proc" in production).
type sysProcFS struct{ root string }

func (p sysProcFS) Cmdline(pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(p.root, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	var argv []string
	for a := range strings.SplitSeq(strings.TrimRight(string(data), "\x00"), "\x00") {
		if a != "" {
			argv = append(argv, a)
		}
	}
	return argv, nil
}

func (p sysProcFS) Stat(pid int) (int, uint64, error) {
	data, err := os.ReadFile(filepath.Join(p.root, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, 0, err
	}
	return parseProcStat(string(data))
}

func (p sysProcFS) Pids() ([]int, error) {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// parseProcStat extracts ppid (field 4) and starttime (field 22) from a
// /proc/<pid>/stat line. comm (field 2) may contain spaces and parentheses,
// so fields are counted from the LAST ')'.
func parseProcStat(stat string) (int, uint64, error) {
	_, after, ok := strings.CutLast(stat, ")")
	if !ok {
		return 0, 0, errors.New("malformed stat: no comm terminator")
	}
	fields := strings.Fields(after)
	// fields[0] is field 3 (state), so field N is fields[N-3].
	if len(fields) < 20 {
		return 0, 0, fmt.Errorf("malformed stat: %d fields after comm", len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("malformed stat ppid: %w", err)
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed stat starttime: %w", err)
	}
	return ppid, start, nil
}

// k3sPidRecord is the content of .k3s.pid: k3s's own PID and its start
// time. StartTime is 0 for PID files written by older obol versions (PID
// only, which was the sudo wrapper's).
type k3sPidRecord struct {
	PID       int
	StartTime uint64
}

func (r k3sPidRecord) String() string {
	if r.StartTime == 0 {
		return strconv.Itoa(r.PID)
	}
	return fmt.Sprintf("%d %d", r.PID, r.StartTime)
}

// k3sPidState is the outcome of checking a PID record against /proc.
type k3sPidState int

const (
	k3sPidGone     k3sPidState = iota // no such process
	k3sPidVerified                    // alive and still our k3s server
	k3sPidReused                      // alive, but a different process
)

// isK3sServerCmdline reports whether argv is the k3s server obol started
// (directly, or the sudo wrapper around it, which relays signals).
func isK3sServerCmdline(argv []string, k3sBinary string) bool {
	// Re-split on spaces: a process that rewrote its title may expose
	// "k3s server" as a single argv entry.
	tokens := strings.Fields(strings.Join(argv, " "))
	isK3s := func(s string) bool { return s == k3sBinary || filepath.Base(s) == "k3s" }
	if len(tokens) > 0 && filepath.Base(tokens[0]) == "sudo" {
		// sudo [options] <k3s> server ...
		i := 1
		for i < len(tokens) && !isK3s(tokens[i]) {
			i++
		}
		tokens = tokens[i:]
	}
	return len(tokens) >= 2 && isK3s(tokens[0]) && tokens[1] == "server"
}

// checkK3sPid verifies that rec still names the k3s server: the process
// exists, its cmdline is k3s server, and (when recorded) its start time
// matches. Anything else is a reused PID that must never be signalled.
func checkK3sPid(proc procFS, rec k3sPidRecord, k3sBinary string) (k3sPidState, string) {
	_, start, err := proc.Stat(rec.PID)
	if err != nil {
		return k3sPidGone, ""
	}
	if rec.StartTime != 0 && start != rec.StartTime {
		return k3sPidReused, fmt.Sprintf("start time %d, recorded %d", start, rec.StartTime)
	}
	argv, err := proc.Cmdline(rec.PID)
	if err != nil {
		return k3sPidReused, fmt.Sprintf("cmdline unreadable: %v", err)
	}
	if !isK3sServerCmdline(argv, k3sBinary) {
		cmd := strings.Join(argv, " ")
		if cmd == "" {
			cmd = "(kernel thread or zombie)"
		}
		return k3sPidReused, "running " + cmd
	}
	return k3sPidVerified, ""
}

// resolveK3sServerPid finds the k3s server process under the sudo wrapper
// obol launched (sudo may interpose a monitor process, so descendants are
// searched breadth-first a few levels deep). It polls briefly because the
// child appears only after sudo has authenticated and exec'd it.
func resolveK3sServerPid(proc procFS, sudoPid int, k3sBinary string, timeout time.Duration) (k3sPidRecord, error) {
	deadline := time.Now().Add(timeout)
	for {
		if rec, ok := findK3sDescendant(proc, sudoPid, k3sBinary); ok {
			return rec, nil
		}
		if time.Now().After(deadline) {
			return k3sPidRecord{}, fmt.Errorf("k3s server process not found under sudo (pid %d)", sudoPid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func findK3sDescendant(proc procFS, rootPid int, k3sBinary string) (k3sPidRecord, bool) {
	pids, err := proc.Pids()
	if err != nil {
		return k3sPidRecord{}, false
	}
	children := map[int][]int{}
	starts := map[int]uint64{}
	for _, pid := range pids {
		ppid, start, err := proc.Stat(pid)
		if err != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
		starts[pid] = start
	}
	level := []int{rootPid}
	for depth := 0; depth < 4 && len(level) > 0; depth++ {
		var next []int
		for _, parent := range level {
			for _, child := range children[parent] {
				argv, err := proc.Cmdline(child)
				if err == nil && len(argv) > 0 && filepath.Base(argv[0]) != "sudo" && isK3sServerCmdline(argv, k3sBinary) {
					return k3sPidRecord{PID: child, StartTime: starts[child]}, true
				}
				next = append(next, child)
			}
		}
		level = next
	}
	return k3sPidRecord{}, false
}

// sudoSignal sends sig ("TERM", "KILL") to a root-owned process.
func sudoSignal(pid int, sig string) error {
	return exec.Command("sudo", "kill", "-"+sig, strconv.Itoa(pid)).Run()
}
