package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/agentskills"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
)

// agentSkillsCommand manages the skills of stack-managed Hermes agents.
// Built-in skills are always installed; opt-in skills come from the pinned
// ObolNetwork/skills release and are recorded so syncs and cluster
// recreation keep them. Sub-agents (Agent CRs) keep their list in the CR:
// `obol agent update <name> --skills +<skill>`.
func agentSkillsCommand(cfg *config.Config) *cli.Command {
	agentFlag := &cli.StringFlag{
		Name:  "agent",
		Usage: "Stack-managed agent to change",
		Value: agentruntime.DefaultInstanceID,
	}

	return &cli.Command{
		Name:  "skills",
		Usage: "List, add or remove an agent's skills",
		Description: `Built-in skills (stack operations: selling, buying, wallets, networks) are
always installed. Opt-in skills come from ObolNetwork/skills ` + agentskills.Release + `, for example
ethereum-reference (contract addresses, L2s, bridges, gas, standards) and
distributed-validators.

Examples:
  obol agent skills list
  obol agent skills list --available
  obol agent skills add ethereum-reference
  obol agent skills remove ethereum-reference

Sub-agents keep their skills in their Agent resource:
  obol agent update <name> --skills +ethereum-reference`,
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "Show the skills installed on an agent",
				Flags: []cli.Flag{
					agentFlag,
					&cli.BoolFlag{Name: "available", Usage: "List the opt-in skills you can add (downloads the pinned release)"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agentSkillsList(cfg, cmd)
				},
			},
			{
				Name:      "add",
				Usage:     "Install opt-in skills on an agent",
				ArgsUsage: "<skill> [skill...]",
				Flags:     []cli.Flag{agentFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agentSkillsChange(cfg, cmd, true)
				},
			},
			{
				Name:      "remove",
				Usage:     "Remove opt-in skills from an agent",
				ArgsUsage: "<skill> [skill...]",
				Flags:     []cli.Flag{agentFlag},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return agentSkillsChange(cfg, cmd, false)
				},
			},
		},
	}
}

type agentSkill struct {
	Name   string `json:"name"`
	Source string `json:"source"` // built-in | opt-in
}

func agentSkillsList(cfg *config.Config, cmd *cli.Command) error {
	u := getUI(cmd)

	if cmd.Bool("available") {
		names, err := agentskills.Available(cfg)
		if err != nil {
			return err
		}

		if u.IsJSON() {
			if names == nil {
				names = []string{}
			}

			return u.JSON(names)
		}

		u.Bold("Opt-in skills in ObolNetwork/skills " + agentskills.Release)

		for _, n := range names {
			u.Print("  " + n)
		}

		u.Blank()
		u.Dim("Install with: obol agent skills add <skill>")
		u.Dim("Some are written for Claude Code outside the stack (run-obol-stack, dvpod…); they work in an agent but add little.")

		return nil
	}

	agent := cmd.String("agent")

	added, err := agentskills.Added(cfg, agent)
	if err != nil {
		return err
	}

	skills := make([]agentSkill, 0, len(added)+10)
	for _, n := range agentskills.Embedded() {
		skills = append(skills, agentSkill{Name: n, Source: "built-in"})
	}

	for _, n := range added {
		skills = append(skills, agentSkill{Name: n, Source: "opt-in"})
	}

	if u.IsJSON() {
		return u.JSON(skills)
	}

	u.Bold("Skills on " + agent)

	for _, s := range skills {
		u.Printf("  %-26s %s", s.Name, s.Source)
	}

	if len(added) == 0 {
		u.Blank()
		u.Dim("No opt-in skills. See what's available: obol agent skills list --available")
	}

	return nil
}

func agentSkillsChange(cfg *config.Config, cmd *cli.Command, add bool) error {
	u := getUI(cmd)
	agent := cmd.String("agent")
	names := cmd.Args().Slice()

	if len(names) == 0 {
		return errors.New("name at least one skill (see 'obol agent skills list --available')")
	}

	current, err := agentskills.Added(cfg, agent)
	if err != nil {
		return err
	}

	next := slices.Clone(current)

	for _, n := range names {
		n = strings.TrimSpace(n)

		switch {
		case agentskills.IsEmbedded(n) && add:
			u.Dim(n + " is built in and always installed")
		case agentskills.IsEmbedded(n):
			return fmt.Errorf("%s is built in and can't be removed", n)
		case add && !slices.Contains(next, n):
			next = append(next, n)
		case !add:
			next = slices.DeleteFunc(next, func(s string) bool { return s == n })
		}
	}

	if slices.Equal(next, current) {
		u.Info("Nothing to change")
		return nil
	}

	// Resolve and download before recording, so a typo or an offline host
	// leaves the record untouched.
	if add {
		probe, err := agentskills.Available(cfg)
		if err != nil {
			return err
		}

		for _, n := range next {
			if !slices.Contains(probe, n) {
				return fmt.Errorf("skill %q is not in ObolNetwork/skills %s (see 'obol agent skills list --available')", n, agentskills.Release)
			}
		}
	}

	if err := agentskills.SetAdded(cfg, agent, next); err != nil {
		return err
	}

	if err := hermes.SyncSkills(cfg, agent, u); err != nil {
		u.Warnf("Recorded; it installs on the next 'obol stack up' (%v)", err)
		return nil
	}

	verb := "Removed"
	if add {
		verb = "Added"
	}

	u.Successf("%s %s on %s; new conversations pick it up", verb, strings.Join(names, ", "), agent)

	return nil
}
