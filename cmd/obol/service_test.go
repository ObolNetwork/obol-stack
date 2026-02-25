package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/urfave/cli/v3"
)

func newServiceTestApp(cfgDir string) *cli.Command {
	cfg := &config.Config{ConfigDir: cfgDir}
	return &cli.Command{
		Name:      "obol",
		Commands:  []*cli.Command{serviceCommand(cfg)},
		Writer:    io.Discard,
		ErrWriter: io.Discard,
	}
}

func TestServiceDeployHelpDoesNotPanic(t *testing.T) {
	app := newServiceTestApp(t.TempDir())
	if err := app.Run(context.Background(), []string{"obol", "service", "deploy", "--help"}); err != nil {
		t.Fatalf("deploy help should not fail: %v", err)
	}
}

func TestServiceServeHelpDoesNotPanic(t *testing.T) {
	app := newServiceTestApp(t.TempDir())
	if err := app.Run(context.Background(), []string{"obol", "service", "serve", "--help"}); err != nil {
		t.Fatalf("serve help should not fail: %v", err)
	}
}

func TestServiceServeRequiresWallet(t *testing.T) {
	app := newServiceTestApp(t.TempDir())
	err := app.Run(context.Background(), []string{"obol", "service", "serve"})
	if err == nil {
		t.Fatal("expected serve to fail without --wallet")
	}
	if !strings.Contains(err.Error(), "--wallet") {
		t.Fatalf("expected wallet requirement error, got: %v", err)
	}
}

// deployContext runs a temporary cli.Command with deployFlags() and the given
// args, then returns the parsed *cli.Command so callers can inspect flag values.
func deployContext(t *testing.T, args ...string) *cli.Command {
	t.Helper()

	var captured *cli.Command
	cmd := &cli.Command{
		Name:  "deploy",
		Flags: deployFlags(),
		Action: func(ctx context.Context, c *cli.Command) error {
			captured = c
			return nil
		},
	}
	fullArgs := append([]string{"deploy"}, args...)
	if err := cmd.Run(context.Background(), fullArgs); err != nil {
		t.Fatalf("run: %v", err)
	}
	if captured == nil {
		t.Fatal("action was not invoked")
	}
	return captured
}

func TestApplyFlagsOnlyMutatesExplicitlySetFlags(t *testing.T) {
	original := &inference.Deployment{
		Name:            "keep-values",
		EnclaveTag:      "com.obol.inference.keep-values",
		ListenAddr:      ":9999",
		UpstreamURL:     "http://example.local:1234",
		WalletAddress:   "0xabc",
		PricePerRequest: "1.5",
		Chain:           "polygon",
		FacilitatorURL:  "https://example.facilitator",
	}

	unchanged := *original
	applyFlags(deployContext(t), &unchanged)
	if unchanged != *original {
		t.Fatalf("applyFlags changed values without explicit flags:\nwant: %+v\ngot:  %+v", *original, unchanged)
	}

	changed := *original
	applyFlags(deployContext(t, "--price", "0.42", "--chain", "base-sepolia"), &changed)
	if changed.PricePerRequest != "0.42" {
		t.Fatalf("price not updated: got %q", changed.PricePerRequest)
	}
	if changed.Chain != "base-sepolia" {
		t.Fatalf("chain not updated: got %q", changed.Chain)
	}
	if changed.ListenAddr != original.ListenAddr || changed.UpstreamURL != original.UpstreamURL || changed.FacilitatorURL != original.FacilitatorURL {
		t.Fatalf("unrelated fields should be unchanged:\nwant: %+v\ngot:  %+v", *original, changed)
	}
}
