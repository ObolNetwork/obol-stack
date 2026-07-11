package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/buyprompts"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

// sellInfoCommand is the read/preview surface for the storefront.
//
//	obol sell info            → storefront branding + the services on sale
//	obol sell info <name>     → one service, buyer-facing (what it is, how to buy)
//	obol sell info set        → change storefront branding (interactive or --flags)
//	obol sell info reset      → clear branding back to defaults
//
// `info` (and `info <name>`) render the *published* /api/services.json envelope,
// so what you see is exactly what a buyer sees: your branding plus only the
// offers that are operationally ready. For operator-side health and conditions
// (including offers that are not yet ready or are draining), use `sell status`.
func sellInfoCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:      "info",
		Usage:     "Show the storefront and its services as buyers see them",
		ArgsUsage: "[service-name]",
		Description: `Renders the published storefront catalog (/api/services.json):

  obol sell info                 Storefront branding + every service on sale
  obol sell info <name>          One service in focus, with how-to-buy
  obol sell info --verbose       Add health and richer per-service detail

Manage the storefront's own branding (independent of individual services):

  obol sell info set             Interactive when no flags are passed;
  obol sell info set --tagline … updates only the fields you pass, leaving
                                 the rest untouched.
  obol sell info set --contact-email … publishes info.contact.email in /openapi.json
  obol sell info reset           Clears all branding back to defaults;
  obol sell info reset --tagline resets only the fields you pass.

This is the buyer's-eye view. For operator health, conditions, and offers that
are not yet ready or are draining, use 'obol sell status'.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Show health and richer per-service detail"},
		},
		Commands: []*cli.Command{
			sellInfoSetCommand(cfg),
			sellInfoResetCommand(cfg),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}
			catalog, err := loadPublishedCatalog(cfg)
			if err != nil {
				return err
			}
			verbose := cmd.Bool("verbose") || u.IsVerbose()

			// Focused single-service view: obol sell info <name>.
			if cmd.NArg() > 0 {
				name := cmd.Args().First()
				entry := findCatalogEntry(catalog, name)
				if entry == nil {
					return fmt.Errorf("no service %q is on sale in the storefront (run 'obol sell info' to list what is)", name)
				}
				if u.IsJSON() {
					return u.JSON(entry)
				}
				printServiceDetail(u, *entry, catalog, verbose)
				return nil
			}

			if u.IsJSON() {
				return u.JSON(catalog)
			}
			printStorefrontHeader(u, catalog)
			printServiceList(u, catalog.Services, verbose)
			return nil
		},
	}
}

func sellInfoSetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "set",
		Usage: "Set storefront branding: name, tagline, logo, theme, favicon, preview image, description",
		Description: `Updates seller-wide storefront branding. With no flags on a TTY this walks
you through each field (pre-filled with the current value). With flags, only
the fields you pass change; everything else is left untouched.

Logo URLs are preflight-checked before publishing: reachability, an image
content-type, https (mixed content), and permissive CORS headers (needed by
sites that embed your catalog). On a TTY a failing check asks before
proceeding; non-interactive runs warn and continue. To sidestep hosting
brittleness entirely (CORS, hotlinking, dead hosts), inline a local image —
it is embedded in the catalog as a self-contained data: URI:

  obol sell info set --logo-file ./logo.png

Theming applies to every seller-facing page (storefront, 402 paywall pages,
sign-in, error pages, /api docs, per-offer landing pages):

  obol sell info set --theme dark                 light (default), dark, obol
  obol sell info set --accent '#7c5cff'           accent override on any preset
  obol sell info set --favicon-file ./fav.png     tab icon (falls back to logo)
  obol sell info set --og-image-file ./og.png     link-preview image
  obol sell info set --description 'What you sell and why it is good.'`,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "display-name", Usage: "Seller title shown in the storefront header"},
			&cli.StringFlag{Name: "tagline", Usage: "Short subtitle under the storefront hero"},
			&cli.StringFlag{Name: "logo-url", Usage: "Logo image URL (https://..., /path on this host, or inline data:image/...;base64)"},
			&cli.StringFlag{Name: "logo-file", Usage: "Local image file to inline as the logo (≤256 KiB, converted to a data: URI — no hosting needed)"},
			&cli.StringFlag{Name: "contact-email", Usage: "Operator contact email published in /openapi.json (x402scan)"},
			&cli.StringFlag{Name: "theme", Usage: "Theme preset for all seller pages: " + strings.Join(storefront.ThemeNames(), ", ") + " (default: " + storefront.DefaultTheme + ")"},
			&cli.StringFlag{Name: "accent", Usage: "Accent color override as #hex (e.g. #0b9b71); empty keeps the preset accent"},
			&cli.StringFlag{Name: "favicon-url", Usage: "Favicon URL (https://..., /path on this host, or inline data:image/...;base64); empty falls back to the logo"},
			&cli.StringFlag{Name: "favicon-file", Usage: "Local image file to inline as the favicon (≤256 KiB)"},
			&cli.StringFlag{Name: "og-image-url", Usage: "Link-preview (og:image) URL; empty uses the storefront's generated preview"},
			&cli.StringFlag{Name: "og-image-file", Usage: "Local image file to inline as the link-preview image (≤256 KiB)"},
			&cli.StringFlag{Name: "description", Usage: "Longer seller description shown on the storefront (markdown subset)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}

			current, err := loadSellerProfile(cfg)
			if err != nil {
				return err
			}

			patch := schemas.StorefrontProfile{}
			setFlags := []string{"display-name", "tagline", "logo-url", "logo-file", "contact-email",
				"theme", "accent", "favicon-url", "favicon-file", "og-image-url", "og-image-file", "description"}
			anyFlag := false
			for _, f := range setFlags {
				if cmd.IsSet(f) {
					anyFlag = true
					break
				}
			}
			if anyFlag {
				// Flag mode: patch only the fields the operator passed.
				if cmd.IsSet("logo-url") && cmd.IsSet("logo-file") {
					return errors.New("--logo-url and --logo-file are mutually exclusive")
				}
				if cmd.IsSet("favicon-url") && cmd.IsSet("favicon-file") {
					return errors.New("--favicon-url and --favicon-file are mutually exclusive")
				}
				if cmd.IsSet("og-image-url") && cmd.IsSet("og-image-file") {
					return errors.New("--og-image-url and --og-image-file are mutually exclusive")
				}
				if cmd.IsSet("display-name") {
					patch.DisplayName = strings.TrimSpace(cmd.String("display-name"))
				}
				if cmd.IsSet("tagline") {
					patch.Tagline = strings.TrimSpace(cmd.String("tagline"))
				}
				if cmd.IsSet("logo-url") {
					patch.LogoURL = strings.TrimSpace(cmd.String("logo-url"))
				}
				if cmd.IsSet("logo-file") {
					uri, err := storefront.InlineLogoFromFile(strings.TrimSpace(cmd.String("logo-file")))
					if err != nil {
						return err
					}
					patch.LogoURL = uri
				}
				if cmd.IsSet("contact-email") {
					patch.ContactEmail = strings.TrimSpace(cmd.String("contact-email"))
				}
				if cmd.IsSet("theme") {
					patch.Theme = strings.TrimSpace(cmd.String("theme"))
				}
				if cmd.IsSet("accent") {
					patch.AccentColor = strings.TrimSpace(cmd.String("accent"))
				}
				if cmd.IsSet("favicon-url") {
					patch.FaviconURL = strings.TrimSpace(cmd.String("favicon-url"))
				}
				if cmd.IsSet("favicon-file") {
					uri, err := storefront.InlineImageFromFile(strings.TrimSpace(cmd.String("favicon-file")), "favicon")
					if err != nil {
						return err
					}
					patch.FaviconURL = uri
				}
				if cmd.IsSet("og-image-url") {
					patch.OGImageURL = strings.TrimSpace(cmd.String("og-image-url"))
				}
				if cmd.IsSet("og-image-file") {
					uri, err := storefront.InlineImageFromFile(strings.TrimSpace(cmd.String("og-image-file")), "OG image")
					if err != nil {
						return err
					}
					patch.OGImageURL = uri
				}
				if cmd.IsSet("description") {
					patch.Description = strings.TrimSpace(cmd.String("description"))
				}
			} else {
				// No flags: prompt interactively (pre-filled with effective values).
				if !u.IsTTY() {
					return errors.New("no flags given and not a TTY: pass --display-name, --tagline, --logo-url, --logo-file, and/or --contact-email")
				}
				effective := storefront.ResolvePublished(&current, mustSellerBaseURL(cfg))
				if v, err := u.Input("Display name", effective.DisplayName); err == nil {
					patch.DisplayName = strings.TrimSpace(v)
				}
				if v, err := u.Input("Tagline", effective.Tagline); err == nil {
					patch.Tagline = strings.TrimSpace(v)
				}
				// An inline data: URI is too long to pre-fill; keep it unless
				// the operator types a replacement.
				logoDefault := effective.LogoURL
				if strings.HasPrefix(logoDefault, "data:") {
					u.Dim("Current logo: " + storefront.DescribeLogoURL(logoDefault) + " (leave blank to keep)")
					logoDefault = ""
				}
				if v, err := u.Input("Logo URL or local image file", logoDefault); err == nil {
					patch.LogoURL = resolveLogoInput(u, v)
				}
				if v, err := u.Input("Contact email (OpenAPI)", effective.ContactEmail); err == nil {
					patch.ContactEmail = strings.TrimSpace(v)
				}
				if v, err := u.Input("Theme ("+strings.Join(storefront.ThemeNames(), "/")+")", effective.Theme); err == nil {
					patch.Theme = strings.TrimSpace(v)
				}
				if v, err := u.Input("Description (markdown, shown on the storefront)", effective.Description); err == nil {
					patch.Description = strings.TrimSpace(v)
				}
				u.Dim("Accent color, favicon, and link-preview image: flags only (--accent, --favicon-file, --og-image-file)")
			}

			if patch == (schemas.StorefrontProfile{}) {
				return errors.New("nothing to set")
			}
			if err := storefront.ValidateLogoURL(patch.LogoURL); err != nil {
				return err
			}
			if err := storefront.ValidateContactEmail(patch.ContactEmail); err != nil {
				return err
			}
			if err := storefront.ValidateThemeName(patch.Theme); err != nil {
				return err
			}
			if err := storefront.ValidateAccentColor(patch.AccentColor); err != nil {
				return err
			}
			if err := storefront.ValidateImageURL(patch.FaviconURL, "favicon"); err != nil {
				return err
			}
			if err := storefront.ValidateImageURL(patch.OGImageURL, "OG image"); err != nil {
				return err
			}
			if err := confirmLogoURL(ctx, u, cfg, patch.LogoURL); err != nil {
				return err
			}

			merged := storefront.MergeProfile(current, patch)
			if err := applySellerProfile(cfg, merged); err != nil {
				return err
			}

			published, err := waitForPublishedCatalog(cfg, &merged, 45*time.Second)
			if err != nil {
				return err
			}

			u.Success("Storefront branding updated")
			printSellerProfile(u, published)
			u.Blank()
			u.Dim("Preview: obol sell info")
			return nil
		},
	}
}

func sellInfoResetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "reset",
		Usage: "Clear storefront branding back to defaults",
		Description: `With no flags, resets all storefront branding to stack defaults. Pass one or
more field flags to reset only those fields, leaving the rest untouched.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "display-name", Usage: "Reset only the display name"},
			&cli.BoolFlag{Name: "tagline", Usage: "Reset only the tagline"},
			&cli.BoolFlag{Name: "logo-url", Usage: "Reset only the logo URL"},
			&cli.BoolFlag{Name: "contact-email", Usage: "Reset only the OpenAPI contact email"},
			&cli.BoolFlag{Name: "theme", Usage: "Reset only the theme (back to " + storefront.DefaultTheme + ")"},
			&cli.BoolFlag{Name: "accent", Usage: "Reset only the accent color"},
			&cli.BoolFlag{Name: "favicon-url", Usage: "Reset only the favicon"},
			&cli.BoolFlag{Name: "og-image-url", Usage: "Reset only the link-preview image"},
			&cli.BoolFlag{Name: "description", Usage: "Reset only the description"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}

			clear := map[string]bool{}
			for _, f := range []string{"display-name", "tagline", "logo-url", "contact-email",
				"theme", "accent", "favicon-url", "og-image-url", "description"} {
				if cmd.Bool(f) {
					clear[f] = true
				}
			}
			partial := len(clear) > 0

			var explicit *schemas.StorefrontProfile
			if partial {
				current, err := loadSellerProfile(cfg)
				if err != nil {
					return err
				}
				cleared := clearProfileFields(current, clear)
				if cleared == (schemas.StorefrontProfile{}) {
					// Everything is back to default — remove the override entirely.
					if err := deleteSellerProfile(cfg); err != nil {
						return err
					}
				} else {
					if err := applySellerProfile(cfg, cleared); err != nil {
						return err
					}
					explicit = &cleared
				}
			} else {
				if err := deleteSellerProfile(cfg); err != nil {
					return err
				}
			}

			published, err := waitForPublishedCatalog(cfg, explicit, 45*time.Second)
			if err != nil {
				return err
			}

			u.Success("Storefront branding reset")
			printSellerProfile(u, published)
			return nil
		},
	}
}

// resolveLogoInput turns an interactive "Logo URL" answer into a profile
// value: a path to an existing local image file is inlined as a data: URI;
// anything else passes through unchanged for URL validation.
func resolveLogoInput(u *ui.UI, v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "data:") ||
		strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	if st, err := os.Stat(v); err != nil || !st.Mode().IsRegular() {
		return v
	}
	uri, err := storefront.InlineLogoFromFile(v)
	if err != nil {
		u.Warn(err.Error())
		return v
	}
	u.Infof("Inlined %s as %s", v, storefront.DescribeLogoURL(uri))
	return uri
}

// confirmLogoURL probes a newly-set logo URL the way browsers will load it
// (reachability, image content-type, permissive CORS, https, size) before it
// is published to every catalog consumer. On a TTY, problems become a
// proceed-anyway confirmation; non-interactive runs warn and continue so
// scripts never block. Default, inline (data:), and empty logos skip the
// probe — there is nothing remote to check.
func confirmLogoURL(ctx context.Context, u *ui.UI, cfg *config.Config, logoURL string) error {
	logoURL = strings.TrimSpace(logoURL)
	if logoURL == "" || strings.HasPrefix(logoURL, "data:") || storefront.IsDefaultLogoURL(logoURL) {
		return nil
	}
	if strings.HasPrefix(logoURL, "/") {
		// Site-relative paths are resolved against the seller origin by
		// consumers; probe the same absolute URL they will fetch.
		logoURL = strings.TrimRight(mustSellerBaseURL(cfg), "/") + logoURL
	}

	var result storefront.LogoPreflight
	_ = u.RunWithSpinner("Checking logo URL", func() error {
		result = storefront.PreflightLogoURL(ctx, logoURL)
		return nil
	})
	if result.OK() {
		return nil
	}
	for _, w := range result.Warnings {
		u.Warn(w)
	}
	if !u.IsTTY() || u.IsJSON() {
		u.Warn("continuing anyway (non-interactive); the logo may not load on all sites")
		return nil
	}
	msg, defaultYes := "The logo may not display on every site. Set it anyway?", true
	if result.LoadFailure {
		msg, defaultYes = "Unable to load the logo image. Set it anyway?", false
	}
	if !u.Confirm(msg, defaultYes) {
		return errors.New("cancelled: storefront branding not updated")
	}
	return nil
}

// clearProfileFields returns a copy of p with the flagged fields (keyed by
// their reset-flag names) emptied, so they fall back to stack defaults while
// the rest of the operator override is preserved.
func clearProfileFields(p schemas.StorefrontProfile, clear map[string]bool) schemas.StorefrontProfile {
	if clear["display-name"] {
		p.DisplayName = ""
	}
	if clear["tagline"] {
		p.Tagline = ""
	}
	if clear["logo-url"] {
		p.LogoURL = ""
	}
	if clear["contact-email"] {
		p.ContactEmail = ""
	}
	if clear["theme"] {
		p.Theme = ""
	}
	if clear["accent"] {
		p.AccentColor = ""
	}
	if clear["favicon-url"] {
		p.FaviconURL = ""
	}
	if clear["og-image-url"] {
		p.OGImageURL = ""
	}
	if clear["description"] {
		p.Description = ""
	}
	return p
}

// findCatalogEntry returns the catalog entry whose name matches, or nil.
func findCatalogEntry(catalog schemas.ServiceCatalog, name string) *schemas.ServiceCatalogEntry {
	name = strings.TrimSpace(name)
	for i := range catalog.Services {
		if catalog.Services[i].Name == name {
			return &catalog.Services[i]
		}
	}
	return nil
}

// serviceHealth summarises an entry's buyer-visible state. Only operationally
// ready offers are published, so the baseline is "ready"; draining and
// registration-pending are the two states a live catalog can still carry.
func serviceHealth(e schemas.ServiceCatalogEntry) string {
	switch {
	case strings.TrimSpace(e.DrainEndsAt) != "":
		return "draining (until " + e.DrainEndsAt + ")"
	case e.RegistrationPending:
		return "ready (registration pending)"
	default:
		return "ready"
	}
}

// howToBuy renders a concise, type-appropriate purchase hint for a service.
// The published catalog carries the canonical instructions (entry.buy,
// generated by internal/buyprompts); the switch below is only a fallback for
// catalogs published by pre-buy-block controllers.
func howToBuy(e schemas.ServiceCatalogEntry) []string {
	if e.Buy != nil {
		if cli := strings.TrimSpace(e.Buy.Prompts[buyprompts.PromptCLI]); cli != "" {
			return []string{cli}
		}
	}
	switch e.Type {
	case "inference":
		base := endpointBase(e.Endpoint)
		return []string{fmt.Sprintf("obol buy inference %s", base)}
	case "agent":
		return []string{fmt.Sprintf("buy.py pay-agent %s --model %s --message '<your prompt>'", e.Endpoint, valueOrPlaceholder(e.Model, "<model>"))}
	default: // http and everything else
		return []string{fmt.Sprintf("buy.py pay %s", e.Endpoint)}
	}
}

func printStorefrontHeader(u *ui.UI, catalog schemas.ServiceCatalog) {
	u.Bold("Storefront")
	u.Printf("  Name:    %s", valueOrNone(catalog.DisplayName))
	u.Printf("  Tagline: %s", valueOrNone(catalog.Tagline))
	u.Printf("  Logo:    %s", valueOrNone(storefront.DescribeLogoURL(catalog.LogoURL)))
	u.Blank()
}

func printServiceList(u *ui.UI, services []schemas.ServiceCatalogEntry, verbose bool) {
	if len(services) == 0 {
		u.Dim("No services are on sale yet. Publish one with 'obol sell inference', 'obol sell http', or 'obol sell agent'.")
		return
	}
	u.Bold(fmt.Sprintf("Services on sale (%d)", len(services)))
	for _, s := range services {
		u.Printf("  %s  [%s]  %s", s.Name, valueOrPlaceholder(s.Type, "service"), valueOrNone(s.Price))
		u.Dim("    " + s.Endpoint)
		if verbose {
			u.Dim("    health: " + serviceHealth(s))
			if desc := strings.TrimSpace(s.Description); desc != "" {
				u.Dim("    " + desc)
			}
			if len(s.Skills) > 0 {
				u.Dim("    skills: " + strings.Join(s.Skills, ", "))
			}
		}
	}
}

func printServiceDetail(u *ui.UI, e schemas.ServiceCatalogEntry, catalog schemas.ServiceCatalog, verbose bool) {
	u.Bold(fmt.Sprintf("%s  [%s]", e.Name, valueOrPlaceholder(e.Type, "service")))
	if desc := strings.TrimSpace(e.Description); desc != "" {
		u.Printf("  %s", desc)
		u.Blank()
	}
	u.Printf("  Price:    %s", valueOrNone(e.Price))
	u.Printf("  Endpoint: %s", e.Endpoint)
	if e.Model != "" {
		u.Printf("  Model:    %s", e.Model)
	}
	if e.PayTo != "" {
		u.Printf("  Pay to:   %s", e.PayTo)
	}
	if e.Network != "" {
		u.Printf("  Network:  %s", e.Network)
	}
	if verbose {
		u.Printf("  Health:   %s", serviceHealth(e))
		if len(e.Skills) > 0 {
			u.Printf("  Skills:   %s", strings.Join(e.Skills, ", "))
		}
	}
	u.Blank()
	u.Bold("How to buy")
	for _, line := range howToBuy(e) {
		u.Printf("  %s", line)
	}
}

// loadPublishedCatalog reads the controller-published /api/services.json feed
// (the obol-skill-md ConfigMap) as the storefront envelope.
func loadPublishedCatalog(cfg *config.Config) (schemas.ServiceCatalog, error) {
	raw, err := kubectlOutput(cfg, "get", "configmap", "obol-skill-md",
		"-n", storefront.ProfileNamespace, "-o", "jsonpath={.data.services\\.json}")
	if err != nil {
		return schemas.ServiceCatalog{}, fmt.Errorf("read published catalog: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		// Nothing published yet — show the resolved profile with no services.
		profile, _ := loadSellerProfile(cfg)
		resolved := storefront.ResolvePublished(&profile, mustSellerBaseURL(cfg))
		return schemas.ServiceCatalog{
			DisplayName: resolved.DisplayName,
			Tagline:     resolved.Tagline,
			LogoURL:     resolved.LogoURL,
			Services:    nil,
		}, nil
	}

	var catalog schemas.ServiceCatalog
	if err := json.Unmarshal([]byte(raw), &catalog); err != nil {
		return schemas.ServiceCatalog{}, fmt.Errorf("parse published catalog: %w", err)
	}
	return catalog, nil
}

func loadSellerProfile(cfg *config.Config) (schemas.StorefrontProfile, error) {
	if raw, err := kubectlOutput(cfg, "get", "configmap", storefront.ProfileConfigMap,
		"-n", storefront.ProfileNamespace, "-o", "jsonpath={.data."+storefront.ProfileDataKey+"}"); err == nil {
		if p, err := storefront.ParseProfile(raw); err != nil {
			return schemas.StorefrontProfile{}, err
		} else if p != nil {
			return *p, nil
		}
	}
	if data, err := os.ReadFile(storefront.ProfileLocalPath(cfg)); err == nil {
		if p, err := storefront.ParseProfile(string(data)); err != nil {
			return schemas.StorefrontProfile{}, err
		} else if p != nil {
			return *p, nil
		}
	}
	return schemas.StorefrontProfile{}, nil
}

func applySellerProfile(cfg *config.Config, profile schemas.StorefrontProfile) error {
	if err := os.MkdirAll(filepath.Dir(storefront.ProfileLocalPath(cfg)), 0o700); err != nil {
		return err
	}
	payload, err := storefront.MarshalProfile(profile)
	if err != nil {
		return err
	}
	if err := os.WriteFile(storefront.ProfileLocalPath(cfg), []byte(payload), 0o600); err != nil {
		return err
	}
	manifest, err := storefront.ConfigMapManifest(profile)
	if err != nil {
		return err
	}
	if err := kubectlApply(cfg, manifest); err != nil {
		return fmt.Errorf("apply storefront profile: %w", err)
	}
	return nil
}

func deleteSellerProfile(cfg *config.Config) error {
	bin, kc := kubectl.Paths(cfg)
	if err := kubectl.RunSilent(bin, kc, "delete", "configmap", storefront.ProfileConfigMap,
		"-n", storefront.ProfileNamespace, "--ignore-not-found"); err != nil {
		return fmt.Errorf("delete storefront profile: %w", err)
	}
	_ = os.Remove(storefront.ProfileLocalPath(cfg))
	return nil
}

func waitForPublishedCatalog(cfg *config.Config, explicit *schemas.StorefrontProfile, timeout time.Duration) (schemas.StorefrontProfile, error) {
	want := storefront.ResolvePublished(explicit, mustSellerBaseURL(cfg))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := kubectlOutput(cfg, "get", "configmap", "obol-skill-md",
			"-n", storefront.ProfileNamespace, "-o", "jsonpath={.data.services\\.json}")
		if err == nil && strings.TrimSpace(raw) != "" {
			var got schemas.ServiceCatalog
			if err := json.Unmarshal([]byte(raw), &got); err == nil && sellerProfilesEqual(got, want) {
				return want, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return want, fmt.Errorf("timed out waiting for controller to publish /api/services.json")
}

func sellerProfilesEqual(catalog schemas.ServiceCatalog, want schemas.StorefrontProfile) bool {
	// An empty published theme means the running controller predates
	// theming: accept it only when the operator wants the default. A
	// non-default want must actually publish (same "controller too old"
	// timeout semantics as every other field — see pitfall 19).
	themeOK := strings.TrimSpace(catalog.Theme) == strings.TrimSpace(want.Theme) ||
		(strings.TrimSpace(catalog.Theme) == "" && strings.TrimSpace(want.Theme) == storefront.DefaultTheme)
	return strings.TrimSpace(catalog.DisplayName) == strings.TrimSpace(want.DisplayName) &&
		strings.TrimSpace(catalog.Tagline) == strings.TrimSpace(want.Tagline) &&
		strings.TrimSpace(catalog.LogoURL) == strings.TrimSpace(want.LogoURL) &&
		themeOK &&
		strings.TrimSpace(catalog.FaviconURL) == strings.TrimSpace(want.FaviconURL) &&
		strings.TrimSpace(catalog.OGImageURL) == strings.TrimSpace(want.OGImageURL) &&
		strings.TrimSpace(catalog.Description) == strings.TrimSpace(want.Description)
}

func sellerBaseURL(cfg *config.Config) (string, error) {
	st, err := tunnel.LoadTunnelState(cfg)
	if err != nil {
		return "", err
	}
	if st != nil && strings.TrimSpace(st.Hostname) != "" {
		return "https://" + strings.TrimSpace(st.Hostname), nil
	}
	if url, err := tunnel.GetTunnelURL(cfg); err == nil && strings.TrimSpace(url) != "" {
		return strings.TrimRight(strings.TrimSpace(url), "/"), nil
	}
	return "http://obol.stack:8080", nil
}

func mustSellerBaseURL(cfg *config.Config) string {
	baseURL, err := sellerBaseURL(cfg)
	if err != nil || baseURL == "" {
		return "http://obol.stack:8080"
	}
	return baseURL
}

func printSellerProfile(u *ui.UI, profile schemas.StorefrontProfile) {
	u.Printf("  Display name:   %s", profile.DisplayName)
	u.Printf("  Tagline:        %s", profile.Tagline)
	u.Printf("  Logo URL:       %s", storefront.DescribeLogoURL(profile.LogoURL))
	if email := strings.TrimSpace(profile.ContactEmail); email != "" {
		u.Printf("  Contact email:  %s", email)
	} else {
		u.Printf("  Contact email:  (not set — x402scan may reject /openapi.json)")
	}
	theme := profile.Theme
	if strings.TrimSpace(theme) == "" {
		theme = storefront.DefaultTheme
	}
	if accent := strings.TrimSpace(profile.AccentColor); accent != "" {
		theme += " (accent " + accent + ")"
	}
	u.Printf("  Theme:          %s", theme)
	if v := strings.TrimSpace(profile.FaviconURL); v != "" {
		u.Printf("  Favicon:        %s", storefront.DescribeLogoURL(v))
	}
	if v := strings.TrimSpace(profile.OGImageURL); v != "" {
		u.Printf("  Preview image:  %s", storefront.DescribeLogoURL(v))
	}
	if v := strings.TrimSpace(profile.Description); v != "" {
		u.Printf("  Description:    %s", v)
	}
}

// endpointBase returns the origin (scheme://host[:port]) of a service endpoint,
// or the endpoint unchanged if it can't be parsed.
func endpointBase(endpoint string) string {
	if i := strings.Index(endpoint, "/services/"); i > 0 {
		return endpoint[:i]
	}
	return strings.TrimRight(endpoint, "/")
}

func valueOrPlaceholder(v, placeholder string) string {
	if strings.TrimSpace(v) == "" {
		return placeholder
	}
	return v
}
