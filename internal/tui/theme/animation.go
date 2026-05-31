package theme

import "github.com/charmbracelet/lipgloss"

// Animation palettes are ordered color sequences (not single roles): a renderer
// cycles through them frame by frame, driven by the model's tick timestamp.

// BreatheColors cycles calm hues for the wellness break overlay's breathing
// animation. The first and third entries are the break-overlay accents
// (ColorBreakTitle, ColorBreakAccent); the indigo and rose hues exist solely
// for this cycle and are not standalone roles.
var BreatheColors = [4]lipgloss.Color{
	ColorBreakTitle,      // sky blue
	initColor("#818CF8"), // indigo
	ColorBreakAccent,     // emerald
	initColor("#F472B6"), // rose
}

// CompleteColors pulse warm tones once the focus timer is up so the screen
// reads unmistakably as "done" without auto-advancing back to work. The gold
// entry coincides numerically with ColorCodeFg but is semantically unrelated;
// kept separate so the animation is not coupled to markdown styling.
var CompleteColors = [3]lipgloss.Color{
	ColorWarning,         // amber
	initColor("#FBBF24"), // gold
	initColor("#FACC15"), // yellow
}
