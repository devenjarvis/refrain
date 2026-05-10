### Fixed

- Planner subprocess now runs in the session's worktree directory instead of baton's process cwd. With multiple registered repos a plan drafted for repo B would previously research repo A's code; it now correctly reads the right codebase.
- `--allowedTools` flag (camelCase) replaces the silently-dropped `--allowed-tools` (kebab). The kebab form was ignored by the Claude CLI, which caused the permission gate to block the planner's `mcp__baton_planner_question__ask_user` tool in non-interactive `-p` mode — the model would see a tool error and abandon any clarifying question rather than surfacing it to the editor.
- `shift+enter` newline insertion in the prompt modal works correctly in Kitty-capable terminals. Bubbletea v2 always enables basic key disambiguation (Kitty protocol flag 1), which disambiguates `shift+enter` from plain `enter`; the modal already extended the `InsertNewline` binding to include `shift+enter` so no further code change was needed.
