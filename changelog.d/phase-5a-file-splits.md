### Changed

- Split oversized `internal/tui` files into the `_model`/`_update`/`_view` layout per CONVENTIONS.md §9 (`dashboard`, `planeditor`) and collapsed the forbidden `xxxpanel.go`/`xxxpanelmodel.go` pairs (`reviewpanel`, `shippingpanel`) into the same three-file split. Pure file reorganization — no behavior change.
