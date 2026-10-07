// Package stubtool is a test double for passthrough tools (kubectl, helm, …).
// A test binary whose argv[0] basename is a tool name runs Main instead of
// its tests (see TestMain in internal/passthrough and cmd/obol), so the same
// binary serves as both the code under test and the tool it launches.
//
// Behaviour, driven by env:
//
//	STUB_EXIT=<n>     exit with status n (default 0)
//	STUB_SIGNAL=TERM  kill itself with SIGTERM after reporting
//	STUB_SLEEP=<dur>  sleep before exiting (signal tests)
//
// It writes one JSON Report line to stdout. `<tool> __complete <args…>`
// instead answers with cobra-style completion lines (see Main).
package stubtool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Names are the argv[0] basenames that select stub mode.
var Names = []string{"kubectl", "helm", "helmfile", "k9s", "stubtool"}

// Report is what the stub prints.
type Report struct {
	Tool             string   `json:"tool"`
	Argv             []string `json:"argv"`
	Kubeconfig       *string  `json:"kubeconfig"`
	HelmfileFilePath *string  `json:"helmfile_file_path"`
	Stdin            string   `json:"stdin"`
	PID              int      `json:"pid"`
}

// IsStub reports whether this process was started as a stub tool.
func IsStub() bool {
	base := filepath.Base(os.Args[0])
	for _, n := range Names {
		if base == n || base == n+".exe" {
			return true
		}
	}
	return false
}

func lookup(key string) *string {
	if v, ok := os.LookupEnv(key); ok {
		return &v
	}
	return nil
}

// Main runs the stub and exits.
func Main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__complete" {
		// Echo what was asked: one candidate naming the word being completed.
		last := "EMPTY"
		if n := len(args); n > 1 && args[n-1] != "" {
			last = args[n-1]
		}
		fmt.Printf("pods\tPod resources\nlast=%s\n:4\n", last)
		os.Exit(0)
	}

	stdin := ""
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		b, _ := io.ReadAll(os.Stdin)
		stdin = string(b)
	}

	rep := Report{
		Tool:             filepath.Base(os.Args[0]),
		Argv:             append([]string{}, args...),
		Kubeconfig:       lookup("KUBECONFIG"),
		HelmfileFilePath: lookup("HELMFILE_FILE_PATH"),
		Stdin:            stdin,
		PID:              os.Getpid(),
	}
	_ = json.NewEncoder(os.Stdout).Encode(rep)

	if d, err := time.ParseDuration(os.Getenv("STUB_SLEEP")); err == nil {
		time.Sleep(d)
	}
	if os.Getenv("STUB_SIGNAL") == "TERM" {
		if p, err := os.FindProcess(os.Getpid()); err == nil {
			_ = p.Signal(syscall.SIGTERM)
			time.Sleep(5 * time.Second) // wait to be killed
		}
	}
	code, _ := strconv.Atoi(os.Getenv("STUB_EXIT"))
	os.Exit(code)
}
