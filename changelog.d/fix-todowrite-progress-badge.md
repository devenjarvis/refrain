### Fixed

- TodoWrite progress badge now appears on Building-phase dashboard cards during real Claude runs — PreToolUse hook was never firing because the generated settings file lacked the required `matcher` field; added `matcher: "*"` to the PreToolUse entry
- TodoItem unmarshalling no longer silently swallows errors; unmarshal failures are logged to stderr when `REFRAIN_HOOK_DEBUG` is set
- TodoItem now accepts both `activeForm` (camelCase) and `active_form` (snake_case) keys for forward compatibility

### Added

- `REFRAIN_HOOK_DEBUG` env var: when set, writes timestamped PreToolUse event details to `.refrain/logs/hooks.log` and TodoWrite unmarshal results to stderr for diagnostics
