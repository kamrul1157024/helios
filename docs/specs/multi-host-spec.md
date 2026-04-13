# Multi-Host Support — Technical Specification

## Problem

Currently a single mobile device can connect to only one helios daemon (host). Users running helios on multiple machines (e.g. work laptop + home server) must disconnect and re-pair each time they switch. Notifications only arrive from the connected host.

## Goal

Allow a single mobile device to be paired with multiple helios hosts simultaneously. All hosts maintain live connections. The UI provides a host filter to scope what's visible. Tapping an OS notification routes to the correct host.

---

## Architecture Overview

```
┌──────────────────────────────────────────┐
│                Mobile App                │
│                                          │
│  ┌──────────────────────────────────┐    │
│  │          HostManager             │    │
│  │  List<HostConnection> hosts      │    │
│  │  String? activeHostId (filter)   │    │
│  │  Map<hostId, DaemonAPIService>   │    │
│  └────┬───────────┬────────────┬────┘    │
│       │           │            │         │
│  ┌────▼──┐   ┌────▼──┐   ┌────▼──┐      │
│  │ SSE+  │   │ SSE+  │   │ SSE+  │      │
│  │ Poll  │   │ SSE   │   │ SSE   │      │
│  │(active)│  │(bg)   │   │(bg)   │      │
│  └───┬───┘   └───┬───┘   └───┬───┘      │
└──────┼───────────┼───────────┼───────────┘
       │           │           │
  ┌────▼──┐   ┌────▼──┐   ┌────▼──┐
  │Host A │   │Host B │   │Host C │
  │daemon │   │daemon │   │daemon │
  └───────┘   └───────┘   └───────┘
```

**Active host**: full real-time experience (SSE + 3s session polling).
**Background hosts**: SSE only (for real-time notification delivery). No session polling. Sessions fetched on-demand when user switches filter.

---

## Data Model

### HostConnection (new model)

File: `mobile/lib/models/host_connection.dart`

```dart
class HostConnection {
  final String id;           // UUID, stable across app restarts
  String label;              // user-editable, e.g. "Work MacBook"
  final String serverUrl;    // tunnel URL
  final String deviceId;     // device KID registered on this host
  String? cookie;            // JWT cookie for auth
  String? privateKeySeed;    // base64url Ed25519 seed (stored in secure storage)
  String? hostname;          // fetched from /api/health, optional
  final int colorIndex;      // index into hostColors palette (0-7)
  final DateTime addedAt;

  Color get color => hostColors[colorIndex % hostColors.length];
}
```

### Host Color Palette

Each host is assigned a color from a fixed palette on creation. The `colorIndex` is persisted and can be changed by the user in host detail settings.

```dart
static const hostColors = [
  Color(0xFF4285F4),  // Blue
  Color(0xFF34A853),  // Green
  Color(0xFFFBBC04),  // Amber
  Color(0xFFEA4335),  // Red
  Color(0xFF9C27B0),  // Purple
  Color(0xFF00ACC1),  // Cyan
  Color(0xFFFF7043),  // Deep Orange
  Color(0xFF5C6BC0),  // Indigo
];
```

Assignment: first host gets index 0 (blue), second gets 1 (green), etc. On host removal, indices don't shift — the index is fixed to the host, not its position. If all 8 are used, it wraps (modulo).

### Storage Layout

All credentials move into per-host entries in secure storage:

| Key | Value |
|-----|-------|
| `helios_hosts` | JSON array of host metadata (id, label, serverUrl, deviceId, addedAt) |
| `helios_host_{id}_key` | Ed25519 private key seed (base64url) |
| `helios_host_{id}_cookie` | JWT cookie string |

Active host ID stored in SharedPreferences:

| Key | Value |
|-----|-------|
| `helios_active_host_id` | UUID string or null (null = "All") |

### Migration from Single-Host Storage

On first launch after upgrade, detect old keys and migrate:

```
if secureStorage.has('helios_private_key'):
  host = HostConnection(
    id: uuid(),
    label: "My Host",  // or hostname from /api/health
    serverUrl: prefs.get('helios_server_url'),
    deviceId: secureStorage.get('helios_device_id'),
    addedAt: now,
  )
  // Move credentials to new keys
  secureStorage.write('helios_host_{id}_key', secureStorage.get('helios_private_key'))
  secureStorage.write('helios_host_{id}_cookie', secureStorage.get('helios_cookie'))
  // Save host list
  secureStorage.write('helios_hosts', jsonEncode([host]))
  prefs.set('helios_active_host_id', host.id)
  // Delete old keys
  secureStorage.delete('helios_private_key')
  secureStorage.delete('helios_cookie')
  prefs.remove('helios_server_url')
```

---

## Service Layer Changes

### HostManager (replaces AuthService)

File: `mobile/lib/services/host_manager.dart`

Replaces `AuthService` as the central auth + host management service.

```dart
class HostManager extends ChangeNotifier {
  List<HostConnection> _hosts = [];
  String? _activeHostId;               // null = "All hosts" filter
  Map<String, DaemonAPIService> _services = {};

  // --- Host lifecycle ---
  Future<void> loadStoredHosts();       // load all hosts, start services
  Future<SetupResult> addHost(String pairingToken, String serverUrl);
  Future<void> removeHost(String hostId);
  Future<void> setActiveHost(String? hostId);  // null = all

  // --- Accessors ---
  List<HostConnection> get hosts;
  String? get activeHostId;
  HostConnection? get activeHost;
  DaemonAPIService? serviceFor(String hostId);

  // --- Auth helpers (scoped to a specific host) ---
  Future<Response> authGet(String hostId, String path);
  Future<Response> authPost(String hostId, String path, {Map<String, dynamic>? body});
  Future<Response> authPatch(String hostId, String path, {Map<String, dynamic>? body});
  Future<Response> authDelete(String hostId, String path);

  // --- Aggregated data ---
  bool get isAuthenticated => _hosts.isNotEmpty;
  bool get hasAnyConnection => _services.values.any((s) => s.connected);
}
```

#### loadStoredHosts()

1. Read `helios_hosts` from secure storage.
2. Run single-host migration if old keys exist.
3. For each host: load credentials, create `DaemonAPIService`, call `start()`.
4. Mark the active host's service for full polling; others for SSE-only.
5. Check device status on each host (`/api/auth/device/me`). Remove hosts where device is revoked.

#### addHost()

1. Generate Ed25519 keypair.
2. Generate device ID (UUID).
3. POST `/api/auth/pair` on the new server.
4. POST `/api/auth/login`, extract cookie.
5. Store credentials under `helios_host_{id}_*` keys.
6. Append to `helios_hosts` list.
7. Create `DaemonAPIService` for the host, `start()`.
8. Wait for approval (poll `/api/auth/device/me`).
9. On approval: set as active host, notify listeners.

#### setActiveHost()

1. If previous active host exists: demote its service to background mode (stop polling).
2. Set `_activeHostId`.
3. If new active host is not null: promote its service to active mode (start polling, fetch sessions immediately).
4. If null ("All"): promote all services? No — keep current approach: only one polls. When "All" is selected, whichever host was last active keeps polling. Or: when "All", poll none and rely on SSE for updates, fetch sessions on pull-to-refresh only.
5. `notifyListeners()` — UI rebuilds with new filter.

**Decision: "All" filter behavior**
When filter = "All", no host gets active polling. All hosts are SSE-only. The session lists shown are whatever was last fetched. Pull-to-refresh triggers `fetchSessions()` on all hosts. This avoids multiplying poll traffic and keeps battery impact minimal.

### DaemonAPIService Changes

The existing `DaemonAPIService` stays mostly unchanged — it's already a self-contained unit. Changes:

1. **Add `hostId` field**: set at construction time, used to tag notifications/sessions.
2. **Add background mode**: new `startBackground()` method that connects SSE but skips session polling.
3. **Replace `attach(AuthService)`**: takes auth headers/URL directly instead of an AuthService reference, since auth is now per-host inside HostManager.
4. **Expose `onNotificationEvent` callback**: so HostManager can route SSE notification events to the OS notification service with host context.

```dart
class DaemonAPIService extends ChangeNotifier {
  final String hostId;
  final String serverUrl;
  String? _cookie;
  bool _isActiveHost = false;

  // Existing fields: _sessions, _notifications, _connected, etc.

  DaemonAPIService({required this.hostId, required this.serverUrl, String? cookie})
    : _cookie = cookie;

  void setCookie(String cookie) { _cookie = cookie; }

  // Full mode: SSE + session polling
  Future<void> startActive() async {
    _isActiveHost = true;
    _startPolling();
    await _connect();
  }

  // Background mode: SSE only, no polling
  Future<void> startBackground() async {
    _isActiveHost = false;
    _pollTimer?.cancel();
    await _connect();
  }

  // Promote from background to active
  void promote() {
    _isActiveHost = true;
    fetchSessions();
    fetchNotifications();
    _startPolling();
  }

  // Demote from active to background
  void demote() {
    _isActiveHost = false;
    _pollTimer?.cancel();
  }
}
```

---

## Notification Routing

### OS Notification Payload

Change payload from plain notification ID to JSON with host context:

```dart
// Before:
payload: notificationId

// After:
payload: jsonEncode({'hostId': hostId, 'notificationId': notificationId})
```

### Firing OS Notifications

In `HomeScreen._handleSSEEvent`, the event now comes tagged with a host ID. The notification title includes the host label when multiple hosts are paired:

```dart
void _handleSSENotification(String hostId, Map data) {
  final hostLabel = _hostManager.hosts.length > 1
      ? _hostManager.hostById(hostId)?.label ?? ''
      : '';
  final prefix = hostLabel.isNotEmpty ? '$hostLabel — ' : '';

  if (type == 'claude.permission') {
    NotificationService.instance.showPermissionNotification(
      id: jsonEncode({'hostId': hostId, 'notificationId': id}),
      toolName: '${prefix}${data['title']}',
      detail: data['detail'],
    );
  }
}
```

### Tapping OS Notification

```dart
void _onNotificationResponse(NotificationResponse response) {
  final payload = jsonDecode(response.payload!);
  final hostId = payload['hostId'];
  final notificationId = payload['notificationId'];

  // Route action to correct host's service
  final service = _hostManager.serviceFor(hostId);
  if (service == null) return;

  if (response.actionId == 'approve' || response.actionId == 'deny') {
    service.sendAction(notificationId, {'action': response.actionId});
  }

  // Switch UI filter to this host
  _hostManager.setActiveHost(hostId);
}
```

---

## UI Changes

### Visual Identity: Host Color Bar

Every session card and notification card gets a **4px colored bar on the left edge**, using the host's assigned color. This is the primary visual indicator of which host an item belongs to — no text labels needed.

```
┌────────────────────────────────────────┐
█ ● Active   claude-opus-4      2s ago  │
█ "refactor the auth module into..."    │
█ .../workspace/helios                  │
█ a1b2c3d4                              │
└────────────────────────────────────────┘
┌────────────────────────────────────────┐
█ ● Idle     claude-sonnet-4    5m ago  │
█ "the tests pass now"                  │
█ .../workspace/other-project           │
█ e5f6g7h8                              │
└────────────────────────────────────────┘

█ = #4285F4 blue    (Work MacBook)
█ = #34A853 green   (Home Server)
```

The bar is always visible regardless of filter — when filtered to a single host, all bars are the same color, which subtly reinforces context.

#### Implementation (Card Widget)

Wrap the existing card content in a `Row` with a leading colored container:

```dart
Card(
  clipBehavior: Clip.antiAlias,  // needed for bar to respect border radius
  child: IntrinsicHeight(
    child: Row(
      children: [
        Container(width: 4, color: hostColor),
        Expanded(child: existingCardContent),
      ],
    ),
  ),
)
```

### AppBar (home_screen.dart)

Replace static "helios" title with host filter chip + per-host colored connection dots:

```
┌─────────────────────────────────────────────┐
│  [All Hosts ▾]            ●●○          [⋮]  │
└─────────────────────────────────────────────┘

│  [Work MacBook ▾]         ●●○          [⋮]  │

│  [helios]                 ●            [⋮]  │  ← single host, no dropdown
```

**Host filter chip**: tappable, shows current filter label. Chevron indicates dropdown. When only one host is paired, show static "helios" text (no dropdown).

**Connection dots**: one dot per host, colored with the **host's assigned color** (not generic green/grey). Dot opacity indicates status:
- Full opacity = connected
- 30% opacity = disconnected/offline

This way the dots serve dual purpose: connection status AND host identification.

```dart
// Connection dots
Row(
  children: hosts.map((h) => Padding(
    padding: EdgeInsets.only(right: 4),
    child: Icon(
      Icons.circle,
      size: 10,
      color: h.color.withOpacity(
        serviceFor(h.id)?.connected == true ? 1.0 : 0.3,
      ),
    ),
  )).toList(),
)
```

### Host Selector Bottom Sheet

Opened by tapping the filter chip. Each host row shows its assigned color dot.

```
┌─────────────────────────────────────────┐
│  Select Host                            │
│                                         │
│  ◉ All Hosts                      (3/3) │
│  ─────────────────────────────────────  │
│  ● Work MacBook            ● connected  │
│    https://abc.trycloudflare.com        │
│                                         │
│  ● Home Server             ● connected  │
│    https://xyz.trycloudflare.com        │
│                                         │
│  ● Office Desktop          ○ offline    │
│    https://def.trycloudflare.com        │
│                                         │
│  ─────────────────────────────────────  │
│  + Add new host                         │
└─────────────────────────────────────────┘

● = host color dot
● / ○ = connection status (colored/dimmed using host color)
```

The currently selected filter has a radio-button style indicator (◉). Tapping a row selects it and closes the sheet.

### Session Card (sessions_screen.dart) — Redesigned Layout

Information priority (highest to lowest):
1. **User prompt** — what is this session doing
2. **Workspace** — where is it running
3. **Time** — when did it last update
4. **Status** — is it active/idle/ended
5. **Model** — which model

Current layout leads with the status badge row, burying the most important info (the prompt). Redesign puts the prompt first.

```
┌────────────────────────────────────────────────┐
█                                                │
█  ● Active  📌                        2s ago    │  ← status + pin + time (glance)
█  refactor the auth module into separate...     │  ← prompt (primary content)
█  ~/workspace/helios                            │  ← workspace
█  claude-opus-4                  Work MacBook    │  ← model + host
█                                                │
└────────────────────────────────────────────────┘

█ = 4px host color bar (left edge, full height)
"Work MacBook" rendered in host color (right-aligned)
```

**Row 1 — Status + time** (instant glance, top corners):
- Left: status icon + colored status badge + pin icon
- Right: time ago
- This is the "is it running / how recent" row — eyes land here first

**Row 2 — Prompt** (primary content):
- `lastUserMessage` in semi-bold, 14px
- Max 2 lines, ellipsis overflow
- If no user message, fall back to `shortCwd` as the title

**Row 3 — Workspace** (context):
- Workspace path (monospace, 12px, dimmed)

**Row 4 — Model + host** (lowest priority):
- Left: model name (11px, dimmed)
- Right: host label in host color (11px, semi-bold)

The host label is always present — color bar is the fast visual scan, text is the explicit identifier.

Compared to current layout:
- Status + time: **same position** (top corners, unchanged)
- Prompt: **promoted** — no longer competes with model on the status row
- Model: **demoted** from row 1 to row 4 — it was cluttering the status row
- Host: **new**, lives with model at the bottom
- Last event + session ID: **removed** — low value, saves vertical space

**Active session highlight**: active sessions still get a subtle colored left border (from status color) in addition to the host color bar. The host bar is outermost (4px), the status border is applied to the card itself (1.5px).

```dart
Card(
  clipBehavior: Clip.antiAlias,
  shape: RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(12),
    side: session.isActive
        ? BorderSide(color: statusColor.withOpacity(0.4), width: 1.5)
        : BorderSide.none,
  ),
  child: IntrinsicHeight(
    child: Row(
      children: [
        Container(width: 4, color: hostColor),
        Expanded(
          child: Padding(
            padding: EdgeInsets.all(12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                // Row 1: Status + pin + time
                Row(
                  children: [
                    Icon(statusIcon, size: 14, color: statusColor),
                    SizedBox(width: 6),
                    StatusBadge(label, color: statusColor),
                    if (session.pinned) ...[
                      SizedBox(width: 6),
                      Icon(Icons.push_pin, size: 14, color: theme.colorScheme.primary),
                    ],
                    Spacer(),
                    Text(session.timeAgo, style: dimmedStyle),
                  ],
                ),
                SizedBox(height: 8),
                // Row 2: Prompt
                Text(
                  session.lastUserMessage ?? session.shortCwd,
                  style: TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                    color: theme.colorScheme.onSurface,
                  ),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
                SizedBox(height: 6),
                // Row 3: Workspace
                Text(
                  session.shortCwd,
                  style: TextStyle(
                    fontSize: 12,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
                SizedBox(height: 4),
                // Row 4: Model + host name
                Row(
                  children: [
                    if (session.model != null)
                      Text(
                        session.model!,
                        style: TextStyle(
                          fontSize: 11,
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    Spacer(),
                    Text(
                      hostLabel,
                      style: TextStyle(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                        color: hostColor,
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ],
    ),
  ),
)
```

Structure (left to right):
1. **Host color bar** (4px, full card height, host assigned color)
2. **Card content** (redesigned 3-row layout)

No text host label needed — the color bar communicates host identity.

### Notification Card — Pending (dashboard_screen.dart)

Priority: what tool/action, detail, workspace context, actions.

```
┌────────────────────────────────────────────────┐
█                                                │
█  Bash                                      [x] │
█  rm -rf /tmp/build                             │
█  ~/workspace/helios                  just now  │
█  [  Approve  ]  [  Deny  ]  ☐  Work MacBook   │
█                                                │
└────────────────────────────────────────────────┘

█ = host color bar (4px, full opacity)
"Work MacBook" rendered in host color (right-aligned)
```

**Row 1**: tool/type name (bold) + dismiss button
**Row 2**: detail/command (monospace, 13px)
**Row 3**: workspace + time (dimmed, 11px)
**Row 4**: action buttons + selection checkbox + host label (right-aligned, host color)

### Notification Card — Status (active, non-actionable)

```
┌────────────────────────────────────────────────┐
█  Session Active                            [x] │
█  Claude is working on refactoring...           │
█  ~/workspace/helios         5s ago  Home Srvr  │
└────────────────────────────────────────────────┘

█ = host color bar
"Home Srvr" in host color
```

### Notification Card — History (resolved)

```
┌────────────────────────────────────────────────┐
█  approved   Bash                  Work MacBook │
█  rm -rf /tmp  2m ago  via mobile               │
└────────────────────────────────────────────────┘

█ = host color bar (30% opacity for resolved items)
"Work MacBook" in host color (also dimmed)
```

Resolved cards: host bar at 30% opacity to visually de-emphasize. Entire card content also at 70% opacity (existing behavior). Host label follows the same opacity.

### Sessions Screen Data Source

Currently reads from `Consumer<DaemonAPIService>`. Changes to read from `HostManager`:

```dart
Consumer<HostManager>(
  builder: (context, hm, _) {
    final sessions = hm.activeHostId == null
        ? hm.allSessions    // merged from all services
        : hm.activeService?.sessions ?? [];
    ...
  },
)
```

### Notification (Dashboard) Screen Data Source

Same pattern — read from HostManager with host filter applied.

### Session/Notification Actions

Actions (approve, deny, send prompt, stop, delete, etc.) must route to the correct host's `DaemonAPIService`. Since sessions and notifications carry a `hostId`:

```dart
// Before:
sse.sendAction(notification.id, body);

// After:
final service = hostManager.serviceFor(notification.hostId);
service?.sendAction(notification.id, body);
```

The `hostId` is set on `Session` and `HeliosNotification` models when the service fetches them.

### Composite Widget Keys

With multiple hosts, session IDs could theoretically collide (both are Claude-generated UUIDs). Use composite keys for `Dismissible`, selection sets, and any map lookups:

```dart
// Dismissible key
key: ValueKey('${session.hostId}:${session.sessionId}')

// Selection sets
final Set<String> _selected = {};  // stores "hostId:sessionId"
```

### New Session Sheet (new_session_sheet.dart)

When creating a new session, add a host selector at the top of the sheet. Shows each host with its color dot. Defaults to the active filter host, or the first connected host if filter = "All".

```
┌─────────────────────────────────────────┐
│  New Session                            │
│                                         │
│  Host                                   │
│  ┌─────────────────────────────────┐    │
│  │ ● Work MacBook              ▾   │    │
│  └─────────────────────────────────┘    │
│                                         │
│  Provider                               │
│  ┌─────────────────────────────────┐    │
│  │ Claude                      ▾   │    │
│  └─────────────────────────────────┘    │
│  ...                                    │
└─────────────────────────────────────────┘
```

Only connected hosts are selectable. Disconnected hosts are shown greyed out.

### Settings Screen (settings_screen.dart)

Add "Hosts" section with color dots:

```
HOSTS
┌─────────────────────────────────────────┐
│  ● Work MacBook            ● connected  │ >
│    https://abc.trycloudflare.com        │
├─────────────────────────────────────────┤
│  ● Home Server             ○ offline    │ >
│    https://xyz.trycloudflare.com        │
└─────────────────────────────────────────┘
+ Add new host

● = host color dot (matches the bar color used in cards)

NOTIFICATIONS
Sound                              [====]
Vibration                          [====]
```

### Host Detail Screen (host_detail_screen.dart) — NEW

Opened from settings by tapping a host row.

```
┌─────────────────────────────────────────┐
│  ← Work MacBook                         │
├─────────────────────────────────────────┤
│                                         │
│  Color                                  │
│  ● ● ● ● ● ● ● ●                      │
│  (tappable palette, selected has ring)  │
│                                         │
│  Label                                  │
│  ┌─────────────────────────────────┐    │
│  │ Work MacBook                    │    │
│  └─────────────────────────────────┘    │
│                                         │
│  Server URL                             │
│  https://abc.trycloudflare.com          │
│                                         │
│  Device ID                              │
│  a1b2c3d4-e5f6-...                      │
│                                         │
│  Status          ● Connected            │
│  Paired          2 days ago             │
│                                         │
│  [Disconnect & Remove]                  │
│                                         │
└─────────────────────────────────────────┘
```

Color picker: row of 8 circles from the palette. Tapping changes the host color and immediately updates all cards in the session/notification lists.

### Setup Screen (setup_screen.dart)

After successful pairing, navigate back to HomeScreen (don't replace it). The new host appears in the host list and auto-selects as active filter.

If no hosts exist (fresh install), the setup screen is shown as today.

### Logout / Disconnect

- Per-host "Disconnect & Remove" in settings replaces global logout.
- The overflow menu "Disconnect" is removed. Replace with "Manage Hosts" which navigates to settings.
- Removing the last host returns to the setup screen.

---

## Sorting

### Sessions (all hosts merged)

```
1. Pinned, active     — by lastEventAt DESC
2. Pinned, non-active — by lastEventAt DESC
3. Active             — by lastEventAt DESC
4. Idle               — by lastEventAt DESC
5. Ended              — by endedAt DESC
```

Host does NOT factor into sort. Activity/urgency beats host grouping. Archived sessions hidden by default (existing behavior).

### Notifications (all hosts merged)

```
1. Pending, needs action   — by createdAt DESC
2. Pending, status-only    — by createdAt DESC
3. Resolved                — by resolvedAt DESC
```

Host does NOT factor into sort. A pending permission from any host is more important than a resolved notification from the active host.

### Badge Counts

Bottom nav badges aggregate across all hosts regardless of filter:
- Sessions badge: count of active sessions across all hosts
- Notifications badge: count of pending-action notifications across all hosts

This ensures the user never misses an urgent item because they're filtered to a different host.

---

## App Lifecycle

### App Foreground

All hosts: SSE connected.
Active host: SSE + 3s session polling.
Background hosts: SSE only.

### App Background (paused)

All SSE connections dropped (OS will kill them anyway).
All polling stopped.
Rely on FCM push if available (future work).

### App Resume

1. Reconnect SSE on all hosts.
2. Fetch notifications from all hosts (single pass).
3. Fetch sessions from active host only.
4. Start polling on active host.

---

## Provider Tree Changes

```dart
// Before:
MultiProvider(
  providers: [
    ChangeNotifierProvider(create: (_) => AuthService()),
    ChangeNotifierProvider(create: (_) => DaemonAPIService()),
  ],
)

// After:
MultiProvider(
  providers: [
    ChangeNotifierProvider(create: (_) => HostManager()),
    // DaemonAPIService instances are owned by HostManager,
    // not provided separately. Accessed via hostManager.serviceFor(id).
  ],
)
```

Screens that currently do `context.read<DaemonAPIService>()` change to `context.read<HostManager>()` and get the service from there.

---

## Model Changes Summary

### Session — add hostId

```dart
class Session {
  final String hostId;  // NEW: which host this session belongs to
  // ... existing fields unchanged
}
```

Set by `DaemonAPIService` when parsing JSON responses (the service knows its own `hostId`).

### HeliosNotification — add hostId

```dart
class HeliosNotification {
  final String hostId;  // NEW: which host this notification belongs to
  // ... existing fields unchanged
}
```

Same approach — set by the service on parse.

---

## Go Backend Changes

**None.** Each helios daemon is independent. It doesn't know about other hosts. Multi-host logic is entirely client-side. The daemon's device table, sessions, notifications, and auth are all self-contained. A mobile device registers as a separate device on each host.

---

## File Change Summary

| File | Action | Description |
|------|--------|-------------|
| `models/host_connection.dart` | **NEW** | HostConnection model |
| `services/host_manager.dart` | **NEW** | Replaces AuthService, manages multiple hosts + services |
| `services/auth_service.dart` | **DELETE** | Absorbed into HostManager |
| `services/daemon_api_service.dart` | **MODIFY** | Add hostId, background mode, promote/demote, self-contained auth |
| `services/notification_service.dart` | **MODIFY** | JSON payload with hostId, host label in notification title |
| `models/session.dart` | **MODIFY** | Add hostId field |
| `models/notification.dart` | **MODIFY** | Add hostId field |
| `main.dart` | **MODIFY** | Replace AuthService with HostManager, remove DaemonAPIService provider |
| `screens/home_screen.dart` | **MODIFY** | Host selector, per-host connection dots, notification routing |
| `screens/sessions_screen.dart` | **MODIFY** | Read from HostManager, show host labels, route actions to correct service |
| `screens/dashboard_screen.dart` | **MODIFY** | Read from HostManager, show host labels, route actions to correct service |
| `screens/setup_screen.dart` | **MODIFY** | Call addHost() instead of setup(), return to home on success |
| `screens/settings_screen.dart` | **MODIFY** | Add hosts management section |
| `screens/host_detail_screen.dart` | **NEW** | Edit label, view details, disconnect |
| `screens/new_session_sheet.dart` | **MODIFY** | Add host selector dropdown |
| `providers/card_registry.dart` | **MODIFY** | Pass hostId through to card builders |
| `providers/claude/cards.dart` | **MODIFY** | Route actions via hostId to correct service |

---

## Implementation Order

1. **Models**: `HostConnection`, add `hostId` to `Session` and `HeliosNotification`
2. **DaemonAPIService**: add hostId, background mode, self-contained auth (decouple from AuthService)
3. **HostManager**: full implementation with migration logic
4. **Provider tree**: swap AuthService → HostManager in main.dart
5. **Home screen**: host selector, connection dots, notification routing
6. **Sessions screen**: read from HostManager, host labels
7. **Dashboard screen**: read from HostManager, host labels
8. **Setup screen**: addHost() flow
9. **Settings screen**: host management UI
10. **Notification service**: JSON payload, host label in title
11. **New session sheet**: host selector
12. **Card registry + card widgets**: route actions via hostId
