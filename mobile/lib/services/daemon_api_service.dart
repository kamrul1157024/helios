import 'dart:async';
import 'dart:convert';
import 'package:cryptography/cryptography.dart';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import '../models/notification.dart';
import '../models/provider.dart';
import '../models/session.dart';
import '../models/message.dart';

/// Callback fired when an SSE event arrives on this host.
typedef SSEEventCallback = void Function(String hostId, SSEEvent event);

class DaemonAPIService extends ChangeNotifier {
  final String hostId;
  final String serverUrl;
  final String _deviceId;
  final Uint8List _privateKeySeed;

  // In-memory JWT cache — re-signed only when expired
  String? _cachedToken;
  DateTime? _tokenExpiresAt;

  http.Client? _client;
  Timer? _reconnectTimer;
  Timer? _pollTimer;
  Timer? _sessionDebounce;
  Timer? _notificationDebounce;
  bool _running = false;
  bool _connected = false;
  bool _isActiveHost = false;
  int _consecutiveFailures = 0;
  static const _offlineThreshold = 2;

  List<HeliosNotification> _notifications = [];
  List<HeliosNotification> get notifications => _notifications;
  List<Session> _sessions = [];
  List<Session> get sessions => _sessions;
  bool get connected => _connected;
  bool get isOffline => _consecutiveFailures >= _offlineThreshold;

  bool _notificationsLoaded = false;
  bool get notificationsLoaded => _notificationsLoaded;
  bool _sessionsLoaded = false;
  bool get sessionsLoaded => _sessionsLoaded;

  // Track last fetch params so polling/SSE refreshes use the same filters
  String? _lastSessionQ;
  String? _lastSessionFilter;
  String? _lastSessionCwd;

  List<SlashCommand> _commands = [];
  List<SlashCommand> get commands => _commands;

  TmuxStatus? _tmuxStatus;
  TmuxStatus? get tmuxStatus => _tmuxStatus;

  bool _pluginBannerDismissed = false;
  bool get pluginBannerDismissed => _pluginBannerDismissed;

  bool _tmuxMissingBannerDismissed = false;
  bool get tmuxMissingBannerDismissed => _tmuxMissingBannerDismissed;

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
    required String deviceId,
    required Uint8List privateKeySeed,
  })  : _deviceId = deviceId,
        _privateKeySeed = privateKeySeed;

  // ==================== Auth Helpers ====================

  /// Returns a cached JWT if still valid (>5 min until expiry), otherwise
  /// signs a fresh one. ~1 sign per hour during active use.
  Future<String> getToken() async {
    final now = DateTime.now().toUtc();
    if (_cachedToken != null &&
        _tokenExpiresAt != null &&
        _tokenExpiresAt!.isAfter(now.add(const Duration(minutes: 5)))) {
      return _cachedToken!;
    }
    _cachedToken = await _signJWT();
    _tokenExpiresAt = now.add(const Duration(hours: 1));
    return _cachedToken!;
  }

  void _invalidateToken() {
    _cachedToken = null;
    _tokenExpiresAt = null;
  }

  Future<String> _signJWT() async {
    final header = {'alg': 'EdDSA', 'typ': 'JWT', 'kid': _deviceId};
    final now = DateTime.now().toUtc().millisecondsSinceEpoch ~/ 1000;
    final payload = {
      'iat': now,
      'exp': now + 3600,
      'sub': 'helios-client',
    };

    final encodedHeader = _base64urlEncode(
        Uint8List.fromList(utf8.encode(jsonEncode(header))));
    final encodedPayload = _base64urlEncode(
        Uint8List.fromList(utf8.encode(jsonEncode(payload))));
    final signingInput = '$encodedHeader.$encodedPayload';

    final algorithm = Ed25519();
    final keyPair = await algorithm.newKeyPairFromSeed(_privateKeySeed);
    final signature = await algorithm.sign(
      utf8.encode(signingInput),
      keyPair: keyPair,
    );

    final encodedSignature =
        _base64urlEncode(Uint8List.fromList(signature.bytes));
    return '$signingInput.$encodedSignature';
  }

  String _base64urlEncode(Uint8List bytes) {
    return base64Url.encode(bytes).replaceAll('=', '');
  }

  Future<Map<String, String>> _authHeaders() async {
    final token = await getToken();
    return {'Authorization': 'Bearer $token'};
  }

  Future<http.Response> _authGet(String path) async {
    return http.get(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(),
    );
  }

  Future<http.Response> _authPost(String path, {Map<String, dynamic>? body}) async {
    return http.post(
      Uri.parse('$serverUrl$path'),
      headers: {
        ...await _authHeaders(),
        'Content-Type': 'application/json',
      },
      body: body != null ? jsonEncode(body) : null,
    );
  }

  Future<http.Response> _authPatch(String path, {Map<String, dynamic>? body}) async {
    return http.patch(
      Uri.parse('$serverUrl$path'),
      headers: {
        ...await _authHeaders(),
        'Content-Type': 'application/json',
      },
      body: body != null ? jsonEncode(body) : null,
    );
  }

  Future<http.Response> _authDelete(String path) async {
    return http.delete(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(),
    );
  }

  // ==================== Lifecycle ====================

  /// Start as the active host: SSE + fallback polling when SSE is down.
  Future<void> startActive() async {
    if (_running) return;
    _running = true;
    _isActiveHost = true;
    await _loadBannerState();
    await _loadSessionCache();
    _connect(); // fire-and-forget — SSE runs in background
  }

  /// Start as a background host: SSE only, no polling.
  Future<void> startBackground() async {
    if (_running) return;
    _running = true;
    _isActiveHost = false;
    await _loadBannerState();
    _connect(); // fire-and-forget — SSE runs in background
  }

  /// Promote from background to active (fetch data, start polling if SSE is down).
  void promote() async {
    _isActiveHost = true;
    await _loadSessionCache();
    fetchSessions();
    fetchNotifications();
    fetchHealth();
    fetchProviders();
    fetchCommands();
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
    _client?.close();
    _client = null;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _pollTimer?.cancel();
    _pollTimer = null;
    _sessionDebounce?.cancel();
    _sessionDebounce = null;
    _notificationDebounce?.cancel();
    _notificationDebounce = null;
    notifyListeners();
  }

  // ==================== Banner State ====================

  Future<void> _loadBannerState() async {
    final prefs = await SharedPreferences.getInstance();
    _pluginBannerDismissed = prefs.getBool('tmux_plugin_banner_dismissed_$hostId') ?? false;
    _tmuxMissingBannerDismissed = prefs.getBool('tmux_missing_banner_dismissed_$hostId') ?? false;
  }

  Future<void> dismissPluginBanner() async {
    _pluginBannerDismissed = true;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool('tmux_plugin_banner_dismissed_$hostId', true);
  }

  Future<void> dismissTmuxMissingBanner() async {
    _tmuxMissingBannerDismissed = true;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool('tmux_missing_banner_dismissed_$hostId', true);
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
      _sessions = list.map((s) => Session.fromJson(s as Map<String, dynamic>, hostId: hostId)).toList();
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

    _client?.close();
    _client = http.Client();

    try {
      final request = http.Request('GET', Uri.parse('$serverUrl/api/events'));
      request.headers.addAll({
        ...await _authHeaders(),
        'Accept': 'text/event-stream',
        'Cache-Control': 'no-cache',
      });

      final response = await _client!.send(request);

      if (response.statusCode == 401) {
        debugPrint('[$hostId] SSE auth failed — refreshing token');
        _invalidateToken();
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
      // SSE is healthy — stop fallback polling
      _pollTimer?.cancel();
      _pollTimer = null;
      notifyListeners();

      String buffer = '';
      String currentEvent = '';

      await for (final chunk in response.stream.transform(utf8.decoder)) {
        if (!_running) break;

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
      if (!_running) return;
      debugPrint('[$hostId] SSE error: $e');
      _consecutiveFailures++;
    }

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
    debugPrint('[$hostId] reconnecting in ${delay.inSeconds}s (failures=$_consecutiveFailures)');
    _reconnectTimer = Timer(delay, _connect);
  }

  /// Force an immediate reconnect attempt, resetting failure count.
  void reconnect() {
    _reconnectTimer?.cancel();
    _consecutiveFailures = 0;
    notifyListeners();
    _connect();
  }

  // ==================== Health ====================

  Future<void> fetchHealth() async {
    try {
      final resp = await _authGet('/api/health');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        if (data['tmux'] != null) {
          _tmuxStatus = TmuxStatus.fromJson(data['tmux']);
          notifyListeners();
        }
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch health: $e');
    }
  }

  // ==================== Notifications API ====================

  Future<void> fetchNotifications() async {
    try {
      final resp = await _authGet('/api/notifications');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['notifications'] as List?) ?? [];
        _notifications = list.map((n) => HeliosNotification.fromJson(n, hostId: hostId)).toList();
        _notificationsLoaded = true;
        notifyListeners();
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch notifications: $e');
    }
  }

  Future<bool> sendAction(String id, Map<String, dynamic> body) async {
    try {
      final resp = await _authPost('/api/notifications/$id/action', body: body);
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to send action: $e');
    }
    return false;
  }

  Future<bool> dismissNotification(String id) async {
    try {
      final resp = await _authPost('/api/notifications/$id/dismiss');
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to dismiss: $e');
    }
    return false;
  }

  Future<bool> batchAction(List<String> ids, Map<String, dynamic> action) async {
    try {
      final resp = await _authPost('/api/notifications/batch', body: {
        'notification_ids': ids,
        'action': action,
      });
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

  Future<void> fetchSessions({String? q, String? status, String? filter, String? cwd, bool updateFilters = false}) async {
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
      if (effectiveFilter != null && effectiveFilter.isNotEmpty) params['filter'] = effectiveFilter;
      if (effectiveCwd != null && effectiveCwd.isNotEmpty) params['cwd'] = effectiveCwd;

      final queryString = params.entries.map((e) => '${e.key}=${Uri.encodeComponent(e.value)}').join('&');
      final path = queryString.isNotEmpty ? '/api/sessions?$queryString' : '/api/sessions';

      final resp = await _authGet(path);
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['sessions'] as List?) ?? [];
        _sessions = list.map((s) => Session.fromJson(s, hostId: hostId)).toList();
        _sessionsLoaded = true;
        notifyListeners();
        // Cache the full unfiltered list for instant display on next launch
        final isUnfiltered = (effectiveQ == null || effectiveQ.isEmpty) &&
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
      final resp = await _authGet('/api/sessions/directories');
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

  Future<TranscriptResult?> fetchTranscript(String sessionId, {int limit = 200, int offset = 0}) async {
    try {
      final resp = await _authGet('/api/sessions/$sessionId/transcript?limit=$limit&offset=$offset');
      if (resp.statusCode == 200) {
        return TranscriptResult.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch transcript: $e');
    }
    return null;
  }

  Future<List<Subagent>> fetchSubagents(String sessionId) async {
    try {
      final resp = await _authGet('/api/sessions/$sessionId/subagents');
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

  Future<bool> sendSessionPrompt(String sessionId, String message) async {
    try {
      final resp = await _authPost('/api/sessions/$sessionId/send', body: {'message': message});
      debugPrint('[$hostId] sendSessionPrompt: status=${resp.statusCode} body=${resp.body}');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to send prompt: $e');
    }
    return false;
  }

  Future<bool> stopSession(String sessionId) async {
    try {
      final resp = await _authPost('/api/sessions/$sessionId/stop');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to stop session: $e');
    }
    return false;
  }

  Future<bool> suspendSession(String sessionId) async {
    try {
      final resp = await _authPost('/api/sessions/$sessionId/suspend');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to suspend session: $e');
    }
    return false;
  }

  Future<bool> resumeSession(String sessionId) async {
    try {
      final resp = await _authPost('/api/sessions/$sessionId/resume');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to resume session: $e');
    }
    return false;
  }

  Future<bool> patchSession(String sessionId, {bool? pinned, bool? archived, String? title}) async {
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
      final resp = await _authPatch('/api/sessions/$sessionId', body: body);
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
      final resp = await _authDelete('/api/sessions/$sessionId');
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

  // ==================== Commands API ====================

  Future<void> fetchCommands() async {
    try {
      final resp = await _authGet('/api/commands');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['commands'] as List?) ?? [];
        _commands = list.map((c) => SlashCommand.fromJson(c)).toList();
        notifyListeners();
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to fetch commands: $e');
    }
  }

  // ==================== Settings API ====================

  /// Fetch all settings and personas from the backend.
  Future<Map<String, dynamic>?> getSettings() async {
    try {
      final resp = await _authGet('/api/settings');
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
      final resp = await _authPost('/api/settings', body: settings);
      return resp.statusCode == 200;
    } catch (e) {
      debugPrint('[$hostId] updateSettings error: $e');
    }
    return false;
  }

  // ==================== Providers & Models API ====================

  Future<void> fetchProviders() async {
    try {
      final resp = await _authGet('/api/providers');
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

  Future<List<ModelInfo>> fetchModels(String providerId, {bool forceRefresh = false}) async {
    if (!forceRefresh && hasModelCache(providerId)) {
      return _modelCache[providerId]!;
    }

    try {
      final endpoint = forceRefresh
          ? '/api/providers/$providerId/models/refresh'
          : '/api/providers/$providerId/models';
      final resp = forceRefresh
          ? await _authPost(endpoint)
          : await _authGet(endpoint);
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
      final resp = await _authGet('/api/files?path=${Uri.encodeComponent(path)}');
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
      final resp = await _authGet('/api/file?path=${Uri.encodeComponent(path)}');
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
    required String prompt,
    String? model,
    String? cwd,
    bool dangerouslySkipPermissions = false,
  }) async {
    try {
      final body = <String, dynamic>{
        'provider': provider,
        'prompt': prompt,
      };
      if (model != null && model.isNotEmpty) body['model'] = model;
      if (cwd != null && cwd.isNotEmpty) body['cwd'] = cwd;
      if (dangerouslySkipPermissions) body['dangerously_skip_permissions'] = true;

      final resp = await _authPost('/api/sessions', body: body);
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
      final resp = await _authGet('/api/git/status?path=${Uri.encodeComponent(path)}');
      if (resp.statusCode == 200) {
        return GitStatus.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get git status for $path: $e');
    }
    return null;
  }

  Future<GitDiff?> gitDiff(String path, String file, {bool staged = false}) async {
    try {
      final stagedParam = staged ? '&staged=true' : '';
      final resp = await _authGet(
        '/api/git/diff?path=${Uri.encodeComponent(path)}&file=${Uri.encodeComponent(file)}$stagedParam',
      );
      if (resp.statusCode == 200) {
        return GitDiff.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('[$hostId] Failed to get git diff for $file: $e');
    }
    return null;
  }

  Future<List<Worktree>> gitWorktrees(String path) async {
    try {
      final resp = await _authGet('/api/git/worktrees?path=${Uri.encodeComponent(path)}');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        return (data['worktrees'] as List?)?.map((e) => Worktree.fromJson(e)).toList() ?? [];
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
    if (size < 1024 * 1024 * 1024) return '${(size / (1024 * 1024)).toStringAsFixed(1)} MB';
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
      entries: list.map((e) => FileEntry.fromJson(e as Map<String, dynamic>)).toList(),
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

class SlashCommand {
  final String name;
  final String description;
  final String icon;

  SlashCommand({required this.name, required this.description, required this.icon});

  factory SlashCommand.fromJson(Map<String, dynamic> json) {
    return SlashCommand(
      name: json['name'] as String,
      description: json['description'] as String? ?? '',
      icon: json['icon'] as String? ?? '',
    );
  }
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
      staged: (json['staged'] as List?)?.map((e) => GitChange.fromJson(e)).toList() ?? [],
      unstaged: (json['unstaged'] as List?)?.map((e) => GitChange.fromJson(e)).toList() ?? [],
      untracked: (json['untracked'] as List?)?.map((e) => GitChange.fromJson(e)).toList() ?? [],
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

class Worktree {
  final String path;
  final String branch;
  final bool isMain;

  Worktree({required this.path, required this.branch, required this.isMain});

  factory Worktree.fromJson(Map<String, dynamic> json) {
    return Worktree(
      path: json['path'] as String,
      branch: json['branch'] as String? ?? '',
      isMain: json['is_main'] as bool? ?? false,
    );
  }

  String get shortPath {
    final parts = path.split('/');
    if (parts.length <= 3) return path;
    return '.../${parts.sublist(parts.length - 2).join('/')}';
  }
}

class TmuxStatus {
  final bool installed;
  final String version;
  final bool serverRunning;
  final bool resurrectPlugin;
  final bool continuumPlugin;
  final bool sessionMgmtReady;

  TmuxStatus({
    required this.installed,
    required this.version,
    required this.serverRunning,
    required this.resurrectPlugin,
    required this.continuumPlugin,
    required this.sessionMgmtReady,
  });

  factory TmuxStatus.fromJson(Map<String, dynamic> json) {
    return TmuxStatus(
      installed: json['installed'] as bool? ?? false,
      version: json['version'] as String? ?? '',
      serverRunning: json['server_running'] as bool? ?? false,
      resurrectPlugin: json['resurrect_plugin'] as bool? ?? false,
      continuumPlugin: json['continuum_plugin'] as bool? ?? false,
      sessionMgmtReady: json['session_mgmt_ready'] as bool? ?? false,
    );
  }
}
