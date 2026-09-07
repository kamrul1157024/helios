# 59 — Background Notifications: A Service on Android, a Push on iOS

## Problem

The phone only hears from the daemon while the app is running. `DaemonAPIService`
holds one SSE stream per host, and `home_screen.dart:74` deliberately keeps that
stream open when the app is paused — there is even a comment saying so. That
works for a backgrounded app and not for a closed one. Swipe Helios out of
recents, or let Doze cut the socket while the phone sleeps, and every approval
raised after that moment is missed. The agent sits blocked until someone happens
to open the app.

Spec 32 fixed the *aftermath*: `retainOnly` (`notification_service.dart:152`)
sweeps the tray on resume so a stale approval does not linger. It did not, and
said it did not, fix delivery — line 325: "A real Android foreground service…
Track the foreground service separately." This is that spec.

What exists today, all of it half a step in:

| Piece | State |
|---|---|
| `flutter_foreground_task: ^9.0.2` | in `pubspec.yaml:26`, zero Dart callers |
| `FOREGROUND_SERVICE`, `FOREGROUND_SERVICE_DATA_SYNC` | declared in `AndroidManifest.xml:6-7`, no `<service>` element to use them |
| `push_subscriptions` table | `internal/store/push_subscriptions.go`, Web Push shape (`endpoint`/`p256dh`/`auth`), no sender, no route — `internal/push/` is already deleted |
| iOS background | no `UIBackgroundModes`, `AppDelegate.swift` is bare boilerplate |

## Shape of the answer

Android can keep a socket open in a foreground service. iOS cannot: there is no
equivalent, and a silent `content-available` push is throttled hard enough that
it cannot be the delivery path for something as time-critical as a blocked
agent. So the two platforms get different answers, in two phases.

Phase 1 is Android, and its only daemon change is a heartbeat interval the
client can name. Phase 2 is iOS and needs a push sender in the daemon, an Apple
Developer account, and a key on every machine that runs `helios`. Phase 1 ships
alone and is useful alone.

---

## Phase 1 — Android foreground service

### Handoff, not co-ownership

`flutter_foreground_task` runs its `TaskHandler` in a **separate Flutter
isolate**. No shared memory, no `Provider` tree, no access to the UI isolate's
`DaemonAPIService` instances.

Two ways to live with that:

**Service owns the SSE always**, UI isolate subscribes over
`sendDataToMain`. One socket, one notification source, no seam. It also means
proxying the whole data layer: `DaemonAPIService` is 1860 lines of
`ChangeNotifier` feeding the query layer from spec 49, and every session list,
transcript tail and file event would have to cross an isolate boundary. Rejected
as disproportionate.

**The two take turns.** The UI isolate owns the stream while the app is
resumed. On `AppLifecycleState.paused` the app starts the service, which opens
its own stream. On `resumed` the app stops the service and reconnects. Exactly
one socket per host at any moment, and the service isolate needs only a thin
client — read credentials, open SSE, post notifications — with no UI state in
it at all.

Take turns. `HostManager.stopAll` / `resumeAll` (`host_manager.dart:424`, `:431`)
are already the handoff points; they gain a service start and stop.

### What the service isolate has to rebuild

It cannot borrow anything from the UI isolate, so it reads the same storage:

- the host list from `FlutterSecureStorage` under `helios_hosts`, and each seed
  under `helios_host_<id>_key` (`host_manager.dart:29`, `:134`)
- an `ApiClient` per host for Ed25519 request signing
- `NotificationService.init()` for the channels and the alert toggles

Platform channels work from the background isolate because
`flutter_foreground_task` registers plugins on the service engine. Secure
storage and `flutter_local_notifications` are both fine there; this is the
normal use of the package, not a stretch of it.

The isolate does **not** parse sessions, transcripts or file events. It handles
`notification` and `notification_resolved` and drops everything else on the
floor.

### Notification ids must stop depending on the payload

`_notifId` hashes the payload string (`notification_service.dart:116`), and
callers pass `jsonEncode({'hostId': …, 'notificationId': …})`. The mapping from
a stable key to that integer lives in the in-memory `_posted` map
(`notification_service.dart:122`).

Two isolates means two `_posted` maps. The service posts a notification, the app
comes back, and `retainOnly` cannot retract it — the UI isolate's map is empty,
so it has no integer to cancel with. The tray keeps an approval that was
answered an hour ago, which is the exact bug spec 32 set out to kill.

Two changes fix it:

1. **Derive the integer from the stable key, not the payload.**
   `_notifId(notifKey(hostId, id))`. Both isolates then compute the same integer
   for the same notification without sharing anything.
2. **Persist the posted key set.** `_posted` writes through to
   `SharedPreferences` on every post and cancel; `init()` reads it back, and
   `resumeAll` calls `reload()` first so the UI isolate sees what the service
   wrote. The map stays in memory as the fast path — the file is the crossing
   point, not the source of truth during a session.

With those, `retainOnly` works across the handoff unchanged, and the de-dupe in
`isPosted` stops the app re-alerting for something the service already posted.

There is a third change those two force. The posted set doubles as the de-dupe
check, and persisting it makes it outlive the notifications it describes: a
force-stop, a reboot or the user swiping the shade empties the tray and tells
nobody. The stale key then answers "already posted" for ever, and the approval
it names never buzzes again — the agent stays blocked and the phone stays
quiet. So `init` and `reloadPosted` both prune the set against
`getActiveNotifications()`, which is the only honest account of what is on
screen. In memory this flaw lasted until the process died; on disk it would
have been permanent.

### The channel that only the activity had

`MainActivity.configureFlutterEngine` registers `com.helios.helios/notifications`
— the channel that creates the notification channels and plays sound manually,
because ColorOS and RealmeUI strip sound from channels
(`notification_service.dart:241`). The service engine is a different
`FlutterEngine`, so that channel would not exist there and every background
notification would be silent on exactly the devices the workaround was written
for.

Pubspec plugins are fine: `flutter_foreground_task` builds its engine with
`FlutterEngine(context)`, which registers them. A channel wired up by hand in an
activity is not a plugin and gets nothing. So it becomes one —
`HeliosNotificationsPlugin` — registered in `MainActivity` and, for the service
engine, from a listener installed in `Application.onCreate`. Application, not
activity: the service can start into a process where no activity has ever run.

### Service type: `specialUse`, not `dataSync`

`dataSync` is the honest classification of "hold a socket open", and it is what
the manifest already asks permission for. Android 15 stops a `dataSync`
foreground service after six hours in any 24-hour window. A watcher that dies
after six hours is a watcher you cannot trust overnight, which is the case that
matters.

`specialUse` has no such cap. Its cost is a Play Store policy review, and Helios
is not on Play — it ships as a sideloaded APK from `make apk` and the release
workflow. So: declare `specialUse` with a subtype explaining the socket, and
keep the `dataSync` permission for older target levels.

```xml
<uses-permission android:name="android.permission.FOREGROUND_SERVICE_SPECIAL_USE"/>

<service
    android:name="com.pravera.flutter_foreground_task.service.ForegroundService"
    android:foregroundServiceType="specialUse"
    android:exported="false">
    <property
        android:name="android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE"
        android:value="Maintains a live connection to the user's own Helios daemon so that agent approval requests arrive without delay."/>
</service>
```

### The persistent notification

A foreground service must show one, so it should say something. "Helios —
watching 3 sessions on ripley", low importance, its own channel so it can be
silenced without touching approvals, tap opens the app. It is also the honest
signal that the app is holding a socket open, which a user is entitled to see.

### Battery optimisation

Doze will still kill the socket on OEMs that ignore the foreground-service
contract unless the app is exempt. Ask for
`REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` from the notification settings screen,
next to the toggle that turns the service on — never on first launch. A prompt
like that on launch, before the user has seen a single notification, reads as an
app overreaching.

### Battery

The service costs almost no CPU — the isolate blocks on a socket read. What it
costs is radio wakeups, and the number that sets them is the SSE heartbeat:
every 30 seconds, from `internal/server/api.go:1547`, matched by a client
watchdog at 30s against a 75s silence threshold
(`daemon_api_service.dart:21`, `:50`).

On cellular each heartbeat drags the modem out of idle. 30s means 2,880 wakeups
a day per host, which models out at roughly 3-5% of a 5000 mAh battery — double
that with two hosts. On Wi-Fi the same traffic is closer to 0.5%, because the
radio is associated anyway. These are estimates, not measurements; verify with
`adb shell dumpsys batterystats` before and after.

Two decisions follow.

**No wakelock.** `flutter_foreground_task` sets `allowWakeLock: true` by
default, which pins the CPU out of deep sleep for as long as the service runs
and would cost more than everything else here combined. It buys nothing: an
inbound TCP packet raises a network interrupt that wakes the CPU on its own.
Set it false, and keep the Wi-Fi lock.

**A slower heartbeat in the background.** 30 seconds is tuned for someone
watching the app, where a dead socket should be noticed quickly. Nobody is
watching a background service, and a 75-second detection window is not worth 3-5%
a day. Let the client name its own interval —
`GET /api/events?heartbeat=240` — and scale the silence threshold with it.
240s stays under common NAT timeouts, and Tailscale is keeping the tunnel alive
underneath in any case. That drops the background cost to about 360 wakeups a
day. The foreground path keeps 30s unchanged.

This adds the one Go change in phase 1: `/api/events` reads an optional
`heartbeat` parameter, clamps it to something sane, and defaults to today's 30s
when it is absent.

### The gap at handoff

Between the app stopping its stream and the service opening one, a notification
can land unseen. Both sides close it the same way, and it is already built:

- **Service start**: `GET /api/notifications`, post anything `pending` whose key
  is not in the persisted posted set.
- **App resume**: `resumeAll` already fetches per host and runs the reconcile
  sweep from spec 32.

A cold start needs the same care for the opposite reason. It never raises
`AppLifecycleState.resumed`, and Android restarts the service on its own after
the app is killed, so without an explicit stop in `loadStoredHosts` the app and
the service both hold a stream to every host and race to post the same
notification.

No new endpoint. `GET /api/notifications` already returns resolved rows
alongside pending ones (`internal/server/api.go:26`).

### Changes

| File | Change |
|---|---|
| `mobile/android/app/src/main/AndroidManifest.xml` | `FOREGROUND_SERVICE_SPECIAL_USE` permission, `<service>` element, subtype property, the custom `Application` |
| `mobile/android/.../HeliosNotificationsPlugin.kt` *(new)* | the sound/channel `MethodChannel`, lifted out of `MainActivity` so both engines get it |
| `mobile/android/.../HeliosApplication.kt` *(new)* | attaches that plugin to the service engine |
| `mobile/lib/services/notification_service.dart` | `_notifId` takes the key; `_posted` persists through `SharedPreferences`; `init` restores it and prunes it against the tray |
| `mobile/lib/services/background_watch.dart` *(new)* | the app-side switch: enable, start, stop, battery exemption |
| `mobile/lib/services/background_watcher.dart` *(new)* | `TaskHandler`: load hosts from secure storage, open one SSE per host, handle `notification` / `notification_resolved`, seed from `GET /api/notifications` on start |
| `mobile/lib/services/host_manager.dart` | `handOffToBackground` on pause; `resumeAll` and `loadStoredHosts` stop the service and re-read the posted set |
| `mobile/lib/services/daemon_api_service.dart` | request `?heartbeat=` and scale `streamSilenceThreshold` to it |
| `internal/server/api.go` | `/api/events` honours an optional, clamped `heartbeat` parameter; defaults to 30s |
| `mobile/lib/screens/notification_settings_screen.dart` | "Watch in background" toggle plus the battery-optimisation prompt |

---

## Phase 2 — iOS via APNs

iOS has no way to hold that socket, so delivery has to come from Apple. This
phase is larger than phase 1 and touches the daemon.

**What Apple requires.** An Apple Developer account, an APNs auth key (`.p8`),
`UIBackgroundModes: remote-notification` in `Info.plist`, and registration in
`AppDelegate.swift` — which is currently untouched boilerplate.

**Alert push, not silent.** `content-available` alone is rate-limited by iOS and
may be delayed for hours on a phone the system considers idle. A blocked agent
cannot wait on a heuristic. Send a real alert with `interruption-level:
time-sensitive`, matching what `DarwinNotificationDetails` already asks for
locally (`notification_service.dart:373`).

**Daemon side.** The `push_subscriptions` table is Web Push shaped and unused;
replace its columns with `(device_kid, platform, token)`. Add
`POST /api/push/register`, a sender, and a call from wherever a notification is
created — the same places that already broadcast `notification` over SSE, so the
push is a second fan-out arm rather than a new pathway.

**Credentials.** Every daemon needs the `.p8` key. That is a real cost for a
self-hosted tool and it should be optional: no key, no push, and the app falls
back to what it does today. It should not be a startup error.

**Content.** Send the title and the notification id, not the body. The phone
fetches the detail over the tailnet when the app opens. Apple's servers then
carry "Permission request from ripley" and nothing about what the agent wanted
to run.

**Off the tailnet.** APNs delivers even when the phone cannot reach the daemon
at all — which means a notification the user can see and cannot act on. Tapping
it must land on a clear "cannot reach ripley" state, not a spinner or a
half-rendered approval card.

---

## Out of scope

**FCM on Android.** The foreground service removes the need, and adding FCM
would mean a Firebase project and a service-account key on every daemon for a
platform that does not need it. Revisit only if OEM process-killers prove the
service unreliable in practice.

**Reworking spec 32's reconcile.** It stays exactly as it is; this spec only
makes its bookkeeping survive an isolate boundary.

## Testing

Automated, in `mobile/test/`:

1. `notification_service_test.dart` — the same key yields the same integer
   across two `NotificationService` instances; a posted key survives an `init()`
   on a fresh instance; `retainOnly` cancels a key that was posted by the
   *other* instance.
2. `background_watcher_test.dart` — the handler seeds from a mocked
   `/api/notifications`, posts only `pending` rows, and skips keys already in
   the persisted set.

Manual, on a real device, which is the only place this can be shown to work:

3. Start a session, background the app, confirm the persistent notification
   appears and an approval still arrives.
4. Swipe the app out of recents. Raise an approval. It must still arrive.
5. `adb shell dumpsys deviceidle force-idle`, raise an approval, confirm
   delivery. This is the case the whole spec exists for.
6. Approve from the phone's notification buttons while the app is closed;
   confirm the notification retracts and the daemon sees the answer.
7. Approve in the terminal while the app is closed; the tray entry must clear
   without opening the app.
8. Reopen the app after (7) and confirm nothing double-posts.
9. Leave it overnight and confirm the service is still connected in the morning
   — the six-hour cap is the reason for `specialUse` and this is the check.
10. `adb shell dumpsys batterystats --reset`, then eight hours on cellular with
    the service running. Compare against the same eight hours with it off. The
    estimates in this spec are modeled; this is the number that decides whether
    the default heartbeat is right.

## Implementation order

1. `notification_service.dart`: key-derived integer, persisted posted set.
2. Manifest: permission, `<service>`, subtype.
3. `background_watcher.dart`: the `TaskHandler` and its SSE client.
4. `host_manager.dart`: start on pause, stop on resume, `reload()` before the
   sweep.
5. Settings toggle and the battery-optimisation prompt.
6. Tests, then the manual pass.
7. Phase 2, as a separate change.

## Notes

- Steps 1 and 2 are independently shippable and carry no risk: a key-derived
  notification id is strictly better than a payload-derived one whether or not
  the service ever lands.
- The service isolate reads the same secure-storage keys the UI isolate writes.
  Anything that changes the host-storage format has to change both, and there is
  no compiler to catch it — worth a comment at both ends.
- `flutter_foreground_task` is already a declared dependency with no callers. If
  this spec is abandoned, remove it from `pubspec.yaml:26` rather than leaving a
  dependency that implies a feature the app does not have.
