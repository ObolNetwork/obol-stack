package main

// obol sell skill — sell a skill (SKILL.md + scripts bundle) as one
// sellable + ratable unit.
//
// Pack the skill directory into a deterministic gzipped tarball, store
// it in a ConfigMap, and publish a ServiceOffer of type=skill. The
// serviceoffer-controller renders a restricted-PSS busybox bundle server
// from the ConfigMap and gates /services/<name>/* behind x402; buyers
// download bundle.tar.gz with a one-shot paid request and can verify the
// sha256 offline and against the seller's ERC-8004 metadata anchor.
//
// To sell a skill's *execution* rather than its bytes, gate the agent
// that carries it with the existing agent path: `obol agent new <name>
// --skills <skill>` then `obol sell agent <name>`.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/embed"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/skillpkg"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/validate"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
	"github.com/urfave/cli/v3"
)

// skillBundleConfigMapSuffix names the operator-owned ConfigMap that
// carries the gzipped bundle bytes: "<offer>-skill-bundle". Distinct
// from monetizeapi.SkillBundleWorkloadName ("so-<offer>-bundle"), which
// names the controller-rendered bundle-server children.
const skillBundleConfigMapSuffix = "-skill-bundle"

var (
	// skillNameRe mirrors the CRD pattern on spec.skill.name.
	skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	// skillVersionRe mirrors the CRD pattern on spec.skill.version.
	skillVersionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func skillBundleConfigMapName(offerName string) string {
	return offerName + skillBundleConfigMapSuffix
}

func sellSkillCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "skill",
		Usage:     "Sell a skill bundle (SKILL.md + scripts) as a paid download",
		ArgsUsage: "<name>",
		Description: `Packages a skill directory into a deterministic gzipped tarball and
publishes it behind an x402 payment gate as a ServiceOffer of
type=skill. The bundle's sha256 is pinned in the offer, surfaced in the
402 response (extra.skill), and can be anchored on the ERC-8004 Identity
Registry with ` + "`obol skills calldata set-hash`" + `.

To sell a skill's execution rather than its bytes, gate the agent that
carries it: ` + "`obol agent new <name> --skills <skill>`" + ` then
` + "`obol sell agent <name>`" + `.

Examples:
  obol sell skill quant-notes --from ./skills/quant-notes --skill-version 0.1.0 \
    --per-request 0.25 --chain base --pay-to 0x...
  obol sell skill buy-x402 --from-embedded buy-x402 --skill-version 0.1.0 --price 0.05`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "from",
				Usage: "Directory containing the skill to package (must contain SKILL.md)",
			},
			&cli.StringFlag{
				Name:  "from-embedded",
				Usage: "Name of an embedded obol skill to package (mutually exclusive with --from)",
			},
			&cli.StringFlag{
				Name:  "skill-name",
				Usage: "Skill name for the <name>@<version> ref (default: the embedded skill name with --from-embedded, otherwise the offer name)",
			},
			&cli.StringFlag{
				Name:     "skill-version",
				Usage:    "Skill version for the <name>@<version> ref (e.g. 0.1.0)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "display-name",
				Usage: "Human-friendly display name for catalog surfaces",
			},
			&cli.StringFlag{
				Name:    "description",
				Aliases: []string{"register-description"},
				Usage:   "Human-readable description. Surfaced on the 402 payment page, in the storefront catalog, and on the ERC-8004 registration document.",
			},
			payToFlag("Payment recipient address"),
			&cli.StringFlag{
				Name:  "chain",
				Usage: "Payment chain (base, base-sepolia, ethereum)",
				Value: "base",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "Payment token (USDC, OBOL)",
				Value: "USDC",
			},
			&cli.StringFlag{
				Name:  "price",
				Usage: "Per-request price in the selected payment token (one paid request = one bundle download)",
			},
			&cli.StringFlag{
				Name:  "per-request",
				Usage: "Per-request price (alias for --price)",
			},
			&cli.StringFlag{
				Name:  "path",
				Usage: "URL path prefix (default: /services/<name>)",
			},
			&cli.IntFlag{
				Name:  "max-timeout",
				Usage: "Payment validity window in seconds",
				Value: 300,
			},
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Namespace for the ServiceOffer AND the bundle ConfigMap (must match — the controller reads the ConfigMap from the offer's namespace)",
				Value:   "default",
			},
			&cli.BoolFlag{
				Name:  "no-register",
				Usage: "Skip ERC-8004 registration metadata. Useful for local dev.",
			},
			&cli.StringFlag{
				Name:  "register-name",
				Usage: "Agent name for ERC-8004 registration (defaults to the offer name)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if cmd.NArg() != 1 {
				return fmt.Errorf("offer name required: obol sell skill <name> (--from <dir> | --from-embedded <skill>)")
			}
			name := strings.TrimSpace(cmd.Args().First())
			if err := validate.Name(name); err != nil {
				return err
			}

			version := strings.TrimSpace(cmd.String("skill-version"))
			if !skillVersionRe.MatchString(version) || len(version) > 64 {
				return fmt.Errorf("invalid --skill-version %q: must match %s (max 64 chars), e.g. 0.1.0", version, skillVersionRe)
			}

			price := strings.TrimSpace(cmd.String("price"))
			if price == "" {
				price = strings.TrimSpace(cmd.String("per-request"))
			}
			if price == "" {
				return fmt.Errorf("price required: use --price or --per-request (skills are priced per request — one paid request, one download)")
			}

			return runSellSkillShare(ctx, cfg, u, cmd, name, version, price)
		},
	}
}

// runSellSkillShare is SHARE mode: pack → ConfigMap → type=skill offer.
func runSellSkillShare(_ context.Context, cfg *config.Config, u *ui.UI, cmd *cli.Command, name, version, price string) error {
	from := strings.TrimSpace(cmd.String("from"))
	fromEmbedded := strings.TrimSpace(cmd.String("from-embedded"))
	if err := validateSkillSourceFlags(from, fromEmbedded); err != nil {
		return err
	}

	srcDir := from
	skillName := name
	if fromEmbedded != "" {
		dir, cleanup, err := materializeEmbeddedSkill(fromEmbedded)
		if err != nil {
			return err
		}
		defer cleanup()
		srcDir = dir
		skillName = fromEmbedded
	}
	if override := strings.TrimSpace(cmd.String("skill-name")); override != "" {
		skillName = override
	}
	if !skillNameRe.MatchString(skillName) || len(skillName) > 64 {
		return fmt.Errorf("invalid skill name %q: must match %s (max 64 chars); pass --skill-name to override", skillName, skillNameRe)
	}

	if info, err := os.Stat(srcDir); err != nil || !info.IsDir() {
		return fmt.Errorf("--from %q is not a readable directory", srcDir)
	}

	// Pack deterministically. Pack enforces the post-gzip size cap
	// (monetizeapi.MaxSkillBundleBytes) and the SKILL.md requirement.
	gz, hash, err := skillpkg.Pack(os.DirFS(srcDir))
	if err != nil {
		return err
	}
	// Warn-only secret scan: the bundle is published verbatim to every
	// buyer, so surface anything that smells like a credential.
	if warnings, scanErr := skillpkg.ScanSecrets(os.DirFS(srcDir)); scanErr == nil {
		for _, w := range warnings {
			u.Warnf("bundle content: %s", w)
		}
	} else {
		u.Warnf("bundle secret scan failed (publishing anyway — inspect the bundle yourself): %v", scanErr)
	}

	if err := kubectl.EnsureCluster(cfg); err != nil {
		return fmt.Errorf("Obol Stack is not running. Start it with `obol stack up` first")
	}

	ns := cmd.String("namespace")

	// Crypto payment resolution — same branch as `sell http` (card
	// payments are deliberately not offered on sell skill v0).
	wallet := strings.TrimSpace(cmd.String("pay-to"))
	if wallet == "" {
		if resolved, rerr := hermes.ResolveWalletAddress(cfg); rerr == nil {
			wallet = resolved
			u.Infof("Using wallet from remote-signer: %s", wallet)
		} else if u.IsTTY() {
			var inputErr error
			wallet, inputErr = u.Input("Wallet address (payment recipient)", "")
			if inputErr != nil || wallet == "" {
				return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
			}
		} else {
			return fmt.Errorf("recipient required: use --pay-to <addr> or set X402_WALLET")
		}
	}
	if err := x402verifier.ValidateWallet(wallet); err != nil {
		return err
	}
	x402verifier.PopulateCABundle(cfg)

	chainName := cmd.String("chain")
	assetTerms, err := resolveAssetTerms(cmd, &chainName)
	if err != nil {
		return err
	}
	symbol := assetTerms.Symbol
	if symbol == "" {
		symbol = strings.ToUpper(cmd.String("token"))
	}

	// Registration block: same builder as `sell http`, with the skill
	// surfaced for discovery plus integrity metadata for ERC-8004.
	reg, registerEnabled, err := buildSellRegistrationConfig(name, sellRegistrationInput{
		NoRegister:  cmd.Bool("no-register"),
		Name:        cmd.String("register-name"),
		Description: cmd.String("description"),
		Skills:      []string{skillName},
	})
	if err != nil {
		return err
	}
	if registerEnabled {
		reg["metadata"] = map[string]string{
			"skillName":    skillName,
			"skillVersion": version,
			"skillSha256":  hash,
		}
	} else {
		reg = nil
	}

	bundleCM := buildSkillBundleConfigMapManifest(skillBundleConfigMapName(name), ns, gz)
	offer := buildSkillShareOfferManifest(skillShareOfferInputs{
		OfferName:       name,
		Namespace:       ns,
		SkillName:       skillName,
		Version:         version,
		SHA256:          hash,
		BundleConfigMap: skillBundleConfigMapName(name),
		DisplayName:     strings.TrimSpace(cmd.String("display-name")),
		Description:     strings.TrimSpace(cmd.String("description")),
		PayTo:           wallet,
		Chain:           chainName,
		Price:           price,
		MaxTimeout:      cmd.Int("max-timeout"),
		AssetTerms:      assetTerms,
		Path:            strings.TrimSpace(cmd.String("path")),
		Registration:    reg,
	})

	if err := preflightOfferPathCollision(cfg, offer); err != nil {
		return err
	}

	// The bundle ConfigMap MUST go through server-side apply: client-
	// side apply copies the whole object (base64 bundle included) into
	// the last-applied-configuration annotation, which blows the 256KiB
	// annotation cap for any bundle over ~190KB.
	if err := applyConfigMapServerSide(cfg, bundleCM); err != nil {
		return fmt.Errorf("apply bundle ConfigMap: %w", err)
	}

	applyOut, err := kubectlApplyOutput(cfg, offer)
	if err != nil {
		return fmt.Errorf("apply ServiceOffer: %w", err)
	}
	if persistErr := persistServiceOffer(cfg, ns, name, skillOfferBundle(ns, name, bundleCM, offer)); persistErr != nil {
		u.Warnf("could not persist offer for resume: %v", persistErr)
	}

	action := "created"
	if strings.Contains(applyOut, "configured") || strings.Contains(applyOut, "unchanged") {
		action = "updated"
	}
	u.Successf("ServiceOffer %s/%s %s (type: skill, %s@%s, %s %s/download → %s)", ns, name, action, skillName, version, price, symbol, wallet)
	u.Infof("Bundle: %d bytes gzipped, sha256 %s", len(gz), hash)
	u.Infof("The controller will verify the hash → publish the bundle server → payment gate → route")
	u.Infof("Check status: obol sell status %s -n %s", name, ns)

	servicePath := strings.TrimSpace(cmd.String("path"))
	if servicePath == "" {
		servicePath = "/services/" + name
	}
	baseURL := "http://obol.stack:8080"
	if tURL, terr := tunnel.EnsureTunnelForSell(cfg, u); terr != nil {
		u.Warnf("Tunnel not started: %v", terr)
		u.Dim("  Start manually with: obol tunnel restart")
	} else {
		baseURL = strings.TrimRight(tURL, "/")
		u.Successf("Tunnel: %s%s", baseURL, servicePath)
	}

	printSkillPurchaseInstructions(u, baseURL, servicePath, skillName, version, chainName, hash)

	if !cmd.Bool("no-register") {
		u.Dim("On-chain identity: obol sell register --chain " + chainName + " (once), then anchor the hash above.")
	}
	return nil
}

// printSkillPurchaseInstructions renders the buyer-facing steps plus
// the seller's set-hash hint. Split out so the share flow stays
// readable.
//
// buy.py pay is text-only: it prints diagnostics before the body and
// decodes the body with errors="replace", so redirecting it to a file
// corrupts binary artifacts. Point it at /skill.json (JSON metadata)
// and steer the bundle download to a binary-safe x402 client.
func printSkillPurchaseInstructions(u *ui.UI, baseURL, servicePath, skillName, version, chain, hash string) {
	bundleURL := baseURL + servicePath + "/bundle.tar.gz"
	metadataURL := baseURL + servicePath + "/skill.json"
	u.Blank()
	u.Bold("Buy it (one paid request = one download):")
	u.Printf("  Probe pricing:  curl -i %s", bundleURL)
	u.Printf("  Paid metadata:  buy.py pay %s", metadataURL)
	u.Printf("  Paid download:  fetch %s with a binary-safe x402 client, save as %s-%s.tar.gz", bundleURL, skillName, version)
	u.Dim("                  (buy.py pay prints the body as text — do NOT redirect it to a file for the bundle)")
	u.Printf("  Verify bundle:  obol skills verify %s-%s.tar.gz --agent-id <seller-agent-id> --skill %s@%s --chain %s",
		skillName, version, skillName, version, chain)
	u.Blank()
	u.Bold("Anchor the bundle hash on ERC-8004 (sellers — submitted with YOUR wallet):")
	u.Printf("  obol skills calldata set-hash %s@%s --agent-id <your-agent-id> --hash %s --chain %s",
		skillName, version, hash, chain)
}

// validateSkillSourceFlags enforces the --from XOR --from-embedded
// contract for SHARE mode.
func validateSkillSourceFlags(from, fromEmbedded string) error {
	switch {
	case from != "" && fromEmbedded != "":
		return fmt.Errorf("--from and --from-embedded are mutually exclusive — pass exactly one")
	case from == "" && fromEmbedded == "":
		return fmt.Errorf("bundle source required: --from <dir> or --from-embedded <embedded-skill-name>")
	default:
		return nil
	}
}

// materializeEmbeddedSkill copies one embedded skill into a temp dir
// (the same normalization path as agent seeding) and returns the
// per-skill directory to pack from. Caller must invoke cleanup.
func materializeEmbeddedSkill(name string) (dir string, cleanup func(), err error) {
	names, err := embed.GetEmbeddedSkillNames()
	if err != nil {
		return "", nil, err
	}
	if !slices.Contains(names, name) {
		return "", nil, fmt.Errorf("embedded skill %q not found; available: %s", name, strings.Join(names, ", "))
	}
	tmp, err := os.MkdirTemp("", "obol-sell-skill-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	if err := embed.WriteSkillSubset(tmp, []string{name}); err != nil {
		cleanup()
		return "", nil, err
	}
	return filepath.Join(tmp, name), cleanup, nil
}

// buildSkillBundleConfigMapManifest renders the operator-owned bundle
// ConfigMap: binaryData[monetizeapi.SkillBundleKey] = gzipped tarball.
func buildSkillBundleConfigMapManifest(cmName, ns string, gz []byte) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      cmName,
			"namespace": ns,
			"labels": map[string]any{
				"app.kubernetes.io/managed-by": "obol-cli",
				"obol.org/skill-bundle":        "true",
			},
		},
		"binaryData": map[string]any{
			monetizeapi.SkillBundleKey: base64.StdEncoding.EncodeToString(gz),
		},
	}
}

// skillShareOfferInputs carries everything buildSkillShareOfferManifest
// needs; a struct so the pure builder stays unit-testable without a
// cli.Command.
type skillShareOfferInputs struct {
	OfferName       string
	Namespace       string
	SkillName       string
	Version         string
	SHA256          string
	BundleConfigMap string
	DisplayName     string
	Description     string
	PayTo           string
	Chain           string
	Price           string
	MaxTimeout      int
	AssetTerms      schemas.AssetTerms
	Path            string
	Registration    map[string]any // nil omits the block
}

// buildSkillShareOfferManifest assembles the type=skill ServiceOffer.
// spec.upstream is pinned to the controller's deterministic bundle-
// server name so reconcileUpstream and routeRuleFromOffer need zero
// changes — and so the controller can reject spoofed upstreams (a skill
// offer may only ever advertise its own bundle server).
func buildSkillShareOfferManifest(in skillShareOfferInputs) map[string]any {
	payment := map[string]any{
		"scheme":            "exact",
		"network":           in.Chain,
		"payTo":             in.PayTo,
		"maxTimeoutSeconds": in.MaxTimeout,
		"price": map[string]any{
			"perRequest": in.Price,
		},
	}
	if !in.AssetTerms.IsZero() {
		payment["asset"] = in.AssetTerms
	}

	skill := map[string]any{
		"name":            in.SkillName,
		"version":         in.Version,
		"sha256":          strings.ToLower(in.SHA256),
		"bundleConfigMap": in.BundleConfigMap,
	}
	if in.DisplayName != "" {
		skill["displayName"] = in.DisplayName
	}
	if in.Description != "" {
		skill["description"] = in.Description
	}

	spec := map[string]any{
		"type":  "skill",
		"skill": skill,
		"upstream": map[string]any{
			"service":    monetizeapi.SkillBundleWorkloadName(in.OfferName),
			"namespace":  in.Namespace,
			"port":       8080,
			"healthPath": "/skill.json",
		},
		"payment": payment,
	}
	if in.Path != "" {
		spec["path"] = in.Path
	}
	if in.Registration != nil {
		spec["registration"] = in.Registration
	}

	return map[string]any{
		"apiVersion": "obol.org/v1alpha1",
		"kind":       "ServiceOffer",
		"metadata": map[string]any{
			"name":      in.OfferName,
			"namespace": in.Namespace,
		},
		"spec": spec,
	}
}

// skillOfferBundle wraps the bundle ConfigMap + type=skill ServiceOffer
// in a v1 List for the resume ledger, modeled on agentOfferBundle. The
// ConfigMap precedes the offer so a replay lands the artifact before
// the controller reconciles the offer against it. The resume path
// routes kind=ConfigMap items through server-side apply (see
// resumeApplyManifest) — replaying the bundle client-side would blow
// the 256KiB last-applied-configuration annotation cap.
func skillOfferBundle(offerNs, name string, bundleCM, offer map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "List",
		"metadata":   map[string]any{"name": name, "namespace": offerNs},
		"items":      []any{bundleCM, offer},
	}
}

// applyConfigMapServerSide applies one ConfigMap manifest with
// `kubectl apply --server-side --force-conflicts`. Server-side apply
// keeps the (potentially ~900KB) binaryData payload out of the
// last-applied-configuration annotation, which client-side apply would
// overflow at 256KiB.
func applyConfigMapServerSide(cfg *config.Config, manifest map[string]any) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal ConfigMap manifest: %w", err)
	}
	bin, kc := kubectl.Paths(cfg)
	return kubectl.ApplyServerSideForceConflicts(bin, kc, raw, "obol-cli")
}

// resumeApplyManifest replays one persisted ledger manifest. Plain
// manifests keep the legacy client-side apply. v1 List bundles are
// applied item by item in order, routing kind=ConfigMap items (skill
// bundle artifacts) through server-side apply — everything else (the
// namespace shims in agent bundles, the offers themselves) stays
// client-side.
func resumeApplyManifest(cfg *config.Config, manifest map[string]any) error {
	if manifest["kind"] != "List" {
		return kubectlApply(cfg, manifest)
	}
	items, _ := manifest["items"].([]any)
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return fmt.Errorf("malformed List item %T in persisted offer bundle", it)
		}
		if m["kind"] == "ConfigMap" {
			if err := applyConfigMapServerSide(cfg, m); err != nil {
				return err
			}
			continue
		}
		if err := kubectlApply(cfg, m); err != nil {
			return err
		}
	}
	return nil
}
