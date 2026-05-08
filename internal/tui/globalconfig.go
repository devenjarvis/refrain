package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/devenjarvis/baton/internal/config"
)

const (
	fieldFocusSession = "Focus Session (min)"
	fieldFocusBreak   = "Break (min)"
	fieldMaxAgents    = "Max Concurrent Agents"
	fieldMaxReview    = "Max Review Backlog"
)

// globalConfigSaveMsg is emitted when the global config form is saved.
type globalConfigSaveMsg struct {
	settings *config.GlobalSettings
}

// globalConfigCancelMsg is emitted when the global config form is cancelled.
type globalConfigCancelMsg struct{}

// globalConfigModel renders a centered overlay for editing global settings.
type globalConfigModel struct {
	form   configForm
	width  int
	height int
}

func newGlobalConfigModel(gs *config.GlobalSettings, width, height int) globalConfigModel {
	if gs == nil {
		gs = &config.GlobalSettings{}
	}

	// Determine current values (use defaults for nil fields).
	audioEnabled := config.DefaultAudioEnabled
	if gs.AudioEnabled != nil {
		audioEnabled = *gs.AudioEnabled
	}
	bypassPerms := config.DefaultBypassPermissions
	if gs.BypassPermissions != nil {
		bypassPerms = *gs.BypassPermissions
	}
	defaultBranch := ""
	if gs.DefaultBranch != nil {
		defaultBranch = *gs.DefaultBranch
	}
	branchPrefix := ""
	if gs.BranchPrefix != nil {
		branchPrefix = *gs.BranchPrefix
	}
	agentProgram := ""
	if gs.AgentProgram != nil {
		agentProgram = *gs.AgentProgram
	}
	ideCommand := ""
	if gs.IDECommand != nil {
		ideCommand = *gs.IDECommand
	}
	sidebarWidth := ""
	if gs.SidebarWidth != nil {
		sidebarWidth = strconv.Itoa(*gs.SidebarWidth)
	}

	focusSessionMinutes := ""
	if gs.FocusSessionMinutes != nil {
		focusSessionMinutes = strconv.Itoa(*gs.FocusSessionMinutes)
	}
	focusBreakMinutes := ""
	if gs.FocusBreakMinutes != nil {
		focusBreakMinutes = strconv.Itoa(*gs.FocusBreakMinutes)
	}
	maxConcurrentAgents := ""
	if gs.MaxConcurrentAgents != nil {
		maxConcurrentAgents = strconv.Itoa(*gs.MaxConcurrentAgents)
	}
	maxReviewBacklog := ""
	if gs.MaxReviewBacklog != nil {
		maxReviewBacklog = strconv.Itoa(*gs.MaxReviewBacklog)
	}

	inputWidth := 30

	var fields []formField
	fields = addToggle(fields, "Audio Enabled", audioEnabled)
	fields = addToggle(fields, "Bypass Permissions", bypassPerms)
	fields = addTextInput(fields, "Default Branch", defaultBranch, "auto-detect", inputWidth)
	fields = addTextInput(fields, "Branch Prefix", branchPrefix, config.DefaultBranchPrefix, inputWidth)
	fields = addTextInput(fields, "Agent Program", agentProgram, config.DefaultAgentProgram, inputWidth)
	fields = addEditorFields(fields, ideCommand)
	fields = addTextInput(fields, "Sidebar Width", sidebarWidth, strconv.Itoa(config.DefaultSidebarWidth), inputWidth)
	fields = addTextInput(fields, fieldFocusSession, focusSessionMinutes, strconv.Itoa(config.DefaultFocusSessionMinutes), inputWidth)
	fields = addTextInput(fields, fieldFocusBreak, focusBreakMinutes, strconv.Itoa(config.DefaultFocusBreakMinutes), inputWidth)
	fields = addTextInput(fields, fieldMaxAgents, maxConcurrentAgents, strconv.Itoa(config.DefaultMaxConcurrentAgents), inputWidth)
	fields = addTextInput(fields, fieldMaxReview, maxReviewBacklog, strconv.Itoa(config.DefaultMaxReviewBacklog), inputWidth)

	return globalConfigModel{
		form:   newConfigForm(fields, width),
		width:  width,
		height: height,
	}
}

func (m globalConfigModel) Update(msg tea.Msg) (globalConfigModel, tea.Cmd) {
	switch msg.(type) {
	case configFormSaveMsg:
		return m, func() tea.Msg {
			return globalConfigSaveMsg{settings: m.extractSettings()}
		}
	case configFormCancelMsg:
		return m, func() tea.Msg { return globalConfigCancelMsg{} }
	}

	cmd := m.form.Update(msg)
	return m, cmd
}

func (m globalConfigModel) View() string {
	boxWidth := 64
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(boxWidth)

	title := StyleTitle.Render("Global Settings")
	hint := StyleSubtle.Render("j/k navigate  ←/→ select  enter edit/toggle  ctrl+s save  esc cancel")

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title, "",
		m.form.View(), "",
		hint,
	)

	box := boxStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// extractSettings converts form field values back to a GlobalSettings struct.
// Only non-default values are set (nil for defaults) to keep config.json clean.
func (m globalConfigModel) extractSettings() *config.GlobalSettings {
	s := &config.GlobalSettings{}

	audioEnabled := m.form.toggleValue("Audio Enabled")
	s.AudioEnabled = &audioEnabled

	bypassPerms := m.form.toggleValue("Bypass Permissions")
	s.BypassPermissions = &bypassPerms

	if v := m.form.textValue("Default Branch"); v != "" {
		s.DefaultBranch = &v
	}
	if v := m.form.textValue("Branch Prefix"); v != "" {
		s.BranchPrefix = &v
	}
	if v := m.form.textValue("Agent Program"); v != "" {
		s.AgentProgram = &v
	}
	if v := extractIDECommand(m.form); v != "" {
		s.IDECommand = &v
	}
	if v := m.form.textValue("Sidebar Width"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			clamped := config.ClampSidebarWidth(n)
			s.SidebarWidth = &clamped
		}
	}

	if v := m.form.textValue(fieldFocusSession); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.FocusSessionMinutes = &n
		}
	}
	if v := m.form.textValue(fieldFocusBreak); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.FocusBreakMinutes = &n
		}
	}
	if v := m.form.textValue(fieldMaxAgents); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			s.MaxConcurrentAgents = &n
		}
	}
	if v := m.form.textValue(fieldMaxReview); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 { // 0 means "off"
			s.MaxReviewBacklog = &n
		}
	}

	return s
}
