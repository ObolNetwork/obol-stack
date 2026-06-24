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

func sellInfoCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "info",
		Usage: "Configure public seller branding in /api/services.json",
		Description: `Sets seller-wide display name, tagline, and logo in the public catalog.
This is independent of individual ServiceOffers and ERC-8004 identity.

Examples:
  obol sell info set --display-name "Acme Labs" --tagline "Paid APIs." --logo-url "https://acme.example/logo.png"
  obol sell info show
  obol sell info reset`,
		Commands: []*cli.Command{
			sellInfoSetCommand(cfg),
			sellInfoShowCommand(cfg),
			sellInfoResetCommand(cfg),
		},
	}
}

func sellInfoSetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "set",
		Usage: "Set seller display name, tagline, and/or logo URL",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "display-name", Usage: "Seller title shown in the storefront header"},
			&cli.StringFlag{Name: "tagline", Usage: "Short subtitle under the storefront hero"},
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

			current, err := loadSellerProfile(cfg)
			if err != nil {
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

			u.Success("Seller profile updated")
			printSellerProfile(u, published)
			u.Blank()
			u.Dim("Verify: curl -s http://obol.stack:8080/api/services.json | jq '{displayName,tagline,logoUrl}'")
			return nil
		},
	}
}

func sellInfoShowCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "show",
		Usage: "Show the current seller profile",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Aliases: []string{"j"}, Usage: "Output as JSON"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}
			profile, err := loadSellerProfile(cfg)
			if err != nil {
				return err
			}
			baseURL, _ := sellerBaseURL(cfg)
			published := storefront.ResolvePublished(&profile, baseURL)
			if u.IsJSON() || cmd.Bool("json") {
				return u.JSON(published)
			}
			printSellerProfile(u, published)
			return nil
		},
	}
}

func sellInfoResetCommand(cfg *config.Config) *cli.Command {
	return &cli.Command{
		Name:  "reset",
		Usage: "Remove custom seller branding and restore defaults",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			u := getUI(cmd)
			if err := kubectl.EnsureCluster(cfg); err != nil {
				return err
			}
			bin, kc := kubectl.Paths(cfg)
			if err := kubectl.RunSilent(bin, kc, "delete", "configmap", storefront.ProfileConfigMap,
				"-n", storefront.ProfileNamespace, "--ignore-not-found"); err != nil {
				return fmt.Errorf("delete seller profile: %w", err)
			}
			_ = os.Remove(storefront.ProfileLocalPath(cfg))

			published, err := waitForPublishedCatalog(cfg, nil, 45*time.Second)
			if err != nil {
				return err
			}

			u.Success("Seller profile reset to defaults")
			printSellerProfile(u, published)
			return nil
		},
	}
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
		return fmt.Errorf("apply seller profile: %w", err)
	}
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
				return schemas.StorefrontProfile{
					DisplayName: got.DisplayName,
					Tagline:     got.Tagline,
					LogoURL:     got.LogoURL,
				}, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return want, fmt.Errorf("timed out waiting for controller to publish /api/services.json")
}

func sellerProfilesEqual(catalog schemas.ServiceCatalog, want schemas.StorefrontProfile) bool {
	return strings.TrimSpace(catalog.DisplayName) == strings.TrimSpace(want.DisplayName) &&
		strings.TrimSpace(catalog.Tagline) == strings.TrimSpace(want.Tagline) &&
		strings.TrimSpace(catalog.LogoURL) == strings.TrimSpace(want.LogoURL)
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
	u.Printf("  Display name: %s", profile.DisplayName)
	u.Printf("  Tagline:      %s", profile.Tagline)
	u.Printf("  Logo URL:     %s", profile.LogoURL)
}
