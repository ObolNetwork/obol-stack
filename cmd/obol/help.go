package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// Root help is generated from the command tree: each top-level command is
// assigned a Category below and rendered in commandCategoryOrder. Commands
// without a category (e.g. urfave's built-in help) land in "Other", so a new
// command can never silently vanish from `obol --help`.
const (
	catStack    = "Stack"
	catAgents   = "Agents"
	catCommerce = "Commerce"
	catModels   = "Models"
	catNetworks = "Networks"
	catApps     = "Apps"
	catTunnel   = "Tunnel & domains"
	catTools    = "Kubernetes tools (passthrough, auto KUBECONFIG)"
	catOther    = "Other"
)

var commandCategoryOrder = []string{
	catStack, catAgents, catCommerce, catModels, catNetworks, catApps, catTunnel, catTools, catOther,
}

var topLevelCategories = map[string]string{
	"stack":    catStack,
	"agent":    catAgents,
	"wallet":   catAgents,
	"hermes":   catAgents,
	"openclaw": catAgents,
	"sell":     catCommerce,
	"buy":      catCommerce,
	"model":    catModels,
	"network":  catNetworks,
	"app":      catApps,
	"tunnel":   catTunnel,
	"domain":   catTunnel,
	"kubectl":  catTools,
	"helm":     catTools,
	"helmfile": catTools,
	"k9s":      catTools,
	"update":   catOther,
	"upgrade":  catOther,
	"version":  catOther,
}

const categorySection = `{{if .Category}}

CATEGORY:
   {{.Category}}{{end}}`

// exitUsage is the conventional exit status for command-line usage errors.
const exitUsage = 2

var rootHelpTemplate = "\n" + ui.Banner() + "\n\n" + `NAME:
   {{template "helpNameTemplate" .}}

USAGE:
   {{.FullName}} [global options] <command> [command options]{{if .Version}}{{if not .HideVersion}}

VERSION:
   {{.Version}}{{end}}{{end}}

GETTING STARTED:
   obol stack init && obol stack up    Bring up the local stack
   obol model setup                    Connect an LLM provider
   obol sell inference <name> ...      Sell inference with x402 payments

COMMANDS:{{obolCommandGroups .}}{{if .VisibleFlags}}
GLOBAL OPTIONS:{{template "visibleFlagTemplate" .}}
{{end}}
Run '{{.FullName}} <command> --help' for details on any command.
`

// configureCLI wires generated help, did-you-mean suggestions and shell
// completion onto the root command.
func configureCLI(root *cli.Command) {
	cli.RootCommandHelpTemplate = rootHelpTemplate
	// Categories only shape the root listing; drop the per-command
	// "CATEGORY:" section urfave would otherwise print.
	cli.CommandHelpTemplate = strings.Replace(cli.CommandHelpTemplate, categorySection, "", 1)
	cli.SubcommandHelpTemplate = strings.Replace(cli.SubcommandHelpTemplate, categorySection, "", 1)
	cli.HelpPrinter = func(w io.Writer, templ string, data any) {
		cli.HelpPrinterCustom(w, templ, data, map[string]any{
			"obolCommandGroups": renderCommandGroups,
		})
	}

	root.EnableShellCompletion = true
	root.ConfigureShellCompletionCommand = func(c *cli.Command) {
		c.Hidden = false
		c.Category = catOther
		c.Usage = "Print a shell completion script (bash, zsh, fish, pwsh)"
		// urfave's completion command does not parse flags, so `obol
		// completion --help` would be read as a shell named "--help".
		printScript := c.Action
		c.Action = func(ctx context.Context, cmd *cli.Command) error {
			if isHelpRequest(cmd.Args().Slice()) {
				return showPassthroughHelp(cmd)
			}
			return printScript(ctx, cmd)
		}
	}

	for _, c := range root.Commands {
		if c.Category == "" {
			c.Category = topLevelCategories[c.Name]
		}
	}

	walkCommands(root, func(c *cli.Command, _ []string) {
		if len(c.Commands) > 0 && c.CommandNotFound == nil {
			c.CommandNotFound = commandNotFound
		}
	})
}

// commandNotFound reports an unknown subcommand with suggestions and exits
// with a usage-error status (urfave's CommandNotFound cannot return an error).
func commandNotFound(_ context.Context, cmd *cli.Command, name string) {
	getUI(cmd).SuggestCommand(cmd, name)
	cli.OsExiter(exitUsage)
}

// renderCommandGroups renders the root COMMANDS section grouped by category
// in commandCategoryOrder, with one shared column alignment.
func renderCommandGroups(cmd *cli.Command) string {
	groups := map[string][]*cli.Command{}
	width := 0
	for _, c := range cmd.VisibleCommands() {
		cat := c.Category
		if !slices.Contains(commandCategoryOrder, cat) {
			cat = catOther
		}
		groups[cat] = append(groups[cat], c)
		width = max(width, len(strings.Join(c.Names(), ", ")))
	}
	if help := cmd.Command("help"); help != nil && !help.Hidden && !slices.Contains(groups[catOther], help) {
		groups[catOther] = append(groups[catOther], help)
		width = max(width, len(strings.Join(help.Names(), ", ")))
	}

	var b strings.Builder
	for _, cat := range commandCategoryOrder {
		if len(groups[cat]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n   %s:\n", cat)
		for _, c := range groups[cat] {
			fmt.Fprintf(&b, "     %-*s  %s\n", width, strings.Join(c.Names(), ", "), c.Usage)
		}
	}
	return b.String()
}

// walkCommands visits cmd and every descendant depth-first, passing the
// command path (excluding the root name).
func walkCommands(cmd *cli.Command, fn func(c *cli.Command, path []string)) {
	var walk func(c *cli.Command, path []string)
	walk = func(c *cli.Command, path []string) {
		fn(c, path)
		for _, sub := range c.Commands {
			walk(sub, append(slices.Clone(path), sub.Name))
		}
	}
	walk(cmd, nil)
}

// isHelpRequest reports whether a SkipFlagParsing passthrough command was
// invoked as `<cmd> -h|--help`. Only the first argument counts, so help flags
// meant for the wrapped tool (after `--` or a subcommand) still pass through.
func isHelpRequest(args []string) bool {
	return len(args) > 0 && (args[0] == "-h" || args[0] == "--help")
}

// showPassthroughHelp prints the Obol-side help for a passthrough command
// without touching the cluster.
func showPassthroughHelp(cmd *cli.Command) error {
	cli.HelpPrinter(cmd.Root().Writer, cli.CommandHelpTemplate, cmd)
	return nil
}
