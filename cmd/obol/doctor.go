package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/doctor"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/tools"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
)

func doctorCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "Check the host, tools, stack and services, and say how to fix problems",
		Description: `Runs every check in a few seconds and prints one line each. Anything that
needs attention comes with the exact command that fixes it.

Nothing is changed unless you pass --fix, which only runs safe, repeatable
repairs: installing missing pinned tools, refreshing a stale kubeconfig and
repopulating the x402 CA bundle.

Exit status: 0 when nothing failed (warnings too, unless --strict), 1 otherwise.

Examples:
  obol doctor
  obol doctor --fix
  obol doctor --offline -o json`,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "fix", Usage: "Run the safe automatic repairs for problems found"},
			&cli.BoolFlag{Name: "offline", Usage: "Skip checks that reach the internet"},
			&cli.BoolFlag{Name: "strict", Usage: "Exit 1 on warnings too"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)

			results := doctor.Run(ctx, doctor.NewEnv(cfg), doctor.Checks(doctorRepairs(cfg, u)), doctor.Options{
				Offline: cmd.Bool("offline"),
				Fix:     cmd.Bool("fix"),
				Timeout: 20 * time.Second,
			})
			sum := doctor.Summarize(results)

			if u.IsJSON() {
				if err := u.JSON(results); err != nil {
					return err
				}
			} else {
				printDoctor(u, results, sum, cmd.Bool("fix"))
			}

			if sum.Fail > 0 || (cmd.Bool("strict") && sum.Warn > 0) {
				return cli.Exit("", 1)
			}

			return nil
		},
	}
}

func doctorRepairs(cfg *config.Config, u *ui.UI) doctor.Repairs {
	return doctor.Repairs{
		InstallTools: func(ctx context.Context, _ *doctor.Env) error {
			return tools.EnsureUI(ctx, u, cfg.BinDir, tools.EnsureOptions{})
		},
		RefreshKubeconfig: func(context.Context, *doctor.Env) error {
			return kubectl.EnsureCluster(cfg) // rewrites the kubeconfig on API port drift
		},
		PopulateCABundle: func(context.Context, *doctor.Env) error {
			x402verifier.PopulateCABundle(cfg)
			return nil
		},
	}
}

func printDoctor(u *ui.UI, results []doctor.Result, sum doctor.Summary, fixed bool) {
	width := 0
	for _, r := range results {
		width = max(width, len(r.Title))
	}

	group := ""
	fixable := 0

	for _, r := range results {
		if r.Group != group {
			if group != "" {
				u.Blank()
			}

			group = r.Group
			u.Bold(group)
		}

		fix := ""
		if r.Status == doctor.StatusWarn || r.Status == doctor.StatusFail {
			fix = r.Fix
		}

		msg := r.Message
		if r.Fixed {
			msg += " (fixed)"
		}

		u.Check(string(r.Status), r.Title, width, msg, fix)

		if r.Fixable && !r.Fixed {
			fixable++
		}
	}

	u.Blank()

	parts := []string{fmt.Sprintf("%d ok", sum.OK)}
	if sum.Warn > 0 {
		parts = append(parts, plural(sum.Warn, "warning"))
	}

	if sum.Fail > 0 {
		parts = append(parts, plural(sum.Fail, "problem"))
	}

	if sum.Skip > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", sum.Skip))
	}

	u.Print(strings.Join(parts, " · "))

	if fixable > 0 && !fixed {
		u.Dim(fmt.Sprintf("obol doctor --fix can repair %d of these automatically.", fixable))
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	return fmt.Sprintf("%d %ss", n, word)
}
