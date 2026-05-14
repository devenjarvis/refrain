### Added

- Stacked PR tracking: refrain now detects when a session's branch targets another open PR (up to 3 levels deep) and displays the full chain in the sidebar (`#101 ✓ → #102 ○`) and preview panel (`↳ PR #101 (branch: feature-base → main)`).
- Checks summary panel shows a stacked-PR context header (`── #102 stacked on #101 ──`) when the session is part of a stack.
