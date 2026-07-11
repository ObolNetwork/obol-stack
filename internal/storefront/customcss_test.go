package storefront

import (
	"strings"
	"testing"
)

func TestValidateCustomCSS(t *testing.T) {
	valid := []string{
		"",
		":root { --green: #ff00ff; }",
		"[data-obol=\"price\"] { font-size: 40px; }\n.obol-card { border-radius: 0; }",
		"/* comments are fine */ body { background: url(https://cdn.example.com/bg.png); }",
	}
	for _, css := range valid {
		if err := ValidateCustomCSS(css); err != nil {
			t.Errorf("ValidateCustomCSS(%q): %v", css, err)
		}
	}

	invalid := []string{
		"</style><script>alert(1)</script>",
		"</StYlE ><p>x",
		"a{}<script>",
		"a{} <!-- sneaky",
		strings.Repeat("x", (64<<10)+1),
	}
	for _, css := range invalid {
		if err := ValidateCustomCSS(css); err == nil {
			t.Errorf("ValidateCustomCSS must reject %.40q", css)
		}
		if got := SafeCustomCSS(css); got != "" {
			t.Errorf("SafeCustomCSS must drop invalid css %.40q, got %.40q", css, got)
		}
	}

	if got := SafeCustomCSS(".x{color:red}"); got != ".x{color:red}" {
		t.Errorf("SafeCustomCSS mangled valid css: %q", got)
	}
}
