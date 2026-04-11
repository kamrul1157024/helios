import 'dart:convert';
import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:cryptography/cryptography.dart';
import 'package:uuid/uuid.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import '../models/host_connection.dart';
import '../models/notification.dart';
import '../models/session.dart';
import 'daemon_api_service.dart';

class HostManager extends ChangeNotifier {
  static const _hostsKey = 'helios_hosts';
  static const _activeHostKey = 'helios_active_host_id';

  // Legacy single-host keys (for migration)
  static const _legacyKeyStorageKey = 'helios_private_key';
  static const _legacyDeviceIdKey = 'helios_device_id';
  static const _legacyServerUrlKey = 'helios_server_url';
  static const _legacyCookieKey = 'helios_cookie';

  final FlutterSecureStorage _secureStorage = const FlutterSecureStorage();

  List<HostConnection> _hosts = [];
  String? _activeHostId;
  final Map<String, DaemonAPIService> _services = {};

  bool _isLoading = true;
  bool _isPendingApproval = false;

  // --- Public accessors ---

  bool get isLoading => _isLoading;
  bool get isAuthenticated => _hosts.isNotEmpty;
  bool get isPendingApproval => _isPendingApproval;
  List<HostConnection> get hosts => List.unmodifiable(_hosts);
  String? get activeHostId => _activeHostId;

  HostConnection? get activeHost {
    if (_activeHostId == null) return null;
    try {
      return _hosts.firstWhere((h) => h.id == _activeHostId);
    } catch (_) {
      return null;
    }
  }

  DaemonAPIService? get activeService => _services[_activeHostId];

  DaemonAPIService? serviceFor(String hostId) => _services[hostId];

  HostConnection? hostById(String hostId) {
    try {
      return _hosts.firstWhere((h) => h.id == hostId);
    } catch (_) {
      return null;
    }
  }

  bool get hasAnyConnection => _services.values.any((s) => s.connected);

  /// All sessions from all hosts, merged.
  List<Session> get allSessions =>
      _services.values.expand((s) => s.sessions).toList();

  /// All notifications from all hosts, merged.
  List<HeliosNotification> get allNotifications =>
      _services.values.expand((s) => s.notifications).toList();

  /// Sessions for the current filter (active host or all).
  List<Session> get filteredSessions {
    if (_activeHostId == null) return allSessions;
    return _services[_activeHostId]?.sessions ?? [];
  }

  /// Notifications for the current filter (active host or all).
  List<HeliosNotification> get filteredNotifications {
    if (_activeHostId == null) return allNotifications;
    return _services[_activeHostId]?.notifications ?? [];
  }

  /// Whether sessions have been loaded (any host for "all", specific for filtered).
  bool get sessionsLoaded {
    if (_activeHostId == null) {
      return _services.values.any((s) => s.sessionsLoaded);
    }
    return _services[_activeHostId]?.sessionsLoaded ?? false;
  }

  /// Whether notifications have been loaded.
  bool get notificationsLoaded {
    if (_activeHostId == null) {
      return _services.values.any((s) => s.notificationsLoaded);
    }
    return _services[_activeHostId]?.notificationsLoaded ?? false;
  }

  // ==================== Lifecycle ====================

  /// Load stored hosts on app start. Migrates from single-host if needed.
  Future<void> loadStoredHosts() async {
    try {
      await _migrateFromLegacy();

      final raw = await _secureStorage.read(key: _hostsKey);
      if (raw != null) {
        final list = jsonDecode(raw) as List;
        _hosts = list.map((h) => HostConnection.fromJson(h as Map<String, dynamic>)).toList();
      }

      final prefs = await SharedPreferences.getInstance();
      _activeHostId = prefs.getString(_activeHostKey);

      // If active host was removed or never set, default to first host
      if (_activeHostId == null || _activeHostId!.isEmpty || !_hosts.any((h) => h.id == _activeHostId)) {
        _activeHostId = _hosts.isNotEmpty ? _hosts.first.id : null;
        if (_activeHostId != null) {
          await prefs.setString(_activeHostKey, _activeHostId!);
        }
      }

      // Start services for all hosts
      for (final host in _hosts) {
        await _startServiceFor(host);
      }
    } catch (e) {
      debugPrint('Failed to load hosts: $e');
    }
    _isLoading = false;
    notifyListeners();
  }

  Future<void> _startServiceFor(HostConnection host) async {
    final cookie = await _secureStorage.read(key: 'helios_host_${host.id}_cookie');
    if (cookie == null) return;

    final service = DaemonAPIService(
      hostId: host.id,
      serverUrl: host.serverUrl,
      cookie: cookie,
    );

    // Forward SSE events to notify HostManager listeners
    service.onSSEEvent = _onServiceSSEEvent;
    service.addListener(_onServiceChanged);

    _services[host.id] = service;

    if (host.id == _activeHostId) {
      service.fetchNotifications();
      service.fetchSessions();
      service.fetchCommands();
      service.fetchHealth();
      service.fetchProviders();
      await service.startActive();
    } else {
      await service.startBackground();
    }
  }

  void _onServiceChanged() {
    notifyListeners();
  }

  void _onServiceSSEEvent(String hostId, SSEEvent event) {
    // HostManager gets notified of all SSE events from all hosts.
    // HomeScreen will listen to this for OS notification dispatch.
    notifyListeners();
  }

  // ==================== Host Management ====================

  /// Pair with a new host via QR code token.
  Future<SetupResult> addHost(
    String pairingToken,
    String serverUrl, {
    void Function(String)? onStatus,
  }) async {
    try {
      // 1. Generate Ed25519 keypair
      onStatus?.call('Generating keys...');
      final algorithm = Ed25519();
      final keyPair = await algorithm.newKeyPair();

      // 2. Generate device ID
      final deviceId = const Uuid().v4();

      // 3. Get public key
      final publicKey = await keyPair.extractPublicKey();
      final publicKeyB64 = _base64urlEncode(Uint8List.fromList(publicKey.bytes));

      // 4. Pair device
      onStatus?.call('Registering device...');
      final pairResp = await http.post(
        Uri.parse('$serverUrl/api/auth/pair'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({
          'token': pairingToken,
          'kid': deviceId,
          'public_key': publicKeyB64,
        }),
      );
      final pairData = jsonDecode(pairResp.body);
      if (pairData['success'] != true) {
        if (pairData['error'] == 'invalid_token') {
          return SetupResult.error(
            'This QR code has expired or already been used. '
            'Generate a new QR from the terminal with: helios start',
          );
        }
        return SetupResult.error(pairData['message'] ?? 'Failed to register device');
      }

      // 5. Sign JWT and login
      onStatus?.call('Authenticating...');
      final jwt = await _signJWT(keyPair, deviceId);
      final loginResp = await http.post(
        Uri.parse('$serverUrl/api/auth/login'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({'token': jwt}),
      );

      if (loginResp.statusCode != 200) {
        return SetupResult.error('Login failed');
      }

      // Extract cookie
      String? cookie;
      final setCookie = loginResp.headers['set-cookie'];
      if (setCookie != null) {
        final match = RegExp(r'helios_token=([^;]+)').firstMatch(setCookie);
        if (match != null) cookie = match.group(1);
      }
      cookie ??= jwt;

      // 6. Store private key seed
      final extractedSeed = await keyPair.extractPrivateKeyBytes();
      final seed = Uint8List.fromList(extractedSeed.sublist(0, 32));

      // 7. Create host connection
      final nextColorIndex = _hosts.isEmpty ? 0 : (_hosts.map((h) => h.colorIndex).reduce((a, b) => a > b ? a : b) + 1);
      final host = HostConnection(
        id: const Uuid().v4(),
        label: 'Host ${_hosts.length + 1}',
        serverUrl: serverUrl,
        deviceId: deviceId,
        colorIndex: nextColorIndex,
        addedAt: DateTime.now(),
      );

      // 8. Persist credentials
      await _secureStorage.write(key: 'helios_host_${host.id}_key', value: _base64urlEncode(seed));
      await _secureStorage.write(key: 'helios_host_${host.id}_cookie', value: cookie);
      _hosts.add(host);
      await _saveHosts();

      // 9. Update device metadata
      await _updateDeviceMetadata(serverUrl, cookie);

      // 10. Wait for approval
      onStatus?.call('Waiting for approval on terminal...');
      _isPendingApproval = true;
      notifyListeners();

      final approved = await _waitForApproval(serverUrl, cookie);
      _isPendingApproval = false;

      if (!approved) {
        // Remove the unapproved host
        await removeHost(host.id);
        notifyListeners();
        return SetupResult.error('Device was rejected by the terminal user.');
      }

      // 11. Set as active and start service
      _activeHostId = host.id;
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_activeHostKey, host.id);

      // Try to fetch hostname for a better label
      await _fetchAndSetHostname(host, cookie);

      await _startServiceFor(host);

      notifyListeners();
      return SetupResult.success();
    } catch (e) {
      _isPendingApproval = false;
      notifyListeners();
      return SetupResult.error('Setup failed: $e');
    }
  }

  /// Remove a host and clean up its credentials and service.
  Future<void> removeHost(String hostId) async {
    _services[hostId]?.stop();
    _services[hostId]?.removeListener(_onServiceChanged);
    _services[hostId]?.dispose();
    _services.remove(hostId);

    _hosts.removeWhere((h) => h.id == hostId);
    await _saveHosts();

    await _secureStorage.delete(key: 'helios_host_${hostId}_key');
    await _secureStorage.delete(key: 'helios_host_${hostId}_cookie');

    if (_activeHostId == hostId) {
      _activeHostId = _hosts.isNotEmpty ? _hosts.first.id : null;
      final prefs = await SharedPreferences.getInstance();
      if (_activeHostId != null) {
        await prefs.setString(_activeHostKey, _activeHostId!);
      } else {
        await prefs.remove(_activeHostKey);
      }
    }

    notifyListeners();
  }

  /// Change the active host filter. null = "All hosts".
  Future<void> setActiveHost(String? hostId) async {
    if (_activeHostId == hostId) return;

    // Demote current active
    if (_activeHostId != null) {
      _services[_activeHostId]?.demote();
    }

    _activeHostId = hostId;

    // Promote new active
    if (hostId != null) {
      _services[hostId]?.promote();
    }

    final prefs = await SharedPreferences.getInstance();
    if (hostId != null) {
      await prefs.setString(_activeHostKey, hostId);
    } else {
      await prefs.remove(_activeHostKey);
    }

    notifyListeners();
  }

  /// Update a host's label.
  Future<void> updateHostLabel(String hostId, String label) async {
    final host = hostById(hostId);
    if (host == null) return;
    host.label = label;
    await _saveHosts();
    notifyListeners();
  }

  /// Update a host's color.
  Future<void> updateHostColor(String hostId, int colorIndex) async {
    final host = hostById(hostId);
    if (host == null) return;
    host.colorIndex = colorIndex;
    await _saveHosts();
    notifyListeners();
  }

  /// Fetch all data for a specific host (used on pull-to-refresh in "All" mode).
  Future<void> refreshHost(String hostId) async {
    final service = _services[hostId];
    if (service == null) return;
    await Future.wait([
      service.fetchSessions(),
      service.fetchNotifications(),
    ]);
  }

  /// Refresh all hosts (used on pull-to-refresh in "All" mode).
  Future<void> refreshAll() async {
    await Future.wait(_services.values.map((s) => Future.wait([
          s.fetchSessions(),
          s.fetchNotifications(),
        ])));
  }

  /// Stop all services (app background).
  void stopAll() {
    for (final service in _services.values) {
      service.stop();
    }
  }

  /// Restart all services (app resume).
  Future<void> resumeAll() async {
    for (final host in _hosts) {
      final service = _services[host.id];
      if (service == null) continue;
      if (host.id == _activeHostId) {
        service.fetchNotifications();
        service.fetchSessions();
        await service.startActive();
      } else {
        await service.startBackground();
      }
    }
  }

  // ==================== Private Helpers ====================

  Future<void> _saveHosts() async {
    final json = jsonEncode(_hosts.map((h) => h.toJson()).toList());
    await _secureStorage.write(key: _hostsKey, value: json);
  }

  /// Wipe legacy single-host storage so the user re-pairs with the new multi-host flow.
  Future<void> _migrateFromLegacy() async {
    final legacyKey = await _secureStorage.read(key: _legacyKeyStorageKey);
    if (legacyKey == null) return; // No legacy data

    debugPrint('Clearing legacy single-host data — user will re-pair.');

    await _secureStorage.delete(key: _legacyKeyStorageKey);
    await _secureStorage.delete(key: _legacyDeviceIdKey);
    await _secureStorage.delete(key: _legacyCookieKey);

    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_legacyServerUrlKey);
  }

  Future<String> _signJWT(SimpleKeyPair keyPair, String deviceId) async {
    final header = {'alg': 'EdDSA', 'typ': 'JWT', 'kid': deviceId};
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
      keyPair: keyPair,
    );

    final encodedSignature = _base64urlEncode(Uint8List.fromList(signature.bytes));
    return '$signingInput.$encodedSignature';
  }

  Future<bool> _waitForApproval(String serverUrl, String cookie) async {
    const maxAttempts = 150; // 5 minutes at 2s intervals
    for (var i = 0; i < maxAttempts; i++) {
      await Future.delayed(const Duration(seconds: 2));
      try {
        final resp = await http.get(
          Uri.parse('$serverUrl/api/auth/device/me'),
          headers: {'Cookie': 'helios_token=$cookie'},
        );
        if (resp.statusCode == 200) {
          final data = jsonDecode(resp.body);
          final status = data['status'] as String?;
          if (status == 'active') return true;
          if (status == 'revoked') return false;
        } else if (resp.statusCode == 401 || resp.statusCode == 403) {
          return false;
        }
      } catch (_) {
        // Network error — keep trying
      }
    }
    return false;
  }

  Future<void> _updateDeviceMetadata(String serverUrl, String cookie) async {
    final platform = defaultTargetPlatform == TargetPlatform.android ? 'Android' : 'iOS';
    final name = '$platform — Helios App';
    try {
      await http.post(
        Uri.parse('$serverUrl/api/auth/device/me'),
        headers: {
          'Cookie': 'helios_token=$cookie',
          'Content-Type': 'application/json',
        },
        body: jsonEncode({
          'name': name,
          'platform': platform,
          'browser': 'Helios App',
        }),
      );
    } catch (_) {
      // Best effort
    }
  }

  Future<void> _fetchAndSetHostname(HostConnection host, String cookie) async {
    try {
      final resp = await http.get(
        Uri.parse('${host.serverUrl}/api/health'),
        headers: {'Cookie': 'helios_token=$cookie'},
      );
      if (resp.statusCode == 200) {
        final data = jsonDecode(resp.body);
        final hostname = data['hostname'] as String?;
        if (hostname != null && hostname.isNotEmpty) {
          host.label = hostname;
          host.hostname = hostname;
          await _saveHosts();
        }
      }
    } catch (_) {
      // Best effort
    }
  }

  // --- Base64url helpers ---

  String _base64urlEncode(Uint8List bytes) {
    return base64Url.encode(bytes).replaceAll('=', '');
  }

  @override
  void dispose() {
    for (final service in _services.values) {
      service.removeListener(_onServiceChanged);
      service.dispose();
    }
    _services.clear();
    super.dispose();
  }
}

class SetupResult {
  final bool ok;
  final String? error;

  SetupResult._(this.ok, this.error);

  factory SetupResult.success() => SetupResult._(true, null);
  factory SetupResult.error(String message) => SetupResult._(false, message);
}
