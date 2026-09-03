# Helios

Helios is a daemon + TUI + mobile app for orchestrating AI coding agents (Claude Code and Codex sessions). Each session runs in a Helios-owned terminal host (`helios ptyhost`) rather than a multiplexer, so output is served from memory to any number of viewers.

## Architecture

- **Go backend** (`cmd/helios/`, `internal/`): daemon server, terminal hosts (`internal/terminal`, `internal/backend`), provider registry, TUI
- **Flutter mobile/desktop** (`mobile/`): Dart app for remote session management
- Daemon exposes a REST API consumed by both the TUI and mobile app

Full picture: [docs/architecture.md](docs/architecture.md). Design intent lives in [docs/specs/](docs/specs/README.md).

## Build & Test

```bash
make build               # Build Go binary (includes codesign)
make test                # Run Go tests: go test ./...
make install             # Install the newest published release
make install-dev         # Install this checkout to /usr/local/bin
make desktop-install     # Install the newest released desktop app (macOS)
make desktop-install-dev # Install this checkout's desktop app (macOS)
make apk                 # Build debug APK
make dmg                 # Build macOS DMG
```

## Versioning

One number lives in `VERSION`, and it is the number the next release will carry.
`desktop/package.json` and `mobile/pubspec.yaml` carry the same one. Bump it in
the PR when you know what the change is worth — a `feat` takes the minor — but
you do not have to: CI runs `./scripts/check-version.sh --fix` on every PR, and
if the number is not past the newest release it takes the release's patch plus
one, pushes that to the branch, and says so in a comment. Run the script without
`--fix` to see where things stand before pushing.

The daemon is never edited by hand: `internal/version.Current` is stamped at
build time from `git describe`, so a release build says `3.9.0` and a build
above one says `3.9.0-13-g0a6231c` rather than claiming a release it is not.

Releases cut themselves. Once `Test` passes on main, `Release` checks whether
`VERSION` names a number that is not out yet, and publishes it if so — tag,
changelog, APK, DMGs, AppImage and deb. Nothing to trigger by hand; a merge that
does not move the number does not release.

## Procedures

- [Install the iOS app on a cabled iPhone](docs/agents/ios-install.md) — no make target exists; read this before trying

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
