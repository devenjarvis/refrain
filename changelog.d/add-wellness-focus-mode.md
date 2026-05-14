### Added

- **Wellness Focus Mode** (`f` to toggle): shifts the dashboard from per-agent monitoring to a silent aggregate view, reducing multi-agent fatigue. Grounded in BCG/2026 research (n=1,488) showing 33% more decision fatigue and 39% more errors when overseeing 4+ parallel AI agents simultaneously.
  - Focus panel shows session progress bar, status counts, attention rows for agents needing input, and last-review timestamp
  - Idle chimes suppressed in focus mode; Waiting (permission prompt) chimes still fire
  - Soft concurrent-agent limit: first `n` press at the configured limit shows a warning, second press overrides
  - Break hint appears in the statusbar after the configured session duration (default 90m)
  - Wellness log appended to `.refrain/logs/wellness.log` on quit (date, duration, agent/session counts, focus switches)
  - Three new global settings: `Focus Mode`, `Focus Session (min)`, `Max Concurrent Agents` — accessible via `s`
