import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/message.dart';
import 'package:helios/screens/file_browser_screen.dart';
import 'package:helios/widgets/message_card.dart';

/// A tool row said what it wrote and left the reader to go and find it. These
/// pin the button that opens the file instead.

Message _toolUse({String? filePath, String tool = 'Edit'}) => Message(
      role: 'tool_use',
      timestamp: '2026-09-04T10:51:49Z',
      tool: tool,
      summary: 'specs/46-codex-provider.md',
      metadata: {
        'file_path': ?filePath,
        'new_string': 'a line',
      },
    );

class _Pushes extends NavigatorObserver {
  final routes = <Route<dynamic>>[];

  List<String?> get names => [for (final r in routes) r.settings.name];

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previous) {
    routes.add(route);
    super.didPush(route, previous);
  }
}

/// The viewer the last push would show, built but never mounted.
///
/// Mounting it would need the daemon providers it reads its file through, and
/// what is under test is the file it was sent to open, not the reading of it.
FileViewerScreen _viewerFrom(WidgetTester tester, _Pushes pushes) {
  final route = pushes.routes.last as MaterialPageRoute;
  return route.builder(tester.element(find.byType(MessageCard)))
      as FileViewerScreen;
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

    // No pump after the tap: the push lands synchronously, and the viewer is
    // read from the route rather than mounted.
    await tester.tap(open);

    expect(pushes.names, contains('/file-viewer'));
    final viewer = _viewerFrom(tester, pushes);
    expect(viewer.path, '/home/dev/helios/docs/spec.md');
    expect(viewer.hostId, 'h1');
    expect(viewer.rootPath, '/home/dev/helios');
  });

  testWidgets('tapping a chip opens the file it names', (tester) async {
    final pushes = await _pumpRow(
      tester,
      Message(
        role: 'assistant',
        timestamp: '2026-09-04T10:51:49Z',
        content: 'Updated: `scratch/opus5-refusal-customer-report.md`',
      ),
    );

    await tester.tap(find.text('opus5-refusal-customer-report.md'));

    expect(pushes.names, contains('/file-viewer'));
    expect(
      _viewerFrom(tester, pushes).path,
      '/home/dev/helios/scratch/opus5-refusal-customer-report.md',
    );
  });

  testWidgets('a chip that repeats the project directory does not double it', (
    tester,
  ) async {
    // The agent writes helios/mobile/lib/main.dart while sitting in .../helios.
    final pushes = await _pumpRow(
      tester,
      Message(
        role: 'assistant',
        timestamp: '2026-09-04T10:51:49Z',
        content: 'Wrote helios/mobile/lib/main.dart just now.',
      ),
    );

    await tester.tap(find.text('main.dart'));

    expect(
      _viewerFrom(tester, pushes).path,
      '/home/dev/helios/mobile/lib/main.dart',
    );
  });

  testWidgets('an absolute chip is opened as written', (tester) async {
    final pushes = await _pumpRow(
      tester,
      Message(
        role: 'assistant',
        timestamp: '2026-09-04T10:51:49Z',
        content: 'See /etc/hosts/entries.conf for the rest.',
      ),
    );

    await tester.tap(find.text('entries.conf'));

    expect(_viewerFrom(tester, pushes).path, '/etc/hosts/entries.conf');
  });

  testWidgets('a path in prose becomes a chip on two segments', (
    tester,
  ) async {
    // "Updated: `scratch/report.md`" got nothing while the rule wanted three
    // segments, which is how most agents name a file they just wrote.
    await _pumpRow(
      tester,
      Message(
        role: 'assistant',
        timestamp: '2026-09-04T10:51:49Z',
        content: 'Updated: `scratch/opus5-refusal-customer-report.md`',
      ),
    );

    expect(find.text('opus5-refusal-customer-report.md'), findsOneWidget);
  });

  testWidgets('two words with a slash between them stay prose', (
    tester,
  ) async {
    await _pumpRow(
      tester,
      Message(
        role: 'assistant',
        timestamp: '2026-09-04T10:51:49Z',
        content: 'The and/or case, N/A, and src/main are not files.',
      ),
    );

    expect(find.byIcon(Icons.insert_drive_file), findsNothing);
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
