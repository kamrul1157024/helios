import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/models/desktop.dart';
import 'package:helios/models/host_connection.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/providers/desktop_providers.dart';
import 'package:helios/screens/desktop_screen.dart';
import 'package:helios/screens/host_detail_screen.dart';
import 'package:helios/services/desktop_bridge.dart';
import 'package:helios/services/host_manager.dart';
import 'package:helios/utils/keysyms.dart';
import 'package:helios/widgets/desktop_display_sheet.dart';
import 'package:helios/widgets/desktop_key_bar.dart';

import 'support/fake_desktop.dart';

/// The phone's remote desktop (spec 66, D8 and D9): the install states, the
/// ticket flow, the screen-size sheet, the key bar, and the entry points.

void _phone(WidgetTester tester) {
  tester.view.physicalSize = const Size(412, 915) * 2;
  tester.view.devicePixelRatio = 2;
  addTearDown(tester.view.reset);
}

/// Lets the requests and the page events run. The overlay's spinner never
/// settles, so this steps the clock rather than waiting for quiet.
Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 10; i++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

Future<(FakeDesktopDaemon, FakeBridge)> _open(
  WidgetTester tester,
  Map<String, dynamic>? status,
) async {
  _phone(tester);
  final daemon = FakeDesktopDaemon(status);
  final bridge = FakeBridge();
  await tester.pumpWidget(desktopApp(daemon, bridge));
  await _settle(tester);
  return (daemon, bridge);
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  group('before the desktop is ready', () {
    testWidgets('missing, with root: Install starts the install', (
      tester,
    ) async {
      final (daemon, _) = await _open(
        tester,
        desktopBody('missing', missing: ['xrdp', 'xfce', 'guacd']),
      );
      expect(find.text('This host has no desktop yet.'), findsOneWidget);
      expect(find.text('Missing: xrdp, XFCE, guacd'), findsOneWidget);

      await tester.tap(find.text('Install'));
      await _settle(tester);
      expect(daemon.calls, contains('POST /api/desktop/install'));
    });

    testWidgets('missing, without root: the command, with Copy', (
      tester,
    ) async {
      await _open(tester, desktopBody('missing', canInstall: false));
      expect(
        find.text('Helios needs root once to install the desktop.'),
        findsOneWidget,
      );
      expect(find.text('helios setup desktop'), findsOneWidget);
      expect(find.byTooltip('Copy'), findsOneWidget);
      expect(find.text('Install'), findsNothing);
    });

    testWidgets('installing: progress and the live steps', (tester) async {
      await _open(
        tester,
        desktopBody(
          'installing',
          steps: ['done', 'running'],
          runningLine: 'Setting up xrdp (0.10.1-1) ...',
        ),
      );
      expect(find.text('Installing the desktop'), findsOneWidget);
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
      expect(find.text('Install xrdp and XFCE'), findsOneWidget);
      expect(find.text('Setting up xrdp (0.10.1-1) ...'), findsOneWidget);
      expect(find.byIcon(Icons.check_circle), findsOneWidget);
    });

    testWidgets('a desktop_state event moves the steps without a refetch', (
      tester,
    ) async {
      _phone(tester);
      final daemon = FakeDesktopDaemon(
        desktopBody('installing', steps: ['running']),
      );
      final container = rp.ProviderContainer(
        overrides: [
          hostManagerProvider.overrideWithValue(HostManager()),
          serviceProvider(desktopHost).overrideWithValue(daemon.service()),
          desktopBridgeFactoryProvider.overrideWithValue(FakeBridge.new),
        ],
      );
      addTearDown(container.dispose);
      await tester.pumpWidget(
        rp.UncontrolledProviderScope(
          container: container,
          child: const MaterialApp(home: DesktopScreen(hostId: desktopHost)),
        ),
      );
      await _settle(tester);
      expect(find.byIcon(Icons.check_circle), findsNothing);

      container
          .read(desktopProvider(desktopHost).notifier)
          .apply(desktopBody('installing', steps: ['done', 'done', 'running']));
      await _settle(tester);
      expect(find.byIcon(Icons.check_circle), findsNWidgets(2));
      expect(daemon.calls.where((c) => c == 'GET /api/desktop'), hasLength(1));
    });

    testWidgets('failed: the error in words, and Retry installs again', (
      tester,
    ) async {
      final (daemon, _) = await _open(
        tester,
        desktopBody(
          'failed',
          error: 'Another install is running. Try again when it ends.',
          steps: ['failed'],
        ),
      );
      expect(
        find.text('Another install is running. Try again when it ends.'),
        findsOneWidget,
      );
      await tester.tap(find.text('Retry'));
      await _settle(tester);
      expect(daemon.calls, contains('POST /api/desktop/install'));
    });

    testWidgets('a Retry that needs root shows the command', (tester) async {
      _phone(tester);
      final daemon =
          FakeDesktopDaemon(desktopBody('failed', error: 'apt failed'))
            ..installStatus = 403
            ..installCode = 'needs_root';
      await tester.pumpWidget(desktopApp(daemon, FakeBridge()));
      await _settle(tester);
      await tester.tap(find.text('Retry'));
      await _settle(tester);
      expect(find.text('helios setup desktop'), findsOneWidget);
    });

    testWidgets('starting: a spinner', (tester) async {
      await _open(tester, desktopBody('starting'));
      expect(find.text('Starting the desktop…'), findsOneWidget);
    });

    testWidgets('unsupported: says it needs Linux', (tester) async {
      await _open(tester, desktopBody('unsupported'));
      expect(find.text('Remote desktop needs a Linux host.'), findsOneWidget);
    });

    testWidgets('an old daemon: says so, and never asks for a ticket', (
      tester,
    ) async {
      final (daemon, bridge) = await _open(tester, null);
      expect(find.text('This host runs an older Helios.'), findsOneWidget);
      expect(daemon.tickets, 0);
      expect(bridge.opened, isEmpty);
    });
  });

  group('the ticket', () {
    testWidgets('opens the page with a ticket and never the bearer token', (
      tester,
    ) async {
      final (daemon, bridge) = await _open(tester, desktopBody('ready'));
      expect(daemon.tickets, 1);
      expect(bridge.opened, hasLength(1));
      final page = bridge.opened.single;
      expect(page.path, '/desktop/');
      expect(page.host, 'host.test');
      expect(Uri.splitQueryString(page.fragment), {
        'ticket': 't1',
        'width': '1280',
        'height': '800',
        'dpi': '120',
      });
      expect(page.query, isEmpty, reason: 'nothing a server would log');
      expect(page.toString(), isNot(contains('Bearer')));
      expect(find.text('Connecting to the desktop…'), findsOneWidget);

      bridge.emit('connected');
      await _settle(tester);
      expect(find.text('Connecting to the desktop…'), findsNothing);
    });

    testWidgets('a 4401 gets a fresh ticket and reconnects in the page', (
      tester,
    ) async {
      final (daemon, bridge) = await _open(tester, desktopBody('ready'));
      bridge.emit('closed', code: closeBadTicket);
      await _settle(tester);
      expect(daemon.tickets, 2);
      expect(bridge.opened, hasLength(1), reason: 'the page stays loaded');
      expect(bridge.connects.single.ticket, 't2');
      expect(bridge.connects.single.width, 1280);

      bridge.emit('connected');
      await _settle(tester);
      expect(find.text('Connecting to the desktop…'), findsNothing);
    });

    testWidgets('4401 after 4401 gives up in words', (tester) async {
      final (daemon, bridge) = await _open(tester, desktopBody('ready'));
      for (var i = 0; i <= DesktopLink.maxReticket; i++) {
        bridge.emit('closed', code: closeBadTicket);
        await _settle(tester);
      }
      expect(daemon.tickets, 1 + DesktopLink.maxReticket);
      expect(find.text(closeMessage(closeBadTicket, '')), findsOneWidget);
      expect(find.text('Reconnect'), findsOneWidget);
    });

    for (final (code, words) in [
      (closeTooManyViewers, 'Four people are already watching'),
      (closeGuacdFailed, 'The desktop service stopped'),
      (closeNotReady, 'The desktop is not ready yet'),
    ]) {
      testWidgets('close $code is put into words', (tester) async {
        final (daemon, bridge) = await _open(tester, desktopBody('ready'));
        bridge
          ..emit('connected')
          ..emit('closed', code: code);
        await _settle(tester);
        expect(find.textContaining(words), findsOneWidget);
        expect(daemon.tickets, 1, reason: 'a new ticket would not help');
        if (code == closeNotReady) {
          expect(
            daemon.calls.where((c) => c == 'GET /api/desktop'),
            hasLength(2),
            reason: 'the state is read again',
          );
        }
      });
    }

    testWidgets('Reconnect takes a new ticket', (tester) async {
      final (daemon, bridge) = await _open(tester, desktopBody('ready'));
      bridge
        ..emit('connected')
        ..emit('closed', code: closeGuacdFailed);
      await _settle(tester);
      await tester.tap(find.text('Reconnect'));
      await _settle(tester);
      expect(daemon.tickets, 2);
      expect(bridge.connects.single.ticket, 't2');
    });
  });

  group('DPI', () {
    testWidgets('a host at 96 gets the phone default once', (tester) async {
      final (daemon, _) = await _open(tester, desktopBody('ready', dpi: 96));
      expect(daemon.dpiWrites, [120]);

      await tester.pumpWidget(const SizedBox());
      final again = FakeDesktopDaemon(desktopBody('ready', dpi: 96));
      await tester.pumpWidget(desktopApp(again, FakeBridge()));
      await _settle(tester);
      expect(again.dpiWrites, isEmpty, reason: 'remembered for the host');
    });

    testWidgets('a host somebody already set is left alone', (tester) async {
      final (daemon, _) = await _open(tester, desktopBody('ready', dpi: 144));
      expect(daemon.dpiWrites, isEmpty);
    });
  });

  group('the screen-size sheet', () {
    Future<(FakeDesktopDaemon, FakeBridge)> openSheet(
      WidgetTester tester,
    ) async {
      final opened = await _open(tester, desktopBody('ready'));
      opened.$2.emit('connected');
      await _settle(tester);
      await tester.tap(find.byTooltip('Screen size and DPI'));
      await _settle(tester);
      return opened;
    }

    testWidgets('starts at 16:10, 1280 × 800', (tester) async {
      await openSheet(tester);
      expect(find.text('1280 × 800'), findsOneWidget);
    });

    testWidgets('an aspect and a height resize the open desktop', (
      tester,
    ) async {
      final (_, bridge) = await openSheet(tester);
      await tester.tap(find.text('16:9'));
      await _settle(tester);
      expect(bridge.resizes.last, (1422, 800));
      await tester.tap(find.text('1080'));
      await _settle(tester);
      expect(bridge.resizes.last, (1920, 1080));
      expect(find.text('1920 × 1080'), findsOneWidget);
    });

    testWidgets('Fit phone takes the phone held sideways', (tester) async {
      final (_, bridge) = await openSheet(tester);
      await tester.tap(find.text('Fit phone'));
      await _settle(tester);
      // 915 / 412 of 800.
      expect(bridge.resizes.last, (1776, 800));
    });

    testWidgets('Custom is clamped to what the host takes', (tester) async {
      final (_, bridge) = await openSheet(tester);
      // The size's Custom, not the DPI's.
      await tester.tap(find.text('Custom').first);
      await _settle(tester);
      await tester.enterText(
        find.byKey(const Key('desktop-width-field')),
        '99',
      );
      await tester.enterText(
        find.byKey(const Key('desktop-height-field')),
        '1001',
      );
      await tester.tap(find.text('Apply'));
      await _settle(tester);
      expect(bridge.resizes.last, (640, 1002));
    });

    testWidgets('a DPI preset goes to the host', (tester) async {
      final (daemon, _) = await openSheet(tester);
      await tester.tap(find.text('144 · 150%'));
      await _settle(tester);
      expect(daemon.dpiWrites, [144]);
    });

    testWidgets('the choice is kept for the host', (tester) async {
      await openSheet(tester);
      await tester.tap(find.text('4:3'));
      await _settle(tester);
      final prefs = await SharedPreferences.getInstance();
      expect(
        prefs.getString(DesktopPrefsNotifier.keyFor(desktopHost)),
        contains('"aspect":"classic4x3"'),
      );
    });

    testWidgets('a refused DPI says why', (tester) async {
      _phone(tester);
      final daemon = FakeDesktopDaemon(desktopBody('ready'))
        ..dpiStatus = 409
        ..dpiCode = 'not_ready';
      final bridge = FakeBridge();
      await tester.pumpWidget(desktopApp(daemon, bridge));
      await _settle(tester);
      await tester.tap(find.byTooltip('Screen size and DPI'));
      await _settle(tester);
      await tester.tap(find.text('192 · 200%'));
      await _settle(tester);
      expect(find.textContaining('not running'), findsOneWidget);
    });
  });

  group('keys', () {
    test('typed characters become keysyms', () {
      expect(keysymForRune('a'.codeUnitAt(0)), 0x61);
      expect(keysymForRune('é'.runes.first), 0xE9);
      expect(keysymForRune('€'.runes.first), 0x010020AC);
      expect(keysymForRune(0x0A), keyReturn);
      expect(chordsForText('Hi'), [
        [0x48],
        [0x69],
      ]);
    });

    test('Ctrl with a capital letter is the plain shortcut', () {
      expect(chordsForText('C', ctrl: true), [
        [keyControl, 0x63],
      ]);
    });

    test('a modifier holds for one key, then lets go', () {
      final m = DesktopModifiers();
      m.toggleCtrl();
      m.toggleAlt();
      expect(m.take(keyTab), [keyControl, keyAlt, keyTab]);
      expect(m.ctrl, isFalse);
      expect(m.alt, isFalse);
      expect(m.take(keyTab), [keyTab]);
    });

    test('typed text takes the modifier on its first character only', () {
      final m = DesktopModifiers()..toggleCtrl();
      expect(m.takeText('ab'), [
        [keyControl, 0x61],
        [0x62],
      ]);
      expect(m.ctrl, isFalse);
    });

    testWidgets('the key bar sends its keys', (tester) async {
      final (_, bridge) = await _open(tester, desktopBody('ready'));
      bridge.emit('connected');
      await _settle(tester);

      // The bar is wider than a phone and builds what is in view.
      Future<void> reach(Finder key) => tester.scrollUntilVisible(
        key,
        60,
        scrollable: find.descendant(
          of: find.byType(DesktopKeyBar),
          matching: find.byType(Scrollable),
        ),
      );

      await tester.tap(find.text('esc'));
      await tester.tap(find.text('ctrl'));
      await _settle(tester);
      await tester.tap(find.text('tab'));
      await tester.tap(find.text('tab'));
      await reach(find.text('^C'));
      await tester.tap(find.text('^C'));
      await reach(find.text('^V'));
      await tester.tap(find.text('^V'));
      await reach(find.byIcon(Icons.keyboard_arrow_left));
      await tester.tap(find.byIcon(Icons.keyboard_arrow_left));
      await _settle(tester);
      expect(bridge.keys, [
        [keyEscape],
        [keyControl, keyTab],
        [keyTab],
        [keyControl, 0x63],
        [keyControl, 0x76],
        [keyLeft],
      ]);
    });

    testWidgets('the phone keyboard types into the desktop', (tester) async {
      final (_, bridge) = await _open(tester, desktopBody('ready'));
      bridge.emit('connected');
      await _settle(tester);

      await tester.tap(find.byTooltip('Show keyboard'));
      await _settle(tester);
      expect(bridge.keyboard, [true]);

      await tester.tap(find.text('ctrl'));
      await _settle(tester);
      final field = find.byType(TextField);
      await tester.enterText(field, '\u200B\u200Bab');
      await _settle(tester);
      expect(bridge.keys, [
        [keyControl, 0x61],
        [0x62],
      ]);

      // A backspace deletes one of the two zero-width spaces the field keeps.
      await tester.enterText(field, '\u200B');
      await _settle(tester);
      expect(bridge.keys.last, [keyBackspace]);
    });
  });

  testWidgets('a keyboard Back closed stays closed after the sheet', (
    tester,
  ) async {
    final (_, bridge) = await _open(tester, desktopBody('ready'));
    bridge.emit('connected');
    await _settle(tester);
    bool sinkFocused() =>
        tester.widget<TextField>(find.byType(TextField)).focusNode!.hasFocus;

    await tester.tap(find.byTooltip('Show keyboard'));
    tester.view.viewInsets = const FakeViewPadding(bottom: 600);
    await _settle(tester);
    expect(sinkFocused(), isTrue);

    // Back: the system hides the keyboard and leaves focus alone.
    tester.view.viewInsets = FakeViewPadding.zero;
    await _settle(tester);
    expect(sinkFocused(), isFalse);
    expect(find.byTooltip('Show keyboard'), findsOneWidget);
    expect(bridge.keyboard, [true, false]);

    await tester.tap(find.byTooltip('Screen size and DPI'));
    await _settle(tester);
    Navigator.of(tester.element(find.byType(DesktopDisplaySheet))).pop();
    await _settle(tester);
    expect(sinkFocused(), isFalse, reason: 'the sheet hands focus to nothing');
    expect(bridge.keyboard, [true, false]);
  });

  group('entry points', () {
    testWidgets('the host page offers the desktop', (tester) async {
      await _hostPage(tester, desktopBody('missing'));
      expect(find.text('Desktop'), findsOneWidget);
      expect(find.text('Not installed'), findsOneWidget);
    });

    testWidgets('an old daemon has no desktop entry', (tester) async {
      await _hostPage(tester, null);
      expect(find.text('Desktop'), findsNothing);
    });

    test('the session menu entry follows the same answer', () async {
      for (final (status, expected) in [
        (desktopBody('ready'), true),
        (null, false),
      ]) {
        final daemon = FakeDesktopDaemon(status);
        final container = rp.ProviderContainer(
          overrides: [
            hostManagerProvider.overrideWithValue(HostManager()),
            serviceProvider(desktopHost).overrideWithValue(daemon.service()),
          ],
        );
        addTearDown(container.dispose);
        container.listen(desktopAvailableProvider(desktopHost), (_, _) {});
        await container.read(desktopProvider(desktopHost).future);
        expect(container.read(desktopAvailableProvider(desktopHost)), expected);
      }
    });
  });

  test('sizes resolve and clamp the way the host will', () {
    const screen = math.Point(412.0, 915.0);
    expect(const DesktopDisplayPrefs().resolve(screen), (1280, 800));
    expect(
      const DesktopDisplayPrefs(
        aspect: DesktopAspect.ultra21x9,
        height: 1440,
      ).resolve(screen),
      (3360, 1440),
    );
    expect(clampDesktopSize(10000, 100), (desktopMaxWidth, desktopMinHeight));
    expect(clampDesktopSize(1281, 801), (1282, 802));
  });
}

class _Hosts extends HostManager {
  @override
  HostConnection? hostById(String hostId) => HostConnection(
    id: desktopHost,
    label: 'build box',
    serverUrl: 'http://host.test:7654',
    deviceId: 'd1',
    colorIndex: 0,
    addedAt: DateTime(2026),
  );
}

Future<void> _hostPage(
  WidgetTester tester,
  Map<String, dynamic>? status,
) async {
  _phone(tester);
  final hosts = _Hosts();
  final daemon = FakeDesktopDaemon(status);
  await tester.pumpWidget(
    ChangeNotifierProvider<HostManager>.value(
      value: hosts,
      child: desktopApp(
        daemon,
        FakeBridge(),
        hosts: hosts,
        home: const HostDetailScreen(hostId: desktopHost),
      ),
    ),
  );
  await _settle(tester);
}
