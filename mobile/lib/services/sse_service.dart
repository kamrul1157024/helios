import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import '../models/notification.dart';
import '../models/session.dart';
import '../models/message.dart';
import 'auth_service.dart';

class SSEService extends ChangeNotifier {
  AuthService? _auth;
  http.Client? _client;
  Timer? _reconnectTimer;
  bool _running = false;
  bool _connected = false;

  List<HeliosNotification> _notifications = [];
  List<HeliosNotification> get notifications => _notifications;
  List<Session> _sessions = [];
  List<Session> get sessions => _sessions;
  bool get connected => _connected;

  final _eventController = StreamController<SSEEvent>.broadcast();
  Stream<SSEEvent> get events => _eventController.stream;

  void attach(AuthService auth) {
    _auth = auth;
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
        notifyListeners();
      }
    } catch (e) {
      debugPrint('Failed to fetch notifications: $e');
    }
  }

  /// Start the persistent SSE connection.
  Future<void> start() async {
    if (_running) return;
    _running = true;
    await _connect();
  }

  /// Stop the SSE connection.
  void stop() {
    _running = false;
    _connected = false;
    _client?.close();
    _client = null;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
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
    // Refresh sessions on session events
    if (type == 'session_status') {
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
