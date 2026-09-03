import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:helios/models/notification.dart';
import 'package:helios/providers/cards.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';

/// A plan is not a yes-or-no question, and the phone asked it as one: Approve
/// or Deny, over a body of raw JSON. These pin the rows that replaced them.

const _plan = '# Plan: give plan approval its own rows\n\n'
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
        'tool_input': {
          'plan': _plan,
          'planFilePath': '~/.claude/plans/rows.md',
        },
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

Future<void> _pumpCard(
  WidgetTester tester,
  HeliosNotification n,
  DaemonAPIService sse,
) async {
  await tester.pumpWidget(
    MaterialApp(
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

    // The plan itself, not the JSON it arrived in.
    expect(find.textContaining('## Context'), findsOneWidget);

    // There is no command to edit and the CLI sends no rule suggestions for
    // this tool, so the row that offers both would lead nowhere.
    expect(find.text('Edit before approving'), findsNothing);
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
