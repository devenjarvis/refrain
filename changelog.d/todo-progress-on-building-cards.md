### Added

- Building-phase session cards now surface TodoWrite progress at a glance. When a Claude agent calls `TodoWrite`, the dashboard card shows a `▸ done/total · N active` progress badge on line 1 (replacing the generic "N active, M idle" count) and the current in-progress task's active description on line 2, with the next pending task on line 3. Error and waiting status conditions still preempt the progress badge. Sessions without any TodoWrite events render unchanged.
