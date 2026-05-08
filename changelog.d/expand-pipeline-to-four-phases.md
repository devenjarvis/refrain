### Changed

- Pipeline now has four sections — PLANNING → BUILDING → REVIEWING → SHIPPING — rather than the prior unified SESSIONS list and merged REVIEW QUEUE
- New sessions land in PLANNING by default; press `b` to advance the cursor-selected planning session to BUILDING when you're done scoping
- SHIPPING is now its own dashboard section showing each open PR's status (clicking the PR badge opens the PR, just like in REVIEWING)
- Pipeline widget labels match the new section names (PLANNING / BUILDING / REVIEWING / SHIPPING)

### Notes

- Persisted sessions from earlier versions restore to BUILDING for backward compatibility
