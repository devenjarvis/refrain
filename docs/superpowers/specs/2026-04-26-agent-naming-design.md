# Agent Naming Design

**Date:** 2026-04-26
**Status:** Approved

## Problem

Agent display names are currently noisy and off-brand. Claude agents get random `adj-noun` placeholders (e.g. `brave-falcon`, `eager-panda`) until Haiku renames the branch after the first user prompt — at which point the agent display name is overwritten with the branch suffix (e.g. `add-dark-mode`). This approach has two problems:

1. The adj-noun names carry no meaning and don't fit Refrain's music theme.
2. When multiple agents share a session (same worktree/branch), they all get renamed to the same branch suffix, making them indistinguishable.

## Design

### Agent names (Claude agents)

Agents are numbered per session: `Track 1`, `Track 2`, `Track 3`, ...

- Internal `Name` field (must match `[a-zA-Z0-9][a-zA-Z0-9_-]*`): `track-1`, `track-2`, `track-3`
- Display name (`GetDisplayName()`): `Track 1`, `Track 2`, `Track 3`
- Assigned at agent creation using the existing `nextAgentNum` counter on `Session`
- **Never changes** — the track number is the agent's stable identity for its lifetime

"Track" is standard music vocabulary (individual recorded layers in a DAW) and immediately communicates that this is one of potentially several parallel workstreams within the session.

### Shell agent

Unchanged: internal name `shell`, display name `shell`. The shell is a special case with only one allowed per session and no music metaphor adds clarity here.

### Session display name

- **Before first prompt:** session display name is the song slug (e.g. `bohemian-rhapsody`) — unchanged from current behaviour
- **After first prompt:** when Haiku renames the branch, the session display name updates to the branch suffix (e.g. `add-dark-mode`) instead of the agent display name

This means `maybeRenameFromPrompt` calls `sess.SetDisplayName(suffix)` instead of `a.SetDisplayName(suffix)`. The agent display name is never updated by Haiku.

The setlist log is written at session *creation* time from the `Track` object (name, artist, ISRC), so it is unaffected by the session display name update.

### `RandomName()` in `internal/agent/names.go`

No longer used for agent naming. Still required as a fallback in `slugifyBranchName` (called when a session is created on an existing branch and the branch suffix is empty or collides). The function and its adj-noun lists remain; they just aren't the user-visible identifier anymore.

## What changes

| Location | Change |
|---|---|
| `internal/agent/session.go` `AddAgentDefault` / `AddAgent` | Set `cfg.Name = fmt.Sprintf("track-%d", num)` using `nextAgentNum`; set display name to `fmt.Sprintf("Track %d", num)` via `a.SetDisplayName(...)` after creation |
| `internal/agent/manager.go` `maybeRenameFromPrompt` | Call `sess.SetDisplayName(suffix)` instead of `a.SetDisplayName(suffix)`; remove the agent display name update |
| `internal/agent/session_name_test.go` | Update test fixtures from `eager-panda` / `warm-ibis` style names to `track-1` style |
| `internal/agent/manager_test.go` / hook integration tests | Update any assertions on agent display names post-rename |

## What does not change

- Session names (song slugs from `internal/songs`) — unchanged
- Shell agent name — stays `shell`
- `RandomName()` — still exists, still used in `slugifyBranchName`
- Setlist logging — unaffected
- Branch rename mechanics — unaffected; Haiku still renames the branch, only the display name target changes
- `hasClaudeName` / `TryStartRename` / `finishRename` gate logic — unaffected

## Success criteria

- A session with two Claude agents shows `Track 1` and `Track 2` in the sidebar — distinct and stable
- After the first user prompt, the session separator updates from the song slug to the Haiku-derived task name
- Agent display names never change after creation
- The shell agent still appears as `shell`
- All existing tests pass with updated fixtures
