# One Version Number, and Builds That Do Not Lie About It

## The claim

A version number is a promise about what is running. Helios makes that promise
four times over — in the Makefile, in two client manifests, and in whatever tag
happens to be on disk — and no two of them have to agree. `make install` stamped
`3.8.0` on a machine whose newest release was `3.9.0`, and nobody noticed until
the desktop started reporting daemon versions and the number was read out loud.

One number, declared in one file, carried by every client, and stamped into
builds from git so a build cannot claim a release it is not.

## Where we are

| Where | What it says | How it got there |
| --- | --- | --- |
| `Makefile:9` | the newest *local* tag | `git describe --tags --abbrev=0`, so a checkout that never fetched `v3.9.0` builds `3.8.0` |
| `desktop/package.json:3` | `0.1.0` | typed once, never moved |
| `mobile/pubspec.yaml:4` | `0.2.0+1` | typed once, overridden at release by `--build-name` |
| `scripts/install.sh:161` | `dev` | builds with a bare `go build` — no ldflags at all |
| release workflow | whatever a human types into the dispatch form | `workflow_dispatch.inputs.version`, required |

Three consequences, all of them live before this spec:

1. **A dev build claims a release.** Thirteen commits past `v3.9.0`, `make build`
   stamps `3.9.0`. The daemon reports it over `/api/health`, the desktop compares
   it with the newest release (`desktop/src/shared/version.ts:27`), and says the
   machine is current. It is not.
2. **An installed daemon claims nothing.** Everything `scripts/install.sh` puts
   on a machine reports `dev`, which every client is built to leave alone — so a
   daemon two releases behind is never flagged, which was the entire point of
   reporting the version.
3. **The desktop app is permanently out of date with itself.** `app.getVersion()`
   reads `0.1.0` from `package.json`, so the release dialog
   (`desktop/src/renderer/components/updates.tsx`) finds every release newer than
   the running app, forever.

And the installer picks its ref with `git describe` against `origin/main`: the
newest *tag*, which is not the newest *release*. A prerelease tag, or one pushed
by mistake, is what people would get.

## The change

### One declared number

`VERSION` at the repo root holds the number the next release will carry.
`desktop/package.json` and `mobile/pubspec.yaml` carry the same one, because
neither can read a file at build time. The daemon carries nothing: it is stamped.

### Builds stamp themselves from git

```make
VERSION ?= $(shell git describe --tags 2>/dev/null | sed 's/^v//')
```

Without `--abbrev=0`, `git describe` answers the tag exactly when the checkout is
on one, and `3.9.0-13-g0a6231c` when it is not. `isNewer` parses the leading
`3.9.0` and reads that as equal to the release, so a dev daemon is not nagged
about — the silence `dev` bought, without the lie about which build it is.

`scripts/install.sh` passes the same string through `-ldflags`, so a daemon
installed from the published one-liner reports the release it was built from.
Its ref comes from `releases/latest` on the API, which skips drafts and
prereleases; the old `git describe` remains the fallback for a clone that cannot
reach GitHub.

### Release installs and dev installs are different questions

`make install` builds the newest **published release**, in a detached worktree
parked on that tag under `~/.helios/release-tree`, driven by the current
Makefile and removed whether the build worked or not. The working tree is never
touched, so a dirty checkout can still install a release. `make install-dev`
builds the checkout. Same pair for `desktop-install`.

Inside the worktree `git describe` answers the tag, so no version is passed
down — the build stamps itself correctly by construction.

### CI moves the number instead of failing over it

`scripts/check-version.sh` resolves a target: whatever `VERSION` declares, as
long as that is past the newest release; otherwise the release's patch plus one.
Without arguments it reports and exits 1. With `--fix` it writes the target to
all three files.

The `Version` job runs `--fix` on every PR, and when anything changed it commits
`chore: bump version to x.y.z`, pushes it to the branch, and comments to say so.
A commit the author did not write is one worth announcing.

Incremental and automatic on purpose. The failure mode this is built for is a
revert: a release goes out as 3.10.0, the change is reverted, and the next
release must not try to be 3.10.0 again. Nobody has to remember that.

### The release reads the file

`workflow_dispatch.inputs.version` becomes optional, defaulting to `VERSION`.
The number was decided in a PR; typing it again at release time is where it goes
wrong. `make release-publish` refuses to tag a number the file does not hold, so
a hand-typed override still has to be honest.

## What we are not doing

- **Deriving the bump from conventional commits.** A `feat` should take the
  minor, and an agent bumping the file by hand can do that. The automatic path
  takes the patch, because the automatic path is a floor, not a judgment.
- **Publishing a daemon binary.** `make install` and the installer both build
  from source; there is nothing to download. If that changes, `make install`
  becomes a download and this spec's worktree goes away.
- **Version numbers on the phone's host list.** The desktop names which daemons
  are behind; the phone does not, yet.

## Tests

- `scripts/check-version.sh` against each way it goes wrong: a manifest edited
  out of step, a `VERSION` equal to the newest release, and `--fix` writing all
  three back. Unreachable GitHub leaves the number alone rather than failing.
- `make install-dev && helios version` prints `3.9.0-13-g0a6231c` on a checkout
  past a release; `make install && helios version` prints the release.
- `/api/health` carries the same string the binary prints.
- A `HELIOS_PREFIX=/tmp/... HELIOS_SRC=/tmp/...` run of `scripts/install.sh`
  installs a daemon that prints a release number, not `dev`.

## Success criteria

Every number a person can read — `helios version`, `/api/health`, the Hosts
pane, the desktop's About, the APK's build name, the release tag — comes from
either `VERSION` or `git describe`, and no two of them disagree.
