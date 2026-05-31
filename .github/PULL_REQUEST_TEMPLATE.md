## Summary

<!-- Bullet points describing what changed and why. Focus on the *why* — the diff shows the *what*. -->

-

## Test plan

<!-- Check the commands you actually ran. Add manual TUI steps when UI behavior changed. -->

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `gofmt -l .` (clean)
- [ ] `golangci-lint run ./...`
- [ ] `go test -race ./...`
- [ ] Manual TUI:

## Checklist

- [ ] Updated `CHANGELOG.md` under `[Unreleased]` (or this PR is release-only)
- [ ] New behavior has tests, or is documented as manual-test-only in `CLAUDE.md`
- [ ] No new public API surface without a note in the PR description
