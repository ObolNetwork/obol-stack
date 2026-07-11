package storefront

import (
	"fmt"
	"os"
	"strings"
)

// maxCustomCSSBytes caps operator CSS. It rides the profile/catalog
// ConfigMaps (1 MiB object limit shared with logos and the service list),
// and every themed page inlines it — keep it a stylesheet, not an asset
// pipeline.
const maxCustomCSSBytes = 64 << 10 // 64 KiB

// ValidateCustomCSS enforces the injection contract for operator CSS: it is
// inlined verbatim inside a <style> element on public pages, so the ONLY
// hard security requirement is that it cannot close that element (or open a
// comment/CDATA escape) and start emitting markup. Everything else is the
// operator styling their own storefront.
func ValidateCustomCSS(css string) error {
	if css == "" {
		return nil
	}
	if len(css) > maxCustomCSSBytes {
		return fmt.Errorf("custom CSS is %d KiB; max %d KiB (it is inlined into every page and stored in the catalog ConfigMap)", len(css)>>10, maxCustomCSSBytes>>10)
	}
	lower := strings.ToLower(css)
	for _, forbidden := range []string{"</style", "<script", "<!--"} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("custom CSS must not contain %q — it is inlined inside a <style> element", forbidden)
		}
	}
	return nil
}

// SafeCustomCSS is the render-time guard: it re-runs the validation and
// returns "" when the stored value would break out of a <style> element.
// Renderers must use this (never the raw profile field) when inlining.
func SafeCustomCSS(css string) string {
	if ValidateCustomCSS(css) != nil {
		return ""
	}
	return css
}

// CustomCSSFromFile reads and validates a local stylesheet for
// `sell info set --css-file`.
func CustomCSSFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read css file: %w", err)
	}
	css := strings.TrimSpace(string(data))
	if css == "" {
		return "", fmt.Errorf("css file %s is empty", path)
	}
	if err := ValidateCustomCSS(css); err != nil {
		return "", err
	}
	return css, nil
}
