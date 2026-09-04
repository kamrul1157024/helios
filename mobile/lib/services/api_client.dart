import 'dart:convert';
import 'dart:io';
import 'package:cryptography/cryptography.dart';
import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';

/// How long an unused connection is held open.
///
/// A daemon can be a long way off — a hundred milliseconds of round trip is
/// ordinary for a host in another region — and opening a connection costs one
/// of those before the request costs another. Sessions are watched in bursts
/// with quiet in between, so the gaps worth surviving are minutes, not the
/// fifteen seconds dart:io keeps by default.
const _idleTimeout = Duration(minutes: 5);

/// Authenticated HTTP client for a single Helios host.
///
/// Owns JWT sign/cache and applies a single auto-refresh on 401:
/// invalidate → re-sign → retry once. Network exceptions propagate to callers.
///
/// One client per host, kept for as long as the host is, so its connections
/// are reused rather than dialled again for every request.
class ApiClient {
  final String serverUrl;
  final String deviceId;
  final Uint8List privateKeySeed;

  String? _cachedToken;
  DateTime? _tokenExpiresAt;

  final http.Client _http;

  ApiClient({
    required this.serverUrl,
    required this.deviceId,
    required this.privateKeySeed,
    http.Client? client,
  }) : _http = client ?? IOClient(HttpClient()..idleTimeout = _idleTimeout);

  /// Drops the connections. The client is unusable afterwards.
  void close() => _http.close();

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
    final payload = {'iat': now, 'exp': now + 3600, 'sub': 'helios-client'};

    final encodedHeader = _base64urlEncode(
      Uint8List.fromList(utf8.encode(jsonEncode(header))),
    );
    final encodedPayload = _base64urlEncode(
      Uint8List.fromList(utf8.encode(jsonEncode(payload))),
    );
    final signingInput = '$encodedHeader.$encodedPayload';

    final algorithm = Ed25519();
    final keyPair = await algorithm.newKeyPairFromSeed(privateKeySeed);
    final signature = await algorithm.sign(
      utf8.encode(signingInput),
      keyPair: keyPair,
    );

    final encodedSignature = _base64urlEncode(
      Uint8List.fromList(signature.bytes),
    );
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
    final resp = await _http.get(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(),
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on GET $path — refreshing token');
      invalidateToken();
      return _http.get(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(),
      );
    }
    return resp;
  }

  Future<http.Response> post(String path, {Map<String, dynamic>? body}) async {
    final encoded = body != null ? jsonEncode(body) : null;
    final resp = await _http.post(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(json: true),
      body: encoded,
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on POST $path — refreshing token');
      invalidateToken();
      return _http.post(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(json: true),
        body: encoded,
      );
    }
    return resp;
  }

  Future<http.Response> patch(String path, {Map<String, dynamic>? body}) async {
    final encoded = body != null ? jsonEncode(body) : null;
    final resp = await _http.patch(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(json: true),
      body: encoded,
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on PATCH $path — refreshing token');
      invalidateToken();
      return _http.patch(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(json: true),
        body: encoded,
      );
    }
    return resp;
  }

  /// Posts files as multipart/form-data, each under the field `file`.
  ///
  /// A [http.MultipartRequest] is spent once sent, so the retry after a 401
  /// builds a second one rather than replaying the first.
  Future<http.Response> postFiles(String path, List<UploadFile> files) async {
    Future<http.Response> attempt() async {
      final request = http.MultipartRequest(
        'POST',
        Uri.parse('$serverUrl$path'),
      );
      request.headers.addAll(await _authHeaders());
      for (final file in files) {
        request.files.add(
          http.MultipartFile.fromBytes('file', file.bytes, filename: file.name),
        );
      }
      return http.Response.fromStream(await _http.send(request));
    }

    final resp = await attempt();
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on POST $path — refreshing token');
      invalidateToken();
      return attempt();
    }
    return resp;
  }

  Future<http.Response> delete(String path) async {
    final resp = await _http.delete(
      Uri.parse('$serverUrl$path'),
      headers: await _authHeaders(),
    );
    if (resp.statusCode == 401) {
      debugPrint('[ApiClient] 401 on DELETE $path — refreshing token');
      invalidateToken();
      return _http.delete(
        Uri.parse('$serverUrl$path'),
        headers: await _authHeaders(),
      );
    }
    return resp;
  }
}

/// A file picked on the device, held in memory until it is uploaded.
class UploadFile {
  final String name;
  final Uint8List bytes;

  /// Where the daemon put it, once it has.
  ///
  /// Set means uploaded, and a retry leaves it alone. The send after an upload
  /// can fail — a cold session that never acknowledges the prompt is the common
  /// one — and the composer keeps its chips so the user can try again; without
  /// this the retry would upload the same bytes and the daemon, which will not
  /// overwrite a name it already holds, would leave a copy behind per attempt.
  String? storedPath;

  UploadFile({required this.name, required this.bytes});

  int get size => bytes.length;

  /// True for the kinds the composer shows a thumbnail for.
  bool get isImage {
    final lower = name.toLowerCase();
    return lower.endsWith('.png') ||
        lower.endsWith('.jpg') ||
        lower.endsWith('.jpeg') ||
        lower.endsWith('.gif') ||
        lower.endsWith('.webp') ||
        lower.endsWith('.heic');
  }
}
