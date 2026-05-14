### Changed

- The Plan Model and Agent Model fields in the global and per-repo config forms are now dropdowns populated with the current Claude model IDs (`claude-opus-4-7`, `claude-sonnet-4-6`, `claude-haiku-4-5-20251001`), cycled with `←`/`→`. Agent Model also offers a blank option meaning "claude default" so it can be left unset. Users who need a model not in the list can still edit `~/.config/refrain/settings.json` (or `.refrain/settings.json`) directly.
