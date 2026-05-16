### Fixed

- Plan drafting on larger codebases no longer fails with `signal killed` — the per-attempt timeout was doubled from 5 to 10 minutes to accommodate the aggressive research phase added in the richer planning prompt.
