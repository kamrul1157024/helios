# Spec: Notification Alert Settings

## Goal

Give users per-type control over which notifications alert (buzz + sound) on their device. Every notification always shows in the OS notification shade — the alert toggle only controls whether it makes noise.

---

## Principles

- **Host is dumb about presentation.** It fires every notification event over SSE unconditionally.
- **Device is the authority.** Each device independently stores its own alert preferences in SharedPreferences.
- **All notifications always show.** The OS notification shade always receives the notification — no type can be hidden.
- **Alert = buzz + sound only.** The single per-type toggle controls whether the device makes noise, nothing else.
- **Blocking notifications default to alert on.** Permission, question, elicitation block Claude until resolved — alerting by default is critical.
- **Informational notifications default to alert on.** Done and error also default on, but can be silenced.

---

## Notification Types

| Type | Blocking? | Default Alert | Category |
|---|---|---|---|
| `claude.permission` | Yes | On | Action required |
| `claude.question` | Yes | On | Action required |
| `claude.elicitation.form` | Yes | On | Action required |
| `claude.elicitation.url` | Yes | On | Action required |
| `claude.done` | No | Off | Informational |
| `claude.error` | No | Off | Informational |

---

## Data Model

### Stored in SharedPreferences (per device)

One map stored as a JSON string:

**`notif_alert_types`** — which types buzz/play sound when a notification arrives

```dart
static const Map<String, bool> _defaultAlertTypes = {
  'claude.permission':        true,
  'claude.question':          true,
  'claude.elicitation.form':  true,
  'claude.elicitation.url':   true,
  'claude.done':              false,
  'claude.error':             false,
};
```

---

## NotificationService API Changes

### New key
```dart
static const _keyAlertTypes = 'notif_alert_types';
```

### New accessors
```dart
bool isAlertEnabled(String notifType)           // should it buzz/play sound?
Future<void> setAlertEnabled(String notifType, bool value)
Map<String, bool> get alertTypes                // full map for UI
```

### Dispatch changes (home_screen.dart)

At the dispatch point where SSE events are handled (lines 99–123), the `silent` flag is determined by:

```dart
final svc = NotificationService.instance;
final silent = !svc.isAlertEnabled(type) || VoiceService.instance.globalVoiceActive;
```

The notification is always shown. `silent: true` suppresses buzz/sound only.

The existing global `soundEnabled` and `vibrationEnabled` booleans remain as master overrides — per-type alert AND global sound enabled → plays sound.

---

## New Screen: NotificationSettingsScreen

### Navigation

Entry point added to the Notifications section in `settings_screen.dart`:

```
Notifications
  [x] Sound          (existing — master override)
  [x] Vibration      (existing — master override)
  >   Alert settings...    ← new, navigates to NotificationSettingsScreen
```

### File

`mobile/lib/screens/notification_settings_screen.dart`

### Layout

```
AppBar: "Alert Settings"

Description:
"Notifications always appear in your notification shade.
 These toggles control whether they also buzz and play sound."

─── Action required ──────────────────────────────
These notifications block Claude until you respond.

  Permission requests                    [toggle ON]
  Claude is asking to use a tool that requires approval.

  Questions                              [toggle ON]
  Claude needs your input to continue.

  Elicitation — form input               [toggle ON]
  An MCP server is requesting structured input from you.

  Elicitation — authentication           [toggle ON]
  An MCP server requires you to authenticate via a URL.

─── Informational ────────────────────────────────
These notifications do not block Claude.

  Session completed                      [toggle ON]
  Claude finished a task.

  Session error                          [toggle ON]
  Claude stopped due to an error.

─────────────────────────────────────────────────
  [Reset to defaults]
```

### Per-row widget

Standard `SwitchListTile`:
- `title`: human-readable label
- `subtitle`: one-line description
- `value`: `isAlertEnabled(type)`
- `onChanged`: `setAlertEnabled(type, value)`
- Blocking types (Action required category) show a `warning_amber` icon in the subtitle when toggled off: `"Alert off — Claude may wait indefinitely for your response"`

### Interaction rules

| User action | Result |
|---|---|
| Toggle alert OFF on blocking type | Toggle turns off; warning note appears in subtitle |
| Toggle alert OFF on informational type | Toggle turns off silently |
| Toggle alert ON (any type) | Alert enabled, no side effects |
| Reset to defaults | All types restored to default map |

---

## Settings Screen Changes (settings_screen.dart)

```dart
// Notifications section — add after existing sound/vibration tiles:
ListTile(
  leading: const Icon(Icons.notifications_outlined),
  title: const Text('Alert settings'),
  subtitle: const Text('Choose which notifications buzz and play sound'),
  trailing: const Icon(Icons.chevron_right, size: 20),
  onTap: () => Navigator.of(context).push(
    MaterialPageRoute(builder: (_) => const NotificationSettingsScreen()),
  ),
),
```

---

## Files to Create / Modify

| File | Change |
|---|---|
| `mobile/lib/screens/notification_settings_screen.dart` | **Create** — new dedicated screen |
| `mobile/lib/services/notification_service.dart` | **Modify** — add `alertTypes` map, `isAlertEnabled`, `setAlertEnabled`, load/save from SharedPreferences |
| `mobile/lib/screens/home_screen.dart` | **Modify** — dispatch always calls show, derives `silent` flag from `isAlertEnabled` |
| `mobile/lib/screens/settings_screen.dart` | **Modify** — add nav tile to new screen under Notifications section |

---

## Out of Scope

- Host-side filtering — host always delivers all events
- Hiding notifications from the shade entirely
- Per-session alert overrides (mute a specific session)
- Notification history / read/unread state
- Web push equivalent (separate concern, different delivery path)
