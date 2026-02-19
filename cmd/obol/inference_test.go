package main

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/inference"
	"github.com/urfave/cli/v2"
)

func newInferenceTestApp(cfgDir string) *cli.App {
	cfg := &config.Config{ConfigDir: cfgDir}
	return &cli.App{
		Name:      "obol",
		Commands:  []*cli.Command{inferenceCommand(cfg)},
		Writer:    io.Discard,
		ErrWriter: io.Discard,
	}
}

func TestInferenceDeployHelpDoesNotPanic(t *testing.T) {
	app := newInferenceTestApp(t.TempDir())
	if err := app.Run([]string{"obol", "inference", "deploy", "--help"}); err != nil {
		t.Fatalf("deploy help should not fail: %v", err)
	}
}

func TestInferenceServeHelpDoesNotPanic(t *testing.T) {
	app := newInferenceTestApp(t.TempDir())
	if err := app.Run([]string{"obol", "inference", "serve", "--help"}); err != nil {
		t.Fatalf("serve help should not fail: %v", err)
	}
}

func TestInferenceServeRequiresWallet(t *testing.T) {
	app := newInferenceTestApp(t.TempDir())
	err := app.Run([]string{"obol", "inference", "serve"})
	if err == nil {
		t.Fatal("expected serve to fail without --wallet")
	}
	if !strings.Contains(err.Error(), "--wallet") {
		t.Fatalf("expected wallet requirement error, got: %v", err)
	}
}

func deployContext(t *testing.T, args ...string) *cli.Context {
	t.Helper()

	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	for _, f := range deployFlags() {
		if err := f.Apply(fs); err != nil {
			t.Fatalf("apply flag: %v", err)
		}
	}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cli.NewContext(&cli.App{}, fs, nil)
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
