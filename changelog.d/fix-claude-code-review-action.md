### Fixed

- Claude Code Review GitHub Action now actually posts review comments on PRs. The previous configuration invoked the `code-review` plugin's slash command, which printed feedback to stdout instead of using the action's inline-comment MCP tool, so reviews ran successfully but never reached the PR. The workflow now uses the canonical inline-comment prompt and grants `mcp__github_inline_comment__create_inline_comment` so feedback gets buffered and flushed to the PR.
