package main

// obol research — owner side of a decentralized auto-research program.
//
//	obol research publish <name>   start the KB + membership server on this
//	                               machine, expose it over a Cloudflare quick
//	                               tunnel, print the public URL workers join.
//	obol research approve <code>   admit a worker (membership decision).
//	obol research status [<name>]  roster, results, champion, payouts.
//
// The server is the host gateway (same spirit as `obol sell inference`): it
// runs on the owner's machine, not in the cluster, and reaches remote
// workers over the real internet via Cloudflare — no tailscale, every KB
// route gated by a groupauth member token.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/research/kb"
	"github.com/ObolNetwork/obol-stack/internal/research/server"
	"github.com/urfave/cli/v3"
)

// researchState is persisted so `approve`/`status` can reach a running
// `publish` server.
type researchState struct {
	Program    string `json:"program"`
	LocalAddr  string `json:"local_addr"`  // http://127.0.0.1:PORT
	PublicURL  string `json:"public_url"`  // https://xxx.trycloudflare.com
	OwnerToken string `json:"owner_token"` // gates approve/status
}

func researchCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "research",
		Usage: "Run a decentralized auto-research program (publish an ID, admit workers, collect results)",
		Commands: []*cli.Command{
			researchPublishCommand(cfg),
			researchApproveCommand(cfg),
			researchStatusCommand(cfg),
		},
	}
}

func researchPublishCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "publish",
		Usage:     "Publish a research program: host the KB + membership server and expose it over a Cloudflare tunnel",
		ArgsUsage: "<name>",
		Description: `Starts the program's knowledge-base + membership server on this machine
and opens a Cloudflare quick tunnel so workers on other obol-stacks can
join over the open internet. Runs in the foreground — Ctrl-C stops the
program. Approve joining workers from another shell with
'obol research approve <user-code>'.`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "objective", Usage: "Free-text hypothesis space"},
			&cli.StringFlag{Name: "metric", Usage: "Metric name (e.g. val_bpb)", Required: true},
			&cli.StringFlag{Name: "direction", Usage: "minimize | maximize", Value: "minimize"},
			&cli.StringFlag{Name: "accept", Usage: "beats-champion | threshold", Value: "beats-champion"},
			&cli.FloatFlag{Name: "threshold", Usage: "Acceptance threshold (when --accept=threshold)"},
			&cli.FloatFlag{Name: "baseline", Usage: "Reference metric value; first improvement's impact is measured against it"},
			&cli.FloatFlag{Name: "pool", Usage: "Reward pool", Value: 0},
			&cli.StringFlag{Name: "token", Usage: "Reward token", Value: "OBOL"},
			&cli.StringFlag{Name: "network", Usage: "Payment chain", Value: "base-sepolia"},
			&cli.StringFlag{Name: "membership", Usage: "open | invite", Value: "invite"},
			&cli.StringFlag{Name: "split", Usage: "by-impact | champion-takes-all", Value: "by-impact"},
			&cli.IntFlag{Name: "port", Usage: "Local port (0 = pick a free one)", Value: 0},
			&cli.BoolFlag{Name: "no-tunnel", Usage: "Serve locally only (skip the Cloudflare tunnel)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("program name required: obol research publish <name>")
			}
			name := strings.TrimSpace(cmd.Args().First())

			prog, err := programFromFlags(name, cmd)
			if err != nil {
				return err
			}

			ownerToken, err := randomToken("obol-research-owner-")
			if err != nil {
				return err
			}
			srv := server.New(prog, cmd.String("membership"), ownerToken, nil)

			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cmd.Int("port")))
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			localAddr := "http://" + ln.Addr().String()
			httpSrv := &http.Server{Handler: srv.Handler()}
			go func() { _ = httpSrv.Serve(ln) }()

			publicURL := localAddr
			var tunnel *exec.Cmd
			if !cmd.Bool("no-tunnel") {
				u.Info("Opening Cloudflare tunnel …")
				turl, tcmd, terr := startQuickTunnel(ctx, ln.Addr().String())
				if terr != nil {
					u.Warnf("tunnel failed (%v) — serving locally only at %s", terr, localAddr)
				} else {
					publicURL = turl
					tunnel = tcmd
				}
			}

			st := researchState{Program: name, LocalAddr: localAddr, PublicURL: publicURL, OwnerToken: ownerToken}
			if err := writeResearchState(cfg, st); err != nil {
				u.Warnf("could not persist program state (approve/status from another shell may not work): %v", err)
			}

			u.Successf("Research program %q published", name)
			u.Infof("Public URL:  %s", publicURL)
			u.Infof("Metric:      %s (%s, %s)", prog.Criteria.Metric, prog.Criteria.Direction, prog.Criteria.Accept)
			u.Infof("Membership:  %s", cmd.String("membership"))
			u.Blank()
			u.Bold("Workers join with:")
			u.Printf("  python3 worker.py --kb %s --program %s --worker <name>", publicURL, name)
			if cmd.String("membership") == server.MembershipInvite {
				u.Dim("Approve each worker's printed code:  obol research approve <user-code>")
			}
			u.Blank()
			u.Dim("Ctrl-C to stop the program.")

			// Run until interrupted.
			sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stop()
			<-sigCtx.Done()

			u.Info("Stopping …")
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = httpSrv.Shutdown(shutCtx)
			if tunnel != nil && tunnel.Process != nil {
				_ = tunnel.Process.Kill()
			}
			_ = removeResearchState(cfg, name)
			return nil
		},
	}
}

func researchApproveCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "approve",
		Usage:     "Admit a worker to the program (the membership decision)",
		ArgsUsage: "<user-code>",
		Flags:     []cli.Flag{&cli.StringFlag{Name: "program", Usage: "Program name (defaults to the only running one)"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("user code required: obol research approve <user-code>")
			}
			st, err := loadResearchState(cfg, cmd.String("program"))
			if err != nil {
				return err
			}
			body, _ := json.Marshal(map[string]string{"user_code": cmd.Args().First()})
			resp, err := ownerPost(ctx, st, "/auth/device/approve", body)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("approve failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
			}
			u.Successf("Approved %s into %q", cmd.Args().First(), st.Program)
			return nil
		},
	}
}

func researchStatusCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "status",
		Usage:     "Show roster, results, champion, and payouts",
		ArgsUsage: "[<name>]",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			st, err := loadResearchState(cfg, cmd.Args().First())
			if err != nil {
				return err
			}
			resp, err := ownerGet(ctx, st, "/status")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			var out struct {
				Program  kb.Program         `json:"program"`
				Roster   []string           `json:"roster"`
				Results  []kb.Result        `json:"results"`
				Champion *kb.Result         `json:"champion"`
				Payouts  map[string]float64 `json:"payouts"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				return err
			}
			u.Bold(fmt.Sprintf("Program %s — %s (%s)", out.Program.ID, out.Program.Criteria.Metric, out.Program.Criteria.Direction))
			u.Infof("Members: %s", strings.Join(out.Roster, ", "))
			if out.Champion != nil {
				u.Successf("Champion: %s = %.6f  (worker %s)", out.Program.Criteria.Metric, out.Champion.Value, out.Champion.Worker)
			} else {
				u.Dim("Champion: none yet")
			}
			for _, r := range out.Results {
				mark := "·"
				if r.Champion {
					mark = "★"
				} else if r.Accepted {
					mark = "+"
				}
				u.Printf("  %s #%d %-10s %.6f  impact %.6f", mark, r.Seq, r.Worker, r.Value, r.Impact)
			}
			if len(out.Payouts) > 0 {
				u.Blank()
				u.Bold("Payouts (" + string(out.Program.Split) + "):")
				for w, amt := range out.Payouts {
					u.Printf("  %-10s %.6f %s", w, amt, out.Program.Token)
				}
			}
			return nil
		},
	}
}

// --- helpers ---

func programFromFlags(name string, cmd *cli.Command) (kb.Program, error) {
	dir := kb.Direction(cmd.String("direction"))
	if dir != kb.Minimize && dir != kb.Maximize {
		return kb.Program{}, fmt.Errorf("--direction must be minimize or maximize")
	}
	acc := kb.AcceptMode(cmd.String("accept"))
	if acc != kb.BeatsChampion && acc != kb.Threshold {
		return kb.Program{}, fmt.Errorf("--accept must be beats-champion or threshold")
	}
	split := kb.SplitMode(cmd.String("split"))
	if split != kb.ByImpact && split != kb.ChampionTakesAll {
		return kb.Program{}, fmt.Errorf("--split must be by-impact or champion-takes-all")
	}
	p := kb.Program{
		ID:        name,
		Objective: cmd.String("objective"),
		Criteria:  kb.Criteria{Metric: cmd.String("metric"), Direction: dir, Accept: acc},
		Pool:      cmd.Float("pool"),
		Token:     cmd.String("token"),
		Network:   cmd.String("network"),
		Split:     split,
	}
	if cmd.IsSet("threshold") {
		t := cmd.Float("threshold")
		p.Criteria.Threshold = &t
	}
	if cmd.IsSet("baseline") {
		b := cmd.Float("baseline")
		p.Baseline = &b
	}
	return p, nil
}

func randomToken(prefix string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

var trycloudflareRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// startQuickTunnel launches `cloudflared tunnel --url http://addr` and
// returns the public trycloudflare URL once it appears on stderr.
func startQuickTunnel(ctx context.Context, addr string) (string, *exec.Cmd, error) {
	bin, err := exec.LookPath("cloudflared")
	if err != nil {
		if _, statErr := os.Stat("/opt/homebrew/bin/cloudflared"); statErr == nil {
			bin = "/opt/homebrew/bin/cloudflared"
		} else {
			return "", nil, fmt.Errorf("cloudflared not found")
		}
	}
	c := exec.CommandContext(ctx, bin, "tunnel", "--no-autoupdate", "--url", "http://"+addr)
	stderr, err := c.StderrPipe()
	if err != nil {
		return "", nil, err
	}
	if err := c.Start(); err != nil {
		return "", nil, err
	}
	urlCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if m := trycloudflareRe.FindString(sc.Text()); m != "" {
				urlCh <- m
				// keep draining so the pipe doesn't block cloudflared
				for sc.Scan() {
				}
				return
			}
		}
	}()
	select {
	case u := <-urlCh:
		return u, c, nil
	case <-time.After(40 * time.Second):
		_ = c.Process.Kill()
		return "", nil, fmt.Errorf("timed out waiting for tunnel URL")
	}
}

func researchStateDir(cfg *config.Config) string { return filepath.Join(cfg.ConfigDir, "research") }

func writeResearchState(cfg *config.Config, st researchState) error {
	dir := researchStateDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(filepath.Join(dir, st.Program+".json"), b, 0o600)
}

func removeResearchState(cfg *config.Config, name string) error {
	return os.Remove(filepath.Join(researchStateDir(cfg), name+".json"))
}

// loadResearchState reads the named program's state, or the only one if name
// is empty.
func loadResearchState(cfg *config.Config, name string) (researchState, error) {
	dir := researchStateDir(cfg)
	if name != "" {
		return readResearchStateFile(filepath.Join(dir, name+".json"))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return researchState{}, fmt.Errorf("no running program (publish one first)")
	}
	var found []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			found = append(found, e.Name())
		}
	}
	if len(found) == 0 {
		return researchState{}, fmt.Errorf("no running program (publish one first)")
	}
	if len(found) > 1 {
		return researchState{}, fmt.Errorf("multiple programs running — pass the name")
	}
	return readResearchStateFile(filepath.Join(dir, found[0]))
}

func readResearchStateFile(path string) (researchState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return researchState{}, fmt.Errorf("read program state: %w", err)
	}
	var st researchState
	if err := json.Unmarshal(b, &st); err != nil {
		return researchState{}, err
	}
	return st, nil
}

func ownerPost(ctx context.Context, st researchState, path string, body []byte) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, st.LocalAddr+path, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+st.OwnerToken)
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

func ownerGet(ctx context.Context, st researchState, path string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.LocalAddr+path, nil)
	req.Header.Set("Authorization", "Bearer "+st.OwnerToken)
	return http.DefaultClient.Do(req)
}
