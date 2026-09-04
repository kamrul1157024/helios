import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:helios/models/notification.dart';
import 'package:helios/providers/cards.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/screens/file_browser_screen.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';

/// A plan is not a yes-or-no question, and the phone asked it as one: Approve
/// or Deny, over a body of raw JSON. These pin the rows that replaced them.

const _plan =
    '# Plan: give plan approval its own rows\n\n'
    '## Context\nThe card only offers Approve and Deny.\n';

HeliosNotification _planNotification() => HeliosNotification(
  id: 'n1',
  source: 'claude',
  sourceSession: 's1',
  cwd: '/tmp/proj',
  type: 'claude.permission',
  status: 'pending',
  createdAt: DateTime.now().toUtc().toIso8601String(),
  payload: {
    'tool_name': 'ExitPlanMode',
    'tool_input': {'plan': _plan, 'planFilePath': '~/.claude/plans/rows.md'},
  },
);

HeliosNotification _bashNotification() => HeliosNotification(
  id: 'n2',
  source: 'claude',
  sourceSession: 's1',
  cwd: '/tmp/proj',
  type: 'claude.permission',
  status: 'pending',
  createdAt: DateTime.now().toUtc().toIso8601String(),
  payload: {
    'tool_name': 'Bash',
    'tool_input': {'command': 'ls -la'},
  },
);

/// A service whose action posts land in [sent] instead of on a daemon.
DaemonAPIService _serviceRecording(List<Map<String, dynamic>> sent) {
  final client = MockClient((req) async {
    if (req.url.path.contains('/auth/')) {
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
    if (req.url.path.endsWith('/action')) {
      sent.add(jsonDecode(req.body) as Map<String, dynamic>);
      return http.Response('{}', 200);
    }
    return http.Response('{}', 200);
  });
  return DaemonAPIService(
    hostId: 'h1',
    serverUrl: 'http://localhost:1',
    api: ApiClient(
      serverUrl: 'http://localhost:1',
      deviceId: 'd1',
      privateKeySeed: Uint8List(32),
      client: client,
    ),
  );
}

/// Records what the card pushes, so a screen that wants a daemon behind it can
/// be inspected without being built.
class _Pushes extends NavigatorObserver {
  final List<Route<dynamic>> routes = [];

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previous) {
    routes.add(route);
  }
}

Future<void> _pumpCard(
  WidgetTester tester,
  HeliosNotification n,
  DaemonAPIService sse, {
  NavigatorObserver? observer,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      navigatorObservers: observer == null ? const [] : [observer],
      home: Scaffold(
        body: SingleChildScrollView(
          child: PermissionCard(
            notification: n,
            sse: sse,
            selected: <String>{},
            onSelectionChanged: () {},
          ),
        ),
      ),
    ),
  );
}

void main() {
  testWidgets('a plan gets the mode rows and a way to disagree', (
    tester,
  ) async {
    await _pumpCard(tester, _planNotification(), _serviceRecording([]));

    expect(find.text('Ready to code?'), findsOneWidget);
    expect(find.text('Yes, and use auto mode'), findsOneWidget);
    expect(find.text('Yes, manually approve edits'), findsOneWidget);
    expect(find.text('Tell Claude what to change'), findsOneWidget);

    // The plan is named and left in its file. Printed in full it filled the
    // screen before the rows that answer it, and as raw markdown at that.
    expect(find.text('Plan: give plan approval its own rows'), findsOneWidget);
    expect(find.text('rows.md'), findsOneWidget);
    expect(find.text('View plan'), findsOneWidget);
    expect(find.textContaining('## Context'), findsNothing);

    // There is no command to edit and the CLI sends no rule suggestions for
    // this tool, so the row that offers both would lead nowhere.
    expect(find.text('Edit before approving'), findsNothing);
  });

  // The plan has to be readable from the phone, and the viewer renders the
  // markdown the card would have shown as characters.
  testWidgets('View plan opens the file the CLI wrote', (tester) async {
    final pushes = _Pushes();
    await _pumpCard(
      tester,
      _planNotification(),
      _serviceRecording([]),
      observer: pushes,
    );

    await tester.tap(find.text('View plan'));

    // The route is read rather than run: the viewer reads the file off the
    // host, which is not what this test is about.
    final route = pushes.routes.last as MaterialPageRoute;
    expect(route.settings.name, '/file-viewer');
    final viewer =
        route.builder(tester.element(find.byType(PermissionCard)))
            as FileViewerScreen;
    expect(viewer.path, '~/.claude/plans/rows.md');
    // Rooted where the plan lives, not at the project: "show in folder" from
    // the viewer has to land on a folder the file is in.
    expect(viewer.rootPath, '~/.claude/plans');
  });

  // The test above reads the route without running it, so it never caught
  // that opening the viewer threw. This one builds the screen.
  //
  // What it threw: `ref` in `initState` resolves the ProviderScope through
  // `dependOnInheritedWidgetOfExactType`, which Flutter forbids there. The
  // check is an assert, so only a debug build shows the red screen.
  testWidgets('the viewer the button opens actually builds', (tester) async {
    await tester.pumpWidget(
      rp.ProviderScope(
        overrides: [
          readFileProvider.overrideWith(
            (ref, key) async => FileReadResult(
              path: key.$2,
              size: _plan.length,
              content: _plan,
            ),
          ),
        ],
        child: MaterialApp(
          home: Scaffold(
            body: SingleChildScrollView(
              child: PermissionCard(
                notification: _planNotification(),
                sse: _serviceRecording([]),
                selected: const <String>{},
                onSelectionChanged: () {},
              ),
            ),
          ),
        ),
      ),
    );

    await tester.tap(find.text('View plan'));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    // The plan is on screen as rendered markdown, which is the whole point of
    // sending the reader here instead of printing it on the card.
    expect(find.byType(FileViewerScreen), findsOneWidget);
    expect(
      find.textContaining('give plan approval its own rows'),
      findsWidgets,
    );
  });

  // A plan the CLI wrote nowhere leaves the card as the only copy, so it
  // keeps the whole text.
  testWidgets('a plan with no file is still readable on the card', (
    tester,
  ) async {
    final n = HeliosNotification(
      id: 'n3',
      source: 'claude',
      sourceSession: 's1',
      cwd: '/tmp/proj',
      type: 'claude.permission',
      status: 'pending',
      createdAt: DateTime.now().toUtc().toIso8601String(),
      payload: {
        'tool_name': 'ExitPlanMode',
        'tool_input': {'plan': _plan},
      },
    );
    await _pumpCard(tester, n, _serviceRecording([]));

    expect(find.textContaining('## Context'), findsOneWidget);
    expect(find.text('View plan'), findsNothing);
  });

  testWidgets('approving a plan waits for a mode, then sends it', (
    tester,
  ) async {
    final sent = <Map<String, dynamic>>[];
    await _pumpCard(tester, _planNotification(), _serviceRecording(sent));

    final approve = find.widgetWithText(FilledButton, 'Approve');
    expect(
      tester.widget<FilledButton>(approve).onPressed,
      isNull,
      reason: 'a plan cannot be approved without saying which way',
    );

    await tester.tap(find.text('Yes, manually approve edits'));
    await tester.pump();
    await tester.tap(approve);
    await tester.pumpAndSettle();

    expect(sent, hasLength(1));
    expect(sent.first['action'], 'approve');
    expect(sent.first['plan_choice'], 'manual');
  });

  testWidgets('typed words send the plan back rather than stopping it', (
    tester,
  ) async {
    final sent = <Map<String, dynamic>>[];
    await _pumpCard(tester, _planNotification(), _serviceRecording(sent));

    await tester.enterText(find.byType(TextField), 'use a queue instead');
    await tester.pump();

    // The refusal button says what the words will do with it.
    expect(find.widgetWithText(FilledButton, 'Send back'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Send back'));
    await tester.pumpAndSettle();

    expect(sent, hasLength(1));
    expect(sent.first['action'], 'deny');
    expect(sent.first['feedback'], 'use a queue instead');
  });

  testWidgets('an ordinary tool keeps Approve and Deny', (tester) async {
    final sent = <Map<String, dynamic>>[];
    await _pumpCard(tester, _bashNotification(), _serviceRecording(sent));

    expect(find.text('Yes, and use auto mode'), findsNothing);
    expect(find.text('Edit before approving'), findsOneWidget);

    final approve = find.widgetWithText(FilledButton, 'Approve');
    expect(tester.widget<FilledButton>(approve).onPressed, isNotNull);
    await tester.tap(approve);
    await tester.pumpAndSettle();

    expect(sent, hasLength(1));
    expect(sent.first, {'action': 'approve'});
  });
}
