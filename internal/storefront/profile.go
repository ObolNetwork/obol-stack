package storefront

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"os"
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

// maxInlineLogoBytes caps the decoded size of an inline data: logo. The
// profile (and the published catalog that embeds it) live in ConfigMaps with
// a hard 1 MiB object limit, so the logo must stay well under that.
const maxInlineLogoBytes = 256 << 10 // 256 KiB

// ValidateLogoURL accepts absolute http(s) URLs, site-relative paths, or
// inline data:image/...;base64 URIs (self-contained — immune to CORS,
// hotlink protection, and dead hosts).
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
	if strings.HasPrefix(raw, "data:") {
		return validateInlineLogo(raw)
	}
	return fmt.Errorf("logo URL must be https://..., http://..., a path starting with /, or an inline data:image/...;base64 URI")
}

// InlineLogoFromFile reads a local image file and returns it as a
// data:image/...;base64 URI suitable for StorefrontProfile.LogoURL. Inline
// logos are self-contained — immune to CORS, hotlink protection, and dead
// hosts — but must fit the ConfigMap budget (maxInlineLogoBytes).
func InlineLogoFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read logo file: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("logo file %s is empty", path)
	}
	if len(data) > maxInlineLogoBytes {
		return "", fmt.Errorf("logo file is %d KiB; max %d KiB for an inline logo (it is embedded in the catalog ConfigMap) — host larger images at an https URL instead", len(data)>>10, maxInlineLogoBytes>>10)
	}
	mime := detectImageMIME(path, data)
	if mime == "" {
		return "", fmt.Errorf("logo file %s does not look like an image (png, jpeg, gif, webp, svg, ico)", path)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// detectImageMIME sniffs an image content-type from file bytes, falling back
// to the extension for SVG (which sniffs as XML/plain text).
func detectImageMIME(path string, data []byte) string {
	sniffed := http.DetectContentType(data)
	if i := strings.Index(sniffed, ";"); i >= 0 {
		sniffed = sniffed[:i]
	}
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		return "image/svg+xml"
	}
	return ""
}

func validateInlineLogo(raw string) error {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok {
		return fmt.Errorf("inline logo: malformed data: URI (missing comma separator)")
	}
	if !strings.HasPrefix(meta, "image/") {
		return fmt.Errorf("inline logo must be a data:image/... URI, got data:%s", meta)
	}
	if !strings.HasSuffix(meta, ";base64") {
		return fmt.Errorf("inline logo must be base64-encoded (data:image/...;base64,...)")
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("inline logo: invalid base64 payload: %w", err)
	}
	if len(decoded) > maxInlineLogoBytes {
		return fmt.Errorf("inline logo is %d KiB decoded; max %d KiB (it is embedded in the catalog ConfigMap)", len(decoded)>>10, maxInlineLogoBytes>>10)
	}
	return nil
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

// DescribeLogoURL returns a terminal-friendly rendering of a logo URL:
// inline data: URIs are summarised (mime + decoded size) instead of dumping
// the base64 payload; everything else passes through unchanged.
func DescribeLogoURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "data:") {
		return raw
	}
	meta, payload, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok {
		return raw
	}
	mime := strings.TrimSuffix(meta, ";base64")
	// DecodedLen ignores '=' padding; subtract it for an exact size.
	pad := 0
	for i := len(payload) - 1; i >= 0 && payload[i] == '='; i-- {
		pad++
	}
	size := len(payload)/4*3 - pad
	return fmt.Sprintf("inline %s (%d KiB)", mime, (size+1023)>>10)
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
