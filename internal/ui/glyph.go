package ui

// Status glyphs for multi-line status views (network status). The glyph is
// always printed; NO_COLOR (or a non-terminal stdout) only drops the color.
const (
	GlyphOK      = "✓"
	GlyphCurrent = "▸"
	GlyphFail    = "✗"
	GlyphBusy    = "◐"
	GlyphStopped = "◼"
	GlyphBullet  = "●"
	GlyphPending = "·"
)

// Paint styles s for a status kind: "ok", "warn", "fail", "info", "dim",
// "bold" or "accent". Unknown kinds return s unchanged. Use it to compose
// lines that are written in one go (e.g. a redrawn --watch frame).
func Paint(kind, s string) string {
	switch kind {
	case "ok":
		return successStyle.Render(s)
	case "warn":
		return warnStyle.Render(s)
	case "fail":
		return errorStyle.Render(s)
	case "info":
		return infoStyle.Render(s)
	case "dim":
		return dimStyle.Render(s)
	case "bold":
		return boldStyle.Render(s)
	case "accent":
		return accentStyle.Render(s)
	default:
		return s
	}
}

// Heading renders a section heading in the same style as Info ("==> title"),
// for callers that build a frame as a string instead of printing directly.
func Heading(title string) string {
	return infoStyle.Render("==>") + " " + title
}
