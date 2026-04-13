import 'dart:convert';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Callback for when a notification action (approve/deny) is tapped.
typedef NotificationActionCallback = void Function(String notificationId, String action);

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

  static const Map<String, bool> _defaultAlertTypes = {
    'claude.permission':       true,
    'claude.question':         true,
    'claude.elicitation.form': true,
    'claude.elicitation.url':  true,
    'claude.done':             true,
    'claude.error':            true,
  };

  bool _soundEnabled = true;
  bool _vibrationEnabled = true;
  Map<String, bool> _alertTypes = Map.of(_defaultAlertTypes);

  bool get soundEnabled => _soundEnabled;
  bool get vibrationEnabled => _vibrationEnabled;
  Map<String, bool> get alertTypes => Map.unmodifiable(_alertTypes);

  bool isAlertEnabled(String notifType) => _alertTypes[notifType] ?? true;

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
          ...decoded.map((k, v) => MapEntry(k, v as bool)),
        };
      } catch (_) {
        _alertTypes = Map.of(_defaultAlertTypes);
      }
    }

    const androidSettings = AndroidInitializationSettings('@mipmap/ic_launcher');
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

    final android = _plugin.resolvePlatformSpecificImplementation<
        AndroidFlutterLocalNotificationsPlugin>();
    if (android != null) {
      // Clean up all old channel IDs.
      for (final old in [
        'helios_permissions', 'helios_general',
        'helios_perm_v2', 'helios_general_v2',
        'helios_perm_v3', 'helios_general_v3',
        'helios_perm_v4', 'helios_general_v4',
        'helios_perm_v5', 'helios_general_v5',
        'helios_perm_v6', 'helios_general_v6',
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
      debugPrint('[NotificationService] Native channel creation not available, using plugin');
      // Fallback to plugin-based channel creation.
      if (android != null) {
        await android.deleteNotificationChannel(_permChannel);
        await android.deleteNotificationChannel(_generalChannel);

        await android.createNotificationChannel(AndroidNotificationChannel(
          _permChannel,
          'Permission Requests',
          description: 'Claude tool permission requests',
          importance: Importance.max,
          playSound: false,
          enableVibration: false,
        ));
        await android.createNotificationChannel(AndroidNotificationChannel(
          _generalChannel,
          'General',
          description: 'General helios notifications',
          importance: Importance.max,
          playSound: false,
          enableVibration: false,
        ));
      }
    }
  }

  Future<bool> requestPermission() async {
    final android = _plugin.resolvePlatformSpecificImplementation<
        AndroidFlutterLocalNotificationsPlugin>();
    if (android != null) {
      final granted = await android.requestNotificationsPermission();
      return granted ?? false;
    }

    final ios = _plugin.resolvePlatformSpecificImplementation<
        IOSFlutterLocalNotificationsPlugin>();
    if (ios != null) {
      final granted = await ios.requestPermissions(alert: true, badge: true, sound: true);
      return granted ?? false;
    }

    final macos = _plugin.resolvePlatformSpecificImplementation<
        MacOSFlutterLocalNotificationsPlugin>();
    if (macos != null) {
      final granted = await macos.requestPermissions(alert: true, badge: true, sound: true);
      return granted ?? false;
    }

    return true;
  }

  /// Show a permission request notification with approve/deny actions.
  Future<void> showPermissionNotification({
    required String id,
    required String toolName,
    required String detail,
    bool silent = false,
  }) async {
    final nid = _notifId(id);
    debugPrint('[NotificationService] showPermission id=$id nid=$nid tool=$toolName');

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
        const AndroidNotificationAction('approve', 'Approve', showsUserInterface: true),
        const AndroidNotificationAction('deny', 'Deny', showsUserInterface: true),
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
      if (!silent) await _playSound();
      debugPrint('[NotificationService] showPermission SUCCESS');
    } catch (e) {
      debugPrint('[NotificationService] showPermission ERROR: $e');
    }
  }

  /// Show a generic notification.
  Future<void> showNotification({
    required String id,
    required String title,
    required String body,
    bool silent = false,
  }) async {
    final nid = _notifId(id);
    debugPrint('[NotificationService] showNotification id=$id nid=$nid title=$title');

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
