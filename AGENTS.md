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
make install-dev         # Install this checkout to ~/.local/bin (override with PREFIX=)
make desktop-install     # Install the newest released desktop app (macOS)
make desktop-install-dev # Install this checkout's desktop app (macOS)
make apk                 # Build debug APK
make dmg                 # Build macOS DMG
```

## Versioning

The repo holds no version number. Nothing to bump in a PR, and nothing to keep
in sync: the numbers in `desktop/package.json` and `mobile/pubspec.yaml` are
`0.0.0` placeholders that every build overrides.

The release decides the number. `scripts/next-version.sh` reads the newest
published release and steps one past it at the part you ask for — the step is
always one, and everything to the right of it goes to zero.

```bash
./scripts/next-version.sh patch   # 3.11.0 -> 3.11.1
./scripts/next-version.sh minor   # 3.11.0 -> 3.12.0
./scripts/next-version.sh major   # 3.11.0 -> 4.0.0
```

Every build stamps that one number: the daemon through `-ldflags` into
`internal/version.Current`, the desktop app through
`--config.extraMetadata.version`, the APK through `--build-name`. A checkout
that was not built by the release path says `3.11.0-13-g0a6231c` or `dev`,
which is the point — a dev build must not claim a release it is not.

Releases are cut by hand. Merging to main does not ship anything; the work
collects there. Run the **Release** workflow from the Actions tab, pick `patch`,
`minor` or `major`, and it tags, writes the changelog and builds the APK, both
DMGs, the AppImage and the deb. It refuses when nothing has landed since the
last tag, so a second press cannot cut a hollow release. Locally the same thing
is `make release BUMP=minor`.

Which part to move is a judgement about the whole batch, not about one PR. The
run's changelog lists every commit since the last tag — read it before you pick.

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
