### Changed

- PR compose screen is now a full-page editor matching the plan editor's typography and layout, replacing the centered modal with a rounded border
- Default mode on open is scroll: the AI-drafted title renders as styled text and the body renders through the markdown renderer with syntax highlighting and left-margin centering on wide terminals
- Press `i` to enter edit mode; the title field (textinput) receives focus first
- In edit mode, `tab`/`shift+tab` cycles focus between the title input and the markdown-aware body textarea
- `esc` in edit mode returns to scroll mode preserving edits; `esc` in scroll mode cancels PR creation
- `ctrl+enter` submits from either mode; `ctrl+d` toggles the draft flag; both work without entering edit mode
- Header renders `PR DRAFT  ›  <session-name>` left-aligned with the draft/ready badge right-aligned
- Footer shows mode-appropriate key hints using the same active/subtle pattern as the plan editor
- `j`/`k`, `pgdn`/`pgup`, `g`/`G` scroll the body in scroll mode
