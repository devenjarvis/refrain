### Added

- Configurable Claude model for plan drafting and spawned agents. New global and per-repo settings `plan_model` (default `claude-sonnet-4-6`) and `agent_model` (default empty — let the Claude CLI pick) can be edited from the Plan Model and Agent Model fields in the global and per-repo config forms. The plan model is forwarded as `--model` to the plan drafter subprocess; the agent model is forwarded as `--model` to spawned `claude` agents (ignored for non-claude agent programs).
