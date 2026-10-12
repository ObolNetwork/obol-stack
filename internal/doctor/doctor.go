// Package doctor checks that the host, tools, cluster and services an Obol
// Stack needs are healthy, and says exactly how to fix what isn't. Checks are
// read-only; the few safe, idempotent repairs run only with --fix.
package doctor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// Status of one check, ordered by severity.
type Status string

const (
	StatusOK   Status = "ok"
	StatusInfo Status = "info"
	StatusSkip Status = "skip"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

func (s Status) rank() int {
	switch s {
	case StatusFail:
		return 3
	case StatusWarn:
		return 2
	default:
		return 0
	}
}

// Result is what a check found.
type Result struct {
	ID      string `json:"id"`
	Group   string `json:"group"`
	Title   string `json:"title"`
	Status  Status `json:"status"`
	Message string `json:"message"`
	// Fix is the exact command (or step) that resolves a warn/fail.
	Fix string `json:"fix,omitempty"`
	// Fixable means `obol doctor --fix` can repair it; Fixed that it did.
	Fixable bool `json:"fixable,omitempty"`
	Fixed   bool `json:"fixed,omitempty"`
}

// Check is one diagnostic.
type Check struct {
	ID    string
	Group string
	Title string
	// NeedsCluster checks are skipped when the stack isn't reachable.
	NeedsCluster bool
	// Online checks reach the internet; --offline skips them.
	Online bool
	Run    func(ctx context.Context, e *Env) Result
	// Repair, when set, is a safe idempotent fix that --fix runs for results
	// the check marked Fixable.
	Repair func(ctx context.Context, e *Env) error
}

// Check groups, in display order.
const (
	GroupHost     = "Host"
	GroupTools    = "Tools"
	GroupStack    = "Stack"
	GroupServices = "Services"
	GroupTunnel   = "Tunnel"
	GroupWallets  = "Wallets"
)

// Groups in display order.
var Groups = []string{GroupHost, GroupTools, GroupStack, GroupServices, GroupTunnel, GroupWallets}

// Env is everything a check touches, so tests can fake it.
type Env struct {
	Cfg *config.Config
	// Run executes a host command and returns its stdout.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// LookupHost resolves a hostname.
	LookupHost func(ctx context.Context, host string) ([]string, error)
	// Listen probes whether a local TCP address is free.
	Listen func(addr string) error
	// HTTP is used for local and public probes.
	HTTP *http.Client
	// FreeBytes reports free space on the filesystem holding path.
	FreeBytes func(path string) (uint64, error)
	// Offline: checks must not touch the internet (tunnel skips its probes).
	Offline bool

	clusterUp bool
}

// NewEnv returns the real environment.
func NewEnv(cfg *config.Config) *Env {
	return &Env{
		Cfg: cfg,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		LookupHost: net.DefaultResolver.LookupHost,
		Listen: func(addr string) error {
			l, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}

			return l.Close()
		},
		HTTP:      &http.Client{Timeout: 5 * time.Second},
		FreeBytes: freeBytes,
	}
}

// ClusterUp reports whether the stack's API server answered.
func (e *Env) ClusterUp() bool { return e.clusterUp }

// Kubectl runs obol's kubectl against the stack kubeconfig.
func (e *Env) Kubectl(ctx context.Context, args ...string) ([]byte, error) {
	return e.Run(ctx, e.Cfg.ToolPath("kubectl"), append([]string{"--kubeconfig", e.kubeconfig()}, args...)...)
}

func (e *Env) kubeconfig() string { return e.Cfg.ConfigDir + "/kubeconfig.yaml" }

// Options control a run.
type Options struct {
	Offline bool
	Fix     bool
	Timeout time.Duration
}

// Run executes every check: the cluster probe first (others depend on it),
// then the rest concurrently, each bounded by Options.Timeout. Results come
// back in display order.
func Run(ctx context.Context, e *Env, checks []Check, opts Options) []Result {
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Second
	}

	e.Offline = opts.Offline
	results := make([]Result, len(checks))

	var gate []int // the cluster probe and anything it gates on

	for i, c := range checks {
		if c.ID == ClusterCheckID {
			gate = append(gate, i)
		}
	}

	for _, i := range gate {
		results[i] = runOne(ctx, e, checks[i], opts)
		e.clusterUp = results[i].Status == StatusOK
	}

	var wg sync.WaitGroup

	for i, c := range checks {
		if c.ID == ClusterCheckID {
			continue
		}

		wg.Go(func() { results[i] = runOne(ctx, e, c, opts) })
	}

	wg.Wait()

	order := make(map[string]int, len(Groups))
	for i, g := range Groups {
		order[g] = i
	}

	sort.SliceStable(results, func(a, b int) bool { return order[results[a].Group] < order[results[b].Group] })

	return results
}

func runOne(ctx context.Context, e *Env, c Check, opts Options) Result {
	base := Result{ID: c.ID, Group: c.Group, Title: c.Title}

	switch {
	case c.Online && opts.Offline:
		base.Status, base.Message = StatusSkip, "skipped (--offline)"
		return base
	case c.NeedsCluster && !e.clusterUp:
		base.Status, base.Message = StatusSkip, "stack not running"
		return base
	}

	cctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	r := safeRun(cctx, e, c)
	r.ID, r.Group, r.Title = c.ID, c.Group, c.Title

	if cctx.Err() != nil && r.Status == "" {
		r.Status, r.Message = StatusWarn, fmt.Sprintf("timed out after %s", opts.Timeout)
	}

	// A check marks a result Fixable only when its Repair addresses that
	// particular finding (e.g. a stale kubeconfig, not a missing stack).
	r.Fixable = r.Fixable && c.Repair != nil && r.Status.rank() > 0

	if r.Fixable {
		if opts.Fix {
			if err := c.Repair(ctx, e); err != nil {
				r.Message += fmt.Sprintf(" (fix failed: %v)", err)
			} else {
				after := safeRun(ctx, e, c)
				r.Fixed = after.Status.rank() < r.Status.rank()
				r.Status, r.Message, r.Fix = after.Status, after.Message, after.Fix
			}
		}
	}

	return r
}

// safeRun turns a panicking check into a warning instead of a crash.
func safeRun(ctx context.Context, e *Env, c Check) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Status: StatusWarn, Message: fmt.Sprintf("check crashed: %v", p)}
		}
	}()

	return c.Run(ctx, e)
}

// Summary counts results by severity.
type Summary struct {
	OK, Warn, Fail, Skip int
}

// Summarize counts results; info counts as ok.
func Summarize(results []Result) Summary {
	var s Summary

	for _, r := range results {
		switch r.Status {
		case StatusFail:
			s.Fail++
		case StatusWarn:
			s.Warn++
		case StatusSkip:
			s.Skip++
		default:
			s.OK++
		}
	}

	return s
}

// ok/warn/fail/info/skip constructors keep checks terse.
func ok(msg string) Result   { return Result{Status: StatusOK, Message: msg} }
func info(msg string) Result { return Result{Status: StatusInfo, Message: msg} }

func warn(msg, fix string) Result { return Result{Status: StatusWarn, Message: msg, Fix: fix} }

func fail(msg, fix string) Result { return Result{Status: StatusFail, Message: msg, Fix: fix} }

// fixable marks a warn/fail result as repairable by the check's Repair.
func fixable(r Result) Result {
	r.Fixable = true
	return r
}

func skip(msg string) Result { return Result{Status: StatusSkip, Message: msg} }
