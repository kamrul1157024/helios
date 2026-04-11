import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import '../models/notification.dart';
import '../models/provider.dart';
import '../models/session.dart';
import '../models/message.dart';
import 'auth_service.dart';

class DaemonAPIService extends ChangeNotifier {
  AuthService? _auth;
  http.Client? _client;
  Timer? _reconnectTimer;
  Timer? _pollTimer;
  bool _running = false;
  bool _connected = false;

  List<HeliosNotification> _notifications = [];
  List<HeliosNotification> get notifications => _notifications;
  List<Session> _sessions = [];
  List<Session> get sessions => _sessions;
  bool get connected => _connected;

  bool _notificationsLoaded = false;
  bool get notificationsLoaded => _notificationsLoaded;
  bool _sessionsLoaded = false;
  bool get sessionsLoaded => _sessionsLoaded;

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

  static const _pluginBannerDismissedKey = 'tmux_plugin_banner_dismissed';
  static const _tmuxMissingBannerDismissedKey = 'tmux_missing_banner_dismissed';

  final _eventController = StreamController<SSEEvent>.broadcast();
  Stream<SSEEvent> get events => _eventController.stream;

  void attach(AuthService auth) {
    _auth = auth;
    _loadPluginBannerState();
  }

  Future<void> _loadPluginBannerState() async {
    final prefs = await SharedPreferences.getInstance();
    _pluginBannerDismissed = prefs.getBool(_pluginBannerDismissedKey) ?? false;
    _tmuxMissingBannerDismissed = prefs.getBool(_tmuxMissingBannerDismissedKey) ?? false;
  }

  Future<void> dismissPluginBanner() async {
    _pluginBannerDismissed = true;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_pluginBannerDismissedKey, true);
  }

  Future<void> dismissTmuxMissingBanner() async {
    _tmuxMissingBannerDismissed = true;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_tmuxMissingBannerDismissedKey, true);
  }

  /// Fetch health status including tmux info.
  Future<void> fetchHealth() async {
    if (_auth == null || !_auth!.isAuthenticated) return;
    try {
      final resp = await _auth!.authGet('/api/health');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        if (data['tmux'] != null) {
          _tmuxStatus = TmuxStatus.fromJson(data['tmux']);
          notifyListeners();
        }
      }
    } catch (e) {
      debugPrint('Failed to fetch health: $e');
    }
  }

  /// Fetch all notifications from the API.
  Future<void> fetchNotifications() async {
    if (_auth == null || !_auth!.isAuthenticated) return;
    try {
      final resp = await _auth!.authGet('/api/notifications');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['notifications'] as List?) ?? [];
        _notifications = list.map((n) => HeliosNotification.fromJson(n)).toList();
        _notificationsLoaded = true;
        notifyListeners();
      }
    } catch (e) {
      debugPrint('Failed to fetch notifications: $e');
    }
  }

  /// Start the persistent SSE connection and session polling.
  Future<void> start() async {
    if (_running) return;
    _running = true;
    _startPolling();
    await _connect();
  }

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
    _client?.close();
    _client = null;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _pollTimer?.cancel();
    _pollTimer = null;
    notifyListeners();
  }

  Future<void> _connect() async {
    if (!_running || _auth == null || !_auth!.isAuthenticated) return;

    _client?.close();
    _client = http.Client();

    try {
      final request = http.Request('GET', Uri.parse('${_auth!.serverUrl}/api/events'));
      request.headers.addAll({
        'Cookie': 'helios_token=${_auth!.cookie}',
        'Accept': 'text/event-stream',
        'Cache-Control': 'no-cache',
      });

      final response = await _client!.send(request);

      if (response.statusCode != 200) {
        debugPrint('SSE connect failed: HTTP ${response.statusCode}');
        _scheduleReconnect();
        return;
      }

      _connected = true;
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
      debugPrint('SSE error: $e');
    }

    _connected = false;
    notifyListeners();
    _scheduleReconnect();
  }

  void _handleEvent(String type, dynamic data) {
    _eventController.add(SSEEvent(type, data));
    // Refresh notifications on any event
    fetchNotifications();
    // Refresh sessions on any session-relevant event
    if (type == 'session_status' ||
        type == 'notification' ||
        type == 'notification_resolved' ||
        type == 'subagent_status') {
      fetchSessions();
    }
  }

  void _scheduleReconnect() {
    if (!_running) return;
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(const Duration(seconds: 3), _connect);
  }

  /// Send an action for any notification type.
  /// The body is type-specific — each card widget builds it.
  Future<bool> sendAction(String id, Map<String, dynamic> body) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/notifications/$id/action', body: body);
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to send action: $e');
    }
    return false;
  }

  /// Dismiss a notification.
  Future<bool> dismissNotification(String id) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/notifications/$id/dismiss');
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to dismiss: $e');
    }
    return false;
  }

  /// Batch action — sends the same action body to multiple notifications.
  Future<bool> batchAction(List<String> ids, Map<String, dynamic> action) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/notifications/batch', body: {
        'notification_ids': ids,
        'action': action,
      });
      if (resp.statusCode == 200) {
        await fetchNotifications();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to batch action: $e');
    }
    return false;
  }

  // ==================== Session API ====================

  Future<void> fetchSessions() async {
    if (_auth == null || !_auth!.isAuthenticated) return;
    try {
      final resp = await _auth!.authGet('/api/sessions');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['sessions'] as List?) ?? [];
        _sessions = list.map((s) => Session.fromJson(s)).toList();
        _sessionsLoaded = true;
        notifyListeners();
      }
    } catch (e) {
      debugPrint('Failed to fetch sessions: $e');
    }
  }

  Future<TranscriptResult?> fetchTranscript(String sessionId, {int limit = 200, int offset = 0}) async {
    if (_auth == null) return null;
    try {
      final resp = await _auth!.authGet('/api/sessions/$sessionId/transcript?limit=$limit&offset=$offset');
      if (resp.statusCode == 200) {
        return TranscriptResult.fromJson(jsonDecode(resp.body));
      }
    } catch (e) {
      debugPrint('Failed to fetch transcript: $e');
    }
    return null;
  }

  Future<List<Subagent>> fetchSubagents(String sessionId) async {
    if (_auth == null) return [];
    try {
      final resp = await _auth!.authGet('/api/sessions/$sessionId/subagents');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['subagents'] as List?) ?? [];
        return list.map((s) => Subagent.fromJson(s)).toList();
      }
    } catch (e) {
      debugPrint('Failed to fetch subagents: $e');
    }
    return [];
  }

  Future<bool> sendSessionPrompt(String sessionId, String message) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/sessions/$sessionId/send', body: {'message': message});
      debugPrint('sendSessionPrompt: status=${resp.statusCode} body=${resp.body}');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to send prompt: $e');
    }
    return false;
  }

  Future<bool> stopSession(String sessionId) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/sessions/$sessionId/stop');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to stop session: $e');
    }
    return false;
  }

  Future<bool> suspendSession(String sessionId) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/sessions/$sessionId/suspend');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to suspend session: $e');
    }
    return false;
  }

  Future<bool> resumeSession(String sessionId) async {
    if (_auth == null) return false;
    try {
      final resp = await _auth!.authPost('/api/sessions/$sessionId/resume');
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to resume session: $e');
    }
    return false;
  }

  // ==================== Commands API ====================

  Future<void> fetchCommands() async {
    if (_auth == null || !_auth!.isAuthenticated) return;
    try {
      final resp = await _auth!.authGet('/api/commands');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['commands'] as List?) ?? [];
        _commands = list.map((c) => SlashCommand.fromJson(c)).toList();
        notifyListeners();
      }
    } catch (e) {
      debugPrint('Failed to fetch commands: $e');
    }
  }

  // ==================== Providers & Models API ====================

  Future<void> fetchProviders() async {
    if (_auth == null || !_auth!.isAuthenticated) return;
    try {
      final resp = await _auth!.authGet('/api/providers');
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final list = (data['providers'] as List?) ?? [];
        _providers = list.map((p) => ProviderInfo.fromJson(p)).toList();
        _providersLoaded = true;
        notifyListeners();
      }
    } catch (e) {
      debugPrint('Failed to fetch providers: $e');
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

    if (_auth == null || !_auth!.isAuthenticated) return [];
    try {
      final endpoint = forceRefresh
          ? '/api/providers/$providerId/models/refresh'
          : '/api/providers/$providerId/models';
      final resp = forceRefresh
          ? await _auth!.authPost(endpoint)
          : await _auth!.authGet(endpoint);
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
      debugPrint('Failed to fetch models for $providerId: $e');
    }
    return _modelCache[providerId] ?? [];
  }

  Future<bool> createSession({
    required String provider,
    required String prompt,
    String? model,
    String? cwd,
    bool dangerouslySkipPermissions = false,
  }) async {
    if (_auth == null) return false;
    try {
      final body = <String, dynamic>{
        'provider': provider,
        'prompt': prompt,
      };
      if (model != null && model.isNotEmpty) body['model'] = model;
      if (cwd != null && cwd.isNotEmpty) body['cwd'] = cwd;
      if (dangerouslySkipPermissions) body['dangerously_skip_permissions'] = true;

      final resp = await _auth!.authPost('/api/sessions', body: body);
      if (resp.statusCode == 200) {
        await fetchSessions();
        return true;
      }
    } catch (e) {
      debugPrint('Failed to create session: $e');
    }
    return false;
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
