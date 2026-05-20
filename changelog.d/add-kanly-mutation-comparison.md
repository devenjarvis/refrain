### Added

- Run [kanly](https://github.com/devenjarvis/kanly) on PRs alongside gremlins for a temporary mutation-testing comparison. The existing gremlins job is unchanged; a new parallel `kanly` job runs in `--diff` mode on the same PR diff and posts its results as a separate non-blocking sticky comment (header `kanly`) with totals and a collapsible full report. A shared `detect` job decides up front whether any production Go files changed and gates both runs. Both jobs are `continue-on-error` so neither tool blocks merges during the bake-off.
