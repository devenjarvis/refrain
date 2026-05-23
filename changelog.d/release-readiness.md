### Added

- Linux audio chime support: tries `paplay` (PulseAudio), then `aplay` (ALSA). Silent on unsupported platforms.
- Linux editor detection: probes PATH for `code`, `zed`, `cursor`, `idea`, and other editor CLIs. Also picks up `$VISUAL` and `$EDITOR`.
- `--version` flag (in addition to the existing `refrain version` subcommand).
- `NO_COLOR` environment variable support: when set, all hardcoded colors are suppressed.

### Fixed

- Removed broken demo GIF image tag from README.
- Added platform notes to README Requirements section (macOS + Linux support, no Windows).
