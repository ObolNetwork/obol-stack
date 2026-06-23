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

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
	"github.com/ObolNetwork/obol-stack/internal/storefront"
	"github.com/ObolNetwork/obol-stack/internal/tunnel"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/urfave/cli/v3"
)

func sellStorefrontCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "storefront",
		Usage: "Configure public storefront branding (name, tagline, logo)",
		Description: `Sets the seller-wide storefront profile served at /api/storefront.json.
This is independent of individual ServiceOffers and ERC-8004 identity.

Examples:
  obol sell storefront set --display-name "Acme Labs" --tagline "Paid APIs." --logo-url "https://acme.example/logo.png"
  obol sell storefront show
  obol sell storefront reset`,
		Commands: []*cli.Command{
			sellStorefrontSetCommand(cfg),
			sellStorefrontShowCommand(cfg),
			sellStorefrontResetCommand(cfg),
		},
	}
}

func sellStorefrontSetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "set",
		Usage: "Set storefront display name, tagline, and/or logo URL",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "display-name", Usage: "Storefront title shown in the header and page hero"},
			&cli.StringFlag{Name: "tagline", Usage: "Short subtitle under the storefront title"},
			&cli.StringFlag{Name: "logo-url", Usage: "Logo image URL (https://... or /path on this host)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}

			patch := schemas.StorefrontProfile{
				DisplayName: strings.TrimSpace(cmd.String("display-name")),
				Tagline:     strings.TrimSpace(cmd.String("tagline")),
				LogoURL:     strings.TrimSpace(cmd.String("logo-url")),
			}
			if patch.DisplayName == "" && patch.Tagline == "" && patch.LogoURL == "" {
				return errors.New("pass at least one of --display-name, --tagline, --logo-url")
			}
			if err := storefront.ValidateLogoURL(patch.LogoURL); err != nil {
				return err
			}

			current, err := loadStorefrontProfile(cfg)
			if err != nil {
				return err
			}
			merged := storefront.MergeProfile(current, patch)
			if err := applyStorefrontProfile(cfg, merged); err != nil {
				return err
			}

			published, err := waitForPublishedStorefront(cfg, &merged, 45*time.Second)
			if err != nil {
				return err
			}

			u.Success("Storefront profile updated")
			printStorefrontProfile(u, published)
			u.Blank()
			u.Dim("Verify: curl -s http://obol.stack:8080/api/storefront.json | jq .")
			return nil
		},
	}
}

func sellStorefrontShowCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "show",
		Usage: "Show the current storefront profile",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Aliases: []string{"j"}, Usage: "Output as JSON"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}
			profile, err := loadStorefrontProfile(cfg)
			if err != nil {
				return err
			}
			baseURL, _ := storefrontBaseURL(cfg)
			published := storefront.ResolvePublished(&profile, baseURL)
			if u.IsJSON() || cmd.Bool("json") {
				return u.JSON(published)
			}
			printStorefrontProfile(u, published)
			return nil
		},
	}
}

func sellStorefrontResetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "reset",
		Usage: "Remove custom storefront branding and restore defaults",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}
			bin, kc := kubectl.Paths(cfg)
			if err := kubectl.RunSilent(bin, kc, "delete", "configmap", storefront.ProfileConfigMap,
				"-n", storefront.ProfileNamespace, "--ignore-not-found"); err != nil {
				return fmt.Errorf("delete storefront profile: %w", err)
			}
			_ = os.Remove(storefront.ProfileLocalPath(cfg))

			published, err := waitForPublishedStorefront(cfg, nil, 45*time.Second)
			if err != nil {
				return err
			}

			u.Success("Storefront profile reset to defaults")
			printStorefrontProfile(u, published)
			return nil
		},
	}
}

func loadStorefrontProfile(cfg *config.Config) (schemas.StorefrontProfile, error) {
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

func applyStorefrontProfile(cfg *config.Config, profile schemas.StorefrontProfile) error {
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

func waitForPublishedStorefront(cfg *config.Config, explicit *schemas.StorefrontProfile, timeout time.Duration) (schemas.StorefrontProfile, error) {
	want := storefront.ResolvePublished(explicit, mustStorefrontBaseURL(cfg))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw, err := kubectlOutput(cfg, "get", "configmap", "obol-skill-md",
			"-n", storefront.ProfileNamespace, "-o", "jsonpath={.data.storefront\\.json}")
		if err == nil && strings.TrimSpace(raw) != "" {
			var got schemas.StorefrontProfile
			if err := json.Unmarshal([]byte(raw), &got); err == nil && storefrontProfilesEqual(got, want) {
				return got, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return want, fmt.Errorf("timed out waiting for controller to publish /api/storefront.json")
}

func storefrontProfilesEqual(a, b schemas.StorefrontProfile) bool {
	return strings.TrimSpace(a.DisplayName) == strings.TrimSpace(b.DisplayName) &&
		strings.TrimSpace(a.Tagline) == strings.TrimSpace(b.Tagline) &&
		strings.TrimSpace(a.LogoURL) == strings.TrimSpace(b.LogoURL)
}

func storefrontBaseURL(cfg *config.Config) (string, error) {
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

func mustStorefrontBaseURL(cfg *config.Config) string {
	baseURL, err := storefrontBaseURL(cfg)
	if err != nil || baseURL == "" {
		return "http://obol.stack:8080"
	}
	return baseURL
}

func printStorefrontProfile(u *ui.UI, profile schemas.StorefrontProfile) {
	u.Printf("  Display name: %s", profile.DisplayName)
	u.Printf("  Tagline:      %s", profile.Tagline)
	u.Printf("  Logo URL:     %s", profile.LogoURL)
}
