package storefront

import (
	"strings"
	"testing"
)

func TestResolveTheme_DefaultIsLight(t *testing.T) {
	for _, name := range []string{"", "nope", "  "} {
		th := ResolveTheme(name, "")
		if th.Name != ThemeLight {
			t.Fatalf("ResolveTheme(%q) = %q, want %q", name, th.Name, ThemeLight)
		}
		if th.Dark {
			t.Fatalf("light theme must not be dark")
		}
	}
}

func TestResolveTheme_PresetsComplete(t *testing.T) {
	for _, name := range ThemeNames() {
		th := ResolveTheme(name, "")
		if th.Name != name {
			t.Fatalf("ResolveTheme(%q).Name = %q", name, th.Name)
		}
		for _, k := range themeVarOrder {
			if v := th.Vars[k]; !strings.HasPrefix(v, "#") {
				t.Errorf("theme %q: token %q = %q, want a hex color", name, k, v)
			}
		}
	}
}

func TestResolveTheme_AccentOverride(t *testing.T) {
	th := ResolveTheme(ThemeLight, "#ff00ff")
	if th.Vars["green"] != "#ff00ff" || th.Vars["green-dim"] != "#ff00ff" {
		t.Fatalf("accent not applied: green=%q green-dim=%q", th.Vars["green"], th.Vars["green-dim"])
	}
	// Invalid accents are ignored at render time (validated at set time).
	th = ResolveTheme(ThemeLight, "red;} body{display:none}")
	if th.Vars["green"] != themePresets[ThemeLight].Vars["green"] {
		t.Fatalf("invalid accent must be ignored, got green=%q", th.Vars["green"])
	}
}

func TestResolveTheme_ReturnsCopy(t *testing.T) {
	th := ResolveTheme(ThemeDark, "")
	th.Vars["bg01"] = "#mutated"
	if themePresets[ThemeDark].Vars["bg01"] == "#mutated" {
		t.Fatal("ResolveTheme leaked the preset map")
	}
}

func TestCSSVars_OrderStableAndSafe(t *testing.T) {
	css := ResolveTheme(ThemeDark, "").CSSVars()
	if !strings.HasPrefix(css, "--bg01:#091011;") {
		t.Fatalf("CSSVars must start with bg01, got %q", css)
	}
	if strings.ContainsAny(css, "<>{}\"'") {
		t.Fatalf("CSSVars contains CSS/HTML-breaking characters: %q", css)
	}
	if ResolveTheme(ThemeDark, "").CSSVars() != css {
		t.Fatal("CSSVars not deterministic")
	}
}

func TestValidateThemeName(t *testing.T) {
	if err := ValidateThemeName(""); err != nil {
		t.Fatalf("empty theme name must be valid: %v", err)
	}
	for _, name := range ThemeNames() {
		if err := ValidateThemeName(name); err != nil {
			t.Fatalf("preset %q must validate: %v", name, err)
		}
	}
	if err := ValidateThemeName("solarized"); err == nil {
		t.Fatal("unknown theme name must fail validation")
	}
}

func TestValidateAccentColor(t *testing.T) {
	valid := []string{"", "#fff", "#ffff", "#2fe4ab", "#2fe4abcc"}
	for _, v := range valid {
		if err := ValidateAccentColor(v); err != nil {
			t.Errorf("ValidateAccentColor(%q): %v", v, err)
		}
	}
	invalid := []string{"red", "#gggggg", "#12345", "url(x)", "#fff;} h1{", "2fe4ab"}
	for _, v := range invalid {
		if err := ValidateAccentColor(v); err == nil {
			t.Errorf("ValidateAccentColor(%q) must fail", v)
		}
	}
}

func TestThemeColorMatchesBg01(t *testing.T) {
	if got := ResolveTheme(ThemeLight, "").ThemeColor(); got != "#ffffff" {
		t.Fatalf("light ThemeColor = %q", got)
	}
	if got := ResolveTheme(ThemeDark, "").ThemeColor(); got != "#091011" {
		t.Fatalf("dark ThemeColor = %q", got)
	}
}
