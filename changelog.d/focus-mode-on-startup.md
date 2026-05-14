### Changed

- The global `focus_mode_enabled` setting is now exclusively the startup default — when `true`, refrain enters focus mode the moment the dashboard mounts. Saving the global settings form no longer overrides the live focus state; the runtime `f` toggle is the only thing that switches focus mode mid-session, so opening settings can't drag a working session out from under you. The form field is now labeled "Focus Mode on Startup" to match.
