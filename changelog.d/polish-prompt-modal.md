### Fixed

- Pasting (cmd+v / bracketed paste) into the new-session prompt modal now inserts the clipboard content into the textarea instead of being silently swallowed.
- The active line in the prompt modal no longer renders with a near-black background on dark terminals — bubbles' default `CursorLine` highlight has been stripped so the textarea reads uniformly.

### Changed

- The new-session prompt modal (`n` when plan-first is enabled) has a polished layout: a "NEW SESSION / Planning →" header band, a rotating header prompt, an example-shape placeholder inside the textarea, a Refrain-purple cursor, and a two-line aligned key-chip footer that surfaces `shift+enter` for newlines alongside `enter`, `ctrl+enter`, and `esc`.
