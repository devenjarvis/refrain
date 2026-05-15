### Changed

- Removed ~1k lines of dead code: the unused `git/diff.go` unified-diff parser (superseded by `internal/diffmodel`), `git.ListWorktrees`, `setlist.Load`, `config.SetRepoAlias`, `config.LoadResolved`, `diffmodel.ZipWrappedRow`, `diff.FileHash`, `tui.configForm.selectIndex`, and three write-only `Agent` fields (`composing`, `sessionStartedAt`, `cleanExit`) along with their tests. No user-facing behavior changes.
