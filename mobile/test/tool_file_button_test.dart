import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/message.dart';
import 'package:helios/widgets/message_card.dart';

/// A tool row said what it wrote and left the reader to go and find it. These
/// pin the button that opens the file instead.

Message _toolUse({String? filePath, String tool = 'Edit'}) => Message(
      role: 'tool_use',
      timestamp: '2026-09-04T10:51:49Z',
      tool: tool,
      summary: 'specs/46-codex-provider.md',
      metadata: {
        if (filePath != null) 'file_path': filePath,
        'new_string': 'a line',
      },
    );

class _Pushes extends NavigatorObserver {
  final routes = <String?>[];

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previous) {
    routes.add(route.settings.name);
    super.didPush(route, previous);
  }
}

Future<_Pushes> _pumpRow(
  WidgetTester tester,
  Message message, {
  String hostId = 'h1',
}) async {
  final pushes = _Pushes();
  await tester.pumpWidget(
    ProviderScope(
      child: MaterialApp(
        navigatorObservers: [pushes],
        home: Scaffold(
          body: MessageCard(
            message: message,
            hostId: hostId,
            sessionCwd: '/home/dev/helios',
          ),
        ),
      ),
    ),
  );
  return pushes;
}

void main() {
  testWidgets('a tool that touched a file offers a way into it', (
    tester,
  ) async {
    final pushes = await _pumpRow(tester, _toolUse(filePath: 'docs/spec.md'));

    final open = find.byIcon(Icons.open_in_new);
    expect(open, findsOneWidget);

    await tester.tap(open);
    await tester.pump();

    expect(pushes.routes, contains('/file-viewer'));

    // The viewer itself is not under test, and it reads its file through a
    // provider it cannot reach here. What matters is that the button routed
    // there; whatever the screen says about a daemon it has no way to call is
    // its own business.
    while (tester.takeException() != null) {}
  });

  testWidgets('a tool that named no file offers nothing', (tester) async {
    await _pumpRow(tester, _toolUse(tool: 'Bash', filePath: null));

    expect(find.byIcon(Icons.open_in_new), findsNothing);
  });

  testWidgets('without a host there is nowhere to open the file', (
    tester,
  ) async {
    // The transcript renders without a host id in previews and tests; a button
    // that cannot reach a daemon would only fail when tapped.
    await _pumpRow(tester, _toolUse(filePath: 'docs/spec.md'), hostId: '');

    expect(find.byIcon(Icons.open_in_new), findsNothing);
  });
}
