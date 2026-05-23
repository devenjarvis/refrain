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
