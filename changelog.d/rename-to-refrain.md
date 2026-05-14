### Changed

- Renamed the project from "Baton" to "Refrain". The binary is now `refrain`, the
  Go module is `github.com/devenjarvis/refrain`, the Homebrew formula installs
  as `devenjarvis/tap/refrain`, and the default branch prefix is `refrain/`.
  Per-repo state moved from `.baton/` to `.refrain/`, global state from
  `~/.baton/` to `~/.refrain/`, and the hook env vars from `BATON_*` to
  `REFRAIN_*`. The Claude Code hook command is now `refrain hook <event>`.

### Migration

- On first launch the new binary auto-migrates `~/.baton/` → `~/.refrain/` and,
  for each registered repo, `<repo>/.baton/` → `<repo>/.refrain/`. After the
  rename `git worktree repair` rewrites the worktree gitdir pointers so active
  worktrees keep working, and `.gitignore` is updated to ignore `.refrain/`.
  Existing `baton/<name>` branches are not renamed; they continue to work but
  new sessions land on `refrain/<name>`.
