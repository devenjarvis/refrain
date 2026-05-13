### Fixed

- PR rows no longer report "Conflicts" for every open PR. The root cause was the GitHub list endpoint always returning null for `mergeable`, which the client coerced to "not mergeable". PR lookups now follow up with the singular `GET /pulls/:number` endpoint, which populates `mergeable_state`. The shipping row shows "Conflicts" only when `mergeable_state` is `"dirty"`. While GitHub is still computing mergeability (state `"unknown"` or absent), the shipping panel header shows "⋯ checking" and the poller arms a 15s burst window so the state resolves promptly.
