### Fixed

- Plan drafts and revisions no longer open the editor with a transitional sentence above `# Goal`. The draft and revise prompts in `internal/agent/planner.go` now require the response to begin with `` `# Goal` `` on the very first line, and `runClaudePlanner` strips any leading content before the first `# Goal` occurrence as a belt-and-suspenders guard.
