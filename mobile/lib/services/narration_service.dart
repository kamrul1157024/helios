import 'dart:async';
import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/narration_event.dart';
import 'voice_service.dart';

/// Manages AI-powered narration via the backend's /api/small-model-text endpoint.
///
/// Events are batched per session with a 2-second debounce window, then sent to
/// the backend for Haiku-generated narration text, which is spoken via TTS.
class NarrationService {
  NarrationService._();
  static final instance = NarrationService._();

  // Persisted settings
  bool _aiNarrationEnabled = true;
  String _customPrompt = '';

  bool get aiNarrationEnabled => _aiNarrationEnabled;
  String get customPrompt => _customPrompt;

  // Debounce state per session
  final Map<String, List<NarrationEvent>> _pendingEvents = {};
  final Map<String, Timer?> _debounceTimers = {};
  final Map<String, String?> _sessionContexts = {};

  /// Callback to call the backend. Set by the app on startup.
  Future<String?> Function(
    String hostId,
    List<NarrationEvent> events,
    String? sessionContext,
    String? systemPrompt,
  )? onNarrate;

  Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();
    _aiNarrationEnabled = prefs.getBool('ai_narration_enabled') ?? true;
    _customPrompt = prefs.getString('narrator_prompt') ?? '';
  }

  Future<void> setAINarrationEnabled(bool value) async {
    _aiNarrationEnabled = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool('ai_narration_enabled', value);
    if (!value) clear();
  }

  Future<void> setCustomPrompt(String value) async {
    _customPrompt = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('narrator_prompt', value);
  }

  /// Queue an event for narration. Events are batched per session with 2s debounce.
  /// [sessionContext] is the last user message — only pass for global voice mode.
  void addEvent(String hostId, String sessionId, NarrationEvent event,
      {String? sessionContext}) {
    if (!_aiNarrationEnabled) return;

    final key = '$hostId:$sessionId';
    _pendingEvents.putIfAbsent(key, () => []);
    _pendingEvents[key]!.add(event);

    if (sessionContext != null) {
      _sessionContexts[key] = sessionContext;
    }

    _debounceTimers[key]?.cancel();
    _debounceTimers[key] = Timer(const Duration(seconds: 2), () {
      _flush(hostId, sessionId);
    });
  }

  /// Flush pending events: call /api/small-model-text, speak result.
  Future<void> _flush(String hostId, String sessionId) async {
    final key = '$hostId:$sessionId';
    final events = _pendingEvents.remove(key);
    final context = _sessionContexts.remove(key);
    _debounceTimers.remove(key);
    if (events == null || events.isEmpty) return;

    if (onNarrate == null) return;

    try {
      final narration = await onNarrate!(
        hostId,
        events,
        context,
        _customPrompt.isNotEmpty ? _customPrompt : null,
      );
      if (narration != null && narration.isNotEmpty) {
        VoiceService.instance.speak(narration);
      }
    } catch (e) {
      debugPrint('Narration flush error: $e');
    }
  }

  /// Clear all pending events (e.g., when voice mode is turned off).
  void clear() {
    for (final timer in _debounceTimers.values) {
      timer?.cancel();
    }
    _debounceTimers.clear();
    _pendingEvents.clear();
    _sessionContexts.clear();
  }
}
