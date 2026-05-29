package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

var noColor = os.Getenv("NO_COLOR") != ""

var (
	ColorPrimary   = initColor("#7C3AED")
	ColorSecondary = initColor("#06B6D4")
	ColorSuccess   = initColor("#10B981")
	ColorWarning   = initColor("#F59E0B")
	ColorError     = initColor("#EF4444")
	ColorMuted     = initColor("#6B7280")
	ColorText      = initColor("#F9FAFB")
	ColorBg        = initColor("#111827")

	StyleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	StyleSubtle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	StyleSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	StyleError = lipgloss.NewStyle().
			Foreground(ColorError)

	StyleWarning = lipgloss.NewStyle().
			Foreground(ColorWarning)

	StyleActive = lipgloss.NewStyle().
			Foreground(ColorSecondary)

	StyleLink = lipgloss.NewStyle().
			Underline(true).
			Foreground(ColorSecondary)

	StyleStatusBar = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(initColor("#1F2937")).
			Padding(0, 1)
)

func initColor(hex string) lipgloss.Color {
	if noColor {
		return lipgloss.Color("")
	}
	return lipgloss.Color(hex)
}

// modalBoxStyle is the canonical centered-overlay box: a rounded primary
// border with 1×2 padding at the given width. Shared by the repo-config,
// repo-checks, and global-settings modals so they render identically.
func modalBoxStyle(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(width)
}

// placeCentered centers content within a width×height viewport. It wraps the
// repeated lipgloss.Place(w, h, Center, Center, content) overlay idiom.
func placeCentered(width, height int, content string) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}
