import 'dart:convert';
import 'package:cryptography/cryptography.dart';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;

/// Authenticated HTTP client for a single Helios host.
///
/// Owns JWT sign/cache and applies a single auto-refresh on 401:
/// invalidate → re-sign → retry once. Network exceptions propagate to callers.
class ApiClient {
  final String serverUrl;
  final String deviceId;
  final Uint8List privateKeySeed;

  String? _cachedToken;
  DateTime? _tokenExpiresAt;

  ApiClient({
    required this.serverUrl,
    required this.deviceId,
    required this.privateKeySeed,
  });

  // ==================== Auth ====================

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

  void invalidateToken() {
    _cachedToken = null;
    _tokenExpiresAt = null;
  }

  Future<String> _signJWT() async {
    final header = {'alg': 'EdDSA', 'typ': 'JWT', 'kid': deviceId};
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
    final keyPair = await algorithm.newKeyPairFromSeed(privateKeySeed);
    final signature = await algorithm.sign(
      utf8.encode(signingInput),
      keyPair: keyPair,
    );

    final encodedSignature =
        _base64urlEncode(Uint8List.fromList(signature.bytes));
    return '$signingInput.$encodedSignature';
  }

  static String _base64urlEncode(Uint8List bytes) {
    return base64Url.encode(bytes).replaceAll('=', '');
  }

  Future<Map<String, String>> _authHeaders({bool json = false}) async {
    final token = await getToken();
    return {
      'Authorization': 'Bearer $token',
      if (json) 'Content-Type': 'application/json',
    };
  }

  // ==================== HTTP verbs with 401 auto-refresh ====================

  Future<http.Response> get(String path) async {
    final resp = await http.get(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(),
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on GET $path — refreshing token');
      invalidateToken();
      return http.get(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(),
      );
    }
    return resp;
  }

  Future<http.Response> post(String path, {Map<String, dynamic>? body}) async {
    final encoded = body != null ? jsonEncode(body) : null;
    final resp = await http.post(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(json: true),
      body: encoded,
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on POST $path — refreshing token');
      invalidateToken();
      return http.post(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(json: true),
        body: encoded,
      );
    }
    return resp;
  }

  Future<http.Response> patch(String path, {Map<String, dynamic>? body}) async {
    final encoded = body != null ? jsonEncode(body) : null;
    final resp = await http.patch(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(json: true),
      body: encoded,
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on PATCH $path — refreshing token');
      invalidateToken();
      return http.patch(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(json: true),
        body: encoded,
      );
    }
    return resp;
  }

  Future<http.Response> delete(String path) async {
    final resp = await http.delete(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(),
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on DELETE $path — refreshing token');
      invalidateToken();
      return http.delete(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(),
      );
    }
    return resp;
  }
}
