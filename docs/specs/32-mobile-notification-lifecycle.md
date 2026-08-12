# 32 — Mobile Notification Lifecycle: Cancel on Resolve, Reconcile on Resume

## Problem

An approval answered anywhere other than the phone stays on the phone. The user
approves in the terminal or in the desktop app, the agent carries on, and the
Android notification is still sitting in the tray an hour later. The only way to
clear it is to open the app and look, which defeats the point of the
notification.

The mobile app has **no notification-cancel path at all**. Three defects, all in
the same layer:

### 1. `NotificationService` cannot cancel

`mobile/lib/services/notification_service.dart` exposes `showPermissionNotification`
and `showNotification` and nothing else. `_plugin.cancel` and `_plugin.cancelAll`
appear nowhere in `mobile/lib`. Once an OS notification is posted it can only be
swiped away by hand.

This is why approving from the phone's *own* notification action buttons also
leaves the notification on screen: `_handleNotificationAction`
(`home_screen.dart:153`) posts the action and never cancels.

### 2. `notification_resolved` is dropped on the floor

`_handleSSEEvent` (`home_screen.dart:81`) starts with:

```dart
if (event.type != 'notification') return;
```

The daemon broadcasts `notification_resolved` faithfully from five places
(`internal/server/api.go:97`, `:123`, `:179`; `internal/provider/claude/hooks.go:366`,
`:612`) and the phone ignores every one of them.

### 3. No resolved guard, no de-dupe

`_handleSSEEvent` shows a notification for any `notification` event regardless of
`data['status']`. A replayed or late event can raise an approval prompt for
something already answered. There is no `seen` set either.

### Contributing: stale background hosts

`resumeAll` (`host_manager.dart:443`) calls `fetchNotifications()` only for the
**active** host. Background hosts keep whatever list they had when the app was
suspended.

### Contributing: no foreground service

`mobile/android/app/src/main/AndroidManifest.xml` declares
`FOREGROUND_SERVICE` and `FOREGROUND_SERVICE_DATA_SYNC` but registers no
`<service>` element. SSE dies under Doze, so every resolution that happens while
the phone is asleep is missed outright. With no reconcile on resume, the tray
keeps an approval that was answered on the laptop hours ago.

## Reference implementation

The desktop app already does this correctly. `desktop/src/main/notify.ts`:

```ts
handleEvent(hostId: string, type: string, data: Record<string, unknown>): void {
  if (type === 'notification') {
    const notif = data as unknown as Notification
    if (notif.status && notif.status !== 'pending') return   // (3) resolved guard
    this.present(hostId, notif)
    return
  }
  if (type === 'notification_resolved') {                    // (2) resolve branch
    const id = typeof data.id === 'string' ? data.id : ''
    if (id) this.resolve(hostId, id)
  }
}

private present(hostId: string, notif: Notification): void {
  const key = `${hostId}:${notif.id}`
  if (this.seen.has(key)) return                             // (3) de-dupe
  ...
}
```

Port this shape, with two deliberate divergences.

**Android can actually retract.** `resolve` does real work instead of only
clearing a tray badge, so mobile removes the entry from its tracking map rather
than keeping a 500-entry `seen` ring.

**The resolved guard must not be blanket.** The desktop's
`notif.status !== 'pending'` test would, on mobile, suppress the "Task
completed" notification entirely: `claude.done` is created with status
`dismissed`, not `pending` (`internal/provider/claude/hooks.go:380`). That is a
notification type users have an alert toggle for and expect
(`notification_service.dart:32`). Mobile therefore guards on *actionable* types
only — see below.

## Design

### The notification-id problem

The OS notification id is derived from the **payload string**, not the
notification id (`notification_service.dart:71`, `:201`, `:249`):

```dart
static int _notifId(String id) => id.hashCode & 0x7FFFFFFF;
```

and callers pass `jsonEncode({'hostId': hostId, 'notificationId': id})` as `id`.

Cancelling by rebuilding that JSON would depend on Dart preserving map key
order, which is not something to rely on for a retraction that silently no-ops
when it is wrong. Instead `NotificationService` keeps its own map from a stable
key to the integer it actually used.

Stable key: `'$hostId:$notificationId'` — the same shape the desktop uses.

### Where cancellation is triggered

Four places, all funnelling into `NotificationService.cancel(hostId, id)`:

| Trigger | Why it is needed |
|---|---|
| SSE `notification_resolved` | Answered on desktop, in the terminal, or by another device |
| Local action tap (`_handleNotificationAction`) | Answered from the phone's own notification buttons |
| Answered in-app (card action) | Answered in the app while the tray copy is still posted |
| Reconcile after `fetchNotifications` | Anything missed while the SSE stream was dead |

The reconcile pass is the one that actually fixes the Doze case: it compares
every tracked notification against the freshly fetched list and cancels the ones
that are no longer `pending`. Without it, an approval answered while the phone
slept is never retracted, because the `notification_resolved` event that would
have said so was broadcast to nobody.

## Changes

### `mobile/lib/services/notification_service.dart`

Track posted notifications and add retraction.

```dart
/// Posted OS notifications, keyed by "hostId:notificationId" → the integer id
/// passed to the plugin. Rebuilding that integer from the payload would depend
/// on JSON key order, and a cancel that silently misses is worse than none.
final Map<String, int> _posted = {};

static String notifKey(String hostId, String notificationId) =>
    '$hostId:$notificationId';
```

`showPermissionNotification` and `showNotification` both gain a required
`key` parameter and record `_posted[key] = nid` on success.

New methods:

```dart
/// Retract a posted notification. No-op when nothing was posted for this key,
/// which is the common case for notification types that never raise one.
Future<void> cancel(String key) async {
  final nid = _posted.remove(key);
  if (nid == null) return;
  try {
    await _plugin.cancel(nid);
  } catch (e) {
    debugPrint('[NotificationService] cancel failed for $key: $e');
  }
}

/// Whether a notification is currently posted for this key. Used as the
/// de-dupe check so the same event delivered twice does not re-alert.
bool isPosted(String key) => _posted.containsKey(key);

Future<void> cancelAll() async { ... }
```

`_posted` is the de-dupe set and the cancel map at once — a notification is
"seen" exactly while it is posted. That is deliberately different from the
desktop's separate 500-entry `seen` ring: on mobile the entry is removed on
cancel, so a genuinely re-raised notification with the same id can alert again.
Notification ids are generated per event (`notifications.GenerateNotificationID`),
so an id is never legitimately reused.

### `mobile/lib/screens/home_screen.dart`

Replace the early return in `_handleSSEEvent`:

```dart
void _handleSSEEvent(String hostId, SSEEvent event) {
  if (event.type == 'notification_resolved') {
    if (event.data is! Map) return;
    final id = (event.data as Map)['id']?.toString();
    if (id != null && id.isNotEmpty) {
      NotificationService.instance.cancel(
        NotificationService.notifKey(hostId, id),
      );
    }
    return;
  }
  if (event.type != 'notification') return;
  ...
}
```

Add the resolved guard and de-dupe before dispatching, and pass the key through:

```dart
final data = event.data as Map;
final type = data['type']?.toString() ?? '';
final id = data['id']?.toString() ?? '';
final status = data['status']?.toString();

// An approval that is already answered must not be raised — a reconnect can
// replay the original event, and the result would be a prompt on screen that
// no action can clear. Only actionable types are gated: claude.done is created
// with status "dismissed" rather than "pending", so a blanket status check
// would silently kill the "Task completed" notification.
if (registry.isActionableType(type) && status != null && status != 'pending') {
  return;
}

final key = NotificationService.notifKey(hostId, id);
if (notifSvc.isPosted(key)) return;
```

`isActionableType` is a new type-level predicate in
`mobile/lib/providers/card_registry.dart`, alongside the existing instance-level
`needsAction`:

```dart
/// Whether this notification type blocks the agent until someone answers.
/// Type-level counterpart to [needsAction], for use when only the type string
/// is in hand (an SSE payload) rather than a parsed notification.
bool isActionableType(String type) =>
    type == 'claude.permission' ||
    type == 'claude.question' ||
    type == 'claude.trust' ||
    type.startsWith('claude.elicitation.');
```

This mirrors `needsClaudeAction` (`providers/claude/notification_ext.dart:17`)
minus the `isPending` term. Keep the two in sync; a test asserts they agree.

Every `notifSvc.show…` call in the type switch takes `key: key` alongside the
existing `id: payload`.

Cancel in `_handleNotificationAction` after the action is posted:

```dart
if (action == 'approve') {
  service.sendAction(notificationId, {'action': 'approve'});
} else if (action == 'deny') {
  service.sendAction(notificationId, {'action': 'deny'});
}
NotificationService.instance.cancel(
  NotificationService.notifKey(hostId, notificationId),
);
```

### `mobile/lib/services/daemon_api_service.dart`

Reconcile after every fetch. `fetchNotifications` already replaces `_notifications`
wholesale; add the sweep at the end:

```dart
_notifications = list
    .map((n) => HeliosNotification.fromJson(n, hostId: hostId))
    .toList();
_notificationsLoaded = true;
_reconcilePostedNotifications();
notifyListeners();
```

```dart
/// Retract OS notifications for anything the daemon no longer considers
/// pending. This is the only thing that clears a notification answered while
/// the SSE stream was dead, which is every approval made while the phone was
/// dozing.
void _reconcilePostedNotifications() {
  for (final n in _notifications) {
    if (n.isPending) continue;
    NotificationService.instance.cancel(
      NotificationService.notifKey(hostId, n.id),
    );
  }
}
```

`GET /api/notifications` returns resolved rows as well as pending ones
(`internal/server/api.go:26` passes an empty status filter through to
`ListNotifications`, which only filters when the parameter is non-empty), so the
resolved entries needed for this sweep are already in the response. No API
change.

### `mobile/lib/services/host_manager.dart`

`resumeAll` must refresh **every** host, not just the active one. A background
host's approvals are exactly the ones most likely to be stale.

```dart
Future<void> resumeAll() async {
  for (final host in _hosts) {
    final service = _services[host.id];
    if (service == null) continue;
    // Every host, not just the active one: a background host's notifications
    // are the ones most likely to have been answered elsewhere while the app
    // was suspended, and reconcile only runs on fetch.
    service.fetchNotifications();
    if (host.id == _activeHostId) {
      service.fetchSessions();
      await service.startActive();
    } else {
      await service.startBackground();
    }
  }
}
```

### In-app card actions

`DaemonAPIService.sendAction` and `dismissNotification` already call
`fetchNotifications()` on success, so the reconcile pass covers the in-app case
with no further change. Confirm this during testing rather than adding a second
cancel call.

## Out of scope

A real Android foreground service. It is the correct long-term fix for SSE dying
under Doze, but it is a separate change with its own manifest, notification
channel and battery-optimisation consent flow. The reconcile-on-resume pass in
this spec makes the missed-resolution case self-healing, which is what the bug
is actually about. Track the foreground service separately.

## Testing

Widget/unit tests in `mobile/test/`:

1. `notification_service_test.dart` — `cancel` on an unposted key is a no-op;
   `isPosted` is true after show and false after cancel; `cancel` calls the
   plugin with the same integer id that `show` used.
2. `home_screen_notification_test.dart` — a `claude.permission` event with
   `status: 'resolved'` raises nothing; the same event twice raises once; a
   `notification_resolved` event cancels a previously raised one; **a
   `claude.done` event with status `dismissed` still raises** (regression guard
   for the non-blanket resolved check).
3. `card_registry_test.dart` — `isActionableType(t)` agrees with
   `needsClaudeAction` for every registered type when status is `pending`.
4. `daemon_api_service_test.dart` — `fetchNotifications` returning a mix of
   pending and resolved rows cancels exactly the resolved ones.

Manual, against a live daemon, which is where the bug was actually observed:

5. Raise a permission from a session, confirm the phone notification appears,
   approve it **in the terminal**, confirm the phone notification disappears
   without opening the app.
6. Same, approved from the desktop app.
7. Same, but put the phone in Doze first (`adb shell dumpsys deviceidle force-idle`)
   so SSE is dead, then approve on the laptop and bring the app to the
   foreground. The notification should clear on resume via reconcile.
8. Approve from the phone's own notification action buttons; the notification
   should retract rather than linger.
9. Let a session finish normally and confirm "Task completed" still arrives.

## Implementation order

1. `NotificationService`: `_posted` map, `key` parameter, `cancel`, `isPosted`,
   `cancelAll`.
2. `card_registry.dart`: `isActionableType`.
3. `home_screen.dart`: resolve branch, resolved guard, de-dupe, key plumbing,
   cancel on local action.
4. `daemon_api_service.dart`: `_reconcilePostedNotifications`.
5. `host_manager.dart`: refresh all hosts on resume.
6. Tests.

## Notes

- No daemon changes. Every event and field this needs is already broadcast; the
  phone was simply not listening.
- `claude.done` and `claude.error` notifications get tracked and cancelled by
  this change too. `claude.done` is fire-and-forget and nothing resolves it, so
  in practice it is only ever retracted by `cancelAll`. `claude.error` gains a
  real resolve path in spec 33, at which point this machinery retracts it for
  free.
- Retracting on `notification_resolved` means a notification answered on the
  desktop vanishes from the phone even if the user never looked at it. That is
  the intended behaviour and matches the desktop's tray badge.
