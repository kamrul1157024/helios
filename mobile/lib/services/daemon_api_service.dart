import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import '../models/host_connection.dart';
import '../models/notification.dart';
import '../models/provider.dart';
import '../models/session.dart';
import '../models/message.dart';
import 'api_client.dart';
import 'notification_service.dart';

/// Callback fired when an SSE event arrives on this host.
typedef SSEEventCallback = void Function(String hostId, SSEEvent event);

/// How long a connected stream may stay silent before it is presumed dead.
/// The daemon heartbeats every 30s, so this is two missed beats plus slack.
const streamSilenceThreshold = Duration(seconds: 75);

/// Whether a stream that still reports itself connected has gone silent for
/// long enough to be presumed dead.
///
/// A phone that sleeps through a network change is left holding a half-open
/// socket: the read never errors, so nothing else notices the stream is gone.
/// Silence measured against the daemon's heartbeat is the only signal there is.
bool isStreamStale(DateTime? lastBytesAt, DateTime now) {
  if (lastBytesAt == null) return false;
  return now.difference(lastBytesAt) > streamSilenceThreshold;
}

class DaemonAPIService extends ChangeNotifier {
  final String hostId;
  final String serverUrl;
  final ApiClient _api;

  http.Client? _client;
  Timer? _reconnectTimer;
  Timer? _pollTimer;
  Timer? _sessionDebounce;
  Timer? _notificationDebounce;
  Timer? _watchdogTimer;
  bool _running = false;
  bool _connected = false;
  bool _isActiveHost = false;
  int _consecutiveFailures = 0;
  static const _offlineThreshold = 2;
  static const _watchdogInterval = Duration(seconds: 30);

  /// When the stream last delivered bytes, heartbeats included.
  DateTime? _lastBytesAt;

  /// Bumped per connect attempt so a superseded one cannot clobber the live one.
  int _connectGeneration = 0;

  List<HeliosNotification> _notifications = [];
  List<HeliosNotification> get notifications => _notifications;
  List<Session> _sessions = [];
  List<Session> get sessions => _sessions;
  bool get connected => _connected;
  bool get isOffline => _consecutiveFailures >= _offlineThreshold;

  /// Connected in name only: the socket is open but the daemon has gone quiet
  /// past the heartbeat window.
  bool get stale => _connected && isStreamStale(_lastBytesAt, DateTime.now());

  bool _notificationsLoaded = false;
  bool get notificationsLoaded => _notificationsLoaded;
  bool _sessionsLoaded = false;
  bool get sessionsLoaded => _sessionsLoaded;

  // Track last fetch params so polling/SSE refreshes use the same filters
  String? _lastSessionQ;
  String? _lastSessionFilter;
  String? _lastSessionCwd;

  List<ProviderInfo> _providers = [];
  List<ProviderInfo> get providers => _providers;
  bool _providersLoaded = false;
  bool get providersLoaded => _providersLoaded;

  // Per-provider model cache: provider ID → models
  final Map<String, List<ModelInfo>> _modelCache = {};
  final Map<String, DateTime> _modelCacheFetchedAt = {};
  static const _modelCacheTTL = Duration(hours: 24);

  final _eventController = StreamController<SSEEvent>.broadcast();
  Stream<SSEEvent> get events => _eventController.stream;

  /// External callback for SSE events (used by HostManager for notification routing).
  SSEEventCallback? onSSEEvent;

  DaemonAPIService({
    required this.hostId,
    required this.serverUrl,
    required ApiClient api,
  }) : _api = api;

  // ==================== Auth Helpers ====================

  /// Exposed so SSE connections (NarrationService) can get a token via callback.
  Future<String> getToken() => _api.getToken();

  // ==================== Lifecycle ====================

  /// Start as the active host: SSE + fallback polling when SSE is down.
  Future<void> startActive() async {
    if (_running) return;
    _running = true;
    _isActiveHost = true;
    _startWatchdog();
    await _loadSessionCache();
    _connect(); // fire-and-forget — SSE runs in background
  }

  /// Start as a background host: SSE only, no polling.
  Future<void> startBackground() async {
    if (_running) return;
    _running = true;
    _isActiveHost = false;
    _startWatchdog();
    _connect(); // fire-and-forget — SSE runs in background
  }

  /// Re-establish the event stream after the app was suspended.
  ///
  /// [startActive] and [startBackground] cannot do this: both return early
  /// while `_running` is set, and it stays set across a pause because the
  /// stream is deliberately kept alive for background notifications. So resume
  /// used to be a no-op on the connection — exactly when the socket is most
  /// likely to have died unnoticed.
  Future<void> resume({required bool asActiveHost}) async {
    if (!_running) {
      await (asActiveHost ? startActive() : startBackground());
      return;
    }

    _isActiveHost = asActiveHost;
    if (asActiveHost) await _loadSessionCache();

    // Timers do not fire while the process is suspended, so the watchdog and
    // any pending backoff came back late or not at all.
    _startWatchdog();

    // A brief screen-off leaves a healthy stream; rebuilding it would cost a
    // reconnect and a refetch for nothing.
    if (_connected && !stale) return;

    reconnect();
  }

  /// Catches a stream that died without ever erroring.
  void _startWatchdog() {
    _watchdogTimer?.cancel();
    _watchdogTimer = Timer.periodic(_watchdogInterval, (_) {
      if (!_running || !stale) return;
      debugPrint('[$hostId] stream silent past the heartbeat window');
      reconnect();
    });
  }

  /// Promote from background to active (fetch data, start polling if SSE is down).
  void promote() async {
    _isActiveHost = true;
    await _loadSessionCache();
    fetchSessions();
    fetchNotifications();
    fetchProviders();
    fetchSortMode();
    if (!_connected) _startPolling();
  }

  /// Demote from active to background (stop polling).
  void demote() {
    _isActiveHost = false;
    _pollTimer?.cancel();
    _pollTimer = null;
  }

  /// Fallback polling when SSE is disconnected. Stopped when SSE reconnects.
  void _startPolling() {
    _pollTimer?.cancel();
    _pollTimer = Timer.periodic(const Duration(seconds: 3), (_) {
      fetchSessions();
    });
  }

  /// Stop the SSE connection and polling.
  void stop() {
    _running = false;
    _connected = false;
    _isActiveHost = false;
    _consecutiveFailures = 0;
    _lastBytesAt = null;
    _client?.close();
    _client = null;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _pollTimer?.cancel();
    _pollTimer = null;
    _watchdogTimer?.cancel();
    _watchdogTimer = null;
    _sessionDebounce?.cancel();
    _sessionDebounce = null;
    _notificationDebounce?.cancel();
    _notificationDebounce = null;
    notifyListeners();
  }

  // ==================== Session Cache ====================

  String get _sessionCacheKey => 'session_cache_$hostId';

  /// Load cached sessions from disk for instant display on launch.
  Future<void> _loadSessionCache() async {
    if (_sessionsLoaded) return;
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_sessionCacheKey);
      if (raw == null) return;
      final list = (jsonDecode(raw) as List?) ?? [];
      _sessions = list
          .map(
            (s) => Session.fromJson(s as Map<String, dynamic>, hostId: hostId),
          )
          .toList();
      _sessionsLoaded = true;
      notifyListeners();
    } catch (_) {
      // Schema changed or corrupt cache — drop it and fetch fresh
      final prefs = await SharedPreferences.getInstance();
      await prefs.remove(_sessionCacheKey);
    }
  }

  /// Persist the raw session JSON for next launch.
  Future<void> _saveSessionCache(String rawJson) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_sessionCacheKey, rawJson);
    } catch (_) {
      // Best effort
    }
  }

  // ==================== SSE ====================

  Future<void> _connect() async {
    if (!_running) return;

    // Every attempt supersedes the one before it. A superseded attempt unwinds
    // once its client is closed, and without this guard its tail would mark the
    // live connection dead and queue a second, racing reconnect.
    final generation = ++_connectGeneration;
    bool superseded() => !_running || generation != _connectGeneration;

    _client?.close();
    final client = http.Client();
    _client = client;

    try {
      final request = http.Request('GET', Uri.parse('$serverUrl/api/events'));
      request.headers.addAll({
        'Authorization': 'Bearer ${await _api.getToken()}',
        'Accept': 'text/event-stream',
        'Cache-Control': 'no-cache',
      });

      final response = await client.send(request);
      if (superseded()) return;

      if (response.statusCode == 401) {
        debugPrint('[$hostId] SSE auth failed — refreshing token');
        _api.invalidateToken();
        _scheduleReconnect();
        return;
      }
      if (response.statusCode != 200) {
        debugPrint('[$hostId] SSE connect failed: HTTP ${response.statusCode}');
        _consecutiveFailures++;
        notifyListeners();
        _scheduleReconnect();
        return;
      }

      _consecutiveFailures = 0;
      _connected = true;
      _lastBytesAt = DateTime.now();
      // SSE is healthy — stop fallback polling
      _pollTimer?.cancel();
      _pollTimer = null;
      // A dead stream is exactly when notification_resolved events go missing,
      // so re-sync the tray against the daemon before trusting the stream
      // again. Resuming the app already does this; a network flap does not.
      fetchNotifications();
      notifyListeners();

      String buffer = '';
      String currentEvent = '';

      await for (final chunk in response.stream.transform(utf8.decoder)) {
        if (superseded()) return;
        // Heartbeats count. The daemon sends ": heartbeat" every 30s, and on a
        // stream carrying no events it is the only proof the socket is alive.
        _lastBytesAt = DateTime.now();

        buffer += chunk;
        final lines = buffer.split('\n');
        buffer = lines.removeLast();

        for (final line in lines) {
          if (line.startsWith('event: ')) {
            currentEvent = line.substring(7).trim();
          } else if (line.startsWith('data: ') && currentEvent.isNotEmpty) {
            try {
              final data = jsonDecode(line.substring(6));
              _handleEvent(currentEvent, data);
            } catch (_) {}
            currentEvent = '';
          }
        }
      }
    } catch (e) {
      if (superseded()) return;
      debugPrint('[$hostId] SSE error: $e');
      _consecutiveFailures++;
    }

    if (superseded()) return;
    _connected = false;
    // SSE dropped — start fallback polling if this is the active host
    if (_isActiveHost && _pollTimer == null) _startPolling();
    notifyListeners();
    _scheduleReconnect();
  }

  void _handleEvent(String type, dynamic data) {
    final event = SSEEvent(type, data);
    _eventController.add(event);
    onSSEEvent?.call(hostId, event);

    // Debounce notification fetches — multiple SSE events within 500ms
    // collapse into a single HTTP call.
    _notificationDebounce?.cancel();
    _notificationDebounce = Timer(const Duration(milliseconds: 500), () {
      fetchNotifications();
    });

    // Debounce session fetches for active host
    if (_isActiveHost &&
        (type == 'session_status' ||
            type == 'session_updated' ||
            type == 'session_deleted' ||
            type == 'notification' ||
            type == 'notification_resolved' ||
            type == 'subagent_status')) {
      _sessionDebounce?.cancel();
      _sessionDebounce = Timer(const Duration(milliseconds: 500), () {
        fetchSessions();
      });
    }
  }

  void _scheduleReconnect() {
    if (!_running) return;
    _reconnectTimer?.cancel();
    // Exponential backoff: 3s, 6s, 12s, 24s, capped at 30s
    final delay = Duration(
      seconds: (3 * (1 << (_consecutiveFailures - 1).clamp(0, 3))).clamp(3, 30),
    );
    debugPrint(
      '[$hostId] reconnecting in ${delay.inSeconds}s (failures=$_consecutiveFailures)',
    );
    _reconnectTimer = Timer(delay, _connect);
  }

  /// Force an immediate reconnect attempt, resetting failure count.
  void reconnect() {
    _reconnectTimer?.cancel();
    _consecutiveFailures = 0;
    notifyListeners();
    _connect();
  }

  // ==================== Notifications API ====================

  Future<void> fetchNotifications() async {
    try {
      final resp = await _api.get('/api/notifications');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['notifications'] as List?) ?? [];
        _notifications = list
            .map((n) => HeliosNotification.fromJson(n, hostId: hostId))
            .toList();
        _notificationsLoaded = true;
        _reconcilePostedNotifications();
        notifyListeners();
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch notifications: $e');
    }
  }

  /// Bring the tray in line with the daemon, which owns notification status.
  ///
  /// This is the only thing that clears a notification answered while the SSE
  /// stream was dead, which is every approval made while the phone was dozing.
  /// Driven by the pending set rather than by the resolved rows: the daemon
  /// prunes old notifications, so one resolved a while back is absent from the
  /// response entirely and no per-row sweep would ever reach it.
  void _reconcilePostedNotifications() {
    NotificationService.instance.retainOnly(
      hostId,
      _notifications.where((n) => n.isPending).map((n) => n.id).toSet(),
    );
  }

  Future<bool> sendAction(String id, Map<String, dynamic> body) async {
    return await sendActionError(id, body) == null;
  }

  /// [sendAction] that reports why it failed, so a card can tell the user
  /// instead of silently doing nothing — indistinguishable, otherwise, from
  /// the action never having been wired up. Returns null on success.
  Future<String?> sendActionError(String id, Map<String, dynamic> body) async {
    try {
      final resp = await _api.post('/api/notifications/$id/action', body: body);
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return null;
      }
      // 410 means someone else got there first — the terminal, or our own
      // answer racing the hook that retracts the notification. Either way the
      // question is answered, which is not a failure worth reporting.
      if (resp.statusCode == 410) {
        await fetchNotifications();
        return null;
      }
      final message = _actionErrorMessage(resp.body);
      debugPrint('[$hostId] Action failed (${resp.statusCode}): $message');
      return message;
    } catch (e) {
      debugPrint('[$hostId] Failed to send action: $e');
      return 'Could not reach the daemon';
    }
  }

  String _actionErrorMessage(String body) {
    try {
      final decoded = jsonDecode(body);
      // jsonError puts the reason in "message"; "error" holds the status text.
      if (decoded is Map && decoded['message'] is String) {
        return decoded['message'] as String;
      }
    } catch (_) {
      // Fall through to the generic message.
    }
    return 'The daemon rejected the action';
  }

  Future<bool> dismissNotification(String id) async {
    try {
      final resp = await _api.post('/api/notifications/$id/dismiss');
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to dismiss: $e');
    }
    return false;
  }

  Future<bool> batchAction(
    List<String> ids,
    Map<String, dynamic> action,
  ) async {
    try {
      final resp = await _api.post(
        '/api/notifications/batch',
        body: {'notification_ids': ids, 'action': action},
      );
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to batch action: $e');
    }
    return false;
  }

  // ==================== Session API ====================

  Future<void> fetchSessions({
    String? q,
    String? status,
    String? filter,
    String? cwd,
    bool updateFilters = false,
  }) async {
    // When called with explicit params from search UI, remember them.
    if (updateFilters) {
      _lastSessionQ = q;
      _lastSessionFilter = filter;
      _lastSessionCwd = cwd;
    }

    // Use the remembered filters for background refreshes (polling/SSE).
    final effectiveQ = q ?? _lastSessionQ;
    final effectiveFilter = filter ?? _lastSessionFilter;
    final effectiveCwd = cwd ?? _lastSessionCwd;

    try {
      final params = <String, String>{};
      if (effectiveQ != null && effectiveQ.isNotEmpty) params['q'] = effectiveQ;
      if (status != null && status.isNotEmpty) params['status'] = status;
      if (effectiveFilter != null && effectiveFilter.isNotEmpty) {
        params['filter'] = effectiveFilter;
      }
      if (effectiveCwd != null && effectiveCwd.isNotEmpty) {
        params['cwd'] = effectiveCwd;
      }

      final queryString = params.entries
          .map((e) => '${e.key}=${Uri.encodeComponent(e.value)}')
          .join('&');
      final path = queryString.isNotEmpty
          ? '/api/sessions?$queryString'
          : '/api/sessions';

      final resp = await _api.get(path);
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['sessions'] as List?) ?? [];
        _sessions = list
            .map((s) => Session.fromJson(s, hostId: hostId))
            .toList();
        _sessionsLoaded = true;
        notifyListeners();
        // Cache the full unfiltered list for instant display on next launch
        final isUnfiltered =
            (effectiveQ == null || effectiveQ.isEmpty) &&
            (effectiveCwd == null || effectiveCwd.isEmpty);
        if (isUnfiltered) {
          _saveSessionCache(jsonEncode(list));
        }
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch sessions: $e');
    }
  }

  Future<List<DirectoryInfo>> fetchDirectories() async {
    try {
      final resp = await _api.get('/api/sessions/directories');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['directories'] as List?) ?? [];
        return list.map((d) => DirectoryInfo.fromJson(d)).toList();
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch directories: $e');
    }
    return [];
  }

  /// Reads a page of a transcript, or — given [afterSeq] and [epoch] — only
  /// what has been written since that message. Following a running session
  /// through deltas is what stops an event from costing a whole page.
  Future<TranscriptResult?> fetchTranscript(
    String sessionId, {
    int limit = 50,
    int offset = 0,
    int? afterSeq,
    String? epoch,
  }) async {
    try {
      final query = afterSeq != null
          ? 'limit=$limit&after_seq=$afterSeq&epoch=${Uri.encodeQueryComponent(epoch ?? '')}'
          : 'limit=$limit&offset=$offset';
      final resp = await _api.get(
        '/api/sessions/$sessionId/transcript?$query',
      );
      debugPrint(
        '[$hostId] fetchTranscript[$sessionId] status=${resp.statusCode}',
      );
      if (resp.statusCode == 200) {
        return TranscriptResult.fromJson(jsonDecode(resp.body));
      }
      debugPrint(
        '[$hostId] fetchTranscript[$sessionId] non-200 body=${resp.body}',
      );
    } catch (e) {
      debugPrint('[$hostId] fetchTranscript[$sessionId] exception: $e');
    }
    return null;
  }

  Future<List<Subagent>> fetchSubagents(String sessionId) async {
    try {
      final resp = await _api.get('/api/sessions/$sessionId/subagents');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['subagents'] as List?) ?? [];
        return list.map((s) => Subagent.fromJson(s)).toList();
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch subagents: $e');
    }
    return [];
  }

  /// Uploads attachments and returns the path the daemon stored each under.
  ///
  /// The bytes stop at the daemon: the prompt carries the paths, and the agent
  /// opens them with its own tools. Returns null when the upload failed, which
  /// the caller reports rather than sending a prompt naming files that are not
  /// there.
  Future<List<String>?> uploadSessionFiles(
    String sessionId,
    List<UploadFile> files,
  ) async {
    if (files.isEmpty) return const [];
    try {
      final resp = await _api.postFiles('/api/sessions/$sessionId/files', files);
      if (resp.statusCode != 200) {
        debugPrint('[$hostId] uploadSessionFiles: ${resp.statusCode} ${resp.body}');
        return null;
      }
      final data = jsonDecode(resp.body);
      final list = (data['files'] as List?) ?? [];
      return list.map((f) => f['path'] as String).toList();
    } catch (e) {
      debugPrint('[$hostId] Failed to upload files: $e');
      return null;
    }
  }

  /// Sends a prompt and waits for the agent to accept it.
  ///
  /// Returns null when it did. Anything else is the reason it did not, worth
  /// showing: the daemon only answers once the agent has confirmed, so a
  /// failure here means the message is not in the session.
  Future<String?> sendSessionPrompt(String sessionId, String message) async {
    try {
      final resp = await _api.post(
        '/api/sessions/$sessionId/send',
        body: {'message': message},
      );
      debugPrint(
        '[$hostId] sendSessionPrompt: status=${resp.statusCode} body=${resp.body}',
      );
      if (resp.statusCode == 200) {
        await fetchSessions();
        return null;
      }
      return _sendErrorMessage(resp.body);
    } catch (e) {
      debugPrint('[$hostId] Failed to send prompt: $e');
      if (HostConnection.isTailnetUrl(serverUrl)) {
        return 'Could not reach the daemon — switch Tailscale on';
      }
      return 'Could not reach the daemon';
    }
  }

  String _sendErrorMessage(String body) {
    try {
      final data = jsonDecode(body);
      final reason = (data['error'] as String?) ?? '';
      if (reason == 'session_busy') return 'The session is busy';
      if (reason == 'session_terminated') return 'The session has ended';
      if (reason.isNotEmpty) return reason;
    } catch (_) {}
    return 'Failed to send prompt';
  }

  Future<bool> stopSession(String sessionId) async {
    try {
      final resp = await _api.post('/api/sessions/$sessionId/stop');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to stop session: $e');
    }
    return false;
  }

  Future<bool> terminateSession(String sessionId) async {
    try {
      final resp = await _api.post('/api/sessions/$sessionId/terminate');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to terminate session: $e');
    }
    return false;
  }

  /// Switches a session's permission mode, returning null on success or a
  /// message to show the user.
  ///
  /// Unlike the other session actions this reports why it failed: the daemon
  /// restarts the agent to apply the mode and refuses while it is working, and
  /// a silent no-op would leave the sheet showing the old mode with no
  /// explanation.
  Future<String?> setPermissionMode(String sessionId, String mode) async {
    try {
      final resp = await _api.post(
        '/api/sessions/$sessionId/permission-mode',
        body: {'mode': mode},
      );
      if (resp.statusCode == 200) {
        await fetchSessions();
        return null;
      }
      final body = jsonDecode(resp.body) as Map<String, dynamic>;
      if (body['error'] == 'session_busy') {
        return 'Session is busy — stop it first to change the mode';
      }
      if (body['error'] == 'session_ended') {
        return 'Session has ended';
      }
      return body['error'] as String? ?? 'Failed to change permission mode';
    } catch (e) {
      debugPrint('[$hostId] Failed to set permission mode: $e');
      return 'Failed to change permission mode';
    }
  }

  Future<bool> resumeSession(String sessionId) async {
    try {
      final resp = await _api.post('/api/sessions/$sessionId/resume');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to resume session: $e');
    }
    return false;
  }

  Future<bool> patchSession(
    String sessionId, {
    bool? pinned,
    bool? archived,
    String? title,
  }) async {
    // Optimistically update the local session list for instant UI feedback.
    // Use Future.microtask to defer the notification so any dialog/sheet that
    // triggered this call finishes its pop transition first — avoids the
    // _dependents.isEmpty assertion in framework.dart.
    final idx = _sessions.indexWhere((s) => s.sessionId == sessionId);
    Session? original;
    if (idx != -1) {
      original = _sessions[idx];
      _sessions[idx] = original.copyWith(
        pinned: pinned ?? original.pinned,
        archived: archived ?? original.archived,
        title: title,
      );
      Future.microtask(() => notifyListeners());
    }

    try {
      final body = <String, dynamic>{};
      if (pinned != null) body['pinned'] = pinned;
      if (archived != null) body['archived'] = archived;
      if (title != null) body['title'] = title;
      final resp = await _api.patch('/api/sessions/$sessionId', body: body);
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to patch session: $e');
    }

    // Revert on failure.
    if (original != null && idx != -1 && idx < _sessions.length) {
      _sessions[idx] = original;
      notifyListeners();
    }
    return false;
  }

  Future<bool> deleteSession(String sessionId) async {
    // Optimistically remove from local list for instant UI feedback.
    final original = List<Session>.from(_sessions);
    _sessions.removeWhere((s) => s.sessionId == sessionId);
    Future.microtask(() => notifyListeners());

    try {
      final resp = await _api.delete('/api/sessions/$sessionId');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to delete session: $e');
    }

    // Revert on failure.
    _sessions = original;
    notifyListeners();
    return false;
  }

  // ==================== Terminals API ====================

  /// The live terminals beside a session: its agent, then any shells.
  Future<List<TerminalInfo>> fetchTerminals(String sessionId) async {
    try {
      final resp = await _api.get('/api/sessions/${Uri.encodeComponent(sessionId)}/terminals');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['terminals'] as List?) ?? [];
        return list.map((t) => TerminalInfo.fromJson(t as Map<String, dynamic>)).toList();
      }
    } catch (e) {
      debugPrint('[$hostId] fetchTerminals error: $e');
    }
    return [];
  }

  /// Opens a shell in the session's directory. It runs no agent and keeps no
  /// transcript: it is there for when the agent is busy and you want to run
  /// something yourself.
  Future<TerminalInfo?> openShell(String sessionId) async {
    try {
      final resp = await _api.post('/api/sessions/${Uri.encodeComponent(sessionId)}/terminals');
      if (isSuccess(resp.statusCode)) {
        return TerminalInfo.fromJson(jsonDecode(resp.body) as Map<String, dynamic>);
      }
      debugPrint('[$hostId] openShell failed: ${resp.statusCode} ${resp.body}');
    } catch (e) {
      debugPrint('[$hostId] openShell error: $e');
    }
    return null;
  }

  /// Closes a shell. The daemon refuses this for a session's agent, which has
  /// stop and terminate of its own.
  Future<bool> closeTerminal(String terminalId) async {
    try {
      final resp = await _api.delete('/api/terminals/${Uri.encodeComponent(terminalId)}');
      return isSuccess(resp.statusCode);
    } catch (e) {
      debugPrint('[$hostId] closeTerminal error: $e');
    }
    return false;
  }

  // ==================== Settings API ====================

  /// Fetch all settings and personas from the backend.
  Future<Map<String, dynamic>?> getSettings() async {
    try {
      final resp = await _api.get('/api/settings');
      if (resp.statusCode == 200) {
        return jsonDecode(resp.body) as Map<String, dynamic>;
      }
    } catch (e) {
      debugPrint('[$hostId] getSettings error: $e');
    }
    return null;
  }

  /// Update settings on the backend (bulk upsert).
  Future<bool> updateSettings(Map<String, String> settings) async {
    try {
      final resp = await _api.post('/api/settings', body: settings);
      return isSuccess(resp.statusCode);
    } catch (e) {
      debugPrint('[$hostId] updateSettings error: $e');
    }
    return false;
  }

  // ==================== Host settings ====================

  /// Settings the daemon owns rather than this device. They live in this
  /// host's database, so each paired host holds its own and a phone with two
  /// hosts cannot set one and mean the other. Reading them from "whichever
  /// host answered first" is how they came to look like app settings.
  static const settingAutoTitle = 'autotitle.enabled';
  static const settingAutoTitleEmoji = 'autotitle.emoji';
  static const settingEvict = 'memory.evict';
  static const settingBudgetFraction = 'memory.budget_fraction';

  /// The budget slider's travel, as a share of the host's memory. It stops
  /// short of the whole machine at both ends: below a twentieth the budget
  /// cannot hold one agent, and at 100% eviction would only begin once the
  /// host was already swapping.
  static const budgetMin = 0.05;
  static const budgetMax = 0.9;
  static const budgetDefault = 0.25;

  bool _autoTitleEnabled = false;
  bool _autoTitleEmoji = false;
  bool _evictEnabled = false;
  double _budgetFraction = budgetDefault;
  bool _hostSettingsLoaded = false;

  bool get autoTitleEnabled => _autoTitleEnabled;
  bool get autoTitleEmoji => _autoTitleEmoji;
  bool get evictEnabled => _evictEnabled;
  double get budgetFraction => _budgetFraction;

  /// False until this host has answered once. A host that has never been read
  /// shows nothing, rather than a default that is probably not its value.
  bool get hostSettingsLoaded => _hostSettingsLoaded;

  /// Reads every daemon-owned setting in one request. The sort mode arrives in
  /// the same response, so fetching it on its own would ask the host the same
  /// question twice.
  Future<void> fetchHostSettings() async {
    final body = await getSettings();
    if (body == null) return;
    final settings = (body['settings'] as Map<String, dynamic>?) ?? const {};
    _manualOrder = settings[_sortModeSetting] == 'manual';
    _autoTitleEnabled = settings[settingAutoTitle] == 'true';
    // Off unless turned on: Flutter ships no Nerd Font, so the glyphs render
    // as empty boxes on the phone.
    _autoTitleEmoji = settings[settingAutoTitleEmoji] == 'true';
    _evictEnabled = settings[settingEvict] == 'true';
    // Clamped rather than trusted: the setting predates the slider, so a
    // stored value can sit outside its travel.
    final budget = double.tryParse('${settings[settingBudgetFraction] ?? ''}');
    _budgetFraction =
        budget == null ? budgetDefault : budget.clamp(budgetMin, budgetMax);
    _hostSettingsLoaded = true;
    notifyListeners();
  }

  Future<bool> setAutoTitleEnabled(bool value) {
    final previous = _autoTitleEnabled;
    return _writeHostSetting(
      settingAutoTitle,
      value ? 'true' : 'false',
      () => _autoTitleEnabled = value,
      () => _autoTitleEnabled = previous,
    );
  }

  Future<bool> setAutoTitleEmoji(bool value) {
    final previous = _autoTitleEmoji;
    return _writeHostSetting(
      settingAutoTitleEmoji,
      value ? 'true' : 'false',
      () => _autoTitleEmoji = value,
      () => _autoTitleEmoji = previous,
    );
  }

  Future<bool> setEvictEnabled(bool value) {
    final previous = _evictEnabled;
    return _writeHostSetting(
      settingEvict,
      value ? 'true' : 'false',
      () => _evictEnabled = value,
      () => _evictEnabled = previous,
    );
  }

  Future<bool> setBudgetFraction(double value) {
    final previous = _budgetFraction;
    final clamped = value.clamp(budgetMin, budgetMax);
    return _writeHostSetting(
      settingBudgetFraction,
      clamped.toStringAsFixed(2),
      () => _budgetFraction = clamped,
      () => _budgetFraction = previous,
    );
  }

  /// Paints the new value first so the control answers the tap, and puts the
  /// old one back if the host refuses or cannot be reached. Returning the
  /// verdict is the point: a switch that flips and then quietly did nothing is
  /// worse than one that says it failed.
  Future<bool> _writeHostSetting(
    String key,
    String value,
    void Function() apply,
    void Function() revert,
  ) async {
    apply();
    notifyListeners();
    if (await updateSettings({key: value})) return true;
    revert();
    notifyListeners();
    return false;
  }

  // ==================== Session order ====================

  /// The daemon owns the mode, so every client of this host agrees on it.
  static const _sortModeSetting = 'sessions.sort';
  bool _manualOrder = false;
  bool get manualOrder => _manualOrder;

  /// The name the session list calls. The mode comes back with the rest of the
  /// host's settings.
  Future<void> fetchSortMode() => fetchHostSettings();

  /// Switching to manual freezes [visibleOrder] as it stands, so the list does
  /// not jump the moment it stops sorting itself.
  Future<void> setManualOrder(bool manual, {List<String> visibleOrder = const []}) async {
    if (manual && visibleOrder.isNotEmpty) await setSessionOrder(visibleOrder);
    final ok = await updateSettings({_sortModeSetting: manual ? 'manual' : 'activity'});
    if (!ok) return;
    _manualOrder = manual;
    notifyListeners();
  }

  /// Writes the arrangement, painting it locally first so the drag lands
  /// without waiting for the round trip.
  Future<bool> setSessionOrder(List<String> sessionIds) async {
    final previous = _sessions;
    final positions = {for (var i = 0; i < sessionIds.length; i++) sessionIds[i]: i};
    _sessions = _sessions
        .map((s) => positions.containsKey(s.sessionId) ? s.copyWith(sortOrder: positions[s.sessionId]) : s)
        .toList();
    notifyListeners();
    try {
      final resp = await _api.post('/api/sessions/order', body: {'order': sessionIds});
      if (resp.statusCode == 200) return true;
    } catch (e) {
      debugPrint('[$hostId] setSessionOrder error: $e');
    }
    _sessions = previous;
    notifyListeners();
    return false;
  }

  // ==================== Providers & Models API ====================

  Future<void> fetchProviders() async {
    try {
      final resp = await _api.get('/api/providers');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['providers'] as List?) ?? [];
        _providers = list.map((p) => ProviderInfo.fromJson(p)).toList();
        _providersLoaded = true;
        notifyListeners();
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch providers: $e');
    }
  }

  List<ModelInfo> getCachedModels(String providerId) {
    return _modelCache[providerId] ?? [];
  }

  bool hasModelCache(String providerId) {
    final fetchedAt = _modelCacheFetchedAt[providerId];
    if (fetchedAt == null) return false;
    return DateTime.now().difference(fetchedAt) < _modelCacheTTL;
  }

  Future<List<ModelInfo>> fetchModels(
    String providerId, {
    bool forceRefresh = false,
  }) async {
    if (!forceRefresh && hasModelCache(providerId)) {
      return _modelCache[providerId]!;
    }

    try {
      final endpoint = forceRefresh
          ? '/api/providers/$providerId/models/refresh'
          : '/api/providers/$providerId/models';
      final resp = forceRefresh
          ? await _api.post(endpoint)
          : await _api.get(endpoint);
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['models'] as List?) ?? [];
        final models = list.map((m) => ModelInfo.fromJson(m)).toList();
        _modelCache[providerId] = models;
        _modelCacheFetchedAt[providerId] = DateTime.now();
        notifyListeners();
        return models;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch models for $providerId: $e');
    }
    return _modelCache[providerId] ?? [];
  }

  // ==================== File Browser API ====================

  Future<FileListing?> listFiles(String path) async {
    try {
      final resp = await _api.get(
        '/api/files?path=${Uri.encodeComponent(path)}',
      );
      if (resp.statusCode == 200) {
        return FileListing.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to list files at $path: $e');
    }
    return null;
  }

  Future<FileReadResult?> readFile(String path) async {
    try {
      final resp = await _api.get(
        '/api/file?path=${Uri.encodeComponent(path)}',
      );
      if (resp.statusCode == 413) {
        final data = jsonDecode(resp.body);
        return FileReadResult.tooLarge(
          path: path,
          size: data['size'] as int? ?? 0,
        );
      }
      if (resp.statusCode == 400) {
        final data = jsonDecode(resp.body);
        if ((data['message'] as String? ?? '').contains('directory')) {
          return FileReadResult.directory(path: path);
        }
      }
      if (resp.statusCode == 200) {
        return FileReadResult.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to read file $path: $e');
    }
    return null;
  }

  Future<bool> createSession({
    required String provider,
    String? model,
    String? cwd,
    bool dangerouslySkipPermissions = false,
  }) async {
    try {
      final body = <String, dynamic>{'provider': provider};
      if (model != null && model.isNotEmpty) body['model'] = model;
      if (cwd != null && cwd.isNotEmpty) body['cwd'] = cwd;
      if (dangerouslySkipPermissions) {
        body['dangerously_skip_permissions'] = true;
      }

      final resp = await _api.post('/api/sessions', body: body);
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to create session: $e');
    }
    return false;
  }

  Future<GitStatus?> gitStatus(String path) async {
    try {
      final resp = await _api.get(
        '/api/git/status?path=${Uri.encodeComponent(path)}',
      );
      if (resp.statusCode == 200) {
        return GitStatus.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get git status for $path: $e');
    }
    return null;
  }

  /// The working-tree diff for a file, or its diff at a revision: [to] alone is
  /// that commit against its parent, [from] and [to] together are a range.
  Future<GitDiff?> gitDiff(
    String path,
    String file, {
    bool staged = false,
    String? from,
    String? to,
    bool untracked = false,
  }) async {
    try {
      final query = <String>[
        'path=${Uri.encodeComponent(path)}',
        'file=${Uri.encodeComponent(file)}',
        if (staged) 'staged=true',
        if (untracked) 'untracked=true',
        if (from != null && from.isNotEmpty) 'from=${Uri.encodeComponent(from)}',
        if (to != null && to.isNotEmpty) 'to=${Uri.encodeComponent(to)}',
      ];
      final resp = await _api.get('/api/git/diff?${query.join('&')}');
      if (resp.statusCode == 200) {
        return GitDiff.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get git diff for $file: $e');
    }
    return null;
  }

  /// Commit history. Defaults to what this branch added on top of its base;
  /// pass [all] for the whole history.
  Future<GitLog?> gitLog(
    String path, {
    String? base,
    bool all = false,
    int limit = 50,
    int skip = 0,
  }) async {
    try {
      final query = <String>[
        'path=${Uri.encodeComponent(path)}',
        'limit=$limit',
        if (skip > 0) 'skip=$skip',
        if (all) 'all=true',
        if (base != null && base.isNotEmpty) 'base=${Uri.encodeComponent(base)}',
      ];
      final resp = await _api.get('/api/git/log?${query.join('&')}');
      if (resp.statusCode == 200) {
        return GitLog.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get git log for $path: $e');
    }
    return null;
  }

  /// The files one commit touched, or everything between two.
  Future<GitChanges?> gitChanges(String path, String to, {String? from}) async {
    try {
      final query = <String>[
        'path=${Uri.encodeComponent(path)}',
        'to=${Uri.encodeComponent(to)}',
        if (from != null && from.isNotEmpty) 'from=${Uri.encodeComponent(from)}',
      ];
      final resp = await _api.get('/api/git/changes?${query.join('&')}');
      if (resp.statusCode == 200) {
        return GitChanges.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get changes for $to: $e');
    }
    return null;
  }

  Future<List<Worktree>> gitWorktrees(String path) async {
    try {
      final resp = await _api.get(
        '/api/git/worktrees?path=${Uri.encodeComponent(path)}',
      );
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        return (data['worktrees'] as List?)
                ?.map((e) => Worktree.fromJson(e))
                .toList() ??
            [];
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get worktrees for $path: $e');
    }
    return [];
  }

  @override
  void dispose() {
    stop();
    _eventController.close();
    // The client holds connections open between requests, so a host that is
    // gone has to give them back.
    _api.close();
    super.dispose();
  }
}

class SSEEvent {
  final String type;
  final dynamic data;
  SSEEvent(this.type, this.data);
}

class FileEntry {
  final String name;
  final String path;
  final bool isDir;
  final int size;
  final String modTime;

  FileEntry({
    required this.name,
    required this.path,
    required this.isDir,
    required this.size,
    required this.modTime,
  });

  factory FileEntry.fromJson(Map<String, dynamic> json) {
    return FileEntry(
      name: json['name'] as String,
      path: json['path'] as String,
      isDir: json['is_dir'] as bool? ?? false,
      size: (json['size'] as num?)?.toInt() ?? 0,
      modTime: json['mod_time'] as String? ?? '',
    );
  }

  String get formattedSize {
    if (size < 1024) return '$size B';
    if (size < 1024 * 1024) return '${(size / 1024).toStringAsFixed(1)} KB';
    if (size < 1024 * 1024 * 1024) {
      return '${(size / (1024 * 1024)).toStringAsFixed(1)} MB';
    }
    return '${(size / (1024 * 1024 * 1024)).toStringAsFixed(1)} GB';
  }
}

class FileListing {
  final String path;
  final List<FileEntry> entries;

  FileListing({required this.path, required this.entries});

  factory FileListing.fromJson(Map<String, dynamic> json) {
    final list = (json['entries'] as List?) ?? [];
    return FileListing(
      path: json['path'] as String,
      entries: list
          .map((e) => FileEntry.fromJson(e as Map<String, dynamic>))
          .toList(),
    );
  }
}

class FileReadResult {
  final String path;
  final int size;
  final String? content;
  final bool isTooLarge;
  final bool isDirectory;

  FileReadResult({
    required this.path,
    required this.size,
    this.content,
    this.isTooLarge = false,
    this.isDirectory = false,
  });

  factory FileReadResult.fromJson(Map<String, dynamic> json) {
    return FileReadResult(
      path: json['path'] as String,
      size: (json['size'] as num?)?.toInt() ?? 0,
      content: json['content'] as String?,
    );
  }

  factory FileReadResult.tooLarge({required String path, required int size}) {
    return FileReadResult(path: path, size: size, isTooLarge: true);
  }

  factory FileReadResult.directory({required String path}) {
    return FileReadResult(path: path, size: 0, isDirectory: true);
  }

  bool get isBinary {
    final c = content;
    if (c == null || c.isEmpty) return false;
    // Sample first 8KB for binary detection
    final sample = c.length > 8192 ? c.substring(0, 8192) : c;
    int nonPrintable = 0;
    for (final cp in sample.runes) {
      if (cp == 0) return true; // null byte = definitely binary
      if (cp < 9 || (cp > 13 && cp < 32)) nonPrintable++;
    }
    return nonPrintable / sample.runes.length > 0.30;
  }

  String get formattedSize {
    if (size < 1024) return '$size B';
    if (size < 1024 * 1024) return '${(size / 1024).toStringAsFixed(1)} KB';
    return '${(size / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
}

/// One live terminal: a session's agent, or a shell opened beside it.
class TerminalInfo {
  final String id;
  final String parent;
  final String kind;
  final String cwd;

  TerminalInfo({
    required this.id,
    required this.parent,
    required this.kind,
    required this.cwd,
  });

  factory TerminalInfo.fromJson(Map<String, dynamic> json) => TerminalInfo(
        id: json['id'] as String,
        parent: json['parent'] as String? ?? '',
        kind: json['kind'] as String? ?? 'shell',
        cwd: json['cwd'] as String? ?? '',
      );

  bool get isAgent => kind == 'agent';
}

class DirectoryInfo {
  final String cwd;
  final String project;
  final int sessionCount;
  final int activeCount;

  DirectoryInfo({
    required this.cwd,
    required this.project,
    required this.sessionCount,
    required this.activeCount,
  });

  factory DirectoryInfo.fromJson(Map<String, dynamic> json) {
    return DirectoryInfo(
      cwd: json['cwd'] as String,
      project: json['project'] as String? ?? '',
      sessionCount: json['session_count'] as int? ?? 0,
      activeCount: json['active_count'] as int? ?? 0,
    );
  }

  String get shortCwd {
    final parts = cwd.split('/');
    if (parts.length <= 3) return cwd;
    return '.../${parts.sublist(parts.length - 2).join('/')}';
  }
}

class GitChange {
  final String path;
  final String status;

  GitChange({required this.path, required this.status});

  factory GitChange.fromJson(Map<String, dynamic> json) {
    return GitChange(
      path: json['path'] as String,
      status: json['status'] as String? ?? '?',
    );
  }

  String get fileName => path.split('/').last;
}

class GitStatus {
  final String root;
  final String branch;
  final bool dirty;
  final int ahead;
  final int behind;
  final List<GitChange> staged;
  final List<GitChange> unstaged;
  final List<GitChange> untracked;

  GitStatus({
    required this.root,
    required this.branch,
    required this.dirty,
    required this.ahead,
    required this.behind,
    required this.staged,
    required this.unstaged,
    required this.untracked,
  });

  factory GitStatus.fromJson(Map<String, dynamic> json) {
    return GitStatus(
      root: json['root'] as String? ?? '',
      branch: json['branch'] as String,
      dirty: json['dirty'] as bool? ?? false,
      ahead: json['ahead'] as int? ?? 0,
      behind: json['behind'] as int? ?? 0,
      staged:
          (json['staged'] as List?)
              ?.map((e) => GitChange.fromJson(e))
              .toList() ??
          [],
      unstaged:
          (json['unstaged'] as List?)
              ?.map((e) => GitChange.fromJson(e))
              .toList() ??
          [],
      untracked:
          (json['untracked'] as List?)
              ?.map((e) => GitChange.fromJson(e))
              .toList() ??
          [],
    );
  }

  int get totalChanges => staged.length + unstaged.length + untracked.length;
}

class GitDiff {
  final String file;
  final String language;
  final String diff;
  final String stat;

  GitDiff({
    required this.file,
    required this.language,
    required this.diff,
    required this.stat,
  });

  factory GitDiff.fromJson(Map<String, dynamic> json) {
    return GitDiff(
      file: json['file'] as String,
      language: json['language'] as String? ?? '',
      diff: json['diff'] as String? ?? '',
      stat: json['stat'] as String? ?? '',
    );
  }
}

class Commit {
  final String sha;
  final String short;
  final String author;
  final String date;
  final String subject;
  final int files;
  final int insertions;
  final int deletions;

  Commit({
    required this.sha,
    required this.short,
    required this.author,
    required this.date,
    required this.subject,
    required this.files,
    required this.insertions,
    required this.deletions,
  });

  factory Commit.fromJson(Map<String, dynamic> json) {
    return Commit(
      sha: json['sha'] as String? ?? '',
      short: json['short'] as String? ?? '',
      author: json['author'] as String? ?? '',
      date: json['date'] as String? ?? '',
      subject: json['subject'] as String? ?? '',
      files: json['files'] as int? ?? 0,
      insertions: json['insertions'] as int? ?? 0,
      deletions: json['deletions'] as int? ?? 0,
    );
  }

  String get timeAgo => _timeAgo(date);
}

/// Formats an ISO-8601 commit date the way the rest of the app formats times.
String _timeAgo(String iso) {
  if (iso.isEmpty) return '';
  try {
    final d = DateTime.parse(iso);
    final diff = DateTime.now().toUtc().difference(d.toUtc());
    if (diff.inSeconds < 60) return 'just now';
    if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
    if (diff.inHours < 24) return '${diff.inHours}h ago';
    if (diff.inDays < 7) return '${diff.inDays}d ago';
    return '${d.month}/${d.day}';
  } catch (_) {
    return iso;
  }
}

class GitLog {
  final String root;
  final String branch;

  /// What the branch was compared against — empty when there was nothing to
  /// compare against.
  final String base;

  /// 'branch' for base..HEAD, 'all' for the whole history.
  final String scope;
  final List<Commit> commits;
  final bool hasMore;

  GitLog({
    required this.root,
    required this.branch,
    required this.base,
    required this.scope,
    required this.commits,
    required this.hasMore,
  });

  factory GitLog.fromJson(Map<String, dynamic> json) {
    return GitLog(
      root: json['root'] as String? ?? '',
      branch: json['branch'] as String? ?? '',
      base: json['base'] as String? ?? '',
      scope: json['scope'] as String? ?? 'all',
      commits:
          (json['commits'] as List?)?.map((e) => Commit.fromJson(e)).toList() ??
          [],
      hasMore: json['has_more'] as bool? ?? false,
    );
  }
}

class CommitFile {
  final String path;

  /// The old path, on a rename or a copy.
  final String from;
  final String status;
  final int insertions;
  final int deletions;

  CommitFile({
    required this.path,
    required this.from,
    required this.status,
    required this.insertions,
    required this.deletions,
  });

  factory CommitFile.fromJson(Map<String, dynamic> json) {
    return CommitFile(
      path: json['path'] as String? ?? '',
      from: json['from'] as String? ?? '',
      status: json['status'] as String? ?? 'M',
      insertions: json['insertions'] as int? ?? 0,
      deletions: json['deletions'] as int? ?? 0,
    );
  }

  String get fileName => path.split('/').last;
}

class GitChanges {
  final String from;
  final String to;

  /// True when this is one commit rather than a range — only then are the
  /// commit's own subject, author, date and body filled in.
  final bool single;
  final List<CommitFile> files;
  final int insertions;
  final int deletions;
  final bool truncated;
  final String subject;
  final String author;
  final String date;
  final String body;
  final List<String> parents;

  GitChanges({
    required this.from,
    required this.to,
    required this.single,
    required this.files,
    required this.insertions,
    required this.deletions,
    required this.truncated,
    required this.subject,
    required this.author,
    required this.date,
    required this.body,
    required this.parents,
  });

  factory GitChanges.fromJson(Map<String, dynamic> json) {
    return GitChanges(
      from: json['from'] as String? ?? '',
      to: json['to'] as String? ?? '',
      single: json['single'] as bool? ?? false,
      files:
          (json['files'] as List?)
              ?.map((e) => CommitFile.fromJson(e))
              .toList() ??
          [],
      insertions: json['insertions'] as int? ?? 0,
      deletions: json['deletions'] as int? ?? 0,
      truncated: json['truncated'] as bool? ?? false,
      subject: json['subject'] as String? ?? '',
      author: json['author'] as String? ?? '',
      date: json['date'] as String? ?? '',
      body: json['body'] as String? ?? '',
      parents:
          (json['parents'] as List?)?.map((e) => e.toString()).toList() ?? [],
    );
  }

  String get timeAgo => _timeAgo(date);

  String get shortTo => to.length > 7 ? to.substring(0, 7) : to;

  String get shortFrom => from.length > 7 ? from.substring(0, 7) : from;
}

class Worktree {
  final String path;
  final String branch;
  final bool isMain;
  final String head;
  final String subject;

  /// ISO-8601 date of the last commit — what "last touched" is ordered by.
  final String date;
  final bool detached;
  final bool locked;
  final int ahead;
  final int behind;

  /// Number of changed files in that worktree.
  final int dirty;

  /// What ahead/behind were measured against — empty when nothing was found.
  final String base;

  Worktree({
    required this.path,
    required this.branch,
    required this.isMain,
    this.head = '',
    this.subject = '',
    this.date = '',
    this.detached = false,
    this.locked = false,
    this.ahead = 0,
    this.behind = 0,
    this.dirty = 0,
    this.base = '',
  });

  factory Worktree.fromJson(Map<String, dynamic> json) {
    return Worktree(
      path: json['path'] as String,
      branch: json['branch'] as String? ?? '',
      isMain: json['is_main'] as bool? ?? false,
      head: json['head'] as String? ?? '',
      subject: json['subject'] as String? ?? '',
      date: json['date'] as String? ?? '',
      detached: json['detached'] as bool? ?? false,
      locked: json['locked'] as bool? ?? false,
      ahead: json['ahead'] as int? ?? 0,
      behind: json['behind'] as int? ?? 0,
      dirty: json['dirty'] as int? ?? 0,
      base: json['base'] as String? ?? '',
    );
  }

  String get shortPath {
    final parts = path.split('/');
    if (parts.length <= 3) return path;
    return '.../${parts.sublist(parts.length - 2).join('/')}';
  }

  String get timeAgo => _timeAgo(date);
}

/// Whether a response says the request was carried out.
///
/// Not every write answers 200: a write with nothing to report answers 204,
/// which `POST /api/settings` does. Reading that as a failure is what left the
/// sort toggle looking dead — the daemon had stored the mode and the app had
/// decided it hadn't.
bool isSuccess(int statusCode) => statusCode >= 200 && statusCode < 300;

/// Most recently committed first. Worktrees with no date — pruned, or past the
/// detail cap the daemon enforces — keep their listing order at the end.
List<Worktree> sortWorktreesByLastTouched(List<Worktree> worktrees) {
  final order = {for (var i = 0; i < worktrees.length; i++) worktrees[i].path: i};
  final sorted = [...worktrees];
  sorted.sort((a, b) {
    final da = DateTime.tryParse(a.date)?.toUtc();
    final db = DateTime.tryParse(b.date)?.toUtc();
    if (da == null || db == null) {
      if (da == db) return order[a.path]!.compareTo(order[b.path]!);
      return da == null ? 1 : -1;
    }
    final byDate = db.compareTo(da);
    return byDate != 0 ? byDate : order[a.path]!.compareTo(order[b.path]!);
  });
  return sorted;
}
