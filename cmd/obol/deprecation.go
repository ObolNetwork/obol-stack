package main

import (
	"context"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// deprecationRemovalVersion is the release that removes everything below
// (OpenClaw, `obol domain`, `tunnel login` / `--management local`).
const deprecationRemovalVersion = "v0.16"

// deprecatedUsageSuffix marks deprecated commands in help listings.
const deprecatedUsageSuffix = " (deprecated)"

const (
	deprecationOpenClaw    = "openclaw"
	deprecationDomain      = "domain"
	deprecationTunnelLocal = "tunnel-local"
)

// deprecationMessages maps a deprecation key to the lines printed for it.
var deprecationMessages = map[string][]string{
	deprecationOpenClaw: openclaw.DeprecationNotice,
	deprecationDomain: {
		"'obol domain' is deprecated and will be removed in " + deprecationRemovalVersion + ".",
		"Register or transfer the domain in the Cloudflare dashboard, then run: obol tunnel setup",
	},
	deprecationTunnelLocal: {
		"'obol tunnel login' and 'tunnel setup --management local' are deprecated and will be removed in " + deprecationRemovalVersion + ".",
		"Create the tunnel in the Cloudflare dashboard, then run: obol tunnel setup <connector-token>",
	},
}

// warnDeprecated prints the deprecation warning for key on stderr, at most
// once per invocation (state lives on the root command, so it is
// per-process in the CLI and per-tree in tests). ui.Warn always writes to
// stderr, so JSON on stdout is never polluted.
func warnDeprecated(cmd *cli.Command, key string) {
	root := cmd.Root()
	if root.Metadata == nil {
		root.Metadata = map[string]any{}
	}

	seen, _ := root.Metadata["deprecations"].(map[string]bool)
	if seen == nil {
		seen = map[string]bool{}
		root.Metadata["deprecations"] = seen
	}

	if seen[key] {
		return
	}

	seen[key] = true

	warnDeprecation(getUI(cmd), key)
}

func warnDeprecation(u *ui.UI, key string) {
	for i, line := range deprecationMessages[key] {
		if i == 0 {
			line = "Deprecated: " + line
		} else {
			line = "  " + line
		}

		u.Warn(line)
	}
}

// deprecatedBefore returns a Before hook that warns once and continues.
func deprecatedBefore(key string) cli.BeforeFunc {
	return func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
		warnDeprecated(cmd, key)
		return ctx, nil
	}
}

// warnIfOpenClawRuntime is the --runtime flag action on agent commands.
func warnIfOpenClawRuntime(_ context.Context, cmd *cli.Command, value string) error {
	if strings.EqualFold(strings.TrimSpace(value), "openclaw") {
		warnDeprecated(cmd, deprecationOpenClaw)
	}

	return nil
}

// warnIfLocalTunnelManagement warns when tunnel setup uses the deprecated
// browser-login (locally-managed) path.
func warnIfLocalTunnelManagement(cmd *cli.Command, management string) {
	if strings.EqualFold(strings.TrimSpace(management), "local") {
		warnDeprecated(cmd, deprecationTunnelLocal)
	}
}
