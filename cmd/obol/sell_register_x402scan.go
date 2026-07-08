package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/x402scan"
	"github.com/urfave/cli/v3"
)

// sellRegisterX402scanCommand registers the storefront's public origin in the
// x402scan.com discovery index. x402scan crawls the origin's /openapi.json
// (which the serviceoffer-controller already publishes with x-payment-info
// per paid operation), live-probes each advertised endpoint for a real 402
// challenge, and lists the ones that pass. Authentication is SIWX: the
// registry's challenge is signed EIP-191 by the agent's remote-signer — the
// CLI never touches key material.
func sellRegisterX402scanCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "x402scan",
		Usage: "Register this storefront's origin in the x402scan discovery index",
		Description: `Submits the storefront's public origin to x402scan.com so agents can
discover its paid services. x402scan reads /openapi.json from the origin and
probes every advertised endpoint for a live x402 402 challenge, so:

  - a public tunnel hostname must be configured (obol tunnel setup);
    x402scan rejects ephemeral quick-tunnel and private/localhost origins
  - at least one offer must be Ready (obol sell status)

The request is authenticated by signing the registry's Sign-In-With-X
challenge with the agent's wallet via the remote-signer. Re-running is safe:
registration is idempotent per origin, and offers that disappeared from the
catalog are deprecated on the x402scan side.

Examples:
  obol sell register x402scan
  obol sell register x402scan --origin https://store.example.com`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "origin",
				Usage: "Public origin to register (auto-detected from the tunnel hostname if not set)",
			},
			&cli.StringFlag{
				Name:   "registry-url",
				Usage:  "x402scan-compatible registry base URL",
				Value:  x402scan.DefaultBaseURL,
				Hidden: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}

			origin, err := resolveX402scanOrigin(cfg, cmd.String("origin"))
			if err != nil {
				return err
			}

			// Non-fatal preflight: x402scan discovers services from the
			// origin's /openapi.json, so a missing/empty doc means the
			// registration will come back "no discovery" or "no valid
			// resources". Warn early with the local fix.
			preflightOpenAPI(ctx, u, origin)

			// Same signer posture as ERC-8004 registration: all signing via
			// the agent's remote-signer, no key material in the CLI.
			if _, err := hermes.ResolveWalletAddress(cfg); err != nil {
				return fmt.Errorf("no Hermes remote-signer wallet found: %w\n\n  Run 'obol agent init' first, or 'obol wallet import --private-key-file <file>' to seed a specific key", err)
			}
			signerNS, err := hermes.ResolveInstanceNamespace(cfg)
			if err != nil {
				return fmt.Errorf("resolve Hermes instance namespace: %w", err)
			}
			pf, err := startSignerPortForward(cfg, signerNS)
			if err != nil {
				return fmt.Errorf("port-forward to remote-signer: %w", err)
			}
			defer pf.Stop()

			signer := erc8004.NewRemoteSigner(fmt.Sprintf("http://localhost:%d", pf.localPort))
			addr, err := signer.GetAddress(ctx)
			if err != nil {
				return err
			}

			u.Infof("Registering %s with x402scan...", origin)
			u.Dim(fmt.Sprintf("  Signing as agent wallet %s", addr.Hex()))

			client := x402scan.NewClient(cmd.String("registry-url"))
			var result *x402scan.RegisterResult
			err = u.RunWithSpinner("x402scan is crawling and probing the origin", func() error {
				var regErr error
				result, regErr = client.RegisterOrigin(ctx, origin, addr, signer)
				return regErr
			})
			if err != nil {
				switch {
				case errors.Is(err, x402scan.ErrNoDiscovery):
					u.Error(err.Error())
					u.Dim("  x402scan could not read a discovery document from the origin.")
					u.Dim(fmt.Sprintf("  Check that %s/openapi.json is publicly reachable and the tunnel is up (obol tunnel status).", origin))
				case errors.Is(err, x402scan.ErrNoValidResources):
					u.Error(err.Error())
					u.Dim("  A discovery document was found, but no endpoint answered with a live x402 402 challenge.")
					u.Dim("  Check offers are Ready (obol sell status) and publicly reachable through the tunnel.")
				}
				return err
			}

			if u.IsJSON() {
				return u.JSON(result)
			}

			u.Successf("Registered %d/%d resources on x402scan (source: %s)", result.Registered, result.Total, result.Source)
			if result.SIWX > 0 {
				u.Printf("  Identity-only (SIWX) resources: %d", result.SIWX)
			}
			if result.Deprecated > 0 {
				u.Printf("  Deprecated stale resources:     %d", result.Deprecated)
			}
			for _, f := range result.FailedList {
				u.Warnf("failed probe: %s — %s", f.URL, f.Error)
			}
			if result.Warning != "" {
				u.Warn(result.Warning)
			}
			if result.ContactEmail != "" {
				u.Dim("  Contact email on record: " + result.ContactEmail)
			}
			u.Blank()
			u.Dim("Browse the index: https://x402scan.com/resources")
			return nil
		},
	}
}

// resolveX402scanOrigin picks the public origin to register and rejects
// origins x402scan is known to refuse (local hostnames, ephemeral
// quick-tunnel domains) with actionable errors instead of a remote 4xx.
func resolveX402scanOrigin(cfg *config.Config, explicit string) (string, error) {
	origin := strings.TrimSpace(explicit)
	if origin == "" {
		base, err := sellerBaseURL(cfg)
		if err != nil {
			return "", fmt.Errorf("resolve public origin: %w (pass --origin explicitly)", err)
		}
		origin = base
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid origin %q: expected https://<hostname>", origin)
	}
	host := parsed.Hostname()
	switch {
	case host == "obol.stack" || host == "localhost" || host == "127.0.0.1":
		return "", errors.New("no public hostname configured — x402scan must be able to reach the storefront from the internet.\n\n  Set up a permanent tunnel hostname first: obol tunnel setup\n  Then re-run, or pass --origin https://<your-hostname>")
	case strings.HasSuffix(host, ".trycloudflare.com"):
		return "", errors.New("the current tunnel is an ephemeral quick-tunnel (*.trycloudflare.com), which x402scan rejects.\n\n  Register a permanent hostname on your own domain (obol tunnel setup, see also obol domain), then re-run")
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("origin %s must be https:// — x402scan only indexes TLS origins", origin)
	}
	// Registration is per-origin; strip any path so what we submit matches
	// what the registry crawls.
	return parsed.Scheme + "://" + parsed.Host, nil
}

// preflightOpenAPI warns (never fails) when the origin's /openapi.json is
// unreachable or advertises no operations — the two states that make the
// remote registration a guaranteed no-op.
func preflightOpenAPI(ctx context.Context, u *ui.UI, origin string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/openapi.json", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		u.Warnf("could not fetch %s/openapi.json (%v) — x402scan discovers services from it; check the tunnel is up", origin, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		u.Warnf("%s/openapi.json returned HTTP %d — x402scan discovers services from it; check the tunnel is up", origin, resp.StatusCode)
		return
	}
	var doc struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return
	}
	if len(doc.Paths) == 0 {
		u.Warn("the published /openapi.json advertises no operations — put at least one offer on sale (obol sell inference|http|agent) before registering")
		return
	}
	// x402scan indexes per ORIGIN — it crawls this one /openapi.json and
	// lists everything it finds under the origin we submit. On the shared
	// /services/<name> origin model, that means every offer collapses into
	// one mixed listing (a data API next to a chat agent next to a dataset),
	// which reads as a single blurry product to discovery crawlers. Count
	// the distinct offers and warn: the clean shape is one origin per offer.
	offers := map[string]struct{}{}
	for p := range doc.Paths {
		if name := servicesOfferName(p); name != "" {
			offers[name] = struct{}{}
		}
	}
	if len(offers) > 1 {
		u.Warnf("this origin advertises %d offers in one /openapi.json — x402scan indexes per origin, so all of them will be listed together under %s rather than as distinct products. For clean discovery, give each offer its own subdomain (obol tunnel hostname add <host>) and register that origin.", len(offers), origin)
	}
}

// servicesOfferName extracts the offer name from a shared-origin OpenAPI path
// key like "/services/foo/v1/chat/completions" → "foo". Returns "" for paths
// that aren't under /services/ — a per-offer subdomain roots its paths
// elsewhere, so it correctly never trips the multi-offer warning.
func servicesOfferName(path string) string {
	const prefix = "/services/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}
