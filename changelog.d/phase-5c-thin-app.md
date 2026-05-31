### Changed

- Thinned the `internal/tui` App layer toward the CONVENTIONS.md §1/§3/§4/§9
  contract. Pure logic moved off `App`: `prPollInterval` and
  `cachedPRNumberForFallback` are now testable free functions; the config form's
  validation-checks hint refresh and filter live on the `configForm` component;
  the plan editor owns single-step undo and the save-before-revise step
  directly, leaving `App`'s revise handler as pure manager-routing. The shipping
  panel's merge-command factory now closes over the shared GitHub client and
  caches instead of a copied `App`.
- Dissolved the topic-split `update_*.go` buckets into component-aligned files:
  per-view router/handler methods now sit beside the component they serve
  (`diffview_app.go`, `filebrowser_app.go`, `branchpicker_app.go`,
  `repopicker_app.go`, `configform_app.go`, `globalconfig_app.go`), and the
  dashboard input and global lifecycle layers are named `dashboard_keys.go` and
  `app_lifecycle.go`. Pure reorganization — no behavior change.
