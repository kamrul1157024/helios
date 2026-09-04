import 'dart:convert';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Callback for when a notification action (approve/deny) is tapped.
typedef NotificationActionCallback =
    void Function(String notificationId, String action);

class NotificationService {
  NotificationService._();
  static final instance = NotificationService._();

  final _plugin = FlutterLocalNotificationsPlugin();
  NotificationActionCallback? onAction;

  static const _permChannel = 'helios_perm_v7';
  static const _generalChannel = 'helios_general_v7';

  static const _platform = MethodChannel('com.helios.helios/notifications');

  static const _keySoundEnabled = 'notif_sound_enabled';
  static const _keyVibrationEnabled = 'notif_vibration_enabled';
  static const _keyAlertTypes = 'notif_alert_types';

  /// Defaults keyed by the kind of request, not by provider.
  ///
  /// A toggle is about what the notification asks of you — approve something,
  /// answer something — which is the same question whoever raised it. Keying
  /// on kind means a new provider needs no new rows and no new defaults.
  static const Map<String, bool> _defaultAlertTypes = {
    'permission': true,
    'question': true,
    'elicitation.form': true,
    'elicitation.url': true,
    'trust': true,
    'done': true,
    'error': true,
  };

  bool _soundEnabled = true;
  bool _vibrationEnabled = true;
  Map<String, bool> _alertTypes = Map.of(_defaultAlertTypes);

  bool get soundEnabled => _soundEnabled;
  bool get vibrationEnabled => _vibrationEnabled;
  Map<String, bool> get alertTypes => Map.unmodifiable(_alertTypes);

  /// Whether this notification type may buzz.
  ///
  /// A per-type setting wins, so anything a user silenced before toggles were
  /// keyed by kind keeps working. Then the kind. Then true — an unknown
  /// provider must be noisy rather than silent, because a blocked agent
  /// nobody hears is the failure that matters.
  /// Rewrites settings saved when toggles were keyed by notification type.
  ///
  /// A user who silenced `claude.permission` before the upgrade would
  /// otherwise keep that key for ever: the settings switch now reads the kind
  /// and shows ON, flipping it writes `permission`, and the stale per-type key
  /// still wins — silent notifications with no way back but "Reset to
  /// defaults". Folding the old key onto its kind, off winning, drops the
  /// setting the user actually chose in the right place.
  @visibleForTesting
  static Map<String, bool> migrateLegacyAlertKeys(Map<String, bool> stored) {
    final out = <String, bool>{};
    stored.forEach((key, value) {
      final i = key.indexOf('.');
      final kind = i < 0 ? key : key.substring(i + 1);
      if (!_defaultAlertTypes.containsKey(kind)) {
        // Not a kind we know; keep it verbatim rather than guessing.
        out[key] = value;
        return;
      }
      out[kind] = (out[kind] ?? true) && value;
    });
    return out;
  }

  bool isAlertEnabled(String notifType) {
    final byType = _alertTypes[notifType];
    if (byType != null) return byType;
    final i = notifType.indexOf('.');
    if (i >= 0) {
      final byKind = _alertTypes[notifType.substring(i + 1)];
      if (byKind != null) return byKind;
    }
    return true;
  }

  Future<void> setSoundEnabled(bool value) async {
    _soundEnabled = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_keySoundEnabled, value);
  }

  Future<void> setVibrationEnabled(bool value) async {
    _vibrationEnabled = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_keyVibrationEnabled, value);
  }

  Future<void> setAlertEnabled(String notifType, bool value) async {
    _alertTypes[notifType] = value;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyAlertTypes, jsonEncode(_alertTypes));
  }

  Future<void> resetAlertTypes() async {
    _alertTypes = Map.of(_defaultAlertTypes);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_keyAlertTypes, jsonEncode(_alertTypes));
  }

  /// Convert a string ID to a positive notification ID.
  static int _notifId(String id) => id.hashCode & 0x7FFFFFFF;

  /// Posted OS notifications, keyed by [notifKey] → the integer id handed to
  /// the plugin. The integer is derived from the JSON payload string, so
  /// rebuilding it at cancel time would depend on map key order; a retraction
  /// that silently misses is worse than none.
  final Map<String, int> _posted = {};

  /// Stable key for a notification, independent of payload encoding.
  static String notifKey(String hostId, String notificationId) =>
      '$hostId:$notificationId';

  /// Whether a notification is currently posted for this key. Doubles as the
  /// de-dupe check, so a replayed event does not re-alert.
  bool isPosted(String key) => _posted.containsKey(key);

  /// Retract a posted notification. A no-op when nothing is posted for this
  /// key, which is the common case for types that never raise one.
  Future<void> cancel(String key) async {
    final nid = _posted.remove(key);
    if (nid == null) return;
    try {
      await _plugin.cancel(nid);
      debugPrint('[NotificationService] cancel key=$key nid=$nid');
    } catch (e) {
      debugPrint('[NotificationService] cancel failed for $key: $e');
    }
  }

  /// Retract every notification posted for [hostId] that is not in
  /// [pendingIds], so the tray matches what the daemon still considers pending.
  ///
  /// Set-based rather than cancelling the ids the daemon reported as resolved:
  /// the daemon keeps only its most recent notifications, so one resolved long
  /// enough ago is not in the list at all, and cancelling only what is listed
  /// would leave it in the tray forever.
  Future<void> retainOnly(String hostId, Set<String> pendingIds) async {
    final prefix = '$hostId:';
    final stale = _posted.keys
        .where(
          (key) =>
              key.startsWith(prefix) &&
              !pendingIds.contains(key.substring(prefix.length)),
        )
        .toList();
    for (final key in stale) {
      await cancel(key);
    }
  }

  /// Retract every notification this service has posted.
  Future<void> cancelAll() async {
    final ids = _posted.values.toList();
    _posted.clear();
    for (final nid in ids) {
      try {
        await _plugin.cancel(nid);
      } catch (e) {
        debugPrint('[NotificationService] cancelAll failed for $nid: $e');
      }
    }
  }

  Future<void> init() async {
    final prefs = await SharedPreferences.getInstance();
    _soundEnabled = prefs.getBool(_keySoundEnabled) ?? true;
    _vibrationEnabled = prefs.getBool(_keyVibrationEnabled) ?? true;
    final alertJson = prefs.getString(_keyAlertTypes);
    if (alertJson != null) {
      try {
        final decoded = jsonDecode(alertJson) as Map<String, dynamic>;
        _alertTypes = {
          ..._defaultAlertTypes,
          ...migrateLegacyAlertKeys(
            decoded.map((k, v) => MapEntry(k, v as bool)),
          ),
        };
      } catch (_) {
        _alertTypes = Map.of(_defaultAlertTypes);
      }
    }

    const androidSettings = AndroidInitializationSettings(
      '@mipmap/ic_launcher',
    );
    const darwinSettings = DarwinInitializationSettings(
      requestAlertPermission: true,
      requestBadgePermission: true,
      requestSoundPermission: true,
    );

    await _plugin.initialize(
      const InitializationSettings(
        android: androidSettings,
        iOS: darwinSettings,
        macOS: darwinSettings,
      ),
      onDidReceiveNotificationResponse: _onResponse,
    );

    final android = _plugin
        .resolvePlatformSpecificImplementation<
          AndroidFlutterLocalNotificationsPlugin
        >();
    if (android != null) {
      // Clean up all old channel IDs.
      for (final old in [
        'helios_permissions',
        'helios_general',
        'helios_perm_v2',
        'helios_general_v2',
        'helios_perm_v3',
        'helios_general_v3',
        'helios_perm_v4',
        'helios_general_v4',
        'helios_perm_v5',
        'helios_general_v5',
        'helios_perm_v6',
        'helios_general_v6',
      ]) {
        await android.deleteNotificationChannel(old);
      }
    }

    // Create channels via native platform channel to bypass
    // flutter_local_notifications plugin issues with sound on ColorOS/RealmeUI.
    try {
      await _platform.invokeMethod('createChannels', {
        'channels': [
          {
            'id': _permChannel,
            'name': 'Permission Requests',
            'description': 'Claude tool permission requests',
            'importance': 5, // IMPORTANCE_HIGH (max)
          },
          {
            'id': _generalChannel,
            'name': 'General',
            'description': 'General helios notifications',
            'importance': 5,
          },
        ],
      });
      debugPrint('[NotificationService] Native channels created');
    } on MissingPluginException {
      debugPrint(
        '[NotificationService] Native channel creation not available, using plugin',
      );
      // Fallback to plugin-based channel creation.
      if (android != null) {
        await android.deleteNotificationChannel(_permChannel);
        await android.deleteNotificationChannel(_generalChannel);

        await android.createNotificationChannel(
          AndroidNotificationChannel(
            _permChannel,
            'Permission Requests',
            description: 'Claude tool permission requests',
            importance: Importance.max,
            playSound: false,
            enableVibration: false,
          ),
        );
        await android.createNotificationChannel(
          AndroidNotificationChannel(
            _generalChannel,
            'General',
            description: 'General helios notifications',
            importance: Importance.max,
            playSound: false,
            enableVibration: false,
          ),
        );
      }
    }
  }

  Future<bool> requestPermission() async {
    final android = _plugin
        .resolvePlatformSpecificImplementation<
          AndroidFlutterLocalNotificationsPlugin
        >();
    if (android != null) {
      final granted = await android.requestNotificationsPermission();
      return granted ?? false;
    }

    final ios = _plugin
        .resolvePlatformSpecificImplementation<
          IOSFlutterLocalNotificationsPlugin
        >();
    if (ios != null) {
      final granted = await ios.requestPermissions(
        alert: true,
        badge: true,
        sound: true,
      );
      return granted ?? false;
    }

    final macos = _plugin
        .resolvePlatformSpecificImplementation<
          MacOSFlutterLocalNotificationsPlugin
        >();
    if (macos != null) {
      final granted = await macos.requestPermissions(
        alert: true,
        badge: true,
        sound: true,
      );
      return granted ?? false;
    }

    return true;
  }

  /// Show a permission request notification with approve/deny actions.
  Future<void> showPermissionNotification({
    required String id,
    required String key,
    required String toolName,
    required String detail,
    bool silent = false,
  }) async {
    final nid = _notifId(id);
    debugPrint(
      '[NotificationService] showPermission id=$id nid=$nid tool=$toolName',
    );

    final androidDetails = AndroidNotificationDetails(
      _permChannel,
      'Permission Requests',
      channelDescription: 'Claude tool permission requests',
      importance: Importance.max,
      priority: Priority.max,
      playSound: false,
      enableVibration: false,
      fullScreenIntent: true,
      category: AndroidNotificationCategory.alarm,
      actions: [
        const AndroidNotificationAction(
          'approve',
          'Approve',
          showsUserInterface: true,
        ),
        const AndroidNotificationAction(
          'deny',
          'Deny',
          showsUserInterface: true,
        ),
      ],
    );

    const iosDetails = DarwinNotificationDetails(
      presentAlert: true,
      presentBadge: true,
      presentSound: true,
      interruptionLevel: InterruptionLevel.timeSensitive,
    );

    try {
      await _plugin.show(
        nid,
        'helios — Permission Request',
        '$toolName: $detail',
        NotificationDetails(android: androidDetails, iOS: iosDetails),
        payload: id,
      );
      _posted[key] = nid;
      if (!silent) await _playSound();
      debugPrint('[NotificationService] showPermission SUCCESS');
    } catch (e) {
      debugPrint('[NotificationService] showPermission ERROR: $e');
    }
  }

  /// Show a generic notification.
  Future<void> showNotification({
    required String id,
    required String key,
    required String title,
    required String body,
    bool silent = false,
  }) async {
    final nid = _notifId(id);
    debugPrint(
      '[NotificationService] showNotification id=$id nid=$nid title=$title',
    );

    final androidDetails = AndroidNotificationDetails(
      _generalChannel,
      'General',
      channelDescription: 'General helios notifications',
      importance: Importance.max,
      priority: Priority.max,
      playSound: false,
      enableVibration: false,
      fullScreenIntent: true,
      category: AndroidNotificationCategory.alarm,
    );

    const iosDetails = DarwinNotificationDetails(
      presentAlert: true,
      presentBadge: true,
      presentSound: true,
      interruptionLevel: InterruptionLevel.timeSensitive,
    );

    try {
      await _plugin.show(
        nid,
        title,
        body,
        NotificationDetails(android: androidDetails, iOS: iosDetails),
        payload: id,
      );
      _posted[key] = nid;
      if (!silent) await _playSound();
      debugPrint('[NotificationService] showNotification SUCCESS');
    } catch (e) {
      debugPrint('[NotificationService] showNotification ERROR: $e');
    }
  }

  /// Play notification sound and vibrate directly via native Android APIs.
  /// Workaround for OEMs (Realme/ColorOS) that strip sound from notification channels.
  Future<void> _playSound() async {
    if (!_soundEnabled && !_vibrationEnabled) return;
    try {
      await _platform.invokeMethod('playNotificationSound', {
        'sound': _soundEnabled,
        'vibration': _vibrationEnabled,
      });
    } catch (e) {
      debugPrint('[NotificationService] playSound failed: $e');
    }
  }

  void _onResponse(NotificationResponse response) {
    final payload = response.payload;
    if (payload == null) return;

    final actionId = response.actionId;
    if (actionId == 'approve' || actionId == 'deny') {
      onAction?.call(payload, actionId!);
    }
  }
}
