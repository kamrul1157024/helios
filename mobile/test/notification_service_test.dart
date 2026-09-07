import 'package:flutter/services.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/services/notification_service.dart';

/// The channel flutter_local_notifications talks to.
const _pluginChannel = MethodChannel(
  'dexterous.com/flutter/local_notifications',
);

/// Helios' own channel for sound/vibration and channel creation.
const _nativeChannel = MethodChannel('com.helios.helios/notifications');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late List<MethodCall> calls;

  /// What the platform reports is currently in the tray.
  late List<Map<String, Object?>> activeNotifications;

  setUp(() async {
    calls = [];
    activeNotifications = [];
    SharedPreferences.setMockInitialValues({});

    // Unit tests get no plugin registration, so the platform instance the
    // plugin resolves against has to be installed by hand.
    AndroidFlutterLocalNotificationsPlugin.registerWith();

    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(_pluginChannel, (call) async {
      calls.add(call);
      // initialize() returns a bool; everything else here returns void.
      if (call.method == 'initialize') return true;
      if (call.method == 'getActiveNotifications') return activeNotifications;
      return null;
    });
    messenger.setMockMethodCallHandler(_nativeChannel, (call) async => null);

    await NotificationService.instance.init();
    // init() replays channel setup; only the show/cancel traffic matters.
    calls.clear();
  });

  tearDown(() async {
    await NotificationService.instance.cancelAll();
    final messenger =
        TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(_pluginChannel, null);
    messenger.setMockMethodCallHandler(_nativeChannel, null);
  });

  int? cancelledId() {
    for (final c in calls) {
      if (c.method == 'cancel') {
        final args = c.arguments;
        if (args is Map) return args['id'] as int?;
        if (args is int) return args;
      }
    }
    return null;
  }

  int? shownId() {
    for (final c in calls) {
      if (c.method == 'show') {
        return (c.arguments as Map)['id'] as int?;
      }
    }
    return null;
  }

  test('cancel on an unposted key is a no-op', () async {
    await NotificationService.instance.cancel('host-a:missing');
    expect(calls.where((c) => c.method == 'cancel'), isEmpty);
  });

  test('isPosted flips on show and back on cancel', () async {
    const key = 'host-a:n1';
    expect(NotificationService.instance.isPosted(key), isFalse);

    await NotificationService.instance.showNotification(
      id: '{"hostId":"host-a","notificationId":"n1"}',
      key: key,
      title: 'Task completed',
      body: 'done',
      silent: true,
    );
    expect(NotificationService.instance.isPosted(key), isTrue);

    await NotificationService.instance.cancel(key);
    expect(NotificationService.instance.isPosted(key), isFalse);
  });

  // The plugin id is derived from the key, so any isolate holding the same key
  // arrives at the same integer without sharing memory.
  test('cancel uses the same integer id that show used', () async {
    const key = 'host-a:n1';
    await NotificationService.instance.showPermissionNotification(
      id: '{"hostId":"host-a","notificationId":"n1"}',
      key: key,
      toolName: 'Bash',
      detail: 'rm -rf /tmp/x',
      silent: true,
    );
    final posted = shownId();
    expect(posted, isNotNull);

    calls.clear();
    await NotificationService.instance.cancel(key);
    expect(cancelledId(), posted);
  });

  test('cancelling one key leaves the other posted', () async {
    await NotificationService.instance.showNotification(
      id: 'p1',
      key: 'host-a:n1',
      title: 'a',
      body: 'a',
      silent: true,
    );
    await NotificationService.instance.showNotification(
      id: 'p2',
      key: 'host-b:n2',
      title: 'b',
      body: 'b',
      silent: true,
    );

    await NotificationService.instance.cancel('host-a:n1');

    expect(NotificationService.instance.isPosted('host-a:n1'), isFalse);
    expect(NotificationService.instance.isPosted('host-b:n2'), isTrue);
  });

  test('cancelAll retracts everything and clears tracking', () async {
    await NotificationService.instance.showNotification(
      id: 'p1',
      key: 'host-a:n1',
      title: 'a',
      body: 'a',
      silent: true,
    );
    await NotificationService.instance.showNotification(
      id: 'p2',
      key: 'host-b:n2',
      title: 'b',
      body: 'b',
      silent: true,
    );

    calls.clear();
    await NotificationService.instance.cancelAll();

    expect(calls.where((c) => c.method == 'cancel').length, 2);
    expect(NotificationService.instance.isPosted('host-a:n1'), isFalse);
    expect(NotificationService.instance.isPosted('host-b:n2'), isFalse);
  });

  // The daemon owns notification status; the tray is only a view of it. So the
  // sweep is driven by the pending set, not by the rows the daemon reported as
  // resolved — the daemon prunes old notifications, and one that ages out of
  // the response entirely would never be reached by a per-row sweep.
  group('retainOnly', () {
    Future<void> post(String host, String id) =>
        NotificationService.instance.showNotification(
          id: '{"hostId":"$host","notificationId":"$id"}',
          key: NotificationService.notifKey(host, id),
          title: id,
          body: id,
          silent: true,
        );

    test('retracts a posted notification the daemon no longer lists', () async {
      await post('host-a', 'n1');
      await post('host-a', 'n2');

      // n1 is gone from the response altogether, not merely marked resolved.
      await NotificationService.instance.retainOnly('host-a', {'n2'});

      expect(NotificationService.instance.isPosted('host-a:n1'), isFalse);
      expect(NotificationService.instance.isPosted('host-a:n2'), isTrue);
    });

    test('leaves other hosts alone', () async {
      await post('host-a', 'n1');
      await post('host-b', 'n1');

      await NotificationService.instance.retainOnly('host-a', {});

      expect(NotificationService.instance.isPosted('host-a:n1'), isFalse);
      expect(NotificationService.instance.isPosted('host-b:n1'), isTrue);
    });

    test('an empty pending set clears the host', () async {
      await post('host-a', 'n1');
      await post('host-a', 'n2');
      calls.clear();

      await NotificationService.instance.retainOnly('host-a', {});

      expect(calls.where((c) => c.method == 'cancel').length, 2);
    });

    test(
      'everything still pending is left posted and nothing is cancelled',
      () async {
        await post('host-a', 'n1');
        calls.clear();

        await NotificationService.instance.retainOnly('host-a', {'n1'});

        expect(calls.where((c) => c.method == 'cancel'), isEmpty);
        expect(NotificationService.instance.isPosted('host-a:n1'), isTrue);
      },
    );

    test('a host with nothing posted is a no-op', () async {
      await NotificationService.instance.retainOnly('host-z', {'n1'});
      expect(calls.where((c) => c.method == 'cancel'), isEmpty);
    });
  });

  // The foreground service posts from its own isolate, with its own heap. The
  // app can only retract what the service posted by reaching the same integer
  // from the same key, and by learning the key from the one thing they share.
  group('across the isolate boundary', () {
    test('a key posted by the other isolate is retractable here', () async {
      const key = 'host-a:n1';

      // What the service isolate would have posted.
      await NotificationService.instance.showPermissionNotification(
        id: '{"hostId":"host-a","notificationId":"n1"}',
        key: key,
        toolName: 'Bash',
        detail: 'rm -rf /tmp/x',
        silent: true,
      );
      final postedId = shownId();
      expect(postedId, isNotNull);
      activeNotifications = [
        {'id': postedId},
      ];

      // This isolate knows nothing about it until it reads the shared file.
      NotificationService.instance.forgetPostedForTest();
      expect(NotificationService.instance.isPosted(key), isFalse);

      await NotificationService.instance.reloadPosted();
      expect(NotificationService.instance.isPosted(key), isTrue);

      calls.clear();
      await NotificationService.instance.cancel(key);
      expect(cancelledId(), postedId);
    });

    // A force-stop, a reboot or a swipe of the shade empties the tray without
    // telling anyone. The key would otherwise keep answering "already posted"
    // and that approval would never buzz again.
    test('a key whose notification is gone from the tray is dropped', () async {
      const key = 'host-a:n1';
      await NotificationService.instance.showNotification(
        id: 'p1',
        key: key,
        title: 'a',
        body: 'a',
        silent: true,
      );
      expect(NotificationService.instance.isPosted(key), isTrue);

      // The tray now reports nothing, as it would after a force-stop.
      activeNotifications = [];
      await NotificationService.instance.reloadPosted();

      expect(NotificationService.instance.isPosted(key), isFalse);
    });

    test('a key still in the tray survives the prune', () async {
      const key = 'host-a:n1';
      await NotificationService.instance.showNotification(
        id: 'p1',
        key: key,
        title: 'a',
        body: 'a',
        silent: true,
      );
      activeNotifications = [
        {'id': shownId()},
      ];

      await NotificationService.instance.reloadPosted();

      expect(NotificationService.instance.isPosted(key), isTrue);
    });

    test('showing writes the key through to shared storage', () async {
      await NotificationService.instance.showNotification(
        id: 'p1',
        key: 'host-a:n1',
        title: 'a',
        body: 'a',
        silent: true,
      );

      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getStringList('notif_posted_keys'), contains('host-a:n1'));

      await NotificationService.instance.cancel('host-a:n1');
      expect(
        prefs.getStringList('notif_posted_keys'),
        isNot(contains('host-a:n1')),
      );
    });
  });

  test('notifKey is stable and host-scoped', () {
    expect(NotificationService.notifKey('h', 'n'), 'h:n');
    expect(
      NotificationService.notifKey('h1', 'n'),
      isNot(NotificationService.notifKey('h2', 'n')),
    );
  });
}
