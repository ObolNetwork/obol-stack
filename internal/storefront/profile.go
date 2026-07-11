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
	ProfileNamespace = "x402"
	ProfileConfigMap = "obol-storefront-profile"
	ProfileDataKey   = "profile.json"
	// DefaultLogoPath is the light-on-dark Obol wordmark; only legible on
	// the dark/obol themes.
	DefaultLogoPath = "/obol-stack-logo.png"
	// DefaultMarkPath is the dark square Obol mark used as the default
	// brand image on the light theme (paired with the display name as
	// text), where the wordmark would be invisible.
	DefaultMarkPath     = "/obol-logo.png"
	profileLocalRelPath = "storefront/profile.json"
)

// ResolvePublished merges an operator-set profile over stack defaults.
func ResolvePublished(explicit *schemas.StorefrontProfile, baseURL string) schemas.StorefrontProfile {
	baseURL = strings.TrimRight(baseURL, "/")
	profile := schemas.StorefrontProfile{
		DisplayName: "Obol Stack",
		Tagline:     "Unlock Agent and API services with digital payments.",
		LogoURL:     baseURL + DefaultLogoPath,
		Theme:       DefaultTheme,
		// AccentColor/FaviconURL/OGImageURL/Description default to empty:
		// renderers fall back to the preset accent, the logo, and the
		// generated preview image respectively.
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
	if v := strings.TrimSpace(explicit.Theme); v != "" {
		profile.Theme = v
	}
	if v := strings.TrimSpace(explicit.AccentColor); v != "" {
		profile.AccentColor = v
	}
	if v := strings.TrimSpace(explicit.FaviconURL); v != "" {
		profile.FaviconURL = v
	}
	if v := strings.TrimSpace(explicit.OGImageURL); v != "" {
		profile.OGImageURL = v
	}
	if v := strings.TrimSpace(explicit.Description); v != "" {
		profile.Description = v
	}
	if v := strings.TrimSpace(explicit.CustomCSS); v != "" {
		profile.CustomCSS = v
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
	if v := strings.TrimSpace(patch.Theme); v != "" {
		out.Theme = v
	}
	if v := strings.TrimSpace(patch.AccentColor); v != "" {
		out.AccentColor = v
	}
	if v := strings.TrimSpace(patch.FaviconURL); v != "" {
		out.FaviconURL = v
	}
	if v := strings.TrimSpace(patch.OGImageURL); v != "" {
		out.OGImageURL = v
	}
	if v := strings.TrimSpace(patch.Description); v != "" {
		out.Description = v
	}
	if v := strings.TrimSpace(patch.CustomCSS); v != "" {
		out.CustomCSS = v
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

// ValidateImageURL accepts absolute http(s) URLs, site-relative paths, or
// inline data:image/...;base64 URIs (self-contained — immune to CORS,
// hotlink protection, and dead hosts). what names the field in errors
// ("logo", "favicon", "OG image").
func ValidateImageURL(raw, what string) error {
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
		return validateInlineImage(raw, what)
	}
	return fmt.Errorf("%s URL must be https://..., http://..., a path starting with /, or an inline data:image/...;base64 URI", what)
}

// ValidateLogoURL is ValidateImageURL for the logo field.
func ValidateLogoURL(raw string) error { return ValidateImageURL(raw, "logo") }

// InlineImageFromFile reads a local image file and returns it as a
// data:image/...;base64 URI suitable for the profile's image fields. Inline
// images are self-contained — immune to CORS, hotlink protection, and dead
// hosts — but must fit the ConfigMap budget (maxInlineLogoBytes). what names
// the field in errors ("logo", "favicon", "OG image").
func InlineImageFromFile(path, what string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s file: %w", what, err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%s file %s is empty", what, path)
	}
	if len(data) > maxInlineLogoBytes {
		return "", fmt.Errorf("%s file is %d KiB; max %d KiB for an inline image (it is embedded in the catalog ConfigMap) — host larger images at an https URL instead", what, len(data)>>10, maxInlineLogoBytes>>10)
	}
	mime := detectImageMIME(path, data)
	if mime == "" {
		return "", fmt.Errorf("%s file %s does not look like an image (png, jpeg, gif, webp, svg, ico)", what, path)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

// InlineLogoFromFile is InlineImageFromFile for the logo field.
func InlineLogoFromFile(path string) (string, error) { return InlineImageFromFile(path, "logo") }

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

func validateInlineImage(raw, what string) error {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok {
		return fmt.Errorf("inline %s: malformed data: URI (missing comma separator)", what)
	}
	if !strings.HasPrefix(meta, "image/") {
		return fmt.Errorf("inline %s must be a data:image/... URI, got data:%s", what, meta)
	}
	if !strings.HasSuffix(meta, ";base64") {
		return fmt.Errorf("inline %s must be base64-encoded (data:image/...;base64,...)", what)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fmt.Errorf("inline %s: invalid base64 payload: %w", what, err)
	}
	if len(decoded) > maxInlineLogoBytes {
		return fmt.Errorf("inline %s is %d KiB decoded; max %d KiB (it is embedded in the catalog ConfigMap)", what, len(decoded)>>10, maxInlineLogoBytes>>10)
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
