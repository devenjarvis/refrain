### Fixed

- Planner subprocess now passes `--allowed-tools` alongside `--tools` so `ask_user` (and `WebFetch`/`WebSearch`) are auto-approved in the non-interactive `-p` session. Previously `--tools` only controlled tool visibility; without `--allowed-tools` the permission gate blocked the MCP call and the planner silently skipped clarifying questions.
