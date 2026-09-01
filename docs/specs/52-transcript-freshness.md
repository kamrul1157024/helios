# Transcript Freshness: Heal a Missed Event Without a Spinner

## The claim

The mobile transcript should recover from a missed event on its own, and it must
do so without ever putting the skeleton back over messages that are already on
screen.

Both halves matter, and today it does neither. The only thing that refreshes the
transcript is a `session_status` event addressed to that exact session; if the
one at the end of a turn is lost, the conversation stays frozen until the process
restarts. And the mechanism that would otherwise fix it — invalidating the
provider — is precisely the mechanism that wipes the list and throws away the
reader's place, which is why `transcript.dart:61-63` forbids it.

So this is not "add a refresh". Every heal below goes through `pullNew`, whose
whole state transition is `AsyncData → AsyncData`. `isLoading` is never set.

**Scope: `mobile/`, plus one change in `internal/transcript`.** The daemon change
is a single additive response field. No route changes shape, no existing field
changes meaning, no new event type, and `internal/server`, `desktop/`, `cmd/` and
`internal/tui` are untouched. An older client talking to a newer daemon behaves
exactly as it does today.

Nearly all of the work is on the client, and that is the argument rather than an
accident of scope — see *The daemon is not asked to notice the drop*.

## Where we are

| | Today |
|---|---|
| Transcript cache | `TranscriptNotifier`, a Riverpod `AsyncNotifierProvider.family` (`transcript.dart:137`) — **not** `autoDispose`, so it lives as long as the process |
| Refresh triggers | one: a real `session_status` whose `session_id` matches (`session_detail_screen.dart:102-103`), debounced 500 ms (`:107-110`) |
| Plus | a pull 500 ms after sending a prompt, and again at 5 s and 10 s (`:364,371-375`) |
| Event-driven invalidation | none — `CacheTarget.transcript: break;` (`cache_invalidator.dart:58-59`) |
| On SSE reconnect | the notification tray re-syncs (`daemon_api_service.dart:280`); the transcript does nothing |
| On app resume | `resumeAll` reconnects each host (`host_manager.dart:414-424`); the transcript does nothing |
| Gap detection | `epoch` + `after_seq`, already in the protocol (`api.go:451-461`) and already honoured by the client (`transcript.dart:33`) |
| Loading render | `_loading = transcript.isLoading` (`session_detail_screen.dart:460`) → full `MessageListSkeleton` (`:502-506`) |

The protocol is not the problem. Spec 38 built `seq`, `epoch` and `after_seq`
exactly so a client could ask "what have I missed", and the client asks
correctly. What is missing is every occasion on which it should ask.

## What this costs today, stated plainly

### The stream is lossy by construction, and says nothing about it

`internal/server/sse.go:36-42`:

```go
for client := range b.clients {
	select {
	case client.ch <- event:
	default:
		// Client buffer full, skip
	}
}
```

Sixty-four slots per client (`sse.go:57`). A phone on a slow link overflows, and
the drop is silent in both directions — no error to the daemon, no signal to the
client. `session_status` is broadcast from fourteen sites including every
`PreToolUse`, so a turn end is a burst, and a burst is exactly when a slow
client's buffer is full.

Disconnect is the same story with a different cause. `sse.go:65-70` deletes the
client and keeps nothing; there is no `Last-Event-ID`, no replay buffer, no
resume cursor. Whatever was broadcast during the gap is gone.

None of that is changed by this spec. It is stated because it is the premise: the
stream is best-effort by design, so a client that treats an event as its only
cue to re-read has built on something that was never load-bearing.

### One dropped event is survivable. The last one is not

`pullNew` asks for everything after the newest `seq` it holds, so a single lost
event heals on the next one. That makes the failure look intermittent and
harmless, which is why it has survived.

It is neither, because the burst that overflows the buffer is at the end of a
turn. Drop the last `session_status` and the agent is now idle: no further event
will ever be broadcast for that session. The transcript is frozen, and because
the provider is not `autoDispose` (`transcript.dart:137`), leaving the screen and
coming back re-reads the same held `Transcript`. Only a restart clears it.

### The delta silently drops its middle

`internal/transcript/store.go:134-137`:

```go
if limit > 0 && len(fresh) > limit {
	fresh = fresh[len(fresh)-limit:]
}
```

More than `limit` messages past `after_seq` — a backgrounded app, a long turn —
and the daemon answers with the newest fifty and discards everything between.
The client appends that run to a list ending at `after_seq` (`transcript.dart:41-48`),
so the held conversation now has a hole and a `seq` jump across it.

Nothing in the answer says so. `epoch` is unchanged, because the transcript did
not fork. `HasMore` on this path is `start > 0` (`store.go:145`), which reports
whether *older* pages exist — a different question, and the one the client
already reads that way to drive `loadOlder`. So the hole is undetectable by the
client and permanent.

### `pullNew` reads its base before the await and writes it after

`transcript.dart:95-112` captures `held` at `:96` and writes `appendDelta(held, …)`
at `:111`, with a network round trip in between and no in-flight guard.
`loadOlder` has one — `_loadingOlder`, `:118,121` — which makes the omission look
accidental rather than considered.

Overlap is reachable: the SSE debounce timer (`session_detail_screen.dart:108`)
and the 5 s / 10 s resend timers (`:371-375`) are independent. Two pulls in
flight from `seq 100`, the second answering 101–107 and the first answering
101–105 but landing later, and the state regresses to 105. It heals on the next
event — and at turn end there is no next event.

### A failed fetch is indistinguishable from an empty one

`fetchTranscript` catches everything and returns `null` (`daemon_api_service.dart:556-559`).
`pullNew` then returns at `:110` without retrying and without recording anything.
The event arrived, the pull fired, the request failed on a flaky link — and the
outcome is identical to the dropped-event case.

### The synthetic-event path does not reach the screens

`_markStale` (`daemon_api_service.dart:80-81`) raises an event on `onSSEEvent`
only. That reaches the cache invalidator through `HostManager` (`host_manager.dart:167`).
It does **not** reach `_eventController`, which is what the three screens listen
on (`session_detail_screen.dart:98`, `home_screen.dart:58`, `terminal_screen.dart:53`).
Only `_handleEvent` (`:322-325`) feeds both.

This is why "resume re-syncs the tray" does not imply "resume re-syncs anything a
screen holds". Anything meant to reach a viewer has to go through `_handleEvent`.

## The change

### Healing goes through `pullNew`, never through invalidation

`ref.invalidate(transcriptProvider(...))` does not appear anywhere in this
design. It cannot: in Riverpod an invalidated notifier becomes
`AsyncLoading.copyWithPrevious(old)`, which keeps `hasValue` but still reports
`isLoading`, and `session_detail_screen.dart:460` reads exactly that field to
decide whether to draw the skeleton. Invalidation blanks the conversation and
loses the scroll position on every heal.

`pullNew` assigns `AsyncData` directly (`transcript.dart:111`) and touches no
loading state. Every trigger added below calls it.

### The render guard stops lying

`session_detail_screen.dart:460` becomes:

```dart
_loading = transcript.isLoading && !transcript.hasValue;
```

The skeleton is then a first-load affordance rather than a refresh one, which is
what it was always meant to be.

This is defence in depth rather than a load-bearing part of the heal, and it pays
for itself immediately: it makes the `ref.invalidateSelf()` fallback at
`transcript.dart:100` free. `build` returns `const Transcript()` rather than
throwing when there is nothing to show (`:74,79`), so the provider always holds a
value by the time that branch can run, and the refresh now keeps whatever is on
screen while it re-reads.

### `pullNew` becomes single-flight, re-reads after the await, and loops

```dart
Future<void>? _inFlight;
bool _again = false;

Future<void> pullNew() {
  if (_inFlight != null) {
    _again = true;
    return _inFlight!;
  }
  _inFlight = _pull().whenComplete(() {
    _inFlight = null;
    if (_again) {
      _again = false;
      pullNew();
    }
  });
  return _inFlight!;
}
```

A trigger arriving mid-flight schedules a trailing re-run rather than being
dropped. Dropping it would reintroduce the bug this spec exists to fix, one scale
smaller: the whole failure mode is a signal that arrives and is not acted on. The
guarantee wanted is that every trigger is followed by a pull that *started* after
it, and the trailing flag is the cheapest thing that gives it.

`_pull` re-reads the state on each iteration and keeps going while the daemon
says there is more:

```dart
Future<void> _pull() async {
  final service = _service;
  if (service == null) return;
  while (true) {
    final held = state.valueOrNull;
    if (held == null || held.messages.isEmpty || held.epoch.isEmpty) {
      ref.invalidateSelf();
      return;
    }
    final result = await service.fetchTranscript(
      arg.$2,
      limit: pageSize,
      afterSeq: held.messages.last.seq,
      epoch: held.epoch,
    );
    if (result == null) return;
    state = AsyncData(appendDelta(state.valueOrNull ?? held, result));
    if (!result.moreAfter) return;
  }
}
```

Reading `state` after the await is safe rather than merely better, and single
flight is what makes it so: the only other writer to this notifier is `loadOlder`,
which prepends and never touches the tail. Without the guard, re-reading would be
the more dangerous of the two options.

An `epoch` change mid-loop replaces the list and ends the loop, because
`appendDelta` (`:33-40`) returns the fresh page and `more_after` is false on a
page.

### The states, and which one draws the skeleton

```
                   cold cell
                       |
                       v
                +-------------+
                |   Loading   |   the skeleton is drawn here, and nowhere else
                +-------------+
                       |
                       |  page arrives — or the fetch fails and build()
                       |  hands back an empty Transcript
                       v
                +-------------+
        +------>|    Held     |
        |       +-------------+
        |              |
        |              |  trigger: screen mount · foreground
        |              |           · reconnect · session_status
        |              v
        |    +->+-------------+   trigger arrives    +-------------+
        |    |  |   Pulling   |--------------------->|   Queued    |
        |    |  |             |<---------------------|             |
        |    |  +-------------+   flight ends:       +-------------+
        |    |     |       |      run once more
        |    +-----+       |
        |   more_after:    |   delta appended and more_after false
        |   advance the    |   epoch_changed: the list is replaced
        |   cursor, ask    |   null answer: the list is untouched
        |   again          |
        +------------------+
```

Only **Loading** draws `MessageListSkeleton`. It is the one state with no value
behind it, and after the guard change at `session_detail_screen.dart:460` it is
the only state that reports `isLoading` without `hasValue`.

**Pulling** and **Queued** are not loading states at all — the cell holds
`AsyncData` for their whole duration, and the widget never rebuilds into the
skeleton branch.

The `invalidateSelf` fallback is the one edge left off the diagram, because it is
the only one that does not fit the shape: it takes **Held** to an
`AsyncLoading` carrying the previous value, and back to **Held** when the page
arrives. It reports `isLoading`, so before the guard change it flashed the
skeleton; after it, the conversation stays on screen while the page is re-read.

The four triggers in the table below are the only edges into **Pulling**. Every
one of them is a `pullNew` call, which is the entire reason this state machine
has no path from **Held** back to **Loading**.

### The delta stops dropping its middle

`store.go:134-137` keeps the **oldest** `limit` and reports the truncation:

```go
moreAfter := false
if limit > 0 && len(fresh) > limit {
	fresh = fresh[:limit]
	moreAfter = true
}
```

Oldest rather than newest, because contiguity is what lets the client's loop
terminate correctly. It appends a run starting at `afterSeq+1`, advances its
cursor to the last `seq` it received, and asks again. Keeping the newest can
never be made contiguous — there is no cursor that would fetch the skipped range,
which is why the hole today is permanent.

`TranscriptResult` gains `more_after`, false on the paging path and false when
the delta fits. It is additive: a client that ignores it behaves as today.

`HasMore` keeps its current meaning untouched. The two answer different
questions — one about older pages, one about the tail — and collapsing them is
what made the hole invisible in the first place.

### The daemon is not asked to notice the drop

The obvious server-side answer is to record the discard — a `dropped` bit on
`sseClient`, flushed as a `resync` frame the next time anything is written to
that client. It was the first draft of this section, and it is the wrong shape.

The flush has to ride some frame, and on an idle session the only frame left is
the 30 s heartbeat (`sse.go:76`). So the recovery time for the exact failure this
spec is about — the last event of a turn, after which the agent goes quiet — is
pinned to a heartbeat interval. Half a minute of a visibly wrong conversation, in
exchange for a new atomic on the broadcaster, a new event type, a fan-out through
`_handleEvent`, a new unfiltered branch in the screen, and a `cache_effects` row.

**Staleness only matters when someone is looking at it.** That reframes the
problem: the client does not need to be told it missed something, it needs to
re-read whenever a viewer arrives. There are exactly three ways a viewer arrives
at a transcript, all of them already observable on the client, none of them
requiring the daemon to know anything:

- the session screen is opened,
- the app comes back to the foreground — unlock, app switcher, notification tap,
- the stream reconnects after a drop.

Cover those and the reader never *sees* a stale transcript, whatever the stream
did. The heal latency stops mattering, because the heal always lands before the
eye does.

This also happens to match where drops come from. Overflowing 64 slots requires
the client to stop draining for a while, and a foregrounded app on a live
connection drains continuously. The buffer fills when the process is suspended or
the link has stalled — and both of those end in a resume or a reconnect, which
are two of the three triggers. The window the daemon would have reported is
almost exactly the window the client can already detect on its own.

So `sse.go` is left alone. The 64-slot buffer stays, the `default:` branch stays,
and no new event type is introduced.

### The triggers

| When | Today | Becomes |
|---|---|---|
| `session_status` for this session | pull, 500 ms debounce (`session_detail_screen.dart:107-110`) | unchanged |
| Detail screen mounts | nothing — `build` already ran on the live provider, so re-entering the session fetches nothing | pull |
| App returns to the foreground | nothing for the transcript (`host_manager.dart:414-424` reconnects the stream) | pull |
| SSE reconnects | the tray re-syncs (`daemon_api_service.dart:272-280`); the transcript does not | pull |

The first three are pure client changes and need no new plumbing at all.

**Mount** is one `pullNew()` in `initState`, beside the `_loadGitStatus()` that
is already there (`session_detail_screen.dart:93`). This is the trigger that
makes "go back and come back" work, which today it does not: the provider is not
`autoDispose`, so re-entry re-reads the held `Transcript` and issues no request.

**Foreground** is a `WidgetsBindingObserver` on the detail screen, resuming on
`AppLifecycleState.resumed`. `terminal_screen.dart:73-84` already does exactly
this for its shells, so the pattern is in the codebase. It has to be the detail
screen's own observer rather than `home_screen`'s (`:71-77`), because home has no
idea which transcript is on top of the navigator stack.

One `resumed` covers unlock, returning from the app switcher, and tapping a
notification — they are the same lifecycle edge. It also fires after a transient
`inactive` that never reached `paused`, such as pulling down Control Center, so
this will sometimes pull when nothing changed. That is deliberate: a delta that
finds nothing is one small request against a cursor the client already holds, and
single-flight collapses a burst of them. Trying to tell a real return from a
spurious one would cost more than the requests it saves.

**Reconnect** raises a synthetic event through `_handleEvent` (`:322-325`) rather
than `_markStale` (`:80-81`), for the reason given above — `_markStale` reaches
the invalidator and no screen. This is the one trigger that needs the event path,
because the screen has no other way to learn the socket came back.

## What we are not doing

- **Anything in `internal/server/sse.go`** — no `dropped` bit, no `resync` event,
  no larger buffer. Argued above: the client can already detect every window in
  which the drop matters, and a server signal would have to wait for a frame to
  ride out on.
- **Sequence numbers on the SSE stream.** A cursor is a promise that the daemon
  can serve the range behind it, which means a replay buffer — a second, weaker
  copy of a transcript the daemon can already replay from disk by `after_seq`.
- **A poll while the detail screen is open.** The residual gap is a reader
  watching a live session who never leaves, never backgrounds, and never
  reconnects. That is also the case where the buffer is least likely to overflow,
  because a foregrounded app drains it continuously. A timer would fire mostly to
  learn nothing, on a battery. If this gap shows up in practice, a slow poll
  gated on *screen mounted **and** session active* is the answer — but the
  evidence for it should come from use, not from this document.
- **Making the transcript provider `autoDispose`.** It would refetch page one on
  every re-entry and discard every older page the reader had scrolled back to,
  which is the behaviour `transcript.dart:61-63` is written to prevent.
- **Surfacing fetch failures in the UI.** `fetchTranscript` swallowing errors
  (`daemon_api_service.dart:556-559`) is a real gap, but the trailing re-run and
  the reconnect trigger already convert a transient failure into a later success.
  An error affordance in the transcript is a design question, not a correctness
  one, and it is not this spec.
- **Desktop.** `chat.tsx` has the same shape and its own conversion in spec 48.
- **The terminal.** A shell is a live byte stream and has nothing to re-read.

## Tests

Dart, in `test/transcript_test.dart`, which already asserts the reducers directly
and needs no widget tree:

- A delta with `more_after` appends and the loop asks again; the final list
  equals what one unbounded fetch of the same range would have produced —
  contiguous `seq`, no repeat.
- Two overlapping `pullNew` calls put one request in flight and schedule exactly
  one trailing re-run, and the state never regresses: the list after the second
  is a superset of the list after the first.
- A `null` answer leaves the held list and epoch untouched, clears `_inFlight`,
  and leaves the next `pullNew` able to run.
- An `epoch_changed` answer arriving mid-loop replaces the list rather than
  appending, and ends the loop.
- The render guard: `AsyncLoading` carrying a previous value reads as not
  loading; a bare `AsyncLoading` reads as loading.

Widget, in `test/` beside the existing screen tests — these three are the whole
point of the spec and none of them can be asserted at the reducer:

- Mounting the detail screen over a provider that already holds a transcript
  issues a delta request. This fails today.
- `didChangeAppLifecycleState(resumed)` issues a delta request, and does so
  whether or not the state passed through `paused` first.
- Neither of the above draws `MessageListSkeleton` while the request is in
  flight; the messages already rendered stay rendered.

Go:

- `Delta` with more than `limit` messages past `afterSeq` returns the oldest
  `limit`, starting at `afterSeq+1`, with `more_after` true.
- Looping `Delta` with the cursor advanced by each answer reproduces the full
  range exactly — no gap, no duplicate — over a fixture larger than several
  pages.
- `more_after` is false on the paging path and false when the delta fits.
- `HasMore` keeps its current value on both paths.

Manual, on a cabled phone, one per trigger:

- Background the app mid-turn long enough to miss several hundred messages,
  then foreground it. The conversation catches up with no gap and no skeleton.
- Lock the phone with the session open, let a turn finish, unlock.
- Leave the session for the list and come back.

## Success criteria

- A reader never sees a stale transcript: opening the session, foregrounding the
  app, or reconnecting all bring it current before the screen is next looked at.
- No path leaves a transcript stale until the process restarts.
- An app that missed 300 messages catches up in `ceil(300/50)` delta requests
  with no hole.
- The skeleton appears once per session per process, on first open.
- `internal/server/sse.go` is unchanged.
