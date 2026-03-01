package ui

import "github.com/charmbracelet/lipgloss"

// Obol brand colors — from blog.obol.org/branding.
const (
	ColorObolGreen  = "#2FE4AB" // Primary brand green
	ColorObolCyan   = "#3CD2DD" // Light blue / info
	ColorObolPurple = "#9167E4" // Accent purple
	ColorObolAmber  = "#FABA5A" // Warning amber
	ColorObolRed    = "#DD603C" // Error red-orange
	ColorObolAcid   = "#B6EA5C" // Highlight acid green
	ColorObolMuted  = "#667A80" // Muted gray
	ColorObolLight  = "#97B2B8" // Light muted
)

// Brand-specific styles for special UI elements.
var (
	bannerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolGreen)).Bold(true)
	taglineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolMuted))
	accentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorObolPurple))
)

// Banner returns the Obol Stack ASCII art rendered in brand colors.
func Banner() string {
	art := "" +
		"   ██████╗ ██████╗  ██████╗ ██╗         ███████╗████████╗ █████╗  ██████╗██╗  ██╗\n" +
		"  ██╔═══██╗██╔══██╗██╔═══██╗██║         ██╔════╝╚══██╔══╝██╔══██╗██╔════╝██║ ██╔╝\n" +
		"  ██║   ██║██████╔╝██║   ██║██║         ███████╗   ██║   ███████║██║     █████╔╝\n" +
		"  ██║   ██║██╔══██╗██║   ██║██║         ╚════██║   ██║   ██╔══██║██║     ██╔═██╗\n" +
		"  ╚██████╔╝██████╔╝╚██████╔╝███████╗    ███████║   ██║   ██║  ██║╚██████╗██║  ██╗\n" +
		"   ╚═════╝ ╚═════╝  ╚═════╝ ╚══════╝    ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝"

	return bannerStyle.Render(art) + "\n" +
		taglineStyle.Render("   Decentralised infrastructure for AI agents")
}
