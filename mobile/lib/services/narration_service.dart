import 'dart:async';
import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'voice_service.dart';

/// Host connection info needed to open a reporter SSE stream.
class ReporterHost {
  final String hostId;
  final String serverUrl;
  final String cookie;

  const ReporterHost({
    required this.hostId,
    required this.serverUrl,
    required this.cookie,
  });
}

/// Manages AI-powered narration via the backend's push-based Reporter SSE.
///
/// Only one mode is active at a time: global (multiple hosts) or session
/// (single host, single session). Calling either connect method disconnects
/// everything first.
class NarrationService {
  NarrationService._();
  static final instance = NarrationService._();

  final Map<String, _ReporterConnection> _connections = {};

  /// Connect globally to all provided hosts (no session filter).
  /// Disconnects any existing connections first.
  void connectGlobal(List<ReporterHost> hosts) {
    disconnectAll();
    for (final host in hosts) {
      _connections[host.hostId] = _ReporterConnection(
        serverUrl: host.serverUrl,
        cookie: host.cookie,
        sessionId: null,
        onNarration: _handleNarration,
      )..connect();
    }
  }

  /// Connect to a single session on its host.
  /// Disconnects any existing connections first.
  void connectSession({
    required ReporterHost host,
    required String sessionId,
  }) {
    disconnectAll();
    final key = '${host.hostId}:$sessionId';
    _connections[key] = _ReporterConnection(
      serverUrl: host.serverUrl,
      cookie: host.cookie,
      sessionId: sessionId,
      onNarration: _handleNarration,
    )..connect();
  }

  /// Disconnect all reporter connections.
  void disconnectAll() {
    for (final conn in _connections.values) {
      conn.close();
    }
    _connections.clear();
  }

  /// Whether any connections are active.
  bool get isActive => _connections.isNotEmpty;

  void _handleNarration(String narration) {
    if (narration.isNotEmpty) {
      VoiceService.instance.speak(narration);
    }
  }
}

/// A single SSE connection to GET /api/reporter.
class _ReporterConnection {
  final String serverUrl;
  final String cookie;
  final String? sessionId;
  final void Function(String narration) onNarration;

  http.Client? _client;
  bool _running = false;
  Timer? _reconnectTimer;

  _ReporterConnection({
    required this.serverUrl,
    required this.cookie,
    required this.sessionId,
    required this.onNarration,
  });

  void connect() {
    _running = true;
    _doConnect();
  }

  Future<void> _doConnect() async {
    if (!_running) return;

    _client?.close();
    _client = http.Client();

    try {
      var path = '/api/reporter';
      if (sessionId != null) {
        path += '?session=$sessionId';
      }

      final request = http.Request('GET', Uri.parse('$serverUrl$path'));
      request.headers.addAll({
        'Cookie': 'helios_token=$cookie',
        'Accept': 'text/event-stream',
        'Cache-Control': 'no-cache',
      });

      final response = await _client!.send(request);

      if (response.statusCode != 200) {
        debugPrint('[NarrationService] reporter SSE failed: HTTP ${response.statusCode}');
        _scheduleReconnect();
        return;
      }

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
            if (currentEvent == 'narration') {
              try {
                final data = jsonDecode(line.substring(6));
                final text = data['text'] as String? ?? '';
                if (text.isNotEmpty) {
                  onNarration(text);
                }
              } catch (e) {
                debugPrint('[NarrationService] parse error: $e');
              }
            }
            currentEvent = '';
          }
        }
      }
    } catch (e) {
      if (!_running) return;
      debugPrint('[NarrationService] reporter SSE error: $e');
    }

    if (_running) _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (!_running) return;
    _reconnectTimer?.cancel();
    _reconnectTimer = Timer(const Duration(seconds: 5), _doConnect);
  }

  void close() {
    _running = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _client?.close();
    _client = null;
  }
}
