### Changed

- The soft concurrency warning now counts active sessions instead of individual agents. Sessions in Shipping or Complete phases are excluded since they require no active oversight. Config key renamed from `max_concurrent_agents` to `max_concurrent_sessions` (old key silently ignored).
