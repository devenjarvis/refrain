### Fixed

- Branch rename and task summary subprocesses no longer crash on Claude's strict MCP schema validator. The `--mcp-config` payload is now `{"mcpServers":{}}` instead of `{}`, which Claude rejects with `mcpServers: Invalid input: expected record, received undefined`.
