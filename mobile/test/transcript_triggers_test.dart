import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/models/session.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/providers/theme_provider.dart';
import 'package:helios/screens/session_detail_screen.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/services/host_manager.dart';
import 'package:helios/widgets/skeleton.dart';

/// The two moments a reader arrives at a transcript.
///
/// An event is a hint that there is more to read, never the only cue: the
/// daemon drops a broadcast to a client whose buffer is full and keeps nothing
/// across a disconnect. So the screen reads again whenever someone can see it —
/// on open, and on returning to the foreground. Neither can be asserted below
/// the widget, because both are wiring rather than logic.

const _host = 'h1';
const _session = 's1';

Session session() => Session(
      hostId: _host,
      sessionId: _session,
      source: 'claude',
      cwd: '/tmp/p',
      project: 'p',
      status: 'idle',
      createdAt: '2026-01-01T00:00:00Z',
    );

/// Answers every transcript read with one more message than the last.
class Daemon {
  Daemon({this.empty = false, this.gateAfter});

  /// Answer with no messages at all — a session opened before the agent has
  /// written anything.
  final bool empty;

  /// Hold every read past this many, so a test can look at the screen while a
  /// request is still out.
  final int? gateAfter;
  final _gate = Completer<void>();
  void release() => _gate.complete();

  final List<Uri> asked = [];
  int _seq = 0;

  Future<http.Response> handle(http.Request req) async {
    final path = req.url.path;
    if (path.contains('/auth/')) {
      return http.Response(
        jsonEncode({
          'token': 't',
          'expires_at': DateTime.now()
              .toUtc()
              .add(const Duration(hours: 1))
              .toIso8601String(),
        }),
        200,
      );
    }
    if (!path.endsWith('/transcript')) return http.Response('{}', 404);

    asked.add(req.url);
    if (gateAfter != null && asked.length > gateAfter!) await _gate.future;
    if (empty) {
      return http.Response(
        jsonEncode({
          'messages': [],
          'total': 0,
          'returned': 0,
          'offset': 0,
          'has_more': false,
          'epoch': 'e1',
        }),
        200,
      );
    }
    _seq++;
    return http.Response(
      jsonEncode({
        'messages': [
          {
            'seq': _seq,
            'role': 'assistant',
            'content': 'm$_seq',
            'timestamp': '2026-01-01T00:00:00Z',
          }
        ],
        'total': _seq,
        'returned': 1,
        'offset': 0,
        'has_more': false,
        'epoch': 'e1',
      }),
      200,
    );
  }

  List<Uri> get deltas =>
      asked.where((u) => u.queryParameters.containsKey('after_seq')).toList();
}

/// One container for the whole test, so the transcript survives the screen
/// being torn down — which is the situation these triggers exist for.
class Harness {
  Harness(this.tester, {bool empty = false, int? gateAfter})
      : daemon = Daemon(empty: empty, gateAfter: gateAfter),
        hosts = HostManager() {
    final service = DaemonAPIService(
      hostId: _host,
      serverUrl: 'http://localhost:1',
      api: ApiClient(
        serverUrl: 'http://localhost:1',
        deviceId: 'd1',
        privateKeySeed: Uint8List(32),
        client: MockClient(daemon.handle),
      ),
    );
    container = rp.ProviderContainer(
      overrides: [
        hostManagerProvider.overrideWithValue(hosts),
        serviceProvider(_host).overrideWithValue(service),
      ],
    );
  }

  final WidgetTester tester;
  final Daemon daemon;
  final HostManager hosts;
  late final rp.ProviderContainer container;

  Future<void> show(Widget child) async {
    await tester.pumpWidget(
      rp.UncontrolledProviderScope(
        container: container,
        child: MultiProvider(
          providers: [
            ChangeNotifierProvider.value(value: hosts),
            ChangeNotifierProvider(create: (_) => ThemeProvider()),
          ],
          child: MaterialApp(home: child),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 50));
  }

  Future<void> openSession() => show(SessionDetailScreen(session: session()));

  Future<void> leaveSession() => show(const Scaffold());

  void dispose() {
    container.dispose();
    hosts.dispose();
  }
}

Harness harnessFor(WidgetTester tester, {bool empty = false, int? gateAfter}) {
  // The default 800x600 test surface is shorter than the composer plus the
  // message list, and the overflow is reported as a test failure.
  tester.view.physicalSize = const Size(1080, 2400);
  tester.view.devicePixelRatio = 3.0;
  addTearDown(tester.view.reset);

  final h = Harness(tester, empty: empty, gateAfter: gateAfter);
  addTearDown(h.dispose);
  return h;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('coming back to a session reads what arrived while it was closed',
      (tester) async {
    final h = harnessFor(tester);

    await h.openSession();
    expect(h.daemon.asked, hasLength(1), reason: 'the first page, from build()');
    expect(h.daemon.deltas, isEmpty,
        reason: 'nothing held yet, so there is no cursor to quote');

    await h.leaveSession();
    await h.openSession();

    expect(h.daemon.deltas, hasLength(1),
        reason: 'the provider outlives the screen, so build() does not run '
            'again — without this trigger, re-entering reads nothing');
  });

  testWidgets('coming back to the foreground reads the tail', (tester) async {
    final h = harnessFor(tester);
    await h.openSession();
    final before = h.daemon.deltas.length;

    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump(const Duration(milliseconds: 50));

    expect(h.daemon.deltas.length, before + 1,
        reason: 'a turn can finish while the phone is locked, and the event '
            'that announced it is gone by the time the screen is back');
  });

  testWidgets('neither read puts the skeleton back over the conversation',
      (tester) async {
    final h = harnessFor(tester);
    await h.openSession();
    await h.leaveSession();
    await h.openSession();

    // Mid-read: the delta is out, and the messages already held must still be
    // the thing on screen.
    expect(find.text('m1'), findsOneWidget);
    await tester.pump(const Duration(milliseconds: 50));
    expect(find.text('m2'), findsOneWidget);
  });

  testWidgets('a session with nothing written yet re-reads without a skeleton',
      (tester) async {
    // Nothing is held, so there is no epoch to quote and the catch-up falls
    // back to reading the page again. That is a refresh, and a refreshing
    // AsyncValue still reports isLoading while carrying its previous value.
    // The catch-up read is held open, so the screen is observed while the
    // refresh is still out rather than after it has landed.
    final h = harnessFor(tester, empty: true, gateAfter: 1);
    await h.openSession();
    await h.leaveSession();
    await h.openSession();

    expect(find.byType(MessageListSkeleton), findsNothing,
        reason: 'the skeleton belongs to a first load; drawing it on a refresh '
            'is what threw away the reader place this whole change protects');
  });
}
