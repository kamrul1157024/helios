import 'package:flutter_local_notifications/flutter_local_notifications.dart';

/// Callback for when a notification action (approve/deny) is tapped.
typedef NotificationActionCallback = void Function(String notificationId, String action);

class NotificationService {
  NotificationService._();
  static final instance = NotificationService._();

  final _plugin = FlutterLocalNotificationsPlugin();
  NotificationActionCallback? onAction;

  Future<void> init() async {
    const androidSettings = AndroidInitializationSettings('@mipmap/ic_launcher');
    const iosSettings = DarwinInitializationSettings(
      requestAlertPermission: true,
      requestBadgePermission: true,
      requestSoundPermission: true,
    );

    await _plugin.initialize(
      const InitializationSettings(android: androidSettings, iOS: iosSettings),
      onDidReceiveNotificationResponse: _onResponse,
    );
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

    return true;
  }

  /// Show a permission request notification with approve/deny actions.
  Future<void> showPermissionNotification({
    required String id,
    required String toolName,
    required String detail,
  }) async {
    final androidDetails = AndroidNotificationDetails(
      'helios_permissions_v2',
      'Permission Requests',
      channelDescription: 'Claude tool permission requests',
      importance: Importance.max,
      priority: Priority.high,
      playSound: true,
      enableVibration: true,
      actions: [
        const AndroidNotificationAction('approve', 'Approve', showsUserInterface: true),
        const AndroidNotificationAction('deny', 'Deny', showsUserInterface: true),
      ],
    );

    const iosDetails = DarwinNotificationDetails(
      presentAlert: true,
      presentBadge: true,
      presentSound: true,
    );

    await _plugin.show(
      id.hashCode,
      'helios — Permission Request',
      '$toolName: $detail',
      const NotificationDetails(android: androidDetails, iOS: iosDetails),
      payload: id,
    );
  }

  /// Show a generic notification.
  Future<void> showNotification({
    required String id,
    required String title,
    required String body,
  }) async {
    const androidDetails = AndroidNotificationDetails(
      'helios_general_v2',
      'General',
      channelDescription: 'General helios notifications',
      importance: Importance.high,
      priority: Priority.high,
      playSound: true,
      enableVibration: true,
    );

    const iosDetails = DarwinNotificationDetails(
      presentAlert: true,
      presentBadge: true,
      presentSound: true,
    );

    await _plugin.show(
      id.hashCode,
      title,
      body,
      const NotificationDetails(android: androidDetails, iOS: iosDetails),
      payload: id,
    );
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
