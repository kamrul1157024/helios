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

/// Codex's own approval dialog offers "No, and tell Codex what to do
/// differently". The phone painted a bare Deny over it. These pin the words
/// that replaced it.

HeliosNotification _permission(String source) => HeliosNotification(
      id: 'n1',
      source: source,
      sourceSession: 's1',
      cwd: '/tmp/proj',
      type: '$source.permission',
      status: 'pending',
      createdAt: DateTime.now().toUtc().toIso8601String(),
      payload: {
        'tool_name': 'shell',
        'tool_input': {'command': 'rm -rf build'},
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
  testWidgets('a Codex approval can be refused in words', (tester) async {
    final sent = <Map<String, dynamic>>[];
    await _pumpCard(tester, _permission('codex'), _serviceRecording(sent));

    expect(find.text('Tell Codex what to do differently'), findsOneWidget);

    await tester.enterText(find.byType(TextField), 'use git clean instead');
    await tester.pump();

    // The refusal button says what the words will do with it.
    expect(find.widgetWithText(FilledButton, 'Send back'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Send back'));
    await tester.pumpAndSettle();

    expect(sent, hasLength(1));
    expect(sent.first['action'], 'deny');
    expect(sent.first['feedback'], 'use git clean instead');
  });

  testWidgets('a Codex approval is still one tap to say yes', (tester) async {
    final sent = <Map<String, dynamic>>[];
    await _pumpCard(tester, _permission('codex'), _serviceRecording(sent));

    // Nothing to pick first: Codex has no mode rows, only the words.
    expect(find.text('Yes, and use auto mode'), findsNothing);

    final approve = find.widgetWithText(FilledButton, 'Approve');
    expect(tester.widget<FilledButton>(approve).onPressed, isNotNull);
    await tester.tap(approve);
    await tester.pumpAndSettle();

    expect(sent, hasLength(1));
    expect(sent.first, {'action': 'approve'});
  });

  testWidgets('an ordinary Claude tool keeps its bare Deny', (tester) async {
    await _pumpCard(tester, _permission('claude'), _serviceRecording([]));

    // Claude writes a rule for a tool it is allowed, and the feedback row
    // belongs to the plan card. Only Codex refuses in words on every tool.
    expect(find.text('Tell Codex what to do differently'), findsNothing);
    expect(find.byType(TextField), findsNothing);
    expect(find.widgetWithText(FilledButton, 'Deny'), findsOneWidget);
  });
}
