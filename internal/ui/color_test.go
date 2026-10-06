package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestDisableColorStripsForegroundColor(t *testing.T) {
	saved := []lipgloss.Style{infoStyle, successStyle, warnStyle, errorStyle, dimStyle, boldStyle, bannerStyle, taglineStyle, accentStyle}
	t.Cleanup(func() {
		infoStyle, successStyle, warnStyle, errorStyle, dimStyle, boldStyle, bannerStyle, taglineStyle, accentStyle =
			saved[0], saved[1], saved[2], saved[3], saved[4], saved[5], saved[6], saved[7], saved[8]
	})

	if _, ok := dimStyle.GetForeground().(lipgloss.NoColor); ok {
		t.Fatal("precondition: dimStyle should carry a brand color")
	}

	disableColor()

	for name, s := range map[string]lipgloss.Style{
		"info": infoStyle, "success": successStyle, "warn": warnStyle, "error": errorStyle,
		"dim": dimStyle, "banner": bannerStyle, "tagline": taglineStyle, "accent": accentStyle,
	} {
		if _, ok := s.GetForeground().(lipgloss.NoColor); !ok {
			t.Errorf("%s style still has a foreground color after disableColor", name)
		}
	}
	if !infoStyle.GetBold() {
		t.Error("NO_COLOR governs color only; bold should be preserved")
	}
}

func TestColorDisabledHonoursNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if ColorDisabled() {
		t.Fatal("empty NO_COLOR must not disable color")
	}
	t.Setenv("NO_COLOR", "1")
	if !ColorDisabled() {
		t.Fatal("NO_COLOR=1 must disable color")
	}
}
