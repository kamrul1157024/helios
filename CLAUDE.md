# Helios

Helios is a daemon + TUI + mobile app for orchestrating AI coding agents (Claude Code sessions) via tmux.

## Architecture

- **Go backend** (`cmd/helios/`, `internal/`): daemon server, tmux integration, provider registry, TUI
- **Flutter mobile/desktop** (`mobile/`): Dart app for remote session management
- Daemon exposes a REST API consumed by both the TUI and mobile app

## Build & Test

```bash
make build        # Build Go binary (includes codesign)
make test         # Run Go tests: go test ./...
make install      # Build + install to /usr/local/bin
make apk          # Build debug APK
make dmg          # Build macOS DMG
```

## Coding Conventions

### Go

- Follow standard Go conventions: `gofmt`, effective Go, and Go Code Review Comments
- Error handling: always check errors; never use `_` to discard errors. Wrap errors with `fmt.Errorf("context: %w", err)` for traceability
- Naming: use MixedCaps/mixedCaps (no underscores). Acronyms are all-caps (e.g., `HTTPServer`, `APIClient`)
- Packages: short, lowercase, single-word names. No `util` or `common` packages
- Functions: return `error` as the last return value. Use named returns sparingly, only when it improves clarity
- Interfaces: define at the consumer site, not the producer. Keep them small (1-2 methods)
- No `init()` functions unless absolutely necessary
- Use `context.Context` as the first parameter for functions that do I/O or may be cancelled

### Dart / Flutter

- Follow Dart style guide and `dart analyze` rules
- Use `const` constructors wherever possible
- Prefer `StatelessWidget` over `StatefulWidget` when state is not needed
- Name files in `snake_case.dart`

### Commit Messages

Use conventional commits: `type: short description`

Types: `feat`, `fix`, `docs`, `ci`, `refactor`, `test`, `chore`

Keep subject line under 72 characters. Use imperative mood ("add feature" not "added feature").

### General

- No dead code or commented-out code in commits
- No `TODO` or `FIXME` comments without a linked issue
- Keep functions short and focused; extract when a function exceeds ~40 lines
- Prefer returning early over deep nesting
