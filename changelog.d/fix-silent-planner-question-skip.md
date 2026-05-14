### Fixed

- Planner clarifying questions are no longer silently dropped when no plan editor is open. The `plannerQuestionMsg` handler now writes a structured JSON line per question to `.refrain/logs/planner.log` (one of `auto-opened`, `routed-to-existing`, `routed-to-background-editor`, `skipped-no-editor`, or `skipped-session-missing`) and surfaces a status-bar error when the skip path fires, so a question that can't reach the editor is visible to the developer instead of vanishing into an empty answer on the socket.
