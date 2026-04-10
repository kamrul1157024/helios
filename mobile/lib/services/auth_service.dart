import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:cryptography/cryptography.dart';
import 'package:uuid/uuid.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

class AuthService extends ChangeNotifier {
  static const _keyStorageKey = 'helios_private_key';
  static const _deviceIdKey = 'helios_device_id';
  static const _serverUrlKey = 'helios_server_url';
  static const _cookieKey = 'helios_cookie';

  final FlutterSecureStorage _secureStorage = const FlutterSecureStorage();

  bool _isLoading = true;
  bool _isAuthenticated = false;
  String? _serverUrl;
  String? _deviceId;
  String? _cookie;
  SimpleKeyPair? _keyPair;

  bool get isLoading => _isLoading;
  bool get isAuthenticated => _isAuthenticated;
  String? get serverUrl => _serverUrl;
  String? get deviceId => _deviceId;
  String? get cookie => _cookie;

  /// Load stored credentials on app start.
  Future<void> loadStoredCredentials() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      _serverUrl = prefs.getString(_serverUrlKey);
      _deviceId = await _secureStorage.read(key: _deviceIdKey);
      _cookie = await _secureStorage.read(key: _cookieKey);
      final storedKey = await _secureStorage.read(key: _keyStorageKey);

      if (_serverUrl != null && _deviceId != null && _cookie != null && storedKey != null) {
        await _importPrivateKey(storedKey);
        // Verify the cookie is still valid
        final valid = await _verifyCookie();
        _isAuthenticated = valid;
      }
    } catch (e) {
      debugPrint('Failed to load credentials: $e');
    }
    _isLoading = false;
    notifyListeners();
  }

  /// Set up the device from a QR code payload.
  /// [key] is the base64url-encoded Ed25519 seed from the QR.
  /// [serverUrl] is the base URL of the helios daemon.
  Future<SetupResult> setup(String key, String serverUrl) async {
    try {
      // 1. Import the private key
      await _importPrivateKey(key);

      // 2. Get or create device ID
      _deviceId = await _secureStorage.read(key: _deviceIdKey);
      if (_deviceId == null) {
        _deviceId = const Uuid().v4();
        await _secureStorage.write(key: _deviceIdKey, value: _deviceId);
      }

      _serverUrl = serverUrl;
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_serverUrlKey, serverUrl);

      // 3. Get public key
      final publicKey = await _getPublicKeyBase64();

      // 4. Pair device
      final pairResp = await http.post(
        Uri.parse('$serverUrl/api/auth/pair'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({'kid': _deviceId, 'public_key': publicKey}),
      );
      final pairData = jsonDecode(pairResp.body);
      if (pairData['success'] != true) {
        if (pairData['error'] == 'key_already_claimed') {
          return SetupResult.error('This QR code has already been used by another device. Generate a new QR from the terminal with: helios start');
        }
        return SetupResult.error(pairData['message'] ?? 'Failed to register device');
      }

      // 5. Sign JWT and login
      final jwt = await _signJWT();
      final loginResp = await http.post(
        Uri.parse('$serverUrl/api/auth/login'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({'token': jwt}),
      );

      if (loginResp.statusCode != 200) {
        return SetupResult.error('Login failed');
      }

      // Extract cookie from response
      final setCookie = loginResp.headers['set-cookie'];
      if (setCookie != null) {
        // Parse the helios_token cookie value
        final match = RegExp(r'helios_token=([^;]+)').firstMatch(setCookie);
        if (match != null) {
          _cookie = match.group(1);
          await _secureStorage.write(key: _cookieKey, value: _cookie);
        }
      }

      // If no set-cookie header (some HTTP clients strip it), use the JWT directly
      _cookie ??= jwt;
      await _secureStorage.write(key: _cookieKey, value: _cookie);

      // 6. Store private key
      await _secureStorage.write(key: _keyStorageKey, value: key);

      // 7. Update device metadata
      await _updateDeviceMetadata();

      _isAuthenticated = true;
      notifyListeners();
      return SetupResult.success();
    } catch (e) {
      return SetupResult.error('Setup failed: $e');
    }
  }

  /// Make an authenticated HTTP request.
  Future<http.Response> authGet(String path) async {
    return http.get(
      Uri.parse('$_serverUrl$path'),
      headers: _authHeaders(),
    );
  }

  Future<http.Response> authPost(String path, {Map<String, dynamic>? body}) async {
    return http.post(
      Uri.parse('$_serverUrl$path'),
      headers: {
        ..._authHeaders(),
        'Content-Type': 'application/json',
      },
      body: body != null ? jsonEncode(body) : null,
    );
  }

  Map<String, String> _authHeaders() {
    return {
      'Cookie': 'helios_token=$_cookie',
    };
  }

  Future<void> logout() async {
    await _secureStorage.delete(key: _keyStorageKey);
    await _secureStorage.delete(key: _cookieKey);
    _isAuthenticated = false;
    _cookie = null;
    _keyPair = null;
    notifyListeners();
  }

  /// Verify the stored cookie is still valid by hitting the API.
  Future<bool> _verifyCookie() async {
    try {
      final resp = await authGet('/api/auth/device/me');
      return resp.statusCode == 200;
    } catch (_) {
      return false;
    }
  }

  /// Import an Ed25519 private key seed from base64url.
  Future<void> _importPrivateKey(String base64urlSeed) async {
    final seed = _base64urlDecode(base64urlSeed);
    final algorithm = Ed25519();
    _keyPair = await algorithm.newKeyPairFromSeed(seed.toList());
  }

  /// Get the public key as base64url string.
  Future<String> _getPublicKeyBase64() async {
    if (_keyPair == null) throw StateError('No key pair loaded');
    final publicKey = await _keyPair!.extractPublicKey();
    final bytes = Uint8List.fromList(publicKey.bytes);
    return _base64urlEncode(bytes);
  }

  /// Sign a JWT for authentication.
  Future<String> _signJWT() async {
    if (_keyPair == null) throw StateError('No key pair loaded');
    if (_deviceId == null) throw StateError('No device ID');

    final header = {'alg': 'EdDSA', 'typ': 'JWT', 'kid': _deviceId!};
    final now = DateTime.now().toUtc().millisecondsSinceEpoch ~/ 1000;
    final payload = {
      'iat': now,
      'exp': now + 3600,
      'sub': 'helios-client',
    };

    final encodedHeader = _base64urlEncode(Uint8List.fromList(utf8.encode(jsonEncode(header))));
    final encodedPayload = _base64urlEncode(Uint8List.fromList(utf8.encode(jsonEncode(payload))));
    final signingInput = '$encodedHeader.$encodedPayload';

    final algorithm = Ed25519();
    final signature = await algorithm.sign(
      utf8.encode(signingInput),
      keyPair: _keyPair!,
    );

    final encodedSignature = _base64urlEncode(Uint8List.fromList(signature.bytes));
    return '$signingInput.$encodedSignature';
  }

  /// Auto-detect platform and update device metadata.
  Future<void> _updateDeviceMetadata() async {
    final platform = defaultTargetPlatform == TargetPlatform.android ? 'Android' : 'iOS';
    final name = '$platform — Helios App';
    try {
      await authPost('/api/auth/device/me', body: {
        'name': name,
        'platform': platform,
        'browser': 'Helios App',
      });
    } catch (_) {
      // Best effort
    }
  }

  // --- Base64url helpers ---

  String _base64urlEncode(Uint8List bytes) {
    return base64Url.encode(bytes).replaceAll('=', '');
  }

  Uint8List _base64urlDecode(String str) {
    String padded = str.replaceAll('-', '+').replaceAll('_', '/');
    while (padded.length % 4 != 0) {
      padded += '=';
    }
    return Uint8List.fromList(base64.decode(padded));
  }
}

class SetupResult {
  final bool ok;
  final String? error;

  SetupResult._(this.ok, this.error);

  factory SetupResult.success() => SetupResult._(true, null);
  factory SetupResult.error(String message) => SetupResult._(false, message);
}
