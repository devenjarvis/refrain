package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/devenjarvis/refrain/internal/tui/theme"
)

// This file is the tui package's bridge to the design-system registry in
// internal/tui/theme. The registry is the single source of truth for token
// VALUES; the aliases below let tui code reference roles unqualified, and the
// Style* objects compose those roles into the lipgloss styles this package
// renders. Subpackages (diff, mdrender) import the theme package directly.
// See DESIGN.md for the role catalog.

// Color-role aliases. Values live in internal/tui/theme; never redefine a hex
// here — add the role there.
var (
	ColorPrimary       = theme.ColorPrimary
	ColorSecondary     = theme.ColorSecondary
	ColorPrimaryLight  = theme.ColorPrimaryLight
	ColorSuccess       = theme.ColorSuccess
	ColorWarning       = theme.ColorWarning
	ColorError         = theme.ColorError
	ColorWaiting       = theme.ColorWaiting
	ColorMuted         = theme.ColorMuted
	ColorMutedLight    = theme.ColorMutedLight
	ColorText          = theme.ColorText
	ColorTextProse     = theme.ColorTextProse
	ColorBg            = theme.ColorBg
	ColorSurfaceRaised = theme.ColorSurfaceRaised
	ColorHairline      = theme.ColorHairline
	ColorBuilding      = theme.ColorBuilding
	ColorReviewing     = theme.ColorReviewing
	ColorShipping      = theme.ColorShipping
	ColorBreakTitle    = theme.ColorBreakTitle
	ColorBreakAccent   = theme.ColorBreakAccent
)

// Composed styles. These are tui-presentation compositions built from the
// color roles above; subpackages build their own from the registry directly.
var (
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
			Background(ColorSurfaceRaised).
			Padding(theme.PadStatusBar[0], theme.PadStatusBar[1])
)

// modalBoxStyle is the canonical centered-overlay box: a rounded primary border
// with PadModal padding at the given width. Shared by the repo-config,
// repo-checks, and global-settings modals so they render identically.
func modalBoxStyle(width int) lipgloss.Style {
	return theme.BorderModal().
		Padding(theme.PadModal[0], theme.PadModal[1]).
		Width(width)
}

// placeCentered centers content within a width×height viewport. It wraps the
// repeated lipgloss.Place(w, h, Center, Center, content) overlay idiom.
func placeCentered(width, height int, content string) string {
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)
}
