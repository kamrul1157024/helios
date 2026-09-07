import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:http/http.dart' as http;

import '../models/host_connection.dart';
import '../providers/card_registry.dart' as registry;
import 'api_client.dart';
import 'notification_service.dart';

/// How often the daemon is asked to prove the stream is alive while the app is
/// closed.
///
/// Every beat wakes the phone's radio, and on cellular the 30s the app uses
/// costs a few percent of a battery a day for a stream that is silent almost
/// all of it. Nobody is watching a background service, so noticing a dead
/// socket four minutes late costs nothing. Kept under the five minutes most
/// NATs hold an idle mapping for.
const backgroundHeartbeat = Duration(seconds: 240);

/// How long the background stream may stay silent before it is presumed dead.
const backgroundSilenceThreshold = Duration(seconds: 600);

/// How often [BackgroundWatcher.onRepeatEvent] runs.
const backgroundWatchdogInterval = Duration(minutes: 2);

const _notificationChannelId = 'helios_watch_v1';

/// The entry point Android calls on the service isolate.
@pragma('vm:entry-point')
void startBackgroundWatcher() {
  FlutterForegroundTask.setTaskHandler(BackgroundWatcher());
}

/// Configure the service. Safe to call more than once.
void initBackgroundWatcher() {
  FlutterForegroundTask.init(
    androidNotificationOptions: AndroidNotificationOptions(
      channelId: _notificationChannelId,
      channelName: 'Watching for approvals',
      channelDescription:
          'Shown while Helios is holding a connection open to your daemon.',
      channelImportance: NotificationChannelImportance.LOW,
      priority: NotificationPriority.LOW,
      onlyAlertOnce: true,
    ),
    iosNotificationOptions: const IOSNotificationOptions(),
    foregroundTaskOptions: ForegroundTaskOptions(
      eventAction: ForegroundTaskEventAction.repeat(
        backgroundWatchdogInterval.inMilliseconds,
      ),
      autoRunOnBoot: true,
      autoRunOnMyPackageReplaced: true,
      // A partial wake lock would pin the CPU out of deep sleep for as long as
      // the service runs, and it buys nothing: an inbound packet raises a
      // network interrupt that wakes the CPU by itself.
      allowWakeLock: false,
      allowWifiLock: true,
    ),
  );
}

/// Watches every paired host while the app is closed.
///
/// Runs on its own Flutter isolate, so it shares no memory with the app: the
/// host list comes back out of secure storage, the clients are rebuilt, and
/// what it has already put in the tray is read from the file
/// [NotificationService] mirrors its posted set into. The app and this handler
/// never hold a stream at the same time — [HostManager] starts the service on
/// pause and stops it on resume.
class BackgroundWatcher extends TaskHandler {
  final _storage = const FlutterSecureStorage();
  final Map<String, _HostStream> _streams = {};

  @override
  Future<void> onStart(DateTime timestamp, TaskStarter starter) async {
    await NotificationService.instance.init();
    await NotificationService.instance.reloadPosted();
    NotificationService.instance.onAction = _handleAction;

    final hosts = await _loadHosts();
    debugPrint(
      '[watcher] started by ${starter.name} with ${hosts.length} host(s)',
    );
    for (final host in hosts) {
      _streams[host.host.id] = host;
      unawaited(_seed(host));
      unawaited(_connect(host));
    }

    FlutterForegroundTask.updateService(
      notificationTitle: 'Helios',
      notificationText: _statusLine(),
    );
  }

  @override
  void onRepeatEvent(DateTime timestamp) {
    for (final stream in _streams.values) {
      if (!stream.isStale) continue;
      debugPrint('[watcher] ${stream.host.label} silent — reconnecting');
      unawaited(_connect(stream));
    }
    FlutterForegroundTask.updateService(notificationText: _statusLine());
  }

  @override
  Future<void> onDestroy(DateTime timestamp, bool isTimeout) async {
    for (final stream in _streams.values) {
      stream.close();
    }
    _streams.clear();
  }

  @override
  void onNotificationPressed() => FlutterForegroundTask.launchApp();

  String _statusLine() {
    final live = _streams.values.where((s) => s.connected).length;
    if (_streams.isEmpty) return 'No hosts paired';
    if (live == 0) return 'Reconnecting…';
    if (_streams.length == 1) return 'Watching ${_streams.values.first.label}';
    return 'Watching $live of ${_streams.length} hosts';
  }

  // ==================== Hosts ====================

  /// Rebuild the host list from the same storage the app writes.
  ///
  /// Nothing is shared with the UI isolate, so the format here has to track
  /// `HostManager._saveHosts` by hand. Change one, change the other.
  Future<List<_HostStream>> _loadHosts() async {
    final out = <_HostStream>[];
    try {
      final raw = await _storage.read(key: 'helios_hosts');
      if (raw == null) return out;
      final list = jsonDecode(raw) as List;
      final multi = list.length > 1;
      for (final entry in list) {
        final host = HostConnection.fromJson(entry as Map<String, dynamic>);
        final seedB64 = await _storage.read(key: 'helios_host_${host.id}_key');
        if (seedB64 == null) continue;
        final padded = seedB64.padRight((seedB64.length + 3) & ~3, '=');
        out.add(
          _HostStream(
            host: host,
            showHostLabel: multi,
            api: ApiClient(
              serverUrl: host.serverUrl,
              deviceId: host.deviceId,
              privateKeySeed: base64Url.decode(padded),
            ),
          ),
        );
      }
    } catch (e) {
      debugPrint('[watcher] could not load hosts: $e');
    }
    return out;
  }

  // ==================== Delivery ====================

  /// Post whatever is pending right now and retract whatever is not.
  ///
  /// The app stops its stream before this one opens, and the daemon keeps no
  /// replay buffer, so anything raised in that gap arrives nowhere unless it is
  /// fetched. The same pass clears notifications answered elsewhere while the
  /// handover was in flight.
  Future<void> _seed(_HostStream stream) async {
    try {
      final resp = await stream.api.get('/api/notifications');
      if (resp.statusCode != 200) return;
      final body = jsonDecode(resp.body) as Map<String, dynamic>;
      final list = (body['notifications'] as List?) ?? const [];

      final pending = <String>{};
      for (final entry in list) {
        final n = entry as Map<String, dynamic>;
        if (n['status']?.toString() != 'pending') continue;
        final id = n['id']?.toString() ?? '';
        if (id.isEmpty) continue;
        pending.add(id);
        _raise(stream, n);
      }
      await NotificationService.instance.retainOnly(stream.host.id, pending);
      debugPrint('[watcher] ${stream.label} seeded, ${pending.length} pending');
    } catch (e) {
      debugPrint('[watcher] seed failed for ${stream.label}: $e');
    }
  }

  void _handleEvent(_HostStream stream, String type, dynamic data) {
    if (data is! Map) return;

    if (type == 'notification_resolved') {
      final id = data['id']?.toString();
      if (id == null || id.isEmpty) return;
      NotificationService.instance.cancel(
        NotificationService.notifKey(stream.host.id, id),
      );
      return;
    }

    if (type != 'notification') return;
    _raise(stream, data);
  }

  void _raise(_HostStream stream, Map<dynamic, dynamic> data) {
    final type = data['type']?.toString() ?? '';
    final id = data['id']?.toString() ?? '';
    if (id.isEmpty) return;

    final key = NotificationService.notifKey(stream.host.id, id);
    final notifSvc = NotificationService.instance;
    final raise = registry.shouldRaiseNotification(
      type: type,
      status: data['status']?.toString(),
      alreadyPosted: notifSvc.isPosted(key),
    );
    if (!raise) return;

    final prefix = stream.showHostLabel ? '${stream.label} — ' : '';
    final payload = jsonEncode({
      'hostId': stream.host.id,
      'notificationId': id,
    });
    final silent = !notifSvc.isAlertEnabled(type);
    final kind = registry.kindOfType(type);
    final title = data['title']?.toString();
    final detail = data['detail']?.toString();

    if (kind == 'permission') {
      notifSvc.showPermissionNotification(
        id: payload,
        key: key,
        toolName: '$prefix${title ?? 'Unknown tool'}',
        detail: detail ?? 'Permission requested',
        silent: silent,
      );
      return;
    }

    notifSvc.showNotification(
      id: payload,
      key: key,
      title: prefix + (title ?? registry.labelForKind(kind)),
      body: detail ?? registry.bodyForKind(kind),
      silent: silent,
    );
  }

  /// Answer an approval from the tray while the app is closed.
  ///
  /// The app normally owns this, but it may not be running at all, and an
  /// Approve button that does nothing is worse than no button.
  void _handleAction(String rawPayload, String action) {
    if (action != 'approve' && action != 'deny') return;
    try {
      final payload = jsonDecode(rawPayload) as Map<String, dynamic>;
      final hostId = payload['hostId'] as String?;
      final notificationId = payload['notificationId'] as String?;
      if (hostId == null || notificationId == null) return;

      final stream = _streams[hostId];
      if (stream == null) return;

      unawaited(
        stream.api.post(
          '/api/notifications/$notificationId/action',
          body: {'action': action},
        ),
      );
      NotificationService.instance.cancel(
        NotificationService.notifKey(hostId, notificationId),
      );
    } catch (e) {
      debugPrint('[watcher] action failed: $e');
    }
  }

  // ==================== SSE ====================

  Future<void> _connect(_HostStream stream) async {
    final generation = ++stream.generation;
    stream.client?.close();
    final client = http.Client();
    stream.client = client;

    try {
      final uri = Uri.parse(
        '${stream.host.serverUrl}/api/events'
        '?heartbeat=${backgroundHeartbeat.inSeconds}',
      );
      final request = http.Request('GET', uri);
      request.headers.addAll({
        'Authorization': 'Bearer ${await stream.api.getToken()}',
        'Accept': 'text/event-stream',
        'Cache-Control': 'no-cache',
      });

      final response = await client.send(request);
      if (generation != stream.generation) return;

      if (response.statusCode == 401) {
        stream.api.invalidateToken();
        return;
      }
      if (response.statusCode != 200) {
        debugPrint('[watcher] ${stream.label}: HTTP ${response.statusCode}');
        return;
      }

      stream.connected = true;
      stream.lastBytesAt = DateTime.now();
      debugPrint('[watcher] ${stream.label} connected');

      String buffer = '';
      String currentEvent = '';

      await for (final chunk in response.stream.transform(utf8.decoder)) {
        if (generation != stream.generation) return;
        stream.lastBytesAt = DateTime.now();

        buffer += chunk;
        final lines = buffer.split('\n');
        buffer = lines.removeLast();

        for (final line in lines) {
          if (line.startsWith('event: ')) {
            currentEvent = line.substring(7).trim();
          } else if (line.startsWith('data: ') && currentEvent.isNotEmpty) {
            try {
              _handleEvent(stream, currentEvent, jsonDecode(line.substring(6)));
            } catch (_) {}
            currentEvent = '';
          }
        }
      }
    } catch (e) {
      if (generation != stream.generation) return;
      debugPrint('[watcher] ${stream.label} stream error: $e');
    }

    if (generation != stream.generation) return;
    stream.connected = false;
    // No backoff timer: the watchdog is already ticking, and a timer is one
    // more thing to cancel on destroy for no gain at this cadence.
  }
}

/// One host's connection state inside the service isolate.
class _HostStream {
  _HostStream({
    required this.host,
    required this.api,
    required this.showHostLabel,
  });

  final HostConnection host;
  final ApiClient api;

  /// Whether the notification text should name the host. Off for a single
  /// host, where the name adds nothing.
  final bool showHostLabel;

  http.Client? client;
  bool connected = false;
  DateTime? lastBytesAt;
  int generation = 0;

  String get label => host.label;

  bool get isStale {
    if (!connected) return true;
    final at = lastBytesAt;
    if (at == null) return false;
    return DateTime.now().difference(at) > backgroundSilenceThreshold;
  }

  void close() {
    generation++;
    connected = false;
    client?.close();
    client = null;
    api.close();
  }
}
