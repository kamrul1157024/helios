# A Query Data Layer for the Mobile App

## The claim

The Flutter app should read through one keyed cache, write through one path,
and let the daemon's event stream invalidate keys — the same move PR #114 made
for the desktop renderer.

It is not the same starting point, and the differences matter more than the
similarities. The desktop had no cache at all. Mobile already has five, none of
which know about each other: a `DaemonAPIService` per host that holds sessions,
notifications, providers, a TTL'd model map and the host settings, persists the
sessions to `SharedPreferences`, debounces SSE into refetches and falls back to
polling when the stream drops. That machinery works. What it lacks is any notion
of a *key* — so it covers five resources by hand, and the other twelve reads are
fetched fresh by whichever screen wants them.

**Scope: `mobile/` only.** No daemon route changes shape, no wire format moves,
and `desktop/`, `internal/` and `cmd/` are untouched.

## Where we are

Every figure below was measured against the tree at `205934c`, not estimated.

| | Today |
|---|---|
| State | `provider` ^6.1.4 — `ChangeNotifier`, no keyed cache |
| Server cache | five, hand-rolled and unrelated — see the table below |
| API surface | 43 public `Future` methods on `DaemonAPIService` (1808 lines): 16 returning `bool`, 18 returning a value, 9 returning `void` |
| Screen pattern | `initState()` → `await` → `setState()`, in every one of the 12 screens |
| Crash guards | 72 `mounted` checks across the screens, against 71 awaits |
| Stale-response guards | **none** |
| Live updates | SSE, debounced 500 ms into a blanket refetch (`:348-372`) |
| Fallback | `Timer.periodic` every 3 s while SSE is down (`_startPolling`, `:192-197`) |
| Offline | sessions written to `SharedPreferences` (`_loadSessionCache`/`_saveSessionCache`, `:226-255`) |
| Test baseline | 110 tests, all passing |

### The five caches

This is the part the first draft of this document got wrong, and it changes the
shape of the work. It is not "two cached, nineteen not."

| Resource | Held as | Freshness rule |
|---|---|---|
| Sessions | `_sessions` + `_sessionsLoaded` (`:58,69`) | SSE debounce, 3 s poll, `SharedPreferences` seed |
| Notifications | `_notifications` + `_notificationsLoaded` (`:56,67`) | refetched on **every** SSE event |
| Providers | `_providers` + `_providersLoaded` (`:77,90`) | **none** — fetched once at `:179`, then never again |
| Models | `_modelCache` + `_modelCacheFetchedAt` (`:94-95`) | 24 h TTL |
| Host settings | **five scalars** — `_autoTitleEnabled`, `_autoTitleEmoji`, `_evictEnabled`, `_budgetFraction` (`:914-917`) and `_manualOrder` (`:1014`), plus `_hostSettingsLoaded` (`:918`) | fetched on demand; written through optimistically |

The last row is the one that matters most, and it is dealt with under *Writes*
below. Mobile does not hold a settings *document* — `fetchHostSettings` parses
the daemon's map into five typed fields and throws the envelope away
(`:932-949`).

## What this costs today

### The `mounted` guards are solving the wrong problem

Seventy-two of them is thorough, and they do their job: no `setState` after
dispose. But `mounted` is still true when the *inputs* change under a fetch that
is still in flight, and nothing anywhere guards that. There is no generation
counter, no request token, no sequence check in any screen.

`file_browser_screen.dart:68-88` is the clearest case:

```dart
final listing = await svc.listFiles(path);
if (!mounted) return;
...
_listing = listing;
_currentPath = listing.path;
```

Tap folder A, then B before A answers. Both are mounted, both pass the guard,
and whichever lands last wins. If that is A, the browser silently walks back
into the folder the user just left — and `_currentPath` is rewritten to match,
so it does not even look wrong.

This is the same class of bug as the desktop's files-panel race in PR #112. The
desktop had 69 hand-rolled cancellation flags fighting it. Mobile has none,
because `mounted` reads like it is already handling it.

### Argument-free reads are fetched once per screen that wants them

Three reads take no arguments at all, so every caller is asking the identical
question and getting a separate HTTP round trip:

| Read | Callers |
|---|---|
| `fetchProviders()` | `new_session_sheet.dart:48`, `session_detail_screen.dart:985`, `host_manager.dart:196` (and once internally at `:179`) |
| `fetchDirectories()` | `new_session_sheet.dart:383`, `sessions_screen.dart:172` |
| `fetchHostSettings()` | `settings_screen.dart:49`, `host_detail_screen.dart:34` — plus `fetchSortMode()` (`:1019`), which is an alias for it |

`fetchProviders` (`:1053-1066`) has no TTL and no in-flight coalescing — it GETs
and overwrites. The model cache next to it has a 24h TTL; providers got none.

### The argument-free reads do not return anything

This is the trap in the table above, and it sets the true size of every
migration step.

All three return `Future<void>`. They do not answer the caller — they overwrite
a field and call `notifyListeners()`, and the caller reads the answer afterwards
off a getter:

```dart
Future<void> fetchProviders() async {
  ...
  _providers = list.map((p) => ProviderInfo.fromJson(p)).toList();
  _providersLoaded = true;
  notifyListeners();
}
```

Nine of the 43 public futures are shaped this way: `fetchProviders`,
`fetchSessions`, `fetchNotifications`, `fetchHostSettings`, `fetchSortMode`,
`resume`, `setManualOrder`, `startActive`, `startBackground`.

So "port `fetchProviders`" is not three call sites. It is three call sites, plus
the seven places that read `sse.providers` / `.allProviders` / `.providersLoaded`
(`new_session_sheet.dart:44,45,49,50,273` and
`session_detail_screen.dart:970,978`), plus deleting the two getters, the
`ready`-filtering one beside them, and the loaded flag. Roughly three times the
count the table implies, and the same multiplier applies to every void-returning
read on the list.

The twelve value-returning reads — `listFiles`, `readFile`, `gitStatus`,
`gitDiff`, `gitLog`, `gitChanges`, `gitWorktrees`, `fetchDirectories`,
`fetchModels`, `fetchSubagents`, `fetchTerminals`, `fetchTranscript` — do not
have this problem. They are the cheap half of the migration and should be
sequenced first for that reason alone.

### One repository, fetched once per worktree

`gitStatus` and `gitWorktrees` have three call sites each. Those take a path, so
they only collide when the paths agree — but for one session looking at one
repository, they routinely do: `session_detail_screen.dart:264` asks for
`_effectiveCwd` while `git_status_screen.dart:48` asks for `_root`, and those are
the same directory.

`session_detail_screen.dart:277` is worse than a collision:

```dart
worktrees.map((wt) => svc.gitStatus(wt.path))
```

One `gitStatus` per worktree, fanned out, uncached, every time the screen loads.
`file_browser_screen.dart:101` then asks for the same worktree list again under a
third path. A keyed cache collapses the whole fan-out, which makes this the
single largest measurable win in the document.

### One `FutureBuilder` refetches on every rebuild

`new_session_sheet.dart:382-383`:

```dart
FutureBuilder<List<DirectoryInfo>>(
  future: sse?.fetchDirectories() ?? Future.value([]),
```

The future is constructed inside `build()`. Every rebuild of the sheet — every
keystroke in the working-directory field — starts a fresh request. This is a
bug today, independent of anything proposed here, and it is the one thing in
this document worth fixing on its own even if the rest is rejected.

### Every server event refetches the notifications

`_handleEvent` (`:348-372`) debounces 500 ms and then calls
`fetchNotifications()` for **any** event type, including `session_status`, which
fires several times a turn. Sessions are refetched too, but only for the active
host and only for six named types.

So a busy agent costs a notification refetch every 500 ms, and the session lists
of every background host go stale until the user switches to them. That second
half is not a performance problem, it is a correctness one: the dashboard counts
sessions across hosts.

### Five resources are cached; twelve are not

The five in the table above survive a screen being popped. `gitStatus`,
`gitLog`, `gitDiff`, `gitChanges`, `gitWorktrees`, `listFiles`, `readFile`,
`fetchTranscript`, `fetchTerminals`, `fetchSubagents`, `fetchDirectories` and
`fetchModels`'s uncached path are held in the `State` of whichever screen asked,
and die with it. Reopening the git screen on the same repository refetches
everything it showed a second ago.

Worth stating plainly: the five that *are* cached are five different designs.
One has a TTL, one has a disk seed, one has an SSE trigger, one has nothing, and
one is shredded into typed scalars. Replacing them with one mechanism is the
actual argument for this work — more than any single refetch it saves.

## What the desktop migration actually did

Spec 49 is written by analogy to spec 48, which was implemented as PR #114. So
the most useful evidence available is not the plan — it is the gap between that
plan and the commit. Every gap below is a place this document has probably
inherited the same error.

**The incremental order did not survive.** Spec 48 proposed nine steps and
claimed any of them could be stopped at. The branch is four commits, and one of
them — `8e5c2e4` — is all nine steps at once: 27 renderer files, roughly 2000
lines. The "deliberately double-runs, so any step can be stopped at" safety
valve was never exercised; the hand-patching it was supposed to run beside was
deleted in the same commit that added the cache. This document makes the same
claim in *Migration order* below, and it should be read as an intention rather
than as something the team has ever actually done.

**The writes step largely did not ship.** Spec 48 said twenty-five write methods
would become mutations. Two did. There is no `mutations.ts`; the rest stayed as
store methods calling the API directly. That is the strongest available evidence
for this document's own open question 4 — when the reads are cached and the
writes already work, the writes stop feeling worth moving.

**Scope was breached in exactly the way this document forbids.** Spec 48 said
nothing in `internal/` would change and that needing a daemon change was the
signal it had drifted. PR #114 then changed `internal/server/githistory.go`,
because paging the git log properly for the first time exposed a real daemon
bug. Plus `.nvmrc` and `Makefile`, because the new tests needed a newer Node.
The equivalent warning here: the *Scope* line at the top of this document is a
statement of intent, and the honest expectation is one or two daemon defects
surfacing once reads are deduplicated and paged correctly.

**A read that was not a read.** Spec 48 turned `syncShells` into a query. It was
dead on arrival — `syncShells` acts on the answer rather than rendering it, so
nothing ever watched the key. It was deleted in #115, fifty minutes after it
merged. Worth re-checking this document's own read/write classification against
that mistake: a call whose result drives an *action* is not a cache read.

**What nobody planned for**, and what this document therefore also omits:

- **Escape hatches.** Once the list lives in the cache, code outside the
  component tree still needs it. Desktop grew `sessionsOf`, `sessionById`,
  `groupsOf`, `notificationsOf` and public `invalidate*` methods for this.
  Mobile has 8 such reads of `.sessions`/`.notifications` across
  `session_detail_screen`, `dashboard_screen`, `home_screen` and — awkwardly —
  `HostManager`, which this document lists as out of scope.
- **Hidden side effects on the fetch.** Desktop's `refreshSessions` secretly
  diffed old against new to catch a session going `terminated`; once it was a
  query the store stopped seeing fetches, so it had to subscribe to the query
  cache instead. **Mobile has the same shape in a different place:**
  `fetchSessions` writes the `SharedPreferences` cache, but *only when the fetch
  is unfiltered* (`:554-559`). Under a keyed cache the unfiltered list is one
  key among several, so that rule has to move and become key-aware. Mobile has
  no status-diffing equivalent, which is one surprise it is spared.
- **Empty-argument guards.** About twelve desktop queries needed `enabled:`
  (`cwd !== ''`, `sessionId !== ''`). Every mobile family keyed on a path or id
  needs the same, and the plan never mentions it.
- **Retry policy**, new shared types to make keys expressible, and a way to
  force a reload past a never-stale entry. All discovered, none planned.

## The change

### Riverpod, keyed by host and resource

`flutter_riverpod` is the recommendation, with `AsyncNotifierProvider.family` as
the unit. Note the package name: plain `riverpod` is the Dart-only core and does
not carry `ProviderScope` or `ConsumerWidget`, which is all of what this app
needs.

The three paragraphs that follow are not predictions. They are what happened
when the bootstrap was actually built on branch `spike/riverpod-step2`
(`c887d7f`), which ported `fetchDirectories` end to end.

**The version is 2.6.1, not 3.x.** `flutter pub add flutter_riverpod` resolves
to `^2.6.1` against this app's constraints. Forcing `^3.0.0` resolves too, but
changes 21 dependencies — so Riverpod 3 is a dependency-bump project with its
own review, not a line in `pubspec.yaml`. Plan on 2.6.1 and treat 3.x as
separate work. This also settles codegen by default: `riverpod_generator` is a
Riverpod 3 story, so the hand-written `FutureProvider.family` syntax below is
the right idiom here, and build_runner stays out of CI.

**`provider` and `flutter_riverpod` collide on three exported names** —
`Provider`, `ChangeNotifierProvider` and `Consumer`. "They coexist in one tree"
is true of the runtime and false of the imports: every file that imports both
must `hide`, `show` or alias, or it will not compile. This is a small, dull tax
spread across every file the migration touches, and it has no desktop analogue.

**`HostManager` cannot stay out of scope.** It owns the per-host
`DaemonAPIService` instances, and a Riverpod provider body has no `BuildContext`
to reach it through. The bridge is to build it once in `main` and hand it to
both trees:

```dart
final hostManager = HostManager();
runApp(ProviderScope(
  overrides: [hostManagerProvider.overrideWithValue(hostManager)],
  child: MultiProvider(
    providers: [ChangeNotifierProvider.value(value: hostManager), ...],
```

`HostManager`'s own logic is untouched, so the spirit of "not doing HostManager"
survives — but the claim that it is unaffected does not.

**Measured cost of the bootstrap:** 53 insertions and 12 deletions across
`main.dart` and one call site, plus a 28-line providers file. Analyzer clean,
and all 110 tests still pass. The scaffolding is genuinely cheap; it is the
per-read work after it that carries the cost.

The mapping to what PR #114 built is close enough to reason about:

| Desktop (TanStack Query) | Mobile (Riverpod) |
|---|---|
| query key | `.family` argument |
| `useQuery` | `ref.watch(provider(args))` |
| `isPending` / `error` / `data` | `AsyncValue` |
| `invalidateQueries` | `ref.invalidate` |
| `staleTime: Infinity` | `keepAlive` |
| `gcTime` | `autoDispose` |
| in-flight dedup | built in, per family argument |

The alternative is a direct TanStack port — `fquery` or `cached_query`. Both
model the original more literally. Neither has anything like Riverpod's adoption,
and this app has to be maintained by whoever picks it up next. Taking the less
familiar library to get a closer analogy to a *different codebase* is the wrong
trade.

The family argument carries the host id first, for the reason the desktop keys
do: two daemons hold the same paths and must not answer for each other.

```dart
final gitStatusProvider = FutureProvider.family
    .autoDispose<GitStatus?, (String hostId, String cwd)>((ref, key) { ... });

final sessionsProvider = AsyncNotifierProvider.family
    .autoDispose<SessionsNotifier, List<Session>, String>(...);
```

### What `DaemonAPIService` keeps

It stays. It is the HTTP client, the SSE connection, the reconnect backoff and
the offline persistence, and none of that belongs in a provider. What it loses
is all five caches and the refetch scheduling — `_sessions`, `_notifications`,
`_providers`, `_modelCache`/`_modelCacheFetchedAt`, the five settings scalars,
their four `*Loaded` flags, the two debounce timers and `_startPolling`. Also
`_lastSessionQ`/`_lastSessionFilter`/`_lastSessionCwd` (`:73-75`), which exist
only so a refetch can reconstruct the filters of the fetch before it. Under a
keyed cache the filters *are* the key, so the three fields have nothing to do.

What it keeps holding is genuinely its own: `_connected`, `_isOffline`,
`_lastBytesAt`, `_consecutiveFailures`, `_connectGeneration`. Connection state,
not data.

### SSE becomes invalidation

The desktop's step 3 is worth copying exactly, including the shape: a pure
function from event to the list of keys it takes out, returned as data rather
than applied, so it can be asserted without a fake container. On desktop that
function is `effectsFor` in `keys.ts`, and it is the single most-tested thing in
the PR.

| Event | Invalidates |
|---|---|
| `session_status` | that host's sessions — and patch the one row first, so the list does not flicker |
| `session_updated` / `session_deleted` | that host's sessions |
| `notification` / `notification_resolved` | notifications **and** sessions — a permission request writes `waiting_permission` to the session and announces only the notification (`internal/provider/claude/hooks.go:110,148`) |
| `subagent_status` | that session's subagents |
| everything else | nothing |

Two things fall out. The blanket `fetchNotifications()` on every event type
stops, because only two types now name that key. And **background hosts stop
being a special case** — invalidation is per host, and a host nobody is looking
at simply has no watcher, so nothing refetches until a screen wants it. The
`_isActiveHost` condition at `:361` disappears rather than being reimplemented.

### Polling becomes a fallback for one key, not a loop

The 3 s `Timer.periodic` exists because SSE is the only push and it drops. That
stays as a concept but narrows: while `connected` is false, the sessions
provider refreshes on an interval. Every other key is left alone — nothing today
polls `gitStatus`, and nothing should start.

### Offline

`SharedPreferences` persistence for the session list is a mobile requirement
with no desktop equivalent: the app opens on a train. It stays, as the sessions
provider's seed — the cached list is what `build` returns before the first fetch
resolves, so the dashboard draws immediately rather than showing a spinner over
data it already has.

This is the one place where mobile should *not* copy the desktop, which
explicitly decided against persisting its cache.

## What we are not doing

- **Replacing `provider` wholesale.** `ThemeProvider`, `card_registry`, `verbs`
  and the card widgets hold no server data. They keep working as they are;
  Riverpod and `provider` coexist in one tree.
- **`HostManager`.** It owns pairing, the host list and the active-host
  selection. That is configuration and navigation, not cached server data.
- **The terminal.** `terminal_connection.dart` is a live byte stream with its
  own framing. A cache has nothing to offer it.
- **The daemon.** If this spec ever seems to need a route change, that is the
  signal it has drifted out of scope.

## Writes: three of the four are already optimistic

The first draft of this document proposed *adding* optimistic updates. That was
wrong, and the correction matters because it turns step 8 from a feature into a
port — which is the more dangerous of the two, since working behaviour is being
moved rather than absent behaviour supplied.

| Write | Today |
|---|---|
| `setSessionOrder` (`:1032-1049`) | **already optimistic.** Paints the new order, rolls `_sessions` back to `previous` on failure |
| `patchSession` (`:761-...`) | **already optimistic.** Copies the row, restores `original` on failure |
| the four settings toggles | **already optimistic.** All route through `_writeHostSetting` (`:996-1008`), which takes `apply`/`revert` callbacks |
| `setPermissionMode` (`:724-744`) | **not optimistic**, and should stay that way — it can fail with `session_busy` or `session_ended`, and it returns the message the UI shows |

Two consequences.

**Desktop's `mergeSettings` does not transfer.** That function exists because
the renderer holds the settings *document* and three panes write disjoint keys
of one map. Mobile holds five parsed scalars, so there is no map to clobber and
nothing to merge. The mobile question is the opposite one: whether to keep the
scalars or introduce a document that does not exist today. Keep the scalars —
they are typed, they are clamped at the boundary (`:944-946`), and inventing an
envelope to match the desktop would be copying a solution to a problem mobile
does not have.

**`patchSession` defers its notify, and the reason is load-bearing:**

```dart
// Use Future.microtask to defer the notification so any dialog/sheet that
// triggered this call finishes its pop transition first — avoids the
// _dependents.isEmpty assertion in framework.dart.
Future.microtask(() => notifyListeners());
```

That is a fix for a real framework crash (`:766-778`). Any port has to preserve
the deferral, and Riverpod's own scheduling is not obviously equivalent. Assume
this costs a device test rather than a unit test.

## Migration order

Each step stands alone, and the app works after every one.

Read the caveat first: **PR #114 made this same promise and then landed all nine
steps in one commit.** Incremental delivery here is a discipline the team has
not yet demonstrated, not a property the design gives away for free.

1. **Fix `new_session_sheet.dart:383` on its own.** It is a live bug, not a
   migration, and it should not wait behind a library decision.
2. Add `flutter_riverpod` (2.6.1), wrap the app in `ProviderScope`, bridge
   `HostManager` by override, and port **one** read end to end. Use
   `fetchDirectories`, **not** `fetchProviders`: it returns its value, so it is a
   read and nothing else. `fetchProviders` returns `void` and drags seven getter
   call sites with it, which is the wrong shape for the step that proves the
   pattern. **This step is already done** on `spike/riverpod-step2` (`c887d7f`)
   and can be cherry-picked rather than rewritten — it also carries the step 1
   fix, since `ref.watch` is cached and no longer refetches per keystroke.
3. The key/effect map and its tests. Nothing consumes it yet.
4. SSE → invalidation, running *alongside* the existing debounced refetches. The
   double-run is what makes every later step safe to stop at.
5. Git reads: `gitStatus`, `gitLog`, `gitChanges`, `gitWorktrees`, `gitDiff`.
   Land `gitStatus` first — the worktree fan-out at
   `session_detail_screen.dart:277` is where the saving is visible on a device.
6. File reads: `listFiles`, `readFile`. `readFile` seeds an editor buffer, so it
   gets the treatment `readFile` got on desktop — never stale on its own, and a
   write updates the cache from its own response rather than invalidating.
7. The void-returning reads: `fetchProviders`, `fetchHostSettings`
   (and its `fetchSortMode` alias), `fetchNotifications`, `fetchSessions`. Each
   one deletes its getters and its `*Loaded` flag as it goes. Budget about three
   times what the call-site counts suggest. `fetchSessions` is the awkward one:
   its conditional disk write has to move out with it.
8. Move the caches out of the service; delete the debounce timers,
   `_startPolling`, and `_lastSessionQ`/`_lastSessionFilter`/`_lastSessionCwd`.
   Add the escape hatches the eight external readers need — including
   `HostManager`'s. **This is where the old path is removed** — steps 1-7 are
   reversible, this one is not.
9. Port the three existing optimistic writes onto the new cache, unchanged in
   behaviour, preserving the `Future.microtask` deferral. Leave
   `setPermissionMode` non-optimistic.
10. The transcript. `fetchTranscript` pages backwards and takes an `afterSeq`
    delta, exactly as desktop's does, and `epochChanged`
    (`session_detail_screen.dart:222`) has to drop the held pages rather than
    append to them. Riskiest, so it lands last.

## Tests

The baseline was 110 passing tests across 12 files, none of which touched
`DaemonAPIService` at all — so the optimistic writes this migration has to carry
across were completely unguarded.

**That gap is now closed.** `test/optimistic_write_test.dart` adds five tests
that pin the behaviour before anything moves:

- `setSessionOrder` paints the new order before the daemon answers, and restores
  the old one when it refuses.
- `patchSession` restores the original row on refusal.
- `patchSession` does **not** notify synchronously — the microtask deferral is
  asserted directly, so removing it fails a test rather than crashing a phone.
- A refused settings write reverts its own toggle and leaves its neighbour
  alone.

They construct the real service against a `MockClient`, so they exercise the
actual write paths rather than a re-implementation. The suite is now 115. Write
these *before* step 9, not after — they are the only thing that makes that step
safe, and they were cheap precisely because `ApiClient` already takes an
injectable `http.Client`.

Dart tests can assert the pure half directly, which is the argument for keeping
it pure:

- The family keys: stable, host-namespaced, and different arguments give
  different keys.
- The retry rule: retries a network failure and a 5xx, never a 4xx. `readFile`
  404s routinely because the agent deletes files.
- The SSE→invalidation map: each event type names the keys it takes out.
- The settings scalars: a write to one leaves the other four untouched, and
  `_budgetFraction` stays clamped to its travel.
- The transcript reducer: a delta appended to a page gives what a full fetch
  would have, and an `epochChanged` page replaces rather than appends.
- **The stale-response race**, which is the one thing here that is a real bug
  and not a tidy-up: two `listFiles` calls resolved out of order leave the cache
  holding the later request's answer.

Then on a device, against a real daemon:

- Open the git screen twice on one repository; the second open costs no request.
- Open a session detail with several worktrees; confirm one `gitStatus` per
  distinct path rather than one per worktree per visit.
- Rename a session from a sheet and let the write fail. The sheet must pop
  without tripping the `_dependents.isEmpty` assertion — this is the
  `Future.microtask` behaviour, and it cannot be checked off a widget test.

## Confidence

Assessed against `205934c`, after four things: verifying every claim in this
document against the code, auditing what PR #114 shipped against what spec 48
promised, building the bootstrap for real on `spike/riverpod-step2`, and writing
the regression net for the writes.

| Phase | Was | Now | Why it moved |
|---|---|---|---|
| Step 1 (the `FutureBuilder` bug) | 95 | **98** | Done in the spike; the fix falls out of the port |
| Steps 2-4 (bootstrap, key map, dual-run SSE) | 85 | **90** | Bootstrap built and measured; coexistence proven; tests still green |
| Steps 5-6 (git reads, file reads) | 75 | **80** | `enabled:` guards and the worktree fan-out are now known quantities |
| Steps 7-8 (void reads, removing the old path) | 60 | **65** | The escape hatches and the conditional disk write are now counted rather than guessed |
| Step 9 (write port) | 45 | **75** | The behaviour it must preserve is now pinned by five tests instead of by nothing |
| Step 10 (transcript) | 45 | **45** | Unchanged. Nothing done here yet, and the epoch fork is still untriggerable by hand |
| **All ten** | **58** | **73** | |

What raised it was not optimism — it was converting unknowns into known work.
The escape hatches, the import collisions, the `HostManager` bridge and the
conditional disk write are all *more* work than the first draft implied, and the
score went up anyway, because named work is schedulable and unnamed work is not.

What holds it below 80 is the one thing the evidence actively argues against:
PR #114 promised the same incremental sequence and delivered a single 2000-line
commit. Until one of these steps lands on its own, "each step stands alone" is
untested.

**What would raise it further**, in order of value per hour:

1. **Land steps 1-2 on their own.** They are already written on
   `spike/riverpod-step2`. Merging that single commit and stopping is the only
   thing that turns incrementality from a claim into a fact, and it is the
   difference between 73 and about 80.
2. **Find out how to trigger an epoch change.** Step 10 cannot rise above 45
   until the fork it exists to handle can be produced on demand. This is a
   daemon-side question — worth answering before committing to that step, since
   the answer may be that it is not reachable by hand at all.
3. **Count the requests on a device.** Everything in *What this costs today* is
   read off the source rather than observed. It would confirm the worktree
   fan-out rather than infer it, but it changes no decision in this document,
   which is why it ranks below the other two.

## Open questions

1. **Riverpod's cost.** It is a 16.8k-line app and this touches every screen.
   Steps 1-4 are worth doing regardless; step 8 is the commitment. Worth landing
   1-4 and reassessing before going further.
2. **Which resources deserve `keepAlive`.** Sessions and notifications clearly.
   Git reads probably not — a repository changes under the app constantly.
3. **Whether the disk cache should widen.** Only sessions persist today. The
   dashboard also draws notification counts, and those come back empty on a cold
   launch until the first fetch lands.
4. **Whether steps 9-10 are worth doing at all.** The writes already work and
   are already optimistic; the transcript already pages correctly. Both would be
   moved for consistency rather than to fix anything. If the answer after step 8
   is "the remaining inconsistency is tolerable", stopping there is a legitimate
   outcome rather than an abandoned migration.
