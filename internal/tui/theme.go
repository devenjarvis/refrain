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

	// ColorWaiting is the accent for agents in StatusWaiting (permission
	// prompts, input blocks); surfaced as a status badge in the dashboard.
	ColorWaiting = initColor("#D946EF")

	// Pipeline-section accents: the colors that identify the BUILDING,
	// REVIEWING, and SHIPPING stages across the dashboard and panels.
	ColorBuilding  = initColor("#7ec8e3")
	ColorReviewing = initColor("#9b7fdb")
	ColorShipping  = initColor("#5ab58a")

	// Break-overlay accents: the wellness break overlay's title and resume cue.
	ColorBreakTitle  = initColor("#38BDF8")
	ColorBreakAccent = initColor("#34D399")

	StyleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	StyleSubtle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Status badges: colored, non-bold status text rendered in section rows.
	StyleSuccess = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	StyleError = lipgloss.NewStyle().
			Foreground(ColorError)

	StyleWarning = lipgloss.NewStyle().
			Foreground(ColorWarning)

	StyleWaiting = lipgloss.NewStyle().
			Foreground(ColorWaiting)

	StyleActive = lipgloss.NewStyle().
			Foreground(ColorSecondary)

	StyleLink = lipgloss.NewStyle().
			Underline(true).
			Foreground(ColorSecondary)

	// Section headings: bold accent text titling a panel/section (REVIEW,
	// CHECKS, SHIPPING…). Override the foreground inline for non-primary
	// sections, e.g. StyleHeading.Foreground(ColorShipping).
	StyleHeading = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// StyleCardTitle is a session/card name: bold body-text color.
	StyleCardTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorText)

	// StyleBold is plain bold text that inherits or overrides its color inline.
	StyleBold = lipgloss.NewStyle().Bold(true)

	// StyleMutedItalic is a de-emphasized description line.
	StyleMutedItalic = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true)

	// StyleAccent is plain primary-accent text: cursor bars and primary badges.
	StyleAccent = lipgloss.NewStyle().
			Foreground(ColorPrimary)

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
