# Naming

## Decision: **helios** (Ἥλιος)

The Greek sun god who sees everything from above. Rides his chariot across the sky, watching over all below.

Fits because:
- Watches over all your AI sessions simultaneously
- Sees everything — notifications, status, permissions
- Provider-agnostic — the sun shines on all, not just Claude
- 6 chars, no collisions, sounds good
- `helios new`, `helios ls`, `helios send 3 "fix it"` — reads well

## Binary

Single binary with subcommands:

```
helios daemon start     → starts the daemon
helios                  → launches TUI
helios new "fix bug"    → CLI command
helios ls               → list sessions
```

## Internal Naming

- Binary: `helios`
- Daemon process: `helios daemon`
- tmux session: `helios` (single session, multiple windows)
- tmux windows: `helios:s3-fix-auth` (one per AI session)
- Config directory: `~/.helios/`
- State file: `~/.helios/sessions.json`
- Config file: `~/.helios/config.yaml`
- Client API port: 7654
- Hook API port: 7655

## Repo

`helios` or `helios-ai`

GitHub: `kamrul1157024/helios`
