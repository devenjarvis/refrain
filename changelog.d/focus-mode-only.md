### Changed

- The pipeline view (formerly Focus Mode) is now the only dashboard mode. The split-panel layout, `f` toggle, and `focus_mode_enabled` config key are removed.
- Existing config files containing `focus_mode_enabled` continue to load — the unknown key is silently ignored.
- Workflow keys `c` add agent, `t` shell, `e` IDE, `p` PR, `d` diff, `o` open branch, `a` add repo, `s` settings, `x` kill agent, `X` kill session now act on the cursor-selected session in the pipeline.
- Click on a session card moves the cursor; double-click activates (focusLaunch for active sessions, review panel for queue rows). Clicking the PR indicator on a review row opens the PR in the browser.
- Session creation auto-advances the pipeline cursor to the new session and opens its agent in focusLaunch.
- Status bar shows a single unified hint set instead of separate dashboard / focus-mode / focus-launch sets.

### Removed

- `focus_mode_enabled` config key (silently ignored if present in legacy config).
- The `f` keybinding for toggling Focus Mode.
- The `Focus Mode on Startup` toggle in the global settings form.
- The split-panel "preview" layout — agent terminals are reached only via focusLaunch (`⏎` on a session, `esc` to return).
