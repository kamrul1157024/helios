import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/models/session.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/providers/theme_provider.dart';
import 'package:helios/screens/commit_detail_screen.dart';
import 'package:helios/screens/file_browser_screen.dart';
import 'package:helios/screens/session_detail_screen.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/services/host_manager.dart';
import 'package:helios/widgets/code_view.dart';
import 'package:helios/widgets/git_diff_view.dart';
import 'package:helios/widgets/git_scope.dart';

import 'support/fake_files_daemon.dart';

/// Spec 65, B8: the Git screen is gone, and the Files screen's Changes view
/// does its job — the scope bar on top, a tap on a file opening it on its
/// diff, and the session's git bar landing there.

FakeFilesDaemon _daemon({String base = ''}) => FakeFilesDaemon(
  files: {
    '/repo/a.dart': 'void main() {\n  print(1);\n}\n',
    '/repo/lib/b.dart': 'int b = 1;\nint c = 2;\n',
  },
  gitChanges: {'a.dart': 'M', 'gone.dart': 'D'},
  base: base,
  commits: base.isEmpty
      ? const []
      : [
          {
            'sha': 'c0ffee1234567',
            'short': 'c0ffee1',
            'author': 'agent',
            'date': '2026-09-01T10:00:00Z',
            'subject': 'Serve raw bytes',
            'files': 1,
            'insertions': 3,
            'deletions': 1,
          },
        ],
  reviewChanges: {'lib/b.dart': 'M'},
  touched: [
    {'path': '/repo/lib/b.dart', 'last_at': '2026-09-01T10:00:00Z'},
  ],
);

/// The scope a Changes view was left on is kept per session for the life of
/// the app, so a test that picks one uses a session of its own.
Widget _browser({bool changes = true, String session = 's1'}) =>
    FileBrowserScreen(
      hostId: fakeHost,
      rootPath: '/repo',
      sessionId: session,
      startInChanges: changes,
    );

bool _diffSelected(WidgetTester tester) => tester
    .widget<SegmentedButton<bool>>(find.byType(SegmentedButton<bool>))
    .selected
    .single;

/// A host manager that answers for the fake daemon, so the session screen
/// reads its git status and draws the git bar.
class _Hosts extends HostManager {
  final DaemonAPIService service;

  _Hosts(this.service);

  @override
  DaemonAPIService? serviceFor(String hostId) => service;
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('the Changes chip puts the scope bar on top', (tester) async {
    await tester.pumpWidget(
      filesApp(_daemon(), home: _browser(changes: false)),
    );
    await tester.pumpAndSettle();
    expect(find.byType(GitScopeBar), findsNothing);

    await tester.tap(find.byType(FilterChip));
    await tester.pumpAndSettle();
    expect(find.byType(GitScopeBar), findsOneWidget);
    // No base branch: the working tree, in the two B6 sections.
    expect(find.text('Uncommitted'), findsOneWidget);
    expect(find.text('EDITED BY THIS SESSION'), findsOneWidget);
    expect(find.text('OTHER UNCOMMITTED CHANGES'), findsOneWidget);
  });

  testWidgets('a branch ahead of its base opens on the review', (tester) async {
    final daemon = _daemon(base: 'main');
    await tester.pumpWidget(filesApp(daemon, home: _browser(session: 's2')));
    await tester.pumpAndSettle();

    expect(find.text('Review vs main'), findsOneWidget);
    expect(find.text('1 FILE'), findsOneWidget);
    expect(find.text('b.dart'), findsOneWidget);
    final asked = daemon.requests.lastWhere(
      (r) => r.url.path == '/api/git/changes',
    );
    expect(asked.url.queryParameters['merge_base'], 'true');
    expect(asked.url.queryParameters['from'], 'main');

    // History stays in the scope bar: pick the commit, and its detail is a
    // button away.
    await tester.tap(find.byKey(const ValueKey('git-scope')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Serve raw bytes'));
    await tester.pumpAndSettle();
    expect(find.text('c0ffee1 Serve raw bytes'), findsOneWidget);
    await tester.tap(find.byTooltip('Commit detail'));
    await tester.pumpAndSettle();
    expect(find.byType(CommitDetailScreen), findsOneWidget);
  });

  testWidgets('a changed file opens on its diff, and toggles to source', (
    tester,
  ) async {
    await tester.pumpWidget(filesApp(_daemon(), home: _browser()));
    await tester.pumpAndSettle();

    await tester.tap(find.text('a.dart'));
    await tester.pumpAndSettle();
    expect(find.byType(FileViewerScreen), findsOneWidget);
    expect(find.byType(GitDiffView), findsOneWidget);
    expect(_diffSelected(tester), isTrue);

    await tester.tap(find.text('Source'));
    await tester.pumpAndSettle();
    expect(find.byType(GitDiffView), findsNothing);
    expect(find.byType(CodeView), findsOneWidget);
    expect(_diffSelected(tester), isFalse);

    await tester.tap(find.text('Diff'));
    await tester.pumpAndSettle();
    expect(find.byType(GitDiffView), findsOneWidget);
  });

  testWidgets('a deleted file opens on its diff alone', (tester) async {
    final daemon = _daemon();
    await tester.pumpWidget(filesApp(daemon, home: _browser()));
    await tester.pumpAndSettle();

    await tester.tap(find.text('gone.dart'));
    await tester.pumpAndSettle();
    expect(find.byType(GitDiffView), findsOneWidget);
    expect(find.byType(SegmentedButton<bool>), findsNothing);
    // There is no source to read, so nothing asks for it.
    expect(daemon.reads.where((r) => r.contains('gone.dart')), isEmpty);
  });

  group('session screen', () {
    final session = Session(
      hostId: fakeHost,
      sessionId: 's1',
      source: 'claude',
      cwd: '/repo',
      project: 'repo',
      status: 'idle',
      createdAt: '2026-01-01T00:00:00Z',
    );

    Future<void> show(WidgetTester tester, FakeFilesDaemon daemon) async {
      final service = daemon.service();
      final hosts = _Hosts(service);
      await tester.pumpWidget(
        rp.ProviderScope(
          overrides: [
            hostManagerProvider.overrideWithValue(hosts),
            serviceProvider(fakeHost).overrideWithValue(service),
          ],
          child: MultiProvider(
            providers: [
              ChangeNotifierProvider<HostManager>.value(value: hosts),
              ChangeNotifierProvider(create: (_) => ThemeProvider()),
            ],
            child: MaterialApp(home: SessionDetailScreen(session: session)),
          ),
        ),
      );
      await tester.pump(const Duration(milliseconds: 100));
      await tester.pump(const Duration(milliseconds: 100));
    }

    testWidgets('has no Git entry; its git bar opens Files on Changes, '
        'and a saved sub-route comes back there', (tester) async {
      tester.view.physicalSize = const Size(1080, 2400);
      tester.view.devicePixelRatio = 3.0;
      addTearDown(tester.view.reset);
      final daemon = _daemon();

      await show(tester, daemon);
      expect(find.byTooltip('Git'), findsNothing);
      expect(find.text('main'), findsOneWidget);

      await tester.tap(find.text('main'));
      await tester.pump(const Duration(milliseconds: 500));
      await tester.pump(const Duration(milliseconds: 500));
      expect(find.byType(FileBrowserScreen), findsOneWidget);
      expect(find.byType(GitScopeBar), findsOneWidget);

      // Leave the session with the Files screen still open, then return:
      // the screen it had on top comes back, on Changes.
      await tester.pumpWidget(const SizedBox());
      await show(tester, daemon);
      await tester.pump(const Duration(milliseconds: 500));
      await tester.pump(const Duration(milliseconds: 500));
      expect(find.byType(FileBrowserScreen), findsOneWidget);
      expect(find.byType(GitScopeBar), findsOneWidget);
    });
  });
}
