### Added

- In-app editor for per-repo validation checks. The repo settings overlay now exposes a "Validation Checks" row that opens a list editor for the shell commands run during the review panel's Checks tab — previously the only way to configure these was hand-editing `.refrain/config.json`. Keys: `a` add, `e`/`enter` edit, `d` delete, `ctrl+s` save, `esc` cancel; in row-edit mode, `tab` switches between Name and Command. Blank rows are filtered on save.
