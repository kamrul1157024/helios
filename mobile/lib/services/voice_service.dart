import 'dart:async';
import 'dart:collection';
import 'package:flutter/foundation.dart';
import 'package:flutter_tts/flutter_tts.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:speech_to_text/speech_to_text.dart';
import 'package:url_launcher/url_launcher.dart';

class VoiceService {
  VoiceService._();
  static final instance = VoiceService._();

  final SpeechToText _stt = SpeechToText();
  final FlutterTts _tts = FlutterTts();

  // Persisted settings
  bool _voiceInputEnabled = true;
  bool _autoReadEnabled = true;
  double _speechRate = 0.5;
  String _language = 'en-US';

  // Runtime state
  bool _sttAvailable = false;
  bool _isListening = false;
  bool _isSpeaking = false;
  bool _ttsReady = false;
  bool _globalVoiceActive = false;
  String? _activeSessionId;

  // Speech queue
  final Queue<String> _speechQueue = Queue<String>();
  bool _processingQueue = false;
  static const _maxQueueSize = 3;

  bool get voiceInputEnabled => _voiceInputEnabled;
  bool get autoReadEnabled => _autoReadEnabled;
  double get speechRate => _speechRate;
  String get language => _language;
  bool get ttsReady => _ttsReady;
  bool get sttAvailable => _sttAvailable;
  bool get isListening => _isListening;
  bool get isSpeaking => _isSpeaking;
  bool get globalVoiceActive => _globalVoiceActive;
  String? get activeSessionId => _activeSessionId;
  bool get sessionVoiceActive => _activeSessionId != null;

  VoidCallback? onStateChanged;

  static const _keyVoiceInput = 'voice_input_enabled';
  static const _keyAutoRead = 'voice_auto_read_enabled';
  static const _keySpeechRate = 'voice_speech_rate';
  static const _keyLanguage = 'voice_language';

  Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();
    _voiceInputEnabled = prefs.getBool(_keyVoiceInput) ?? true;
    _autoReadEnabled = prefs.getBool(_keyAutoRead) ?? true;
    _speechRate = prefs.getDouble(_keySpeechRate) ?? 0.5;
    _language = prefs.getString(_keyLanguage) ?? 'en-US';

    // Register TTS handlers early — engine setup is deferred to first speak()
    _tts.setCompletionHandler(() {
      _isSpeaking = false;
      onStateChanged?.call();
      _processNext();
    });
    _tts.setStartHandler(() {
      _isSpeaking = true;
      onStateChanged?.call();
    });
    _tts.setErrorHandler((msg) {
      debugPrint('[VoiceService] tts error: $msg');
      _isSpeaking = false;
      onStateChanged?.call();
      _processNext();
    });
  }

  /// Lazily initialize the TTS engine on first use.
  Future<bool> _ensureTtsReady() async {
    if (_ttsReady) return true;

    debugPrint('[VoiceService] _ensureTtsReady starting');

    for (var attempt = 1; attempt <= 3; attempt++) {
      debugPrint('[VoiceService] attempt $attempt/3');

      if (attempt > 1) {
        await Future.delayed(Duration(seconds: attempt));
      }

      // Try default engine first (no explicit setEngine call)
      try {
        final languages = await _tts.getLanguages;
        if (languages == null || (languages is List && languages.isEmpty)) {
          throw Exception('No languages available');
        }

        final langResult = await _tts.setLanguage(_language);
        if (langResult == 0) {
          await _tts.setLanguage('en');
        }
        await _tts.setSpeechRate(_speechRate);
        await _tts.awaitSpeakCompletion(false);
        _ttsReady = true;
        debugPrint('[VoiceService] TTS ready via default engine');
        return true;
      } catch (e) {
        debugPrint('[VoiceService] default engine failed: $e');
      }

      // If default fails, try each engine explicitly
      var engines = await _tts.getEngines;
      final engineList = (engines is List && engines.isNotEmpty)
          ? engines.map((e) => e.toString()).toList()
          : <String>[];

      for (final engine in engineList) {
        try {
          await _tts.setEngine(engine);
          await Future.delayed(const Duration(milliseconds: 500));

          final languages = await _tts.getLanguages;
          if (languages != null && (languages is List && languages.isNotEmpty)) {
            final langResult = await _tts.setLanguage(_language);
            if (langResult == 0) {
              await _tts.setLanguage('en');
            }
            await _tts.setSpeechRate(_speechRate);
            await _tts.awaitSpeakCompletion(false);
            _ttsReady = true;
            debugPrint('[VoiceService] TTS ready via engine: $engine');
            return true;
          }
        } catch (e) {
          debugPrint('[VoiceService] engine $engine failed: $e');
        }
      }
    }

    debugPrint('[VoiceService] TTS init failed after all attempts');
    return false;
  }

  // ==================== Voice Mode (mutually exclusive) ====================

  void setGlobalVoiceActive(bool value) {
    if (value && _activeSessionId != null) {
      _activeSessionId = null;
      stopListening();
    }
    _globalVoiceActive = value;
    if (!value) stopSpeaking();
    onStateChanged?.call();
  }

  /// Activate session voice for a specific session. Pass null to deactivate.
  void setActiveSession(String? sessionId) {
    if (sessionId != null && _globalVoiceActive) {
      _globalVoiceActive = false;
    }
    _activeSessionId = sessionId;
    if (sessionId == null) stopSpeaking();
    onStateChanged?.call();
  }

  /// Check if voice is active for a specific session.
  bool isSessionActive(String sessionId) => _activeSessionId == sessionId;

  // ==================== Settings ====================

  Future<void> setVoiceInputEnabled(bool value) async {
    _voiceInputEnabled = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_keyVoiceInput, value);
  }

  Future<void> setAutoReadEnabled(bool value) async {
    _autoReadEnabled = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_keyAutoRead, value);
  }

  Future<void> setSpeechRate(double value) async {
    _speechRate = value;
    await _ensureTtsReady();
    await _tts.setSpeechRate(value);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setDouble(_keySpeechRate, value);
  }

  Future<void> setLanguage(String value) async {
    _language = value;
    await _ensureTtsReady();
    await _tts.setLanguage(value);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyLanguage, value);
  }

  // ==================== Diagnostics ====================

  /// Open the Android TTS settings screen so the user can install languages.
  Future<bool> openTtsSettings() async {
    final uri = Uri.parse(
      'intent:#Intent;action=com.android.settings.TTS_SETTINGS;end',
    );
    try {
      final ok = await launchUrl(uri);
      if (ok) return true;
    } catch (_) {}
    return openAppSettings();
  }

  /// Open the Android voice input / STT settings screen.
  Future<bool> openSttSettings() async {
    final uri = Uri.parse(
      'intent:#Intent;action=com.android.settings.VOICE_INPUT_SETTINGS;end',
    );
    try {
      final ok = await launchUrl(uri);
      if (ok) return true;
    } catch (_) {}
    return openAppSettings();
  }

  /// Open the appropriate settings screen based on which service(s) failed.
  Future<bool> openVoiceSettings() async {
    if (!_ttsReady && !_sttAvailable) return openTtsSettings();
    if (!_ttsReady) return openTtsSettings();
    return openSttSettings();
  }

  /// Check if TTS engine is available. Returns null if OK, or an error message.
  Future<String?> checkTtsAvailability() async {
    try {
      final engines = await _tts.getEngines;
      if (engines == null || (engines is List && engines.isEmpty)) {
        return 'No TTS engine found. Install and enable Google Text-to-Speech from the Play Store.';
      }
      final languages = await _tts.getLanguages;
      if (languages == null || (languages is List && languages.isEmpty)) {
        return 'TTS engine found but has no languages. Open Settings → Apps → Google Text-to-Speech and ensure it is enabled.';
      }
      return null;
    } catch (e) {
      return 'TTS check failed: $e';
    }
  }

  /// Check if STT is available. Returns null if OK, or an error message.
  Future<String?> checkSttAvailability() async {
    try {
      final available = await _stt.initialize();
      if (!available) {
        return 'Speech recognition not available. Ensure the Google app is installed and enabled in Settings → Apps.';
      }
      return null;
    } catch (e) {
      return 'STT check failed: $e';
    }
  }

  // ==================== Auto-Start ====================

  /// Try to auto-start both TTS and STT. Returns a warning string if either
  /// failed to start, or null if both are ready.
  Future<String?> ensureServicesReady() async {
    final warnings = <String>[];

    // Try to auto-start TTS
    final ttsOk = await _ensureTtsReady();
    if (!ttsOk) {
      // Use checkTtsAvailability for a detailed diagnostic (it only queries
      // engines/languages, no reinit).
      final ttsWarning = await checkTtsAvailability();
      warnings.add(ttsWarning ?? 'Text-to-speech failed to start.');
    }

    // Try to auto-start STT (don't call checkSttAvailability — it would
    // re-initialize the engine a second time).
    try {
      _sttAvailable = await _stt.initialize();
    } catch (_) {
      _sttAvailable = false;
    }
    if (!_sttAvailable) {
      warnings.add(
        'Speech recognition not available. '
        'Ensure the Google app is installed and enabled in Settings → Apps.',
      );
    }

    if (warnings.isEmpty) return null;
    return warnings.join('\n\n');
  }

  // ==================== STT ====================

  Future<bool> startListening({
    required void Function(String text, bool finalResult) onResult,
    required VoidCallback onDone,
    required void Function(String error) onError,
  }) async {
    debugPrint('[VoiceService] startListening() called');
    final micStatus = await Permission.microphone.request();
    if (!micStatus.isGranted) return false;

    _sttAvailable = await _stt.initialize(
      onError: (error) {
        debugPrint('[VoiceService] stt onError: ${error.errorMsg}');
        _isListening = false;
        onStateChanged?.call();
        onError(error.errorMsg);
      },
      onStatus: (status) {
        debugPrint('[VoiceService] stt onStatus: $status');
        if (status == 'done' || status == 'notListening') {
          _isListening = false;
          onStateChanged?.call();
          onDone();
        }
      },
    );
    debugPrint('[VoiceService] stt.initialize() => $_sttAvailable');
    if (!_sttAvailable) {
      onError('Speech recognition not available on this device');
      return false;
    }

    _isListening = true;
    onStateChanged?.call();

    await _stt.listen(
      onResult: (result) {
        debugPrint('[VoiceService] stt onResult: "${result.recognizedWords}" final=${result.finalResult}');
        onResult(result.recognizedWords, result.finalResult);
      },
      localeId: _language,
      pauseFor: const Duration(seconds: 5),
      listenFor: const Duration(seconds: 30),
      listenOptions: SpeechListenOptions(
        listenMode: ListenMode.dictation,
        partialResults: true,
        onDevice: false,
      ),
    );

    return true;
  }

  void stopListening() {
    if (_isListening) {
      _stt.stop();
      _isListening = false;
      onStateChanged?.call();
    }
  }

  // ==================== TTS Queue ====================

  /// Enqueue text to be spoken. If the queue grows too large, older items
  /// are dropped and replaced with a skip message.
  Future<bool> speak(String text) async {
    if (text.isEmpty) return false;

    if (_speechQueue.length >= _maxQueueSize) {
      // Queue is backed up — drop everything and skip ahead
      _speechQueue.clear();
      _speechQueue.add('Skipping ahead. Check the screen for details.');
      _speechQueue.add(text);
      debugPrint('[VoiceService] queue overflow, skipping to latest');
      // Stop current speech to trigger skip immediately
      if (_isSpeaking) {
        await _tts.stop();
      }
    } else {
      _speechQueue.add(text);
    }

    if (!_processingQueue) {
      _processQueue();
    }

    return true;
  }

  Future<void> _processQueue() async {
    if (_processingQueue) return;
    _processingQueue = true;

    while (_speechQueue.isNotEmpty) {
      final text = _speechQueue.removeFirst();
      debugPrint('[VoiceService] speaking: "${text.length > 80 ? '${text.substring(0, 80)}...' : text}"');

      final ready = await _ensureTtsReady();
      if (!ready) {
        debugPrint('[VoiceService] TTS engine not ready, clearing queue');
        _speechQueue.clear();
        break;
      }

      _isSpeaking = true;
      onStateChanged?.call();

      final ok = await _speakDirect(text);
      if (!ok) {
        // Engine failed — don't try remaining items
        _speechQueue.clear();
        _isSpeaking = false;
        onStateChanged?.call();
        break;
      }

      // Wait for completion before speaking next item
      await _waitForCompletion();
    }

    _processingQueue = false;
  }

  void _processNext() {
    if (_speechQueue.isNotEmpty && !_processingQueue) {
      _processQueue();
    }
  }

  /// Speak text directly (no queue). Returns false on failure.
  Future<bool> _speakDirect(String text) async {
    try {
      final result = await _tts.speak(text).timeout(
        const Duration(seconds: 5),
        onTimeout: () {
          debugPrint('[VoiceService] tts.speak() timed out');
          return 0;
        },
      );

      if (result != 1) {
        // Retry once with engine reset
        _ttsReady = false;
        final retryReady = await _ensureTtsReady();
        if (!retryReady) return false;

        final retryResult = await _tts.speak(text).timeout(
          const Duration(seconds: 5),
          onTimeout: () => 0,
        );
        return retryResult == 1;
      }

      return true;
    } catch (e) {
      debugPrint('[VoiceService] _speakDirect error: $e');
      return false;
    }
  }

  /// Wait for the current utterance to finish (completion handler fires).
  Future<void> _waitForCompletion() async {
    if (!_isSpeaking) return;
    final completer = Completer<void>();
    final prev = onStateChanged;
    onStateChanged = () {
      prev?.call();
      if (!_isSpeaking && !completer.isCompleted) {
        completer.complete();
        onStateChanged = prev;
      }
    };
    // Timeout safety — don't hang forever
    await completer.future.timeout(
      const Duration(seconds: 60),
      onTimeout: () {
        onStateChanged = prev;
        _isSpeaking = false;
      },
    );
  }

  Future<void> stopSpeaking() async {
    _speechQueue.clear();
    await _tts.stop();
    _isSpeaking = false;
    _processingQueue = false;
    onStateChanged?.call();
  }

  void dispose() {
    _speechQueue.clear();
    _stt.stop();
    _tts.stop();
  }
}
