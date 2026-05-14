### Added

- Agent lifecycle pipeline: sessions now track workflow phase (In Progress → Ready to Review → In Review → Shipping → Complete)
- Fullscreen focus mode: `f` now takes over the entire screen with a pipeline widget, review queue, and attention rows — no agent terminal competing for attention
- Review panel: press `r` on a queued session to open a full-screen review view showing your original prompt, top-changed files, and a logic/test/config ratio before going to GitHub
- `m` key marks a finished session as ready for review; `d` defers from review back to the queue
- `MaxReviewBacklog` config setting (default 5): warns before creating new agents when review backlog is large
- Lifecycle phase and original prompt persist across refrain restarts
