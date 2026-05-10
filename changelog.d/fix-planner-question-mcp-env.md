### Fixed

- Planner clarifying questions now actually surface in the plan editor. The `BATON_PLANNER_QUESTION_SOCKET` value is written into the MCP server entry's `env` map in `--mcp-config`, so the `baton planner-question-server` child receives the socket path regardless of how Claude Code manages env inheritance for MCP subprocess hosts. Previously the socket path was only set on the parent `claude` process, the MCP child dialed an empty path and exited silently, and `ask_user` failed as a tool error the planner moved past.
