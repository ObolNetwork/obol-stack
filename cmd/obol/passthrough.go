package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/passthrough"
	"github.com/urfave/cli/v3"
)

// passthroughNames are top-level commands whose arguments belong to another
// program. obol's global flags placed before them are rejected (see
// checkGlobalFlagsBeforePassthrough).
var passthroughNames = []string{"kubectl", "helm", "helmfile", "k9s", "hermes"}

// stackKubeconfig is the kubeconfig `obol stack up` writes.
func stackKubeconfig(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
}

// passthroughCommand builds `obol <tool> …`: the obol-resolved tool runs with
// the user's argv verbatim (minus one leading "--") and KUBECONFIG pinned to
// the stack kubeconfig (an ambient KUBECONFIG is overridden; only an explicit
// --kubeconfig argument opts out). extraEnv, if non-nil, applies further
// tool-specific env defaults.
//
// Process model (internal/passthrough): when the stack kubeconfig exists, or
// --kubeconfig was passed, obol execs the tool (unix: process replaced, so
// TTY, signals and exit codes are native). When the tool will use a stack
// kubeconfig that does not exist yet (cluster never brought up), it runs as a
// child instead so obol can add an "is the stack up?" hint if it fails —
// cluster-free commands (version --client, helm template, completion) still
// just work.
func passthroughCommand(cfg *config.Config, tool string, extraEnv func(cfg *config.Config, args, env []string) []string) *cli.Command {
	return &cli.Command{
		Name:  tool,
		Usage: "Run " + tool + " with stack kubeconfig (passthrough)",
		Description: fmt.Sprintf(`Runs %[1]s against the Obol Stack. Every argument is passed to %[1]s
unchanged (a single leading '--' is dropped), so 'obol %[1]s --help' shows
%[1]s's own help.

KUBECONFIG always points at the stack kubeconfig, even if KUBECONFIG is set
in your shell; only an explicit --kubeconfig argument overrides it. Put
%[1]s flags after the tool name: 'obol %[1]s ... -o json', not 'obol -o json %[1]s ...'.

To use plain %[1]s (with its own completion and plugins) against the stack:
  eval "$(obol env)"`, tool),
		SkipFlagParsing: true,
		ShellComplete:   passthroughShellComplete(cfg, tool),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := passthrough.StripSeparator(cmd.Args().Slice())
			toolPath, err := passthrough.ResolveTool(cfg.BinDir, tool, os.Stderr)
			if err != nil {
				return err
			}

			kubeconfig := stackKubeconfig(cfg)
			env, usesStack := passthrough.KubeEnv(os.Environ(), args, kubeconfig)
			if extraEnv != nil {
				env = extraEnv(cfg, args, env)
			}

			return passthrough.Handoff(toolPath, args, env, passthrough.StackHint(usesStack, kubeconfig))
		},
	}
}

// helmfileEnv points helmfile at the stack helmfile unless the user set
// HELMFILE_FILE_PATH or passed -f/--file.
func helmfileEnv(cfg *config.Config, args, env []string) []string {
	env, _ = passthrough.DefaultEnv(env, args, "HELMFILE_FILE_PATH",
		filepath.Join(cfg.ConfigDir, "helmfile.yaml"), "-f", "--file")
	return env
}

// checkGlobalFlagsBeforePassthrough rejects `obol -o json kubectl …`: the
// global flag would be consumed by obol and silently do nothing, while the
// user almost certainly meant it for the tool. Flags supplied through their
// OBOL_* environment variables are not an error.
func checkGlobalFlagsBeforePassthrough(root *cli.Command) error {
	tool := root.Args().First()
	if !slices.Contains(passthroughNames, tool) {
		return nil
	}

	var given []string
	for _, f := range root.Flags {
		name := f.Names()[0]
		var sources cli.ValueSourceChain
		switch fl := f.(type) {
		case *cli.BoolFlag:
			sources = fl.Sources
		case *cli.StringFlag:
			sources = fl.Sources
		default:
			continue
		}
		if !root.IsSet(name) {
			continue
		}
		if _, fromEnv := sources.Lookup(); fromEnv {
			continue
		}
		given = append(given, "--"+name)
	}
	if len(given) == 0 {
		return nil
	}

	return fmt.Errorf("obol global flag(s) %s must not come before '%s' (obol would consume them, %s would never see them); "+
		"put %s flags after the tool name: obol %s <args> <flags>",
		strings.Join(given, ", "), tool, tool, tool, tool)
}

// --- shell completion delegation ---------------------------------------

// completionTimeout bounds a delegated `<tool> __complete` call, which may
// query the cluster (resource names).
const completionTimeout = 5 * time.Second

// passthroughShellComplete answers `obol <tool> … <TAB>` by asking the tool
// itself: kubectl, helm, helmfile and k9s are cobra programs and implement
// the hidden `__complete` protocol. Results are re-emitted in urfave's
// "token:description" format, which obol's bash/zsh/fish/pwsh scripts read.
//
// urfave's scripts only pass the word under the cursor when it starts with
// "-". So when the last argument is a flag, it is ambiguous whether the user
// is still typing it or completing its value (`-n <TAB>`); both are asked
// for and the shell filters by the current prefix. Completion after a "--"
// is suppressed by urfave.
func passthroughShellComplete(cfg *config.Config, tool string) cli.ShellCompleteFunc {
	return func(ctx context.Context, cmd *cli.Command) {
		args := passthrough.StripSeparator(cmd.Args().Slice())
		toolPath, err := passthrough.ResolveTool(cfg.BinDir, tool, nil)
		if err != nil {
			return
		}
		env, _ := passthrough.KubeEnv(os.Environ(), args, stackKubeconfig(cfg))
		for _, line := range delegateCompletion(ctx, toolPath, args, env) {
			fmt.Fprintln(cmd.Root().Writer, line)
		}
	}
}

// delegateCompletion runs the cobra `__complete` protocol and returns
// urfave-format completion lines, de-duplicated.
func delegateCompletion(ctx context.Context, toolPath string, args, env []string) []string {
	queries := [][]string{append(slices.Clone(args), "")}
	if n := len(args); n > 0 && strings.HasPrefix(args[n-1], "-") {
		queries = append([][]string{args}, queries...)
	}

	var out []string
	seen := map[string]bool{}
	for _, q := range queries {
		ctx, cancel := context.WithTimeout(ctx, completionTimeout)
		c := exec.CommandContext(ctx, toolPath, append([]string{"__complete"}, q...)...)
		c.Env = env
		c.Stderr = io.Discard
		raw, err := c.Output()
		cancel()
		if err != nil && len(raw) == 0 {
			continue
		}
		for _, line := range parseCobraCompletion(raw) {
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out
}

// parseCobraCompletion converts cobra `__complete` output ("value\tdesc"
// lines, then a ":<directive>" line) into urfave "value:desc" lines.
func parseCobraCompletion(raw []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "_activeHelp_") {
			continue
		}
		if strings.HasPrefix(line, ":") {
			break // directive: end of completions
		}
		value, desc, _ := strings.Cut(line, "\t")
		if desc = strings.TrimSpace(desc); desc != "" {
			out = append(out, value+":"+desc)
		} else {
			out = append(out, value)
		}
	}
	return out
}

// --- obol env ------------------------------------------------------------

func envCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "env",
		Usage: "Print shell exports that point kubectl, helm and k9s at the stack",
		Description: `Prints KUBECONFIG (the stack kubeconfig) and a PATH with the obol bin dir
prepended, so plain kubectl/helm/helmfile/k9s, their own shell completion,
krew plugins and IDEs work against the stack without the obol prefix. Needs
no running cluster: it only prints paths. Re-running is harmless (PATH is
not duplicated).

  eval "$(obol env)"                          sh, bash, zsh
  obol env --shell fish | source              fish
  obol env --shell pwsh | Invoke-Expression   PowerShell
  eval "$(obol env --unset)"                  undo

direnv: add this line to .envrc (direnv evaluates it with bash):
  eval "$(obol env --shell bash)"`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "shell",
				Usage: "Shell syntax: sh, bash, zsh, fish or pwsh (default: detected from SHELL)",
			},
			&cli.BoolFlag{
				Name:  "unset",
				Usage: "Print commands that undo the exports",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			kubeconfig, binDir := absPath(stackKubeconfig(cfg)), absPath(cfg.BinDir)

			if u := getUI(cmd); u.IsJSON() {
				return u.JSON(struct {
					Kubeconfig string `json:"kubeconfig"`
					BinDir     string `json:"bin_dir"`
				}{kubeconfig, binDir})
			}

			shell := cmd.String("shell")
			if shell == "" {
				shell = detectShell()
			}
			script, err := renderEnv(shell, kubeconfig, binDir, cmd.Bool("unset"), os.Getenv("PATH"))
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.Root().Writer, script)
			return err
		},
	}
}

func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// detectShell maps $SHELL to an `obol env` syntax family.
func detectShell() string {
	switch base := strings.TrimSuffix(filepath.Base(os.Getenv("SHELL")), ".exe"); base {
	case "fish":
		return "fish"
	case "pwsh", "powershell":
		return "pwsh"
	case ".", "":
		if runtime.GOOS == "windows" {
			return "pwsh"
		}
	}
	return "sh"
}

// renderEnv renders the export (or unset) script for shell. curPath is the
// current PATH, used to compute the PATH without binDir for sh --unset.
func renderEnv(shell, kubeconfig, binDir string, unset bool, curPath string) (string, error) {
	var b strings.Builder
	switch shell {
	case "sh", "bash", "zsh":
		q := shQuote
		if unset {
			// sh syntax implies a POSIX ':'-separated PATH.
			kept := slices.DeleteFunc(strings.Split(curPath, ":"), func(d string) bool { return d == binDir })
			fmt.Fprintf(&b, "unset KUBECONFIG\nexport PATH=%s\n", q(strings.Join(kept, ":")))
			break
		}
		fmt.Fprintf(&b, "export KUBECONFIG=%s\n", q(kubeconfig))
		fmt.Fprintf(&b, "case \":${PATH}:\" in *%s*) ;; *) export PATH=%s\":${PATH}\" ;; esac\n", q(":"+binDir+":"), q(binDir))
		b.WriteString("# Run this command to configure your shell:\n# eval \"$(obol env)\"\n")
	case "fish":
		q := fishQuote
		if unset {
			fmt.Fprintf(&b, "set -e KUBECONFIG\nset -gx PATH (string match -v -- %s $PATH)\n", q(binDir))
			break
		}
		fmt.Fprintf(&b, "set -gx KUBECONFIG %s\n", q(kubeconfig))
		fmt.Fprintf(&b, "contains -- %s $PATH; or set -gx PATH %s $PATH\n", q(binDir), q(binDir))
		b.WriteString("# Run this command to configure your shell:\n# obol env --shell fish | source\n")
	case "pwsh", "powershell":
		q := pwshQuote
		sep := "[IO.Path]::PathSeparator"
		if unset {
			fmt.Fprintf(&b, "Remove-Item Env:KUBECONFIG -ErrorAction SilentlyContinue\n")
			fmt.Fprintf(&b, "$env:PATH = (($env:PATH -split %s) | Where-Object { $_ -ne %s }) -join %s\n", sep, q(binDir), sep)
			break
		}
		fmt.Fprintf(&b, "$env:KUBECONFIG = %s\n", q(kubeconfig))
		fmt.Fprintf(&b, "if (-not (($env:PATH -split %s) -contains %s)) { $env:PATH = %s + %s + $env:PATH }\n", sep, q(binDir), q(binDir), sep)
		b.WriteString("# Run this command to configure your shell:\n# obol env --shell pwsh | Invoke-Expression\n")
	default:
		return "", errors.New("unsupported --shell " + shell + " (use sh, bash, zsh, fish or pwsh)")
	}
	return b.String(), nil
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func pwshQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
