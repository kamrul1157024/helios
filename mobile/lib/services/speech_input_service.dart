import 'package:flutter/foundation.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:speech_to_text/speech_to_text.dart';
import 'package:url_launcher/url_launcher.dart';

class SpeechInputService {
  SpeechInputService._();
  static final instance = SpeechInputService._();

  final SpeechToText _stt = SpeechToText();

  static const _localeId = 'en-US';

  bool _available = false;
  bool _isListening = false;

  bool get available => _available;
  bool get isListening => _isListening;

  /// Open the Android voice input / STT settings screen.
  Future<bool> openSettings() async {
    final uri = Uri.parse(
      'intent:#Intent;action=com.android.settings.VOICE_INPUT_SETTINGS;end',
    );
    try {
      final ok = await launchUrl(uri);
      if (ok) return true;
    } catch (_) {}
    return openAppSettings();
  }

  /// Check if STT is available. Returns null if OK, or an error message.
  Future<String?> checkAvailability() async {
    try {
      _available = await _stt.initialize();
      if (!_available) {
        return 'Speech recognition not available. Ensure the Google app is installed and enabled in Settings → Apps.';
      }
      return null;
    } catch (e) {
      return 'Speech recognition check failed: $e';
    }
  }

  Future<bool> startListening({
    required void Function(String text, bool finalResult) onResult,
    required VoidCallback onDone,
    required void Function(String error) onError,
  }) async {
    final micStatus = await Permission.microphone.request();
    if (!micStatus.isGranted) return false;

    _available = await _stt.initialize(
      onError: (error) {
        _isListening = false;
        onError(error.errorMsg);
      },
      onStatus: (status) {
        if (status == 'done' || status == 'notListening') {
          _isListening = false;
          onDone();
        }
      },
    );
    if (!_available) {
      onError('Speech recognition not available on this device');
      return false;
    }

    _isListening = true;

    await _stt.listen(
      onResult: (result) =>
          onResult(result.recognizedWords, result.finalResult),
      localeId: _localeId,
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
    }
  }
}
