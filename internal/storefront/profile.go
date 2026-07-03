package storefront

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/schemas"
)

const (
	ProfileNamespace    = "x402"
	ProfileConfigMap    = "obol-storefront-profile"
	ProfileDataKey      = "profile.json"
	DefaultLogoPath     = "/obol-stack-logo.png"
	profileLocalRelPath = "storefront/profile.json"
)

// ResolvePublished merges an operator-set profile over stack defaults.
func ResolvePublished(explicit *schemas.StorefrontProfile, baseURL string) schemas.StorefrontProfile {
	baseURL = strings.TrimRight(baseURL, "/")
	profile := schemas.StorefrontProfile{
		DisplayName: "Obol Stack",
		Tagline:     "Unlock Agent and API services with digital payments.",
		LogoURL:     baseURL + DefaultLogoPath,
	}
	if explicit == nil {
		return profile
	}
	if v := strings.TrimSpace(explicit.DisplayName); v != "" {
		profile.DisplayName = v
	}
	if v := strings.TrimSpace(explicit.Tagline); v != "" {
		profile.Tagline = v
	}
	if v := strings.TrimSpace(explicit.LogoURL); v != "" {
		profile.LogoURL = v
	}
	if v := strings.TrimSpace(explicit.ContactEmail); v != "" {
		profile.ContactEmail = v
	}
	return profile
}

// MarshalProfile serialises a profile for ConfigMap storage.
func MarshalProfile(p schemas.StorefrontProfile) (string, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ParseProfile decodes profile JSON from a ConfigMap data key.
func ParseProfile(raw string) (*schemas.StorefrontProfile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var p schemas.StorefrontProfile
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// MergeProfile overlays non-empty patch fields onto base.
func MergeProfile(base, patch schemas.StorefrontProfile) schemas.StorefrontProfile {
	out := base
	if v := strings.TrimSpace(patch.DisplayName); v != "" {
		out.DisplayName = v
	}
	if v := strings.TrimSpace(patch.Tagline); v != "" {
		out.Tagline = v
	}
	if v := strings.TrimSpace(patch.LogoURL); v != "" {
		out.LogoURL = v
	}
	if v := strings.TrimSpace(patch.ContactEmail); v != "" {
		out.ContactEmail = v
	}
	return out
}

// ProfileLocalPath is the host-side record of the operator profile.
func ProfileLocalPath(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, profileLocalRelPath)
}

// ValidateLogoURL accepts absolute http(s) URLs or site-relative paths.
func ValidateLogoURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "/") {
		return nil
	}
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return nil
	}
	return fmt.Errorf("logo URL must be https://..., http://..., or a path starting with /")
}

// ValidateContactEmail accepts a bare operator contact address for OpenAPI
// info.contact.email (required by x402scan discovery audits).
func ValidateContactEmail(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return fmt.Errorf("contact email: %w", err)
	}
	if addr.Address == "" {
		return fmt.Errorf("contact email: address is empty")
	}
	return nil
}

// IsDefaultLogoURL reports whether url is the stack default wordmark (relative or absolute).
func IsDefaultLogoURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return raw == DefaultLogoPath || strings.HasSuffix(raw, DefaultLogoPath)
}

// ConfigMapManifest returns a kubectl-applyable ConfigMap for the profile.
func ConfigMapManifest(p schemas.StorefrontProfile) (map[string]any, error) {
	payload, err := MarshalProfile(p)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      ProfileConfigMap,
			"namespace": ProfileNamespace,
			"labels": map[string]any{
				"app":                 ProfileConfigMap,
				"obol.org/managed-by": "obol-cli",
			},
		},
		"data": map[string]any{
			ProfileDataKey: payload,
		},
	}, nil
}
