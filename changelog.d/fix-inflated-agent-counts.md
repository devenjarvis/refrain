### Fixed

- Quit no longer warns "agents running" when only shell agents or already-exited (Done/Error) agents remain, and the soft concurrent-agent limit no longer counts those toward the cap. `Manager.AgentCount` and `App.activeAgentCount` now exclude shells and exited agents; a new `Session.LiveAgentCount` captures the live-non-shell count for capacity checks.
