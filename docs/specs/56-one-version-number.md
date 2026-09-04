# One Version Number, and Builds That Do Not Lie About It

> **Amended.** The claim held; the mechanism moved. The number no longer lives
> in a `VERSION` file that PRs bump and CI pushes — the release works it out
> from the newest published release, and releases are cut by hand rather than by
> a green main. The sections below say so where they used to say otherwise. What
> did not change: one number, carried by every artifact, stamped at build time,
> and never typed.

## The claim

A version number is a promise about what is running. Helios makes that promise
four times over — in the Makefile, in two client manifests, and in whatever tag
happens to be on disk — and no two of them have to agree. `make install` stamped
`3.8.0` on a machine whose newest release was `3.9.0`, and nobody noticed until
the desktop started reporting daemon versions and the number was read out loud.

One number, worked out at release time, carried by every client, and stamped
into builds so a build cannot claim a release it is not.

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

### No declared number

The repo declares nothing. A number checked into the tree is a number that
drifts from the tag, needs a bot to move it, and has to be reviewed in PRs that
are not about versioning. `scripts/next-version.sh` reads the newest published
release and steps one past it instead — see *The release works out the number*.

`desktop/package.json` and `mobile/pubspec.yaml` still have to hold a version
key, because npm and Flutter require one, so both hold `0.0.0` and every build
overrides it. That is the same placeholder that made the desktop app
permanently out of date with itself before this spec, so the dialog is closed
against it directly: `UpdateChecker.worthShowing` returns null when
`app.isPackaged` is false. A build running out of a checkout is not one whose
user is waiting to hear about a release. `latest()` skips that gate, because the
Hosts pane's question is about the daemons it can see, not about this app.

The daemon carries nothing at all: it is stamped.

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

### The release works out the number

`workflow_dispatch` has no `version` input. Typing a number is where the tag,
the APK's build name and the app's own number become three different numbers.
It has a `bump` dropdown instead — `patch`, `minor`, `major` — and
`scripts/next-version.sh` turns that into a number:

```
3.11.0 + patch -> 3.11.1
3.11.0 + minor -> 3.12.0
3.11.0 + major -> 4.0.0
```

The step is always one and everything to the right of it zeroes, so the illegal
numbers are not rejected, they are unreachable. There is nothing to validate and
nothing to correct.

The base is the newest **release**, not the newest tag: a prerelease, or a tag
pushed by mistake, is not what people are running. An unreachable GitHub is
fatal rather than a fallback — a number guessed from a stale base is a wrong
number on a real tag, and no check downstream would catch it.

The `Version` job resolves it once and the four build jobs read its output, so
the tag, the APK, both DMGs, the AppImage and the deb cannot disagree.

### Releases are cut by hand

`Release` runs on `workflow_dispatch` alone. A merge to main is not a release;
the work collects there until somebody looks at the batch and decides what it is
worth.

That is a reversal. The first cut of this spec had `Release` fire on
`workflow_run` when `Test` went green on main, on the reasoning that a release
nobody has to remember to cut goes out while the change is still fresh. What
that bought in freshness it spent on judgement: a `feat` and a typo fix are the
same event to a trigger, and the number they ship under was whatever the last
PR happened to leave in `VERSION`. Choosing `minor` is a statement about the
batch, and only a person reading the batch can make it.

Two guards, because a button is pressable twice. The job fails when the tag it
resolved already exists — a draft or a hand-pushed tag would otherwise be
reused, putting this release on somebody else's commit. And it fails when no
commits sit between the last tag and `HEAD`, which is what a second press looks
like: without it, the base having moved means the second press cheerfully cuts
`3.11.2` over `3.11.1` with an empty changelog behind it.

`make release BUMP=minor` is the same path locally: it resolves the number once
and hands it to both `apk-release` and `release-publish`. Both refuse anything
that is not a plain `x.y.z`, so the `3.11.0-13-g0a6231c` that `git describe`
hands back on a checkout past a tag cannot reach a tag or an asset name.

### The page hands the file over itself

The links cannot be `github.com` URLs. Android verifies that domain for the
GitHub app, so a tap on the phone the page exists for opens the app on the
release page instead of downloading anything.

So the page links `/download/<asset>` on the daemon. That handler asks GitHub
for the asset without following the answer, reads the `Location` — a signed
`release-assets.githubusercontent.com` URL, which no app claims — and redirects
the browser there. The bytes still come from GitHub and none pass through the
tunnel; only the address the browser is asked to visit changes. Five asset names
are known, and nothing else becomes a URL the daemon will fetch. If GitHub
cannot be reached, the browser gets the `github.com` URL and follows the
redirect itself: one tap that may open the app, which beats a download that
never starts.

## What we are not doing

- **Deriving the bump from conventional commits.** A `feat` in the batch should
  take the minor, and the dropdown defaults to `patch`, so the two disagree
  whenever nobody looks. Reading the commits and picking is a person's job:
  `feat:` is a claim about a subject line, not about compatibility, and a
  release cut on that reading would be wrong exactly when it mattered.
- **Publishing a daemon binary.** `make install` and the installer both build
  from source; there is nothing to download. If that changes, `make install`
  becomes a download and this spec's worktree goes away.
- **Version numbers on the phone's host list.** The desktop names which daemons
  are behind; the phone does not, yet.

## Tests

- `scripts/next-version.sh` for each of the three levels against a known newest
  release, and its refusals: an unrecognised level, an unreachable GitHub, and a
  newest release that is not a plain `x.y.z`.
- A `Release` run with nothing merged since the last tag fails in the `Version`
  job, before anything is built.
- `make install-dev && helios version` prints `3.9.0-13-g0a6231c` on a checkout
  past a release; `make install && helios version` prints the release.
- `/api/health` carries the same string the binary prints.
- A `HELIOS_PREFIX=/tmp/... HELIOS_SRC=/tmp/...` run of `scripts/install.sh`
  installs a daemon that prints a release number, not `dev`.

## Success criteria

Every number a person can read — `helios version`, `/api/health`, the Hosts
pane, the desktop's About, the APK's build name, the release tag — comes from
either the release the button resolved or `git describe`, and no two of them
disagree.
