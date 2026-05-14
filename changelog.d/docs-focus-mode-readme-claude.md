### Added

- **Focus Mode documentation** in README and CLAUDE.md: users can now discover, enable, and use Focus Mode without reading source code.
  - "Why Refrain?" section extended with cognitive-load rationale (BCG brain-fry finding, 9.5-min context-switch cost, 90-min cycle model)
  - New `### Focus Mode` subsection under Usage with keybindings table and config reference (`focus_mode_enabled`, `focus_session_minutes`, `max_concurrent_agents`)
  - Dashboard keybindings table updated with `f` → Toggle Focus Mode
  - Terminal-preview keybindings heading renamed from "Focus mode" to "Terminal preview" to eliminate naming collision
  - CLAUDE.md Key Patterns entry covering the three-panel state machine, `focusCursorSection` enum, wellness features, and config fields
  - CLAUDE.md Focus Mode Development Philosophy section with research-backed guidance for future contributors (3-agent ceiling, batch-over-stream, signal noise budget, oversight tax, 90-minute cycle, wellbeing as differentiator)
