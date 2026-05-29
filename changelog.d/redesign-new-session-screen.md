### Changed

- Replaced the cramped 80×6 centered overlay with a full-viewport **New Session** composition screen. The textarea now fills the available height and the layout adapts to terminal width. On terminals ≥ 110 columns a sidebar shows the `Plan → Build → Review → Ship` flow and three example prompts as a first-impression guide.
- `ctrl+j` is now the advertised newline key, fixing unreliable behaviour on iTerm2 / macOS Terminal / Windows Terminal where `shift+enter` was silently ignored. `shift+enter` continues to work on terminals that support the Kitty keyboard protocol.
- Pressing `esc` returns to whichever view was active before opening the screen (dashboard or repo picker), without creating a session or worktree.
