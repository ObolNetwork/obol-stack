package storefront

import (
	"fmt"
	"regexp"
	"strings"
)

// Theme presets for every seller-facing HTML surface (public storefront,
// 402 paywall pages, SIWX sign-in, error pages, per-offer landing pages,
// Scalar API docs). The preset list is deliberately short and hardcoded;
// operators pick a name via `obol sell info set --theme` and may override
// the accent with a single hex value. Arbitrary palette editing is out of
// scope — full control is the bring-your-own-frontend path.
const (
	ThemeLight = "light"
	ThemeDark  = "dark"
	ThemeObol  = "obol"

	// DefaultTheme is what renders when the operator has not chosen.
	DefaultTheme = ThemeLight
)

// ThemeNames lists the valid preset names, default first.
func ThemeNames() []string { return []string{ThemeLight, ThemeDark, ThemeObol} }

// themeVarOrder fixes the CSS emission order so rendered pages and the
// published themeVars JSON stay byte-stable across builds. The token names
// mirror web/public-storefront/src/app/globals.css (bare, un-prefixed form —
// the storefront maps them onto its --color-* Tailwind vars).
var themeVarOrder = []string{
	"bg01", "bg02", "bg03", "bg04", "bg05",
	"stroke",
	"green", "green-dim",
	"light", "body", "muted",
	"red", "amber",
}

// Theme is a resolved preset: a name, a light/dark hint for browser UI
// (color-scheme, scrollbars), and the token→hex map shared by every surface.
type Theme struct {
	Name string
	Dark bool
	Vars map[string]string
}

var themePresets = map[string]Theme{
	// Light is the default: near-white fintech-checkout neutrals with a
	// darkened Obol green so the accent keeps contrast on white.
	ThemeLight: {
		Name: ThemeLight,
		Dark: false,
		Vars: map[string]string{
			"bg01":      "#ffffff",
			"bg02":      "#f6f8f9",
			"bg03":      "#edf1f2",
			"bg04":      "#e2e8ea",
			"bg05":      "#d5dedf",
			"stroke":    "#dfe6e8",
			"green":     "#0b9b71",
			"green-dim": "#067a57",
			"light":     "#0e1b1e",
			"body":      "#40565c",
			"muted":     "#8299a0",
			"red":       "#c94f2f",
			"amber":     "#b96f10",
		},
	},
	// Dark preserves the pre-theming palette (obol-ui stitches tokens).
	ThemeDark: {
		Name: ThemeDark,
		Dark: true,
		Vars: map[string]string{
			"bg01":      "#091011",
			"bg02":      "#111f22",
			"bg03":      "#182d32",
			"bg04":      "#243d42",
			"bg05":      "#2d4d53",
			"stroke":    "#1e3a3f",
			"green":     "#2fe4ab",
			"green-dim": "#1a7a5c",
			"light":     "#d9eef3",
			"body":      "#9cc2c9",
			"muted":     "#475e64",
			"red":       "#ff7a7a",
			"amber":     "#e89e30",
		},
	},
	// Obol leans into the brand: deep green backgrounds, bright green accent.
	ThemeObol: {
		Name: ThemeObol,
		Dark: true,
		Vars: map[string]string{
			"bg01":      "#05201a",
			"bg02":      "#0a2c23",
			"bg03":      "#0f3a2e",
			"bg04":      "#164a3b",
			"bg05":      "#1d5a48",
			"stroke":    "#1b4a3b",
			"green":     "#2fe4ab",
			"green-dim": "#1a7a5c",
			"light":     "#e4fbf2",
			"body":      "#a3d8c4",
			"muted":     "#59836f",
			"red":       "#ff8a70",
			"amber":     "#e8b04a",
		},
	},
}

// accentColorRe is the strict pattern for operator-supplied accent overrides.
// The value lands in a CSS context on public pages, so anything that is not
// a plain hex color is rejected outright (no named colors, no functions).
var accentColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// ValidateThemeName accepts empty (= default) or one of the preset names.
func ValidateThemeName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if _, ok := themePresets[name]; !ok {
		return fmt.Errorf("unknown theme %q (valid: %s)", name, strings.Join(ThemeNames(), ", "))
	}
	return nil
}

// ValidateAccentColor accepts empty (= preset accent) or a #hex color.
func ValidateAccentColor(accent string) error {
	accent = strings.TrimSpace(accent)
	if accent == "" {
		return nil
	}
	if !accentColorRe.MatchString(accent) {
		return fmt.Errorf("accent color must be a hex value like #0b9b71 (3, 4, 6, or 8 hex digits), got %q", accent)
	}
	return nil
}

// ResolveTheme returns the preset for name with the optional accent override
// applied to the green tokens. Unknown or empty names fall back to
// DefaultTheme; invalid accents are ignored (validation happens at set time —
// render time never fails). The returned Vars map is a copy.
func ResolveTheme(name, accent string) Theme {
	preset, ok := themePresets[strings.TrimSpace(name)]
	if !ok {
		preset = themePresets[DefaultTheme]
	}
	out := Theme{Name: preset.Name, Dark: preset.Dark, Vars: make(map[string]string, len(preset.Vars))}
	for k, v := range preset.Vars {
		out.Vars[k] = v
	}
	if accent = strings.TrimSpace(accent); accent != "" && accentColorRe.MatchString(accent) {
		out.Vars["green"] = accent
		out.Vars["green-dim"] = accent
	}
	return out
}

// CSSVars renders the token map as CSS custom-property declarations
// ("--bg01:#ffffff;--bg02:...;") in a fixed order, for direct inclusion
// inside a :root block. All values come from the hardcoded presets or an
// accent that already matched accentColorRe, so the output is CSS-safe by
// construction.
func (t Theme) CSSVars() string {
	var b strings.Builder
	for _, k := range themeVarOrder {
		v, ok := t.Vars[k]
		if !ok {
			continue
		}
		b.WriteString("--")
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(v)
		b.WriteString(";")
	}
	return b.String()
}

// ThemeColor is the browser-chrome color (<meta name="theme-color">).
func (t Theme) ThemeColor() string { return t.Vars["bg01"] }
