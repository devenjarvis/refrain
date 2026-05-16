### Fixed

The Claude PR Review workflow now authenticates with `CLAUDE_CODE_OAUTH_TOKEN` instead of `ANTHROPIC_API_KEY`, matching the `@claude` mention workflow and unblocking the multi-agent reviewer for accounts that authenticate via Claude Code OAuth rather than a raw Anthropic API key.
