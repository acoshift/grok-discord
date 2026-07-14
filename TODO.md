# TODO

Feature backlog for grok-discord. Order is suggested priority, not a commitment.

## Done

- [x] Channel → project mapping, allowlist, thread sessions
- [x] Commands: `/help`, `/projects`, `/reset`, `/status`
- [x] Grok-named Discord thread titles
- [x] Hide local project paths from Discord messages
- [x] Live progress heartbeats + `/cancel` (aliases: `cancel`, `/stop`, `stop`)
- [x] Discord attachments → prompt context (download, path list, cleanup)
- [x] Reply context: include referenced message text + attachments when tagging Grok
- [x] Per-thread git worktree isolation (`data/worktrees/`, `/reset` cleanup)
- [x] Stream Grok output (`streaming-json` → live Discord message edits)
- [x] Queue follow-ups when a thread is busy (instead of reject)

## Next

### 1. Native Discord slash commands

Replace (or complement) mention + text parse with application commands.

- Register `/grok`, `/cancel`, `/status`, `/projects`, `/reset`, `/help`
- Keep mention path for compatibility during migration

### 2. Idle worktree TTL cleanup

Worktrees currently live until `/reset`. Optionally prune after N days of inactivity.

## Later / nice-to-have

- [ ] `/model` or per-channel model override
- [ ] Rate limiting per user
- [ ] Optional non-yolo / approval gate for destructive tools
- [ ] Summarize final git diff in the completion message
