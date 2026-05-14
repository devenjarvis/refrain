### Changed

- New sessions are now named after a randomly chosen song from a hardcoded catalog (e.g. `refrain/wonderwall`, `refrain/karma-police`) instead of the previous adjective-noun pairs. In-session secondary agents still get adjective-noun names. The first-prompt Haiku rename is unchanged — it still replaces the song slug with a task-derived branch name.
- Each new session also appends a JSON line to `~/.refrain/setlist.jsonl` recording the song that played for it (name, artist, ISRC, slug, repo, session id, timestamp). This is the persistence foundation for a future setlist view.
