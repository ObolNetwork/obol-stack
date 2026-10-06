package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// globalFlagNames returns every name and alias reserved across commands: the
// persistent global flags, urfave's --help/-h, and -v. urfave's --version is
// root-local (so `app install --version <chart-version>` is fine), but `-v`
// reads as "version" to users, so no subcommand may reuse it.
func globalFlagNames(root *cli.Command) []string {
	names := []string{}
	for _, f := range root.Flags {
		names = append(names, f.Names()...)
	}
	names = append(names, cli.HelpFlag.Names()...)
	names = append(names, "v")
	return names
}

// visibleCommands yields every non-hidden command below root whose ancestors
// are visible too, with its path.
func visibleCommands(root *cli.Command, fn func(c *cli.Command, path []string)) {
	var walk func(c *cli.Command, path []string)
	walk = func(c *cli.Command, path []string) {
		for _, sub := range c.Commands {
			if sub.Hidden {
				continue
			}
			p := append(slices.Clone(path), sub.Name)
			fn(sub, p)
			walk(sub, p)
		}
	}
	walk(root, nil)
}

func flagUsage(f cli.Flag) string {
	if df, ok := f.(cli.DocGenerationFlag); ok {
		return df.GetUsage()
	}
	return ""
}

func flagHidden(f cli.Flag) bool {
	vf, ok := f.(cli.VisibleFlag)
	return ok && !vf.IsVisible()
}

// TestCommandTree_HelpHygiene walks the whole command tree and enforces the
// help conventions that previously drifted (see fix/cli-polish).
func TestCommandTree_HelpHygiene(t *testing.T) {
	root := newRootCommand(newTestConfig(t))
	globals := globalFlagNames(root)

	visibleCommands(root, func(c *cli.Command, path []string) {
		name := "obol " + strings.Join(path, " ")

		if strings.TrimSpace(c.Usage) == "" {
			t.Errorf("%s: empty Usage", name)
		}
		if strings.HasSuffix(c.Usage, ".") {
			t.Errorf("%s: command Usage should not end with a period: %q", name, c.Usage)
		}

		for _, f := range c.Flags {
			flagName := "--" + f.Names()[0]
			usage := flagUsage(f)

			// Hidden flags are still parsed, so they must not shadow globals.
			for _, n := range f.Names() {
				if slices.Contains(globals, n) {
					t.Errorf("%s %s: name/alias %q clashes with a global flag", name, flagName, n)
				}
			}

			if flagHidden(f) {
				continue
			}
			if strings.TrimSpace(usage) == "" {
				t.Errorf("%s %s: empty flag Usage", name, flagName)
			}
			// urfave renders the first `backticked` word as the value
			// placeholder, mangling the help line.
			if strings.Contains(usage, "`") {
				t.Errorf("%s %s: backtick in flag Usage renders as a placeholder: %q", name, flagName, usage)
			}
			// One-sentence usages carry no trailing period; multi-sentence
			// ones may.
			if strings.HasSuffix(usage, ".") && !strings.Contains(strings.TrimSuffix(usage, "."), ". ") {
				t.Errorf("%s %s: single-sentence flag Usage should not end with a period: %q", name, flagName, usage)
			}
		}
	})
}

// TestRootHelp_ListsEveryCommand guards against the hand-maintained help
// template regression: every visible top-level command must be in root help
// and carry a category.
func TestRootHelp_ListsEveryCommand(t *testing.T) {
	root := newRootCommand(newTestConfig(t))
	var out bytes.Buffer
	root.Writer = &out

	if err := root.Run(context.Background(), []string{"obol", "--help"}); err != nil {
		t.Fatalf("obol --help: %v", err)
	}
	help := out.String()

	for _, c := range root.Commands {
		if c.Hidden {
			continue
		}
		if !strings.Contains(help, "\n     "+c.Name+" ") && !strings.Contains(help, "\n     "+c.Name+",") {
			t.Errorf("root help does not list %q", c.Name)
		}
		if c.Name != "help" && c.Name != "completion" && !slices.Contains(commandCategoryOrder, c.Category) {
			t.Errorf("top-level command %q has no known category (got %q); add it to topLevelCategories", c.Name, c.Category)
		}
	}
	for _, want := range []string{"completion", "Stack:", "Commerce:", "GLOBAL OPTIONS:"} {
		if !strings.Contains(help, want) {
			t.Errorf("root help missing %q", want)
		}
	}
	// The old template advertised Hermes subcommands that obol never had.
	if strings.Contains(help, "hermes skills") || strings.Contains(help, "hermes chat") {
		t.Error("root help must not advertise native Hermes subcommands as obol commands")
	}
}

// TestUnknownCommand_ExitsWithUsageError covers root and nested groups.
func TestUnknownCommand_ExitsWithUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"obol", "stak"},
		{"obol", "stack", "bogus"},
		{"obol", "openclaw", "skills", "nope"},
	} {
		root := newRootCommand(newTestConfig(t))
		root.Writer = &bytes.Buffer{}

		code := -1
		prev := cli.OsExiter
		cli.OsExiter = func(c int) { code = c }
		_ = root.Run(context.Background(), args)
		cli.OsExiter = prev

		if code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d", args, code, exitUsage)
		}
	}
}

// TestPassthroughHelp_NoCluster ensures --help on SkipFlagParsing passthrough
// commands prints Obol's help instead of resolving an instance (which fails
// without a cluster).
func TestPassthroughHelp_NoCluster(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"obol", "hermes", "--help"}, "obol hermes -- --help"},
		{[]string{"obol", "hermes", "-h"}, "native Hermes CLI"},
		{[]string{"obol", "openclaw", "cli", "--help"}, "obol openclaw cli -- --help"},
		{[]string{"obol", "openclaw", "skills", "add", "--help"}, "<package-or-path>"},
		{[]string{"obol", "openclaw", "skills", "remove", "-h"}, "<skill-name>"},
		{[]string{"obol", "completion", "--help"}, "shell completion"},
	} {
		root := newRootCommand(newTestConfig(t))
		var out bytes.Buffer
		root.Writer = &out

		if err := root.Run(context.Background(), tc.args); err != nil {
			t.Errorf("%v: unexpected error %v", tc.args, err)
			continue
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("%v: help output missing %q:\n%s", tc.args, tc.want, out.String())
		}
	}
}

func TestIsHelpRequest(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{[]string{"--", "--help"}, false},
		{[]string{"chat", "--help"}, false},
	} {
		if got := isHelpRequest(tc.args); got != tc.want {
			t.Errorf("isHelpRequest(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
