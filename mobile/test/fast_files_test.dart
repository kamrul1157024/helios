import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/providers/cache_effects.dart';
import 'package:helios/providers/cache_invalidator.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/screens/file_browser_screen.dart';
import 'package:helios/screens/routes.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/utils/code_highlight.dart';

import 'support/fake_files_daemon.dart';

/// Spec 65 on the phone: the raw read and its ETag, narrow invalidation, the
/// highlight that runs once, the errors that say what failed, and Ask AI that
/// lands back in the session.

final _defaultRunner = highlightRunner;

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    highlightCache.clear();
  });
  tearDown(() => highlightRunner = _defaultRunner);

  group('raw read', () {
    test('sends the ETag back and keeps the same object on a 304', () async {
      final daemon = FakeFilesDaemon(files: {'/repo/a.txt': 'hello'});
      final service = daemon.service();

      final first = await service.readFile('/repo/a.txt');
      expect(first.content, 'hello');
      expect(first.etag, 'W/"5-1"');
      expect(first.encoding, 'utf8');
      expect(daemon.requests.single.headers['If-None-Match'], isNull);

      final second = await service.readFile('/repo/a.txt', previous: first);
      expect(daemon.requests.last.headers['If-None-Match'], 'W/"5-1"');
      // Not merely equal: the same object, so a highlight keyed on it and an
      // image decoded from it are both still good.
      expect(identical(second, first), isTrue);
    });

    test('a picture arrives as bytes, and a 304 keeps the same list', () async {
      final png = Uint8List.fromList([0x89, 0x50, 0x4e, 0x47, 0, 1, 2]);
      final daemon = FakeFilesDaemon(binaries: {'/repo/x.png': png});
      final service = daemon.service();

      final first = await service.readFile('/repo/x.png');
      expect(first.isImage, isTrue);
      expect(first.content, isNull);
      expect(first.bytes, png);

      final second = await service.readFile('/repo/x.png', previous: first);
      expect(identical(second.bytes, first.bytes), isTrue);
    });

    // A daemon from before spec 65 has no raw route. Its 404 carries no code,
    // which is what tells it apart from a file that is not there.
    test(
      'an old daemon falls back to the JSON read, and stays there',
      () async {
        final daemon = FakeFilesDaemon(
          files: {'/repo/a.txt': 'hello'},
          oldDaemon: true,
        );
        final service = daemon.service();

        expect((await service.readFile('/repo/a.txt')).content, 'hello');
        expect((await service.readFile('/repo/a.txt')).content, 'hello');
        expect(daemon.reads, [
          '/api/file/raw /repo/a.txt',
          '/api/file /repo/a.txt',
          '/api/file /repo/a.txt',
        ]);
      },
    );

    test('a missing file is a not-found error, not a fallback', () async {
      final daemon = FakeFilesDaemon(files: {'/repo/a.txt': 'hello'});
      await expectLater(
        daemon.service().readFile('/repo/gone.txt'),
        throwsA(
          isA<FileError>().having(
            (e) => e.kind,
            'kind',
            FileErrorKind.notFound,
          ),
        ),
      );
      expect(daemon.reads, ['/api/file/raw /repo/gone.txt']);
    });

    test('too large is a result the viewer has a view for', () async {
      final daemon = FakeFilesDaemon()
        ..failWith = (_) => http.Response(
          jsonEncode({
            'error': 'too large',
            'code': 'too_large',
            'size': 14680064,
            'limit': 10485760,
          }),
          413,
        );
      final result = await daemon.service().readFile('/repo/huge.log');
      expect(result.isTooLarge, isTrue);
      expect(result.size, 14680064);
      expect(
        FileError(
          FileErrorKind.tooLarge,
          '',
          size: 14680064,
          limit: 10485760,
        ).message,
        'Too large (14 MB, the limit is 10 MB)',
      );
    });
  });

  group('narrow invalidation', () {
    // One write refetches the folder that holds it and the file a viewer is
    // showing — not the sibling folder, and not a file nobody is looking at.
    test('a write refetches only its folder and its open file', () async {
      final daemon = FakeFilesDaemon(
        files: {
          '/repo/lib/a.dart': 'a',
          '/repo/lib/b.dart': 'b',
          '/repo/README.md': 'r',
        },
      );
      final container = rp.ProviderContainer(
        overrides: [
          serviceProvider(fakeHost).overrideWithValue(daemon.service()),
        ],
      );
      addTearDown(container.dispose);
      final refProvider = rp.Provider<rp.Ref>((ref) => ref);

      final watched = [
        listFilesProvider((fakeHost, '/repo')),
        listFilesProvider((fakeHost, '/repo/lib')),
      ];
      final files = [
        readFileProvider((fakeHost, '/repo/lib/a.dart')),
        readFileProvider((fakeHost, '/repo/README.md')),
      ];
      for (final p in watched) {
        container.listen(p, (_, _) {});
        await container.read(p.future);
      }
      for (final p in files) {
        container.listen(p, (_, _) {});
        await container.read(p.future);
      }
      daemon.requests.clear();

      applyCacheEffects(
        container.read(refProvider),
        effectsFor(fakeHost, 'file_changed', {
          'paths': [
            {'path': '/repo/lib/a.dart', 'kind': 'file'},
          ],
        }),
      );
      await container.read(watched[1].future);
      await container.read(files[0].future);

      expect(daemon.reads, [
        '/api/files /repo/lib',
        '/api/file/raw /repo/lib/a.dart',
      ]);
      // And the file went out with its ETag, so an unchanged one is a 304.
      final raw = daemon.requests.firstWhere(
        (r) => r.url.path == '/api/file/raw',
      );
      expect(raw.headers['If-None-Match'], daemon.etagOf('/repo/lib/a.dart'));
    });

    test('a reconnect still takes out every file', () {
      expect(
        effectsFor(fakeHost, 'stream_reconnected', const {}),
        contains(const InvalidateTarget(CacheTarget.files, fakeHost)),
      );
    });
  });

  group('viewer', () {
    testWidgets(
      'a tap on a line in a 5000-line file highlights nothing again',
      (tester) async {
        var runs = 0;
        highlightRunner = (request) async {
          runs++;
          return const HighlightedCode([], []);
        };
        final big = List.generate(5000, (i) => 'final x$i = $i;').join('\n');
        final daemon = FakeFilesDaemon(files: {'/repo/big.dart': big});

        await tester.pumpWidget(
          filesApp(
            daemon,
            home: const FileViewerScreen(
              hostId: fakeHost,
              path: '/repo/big.dart',
              rootPath: '/repo',
              sessionId: 's1',
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(runs, 1);

        await tester.tap(find.text('3'));
        await tester.pump();
        expect(find.text('L3 · 1 line'), findsOneWidget);
        await tester.tap(find.text('7'));
        await tester.pump();
        expect(find.text('L3-7 · 5 lines'), findsOneWidget);
        expect(runs, 1);
      },
    );

    final failures = <String, void Function(FakeFilesDaemon)>{
      'Not found': (d) => d.failWith = (_) => http.Response(
        jsonEncode({'error': 'no such file', 'code': 'not_found'}),
        404,
      ),
      'Permission denied': (d) => d.failWith = (_) => http.Response(
        jsonEncode({'error': 'denied', 'code': 'permission_denied'}),
        403,
      ),
      'Host unreachable': (d) => d.unreachable = true,
    };

    for (final entry in failures.entries) {
      testWidgets('the viewer says "${entry.key}" and offers Retry', (
        tester,
      ) async {
        final daemon = FakeFilesDaemon(files: {'/repo/a.txt': 'hello'});
        entry.value(daemon);
        await tester.pumpWidget(
          filesApp(
            daemon,
            home: const FileViewerScreen(hostId: fakeHost, path: '/repo/a.txt'),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text(entry.key), findsOneWidget);
        expect(find.text('/repo/a.txt'), findsOneWidget);

        // Retry reads again, and shows the file once the host answers.
        daemon
          ..failWith = null
          ..unreachable = false;
        await tester.tap(find.text('Retry'));
        await tester.pumpAndSettle();
        expect(find.text(entry.key), findsNothing);
      });

      testWidgets('the folder list says "${entry.key}" and offers Retry', (
        tester,
      ) async {
        final daemon = FakeFilesDaemon(files: {'/repo/a.txt': 'hello'});
        entry.value(daemon);
        await tester.pumpWidget(
          filesApp(
            daemon,
            home: const FileBrowserScreen(hostId: fakeHost, rootPath: '/repo'),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.text(entry.key), findsOneWidget);
        expect(find.text('Retry'), findsOneWidget);

        daemon
          ..failWith = null
          ..unreachable = false;
        await tester.tap(find.text('Retry'));
        await tester.pumpAndSettle();
        expect(find.text('a.txt'), findsOneWidget);
      });
    }

    testWidgets('wrap is off by default, scrolls sideways, and is saved', (
      tester,
    ) async {
      final long = 'x' * 400;
      final daemon = FakeFilesDaemon(files: {'/repo/long.txt': 'short\n$long'});
      await tester.pumpWidget(
        filesApp(
          daemon,
          home: const FileViewerScreen(
            hostId: fakeHost,
            path: '/repo/long.txt',
          ),
        ),
      );
      await tester.pumpAndSettle();

      RichText longRow() => tester.widget<RichText>(
        find.byWidgetPredicate(
          (w) => w is RichText && w.text.toPlainText() == long,
        ),
      );
      Size longRowSize() => tester.getSize(
        find.byWidgetPredicate(
          (w) => w is RichText && w.text.toPlainText() == long,
        ),
      );

      expect(find.byKey(const ValueKey('code-horizontal')), findsOneWidget);
      expect(longRow().softWrap, isFalse);
      final unwrapped = longRowSize();

      await tester.tap(find.byTooltip('More'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Wrap lines'));
      await tester.pumpAndSettle();

      expect(find.byKey(const ValueKey('code-horizontal')), findsNothing);
      final wrapped = longRowSize();
      expect(wrapped.height, greaterThan(unwrapped.height * 3));
      expect(wrapped.width, lessThan(800));
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getBool('file_viewer_wrap'), isTrue);
    });

    testWidgets('markdown has no "Tap lines to select" hint', (tester) async {
      final daemon = FakeFilesDaemon(
        files: {'/repo/README.md': '# Title\n\nBody'},
      );
      await tester.pumpWidget(
        filesApp(
          daemon,
          home: const FileViewerScreen(
            hostId: fakeHost,
            path: '/repo/README.md',
            sessionId: 's1',
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('Title'), findsOneWidget);
      expect(find.text('Ask AI'), findsOneWidget);
      expect(find.text('Tap lines to select'), findsNothing);
    });
  });

  group('Ask AI', () {
    Future<GlobalKey<NavigatorState>> openViewer(
      WidgetTester tester,
      FakeFilesDaemon daemon,
    ) async {
      final nav = GlobalKey<NavigatorState>();
      await tester.pumpWidget(
        filesApp(daemon, navigatorKey: nav, home: const Text('home')),
      );
      nav.currentState!.push(
        MaterialPageRoute(
          settings: const RouteSettings(name: sessionRoute),
          builder: (_) => const Scaffold(body: Text('session screen')),
        ),
      );
      await tester.pumpAndSettle();
      nav.currentState!.push(
        MaterialPageRoute(
          settings: const RouteSettings(name: fileBrowserRoute),
          builder: (_) => const FileBrowserScreen(
            hostId: fakeHost,
            rootPath: '/repo',
            sessionId: 's1',
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('a.dart'));
      await tester.pumpAndSettle();
      expect(find.byType(FileViewerScreen), findsOneWidget);
      return nav;
    }

    Future<void> ask(WidgetTester tester) async {
      await tester.tap(find.text('Ask AI'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'why?');
      await tester.tap(find.byTooltip('Send'));
      await tester.pumpAndSettle();
    }

    // The bug: popUntil(name != '/file-browser') passed at once with the
    // viewer on top, so a sent question left the reader where they were.
    testWidgets('a sent question lands on the session', (tester) async {
      final daemon = FakeFilesDaemon(files: {'/repo/a.dart': 'void main() {}'});
      await openViewer(tester, daemon);
      await ask(tester);

      expect(find.text('session screen'), findsOneWidget);
      expect(find.byType(FileViewerScreen), findsNothing);
      expect(find.byType(FileBrowserScreen), findsNothing);
      final sent = daemon.requests.lastWhere(
        (r) => r.url.path == '/api/sessions/s1/send',
      );
      expect(jsonDecode(sent.body)['message'], contains('why?'));
    });

    testWidgets('a refused question stays put and says why', (tester) async {
      final daemon = FakeFilesDaemon(files: {'/repo/a.dart': 'void main() {}'})
        ..sendError = 'session_busy';
      await openViewer(tester, daemon);
      await ask(tester);

      expect(find.byType(FileViewerScreen), findsOneWidget);
      expect(find.text('The session is busy'), findsOneWidget);
      expect(find.text('Edit'), findsOneWidget);
    });
  });

  group('browser layout', () {
    testWidgets('the breadcrumb starts at the root and shows its end', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(360, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      const deep = '/home/me/repo/internal/server/handlers/files/raw/endpoints';
      final daemon = FakeFilesDaemon(files: {'$deep/a.go': 'package raw'});
      await tester.pumpWidget(
        filesApp(
          daemon,
          home: const FileBrowserScreen(
            hostId: fakeHost,
            rootPath: '/home/me/repo',
            startPath: deep,
          ),
        ),
      );
      await tester.pumpAndSettle();

      final root = find.byKey(const ValueKey('crumb:/home/me/repo'));
      final last = find.byKey(const ValueKey('crumb:$deep'));
      // Nothing above the checkout: no `/`, no `home`.
      expect(find.byKey(const ValueKey('crumb:/')), findsNothing);
      expect(find.byKey(const ValueKey('crumb:/home')), findsNothing);
      // Scrolled so the folder you are in is the one on screen.
      expect(last, findsOneWidget);
      expect(tester.getRect(last).right, lessThanOrEqualTo(360));
      expect(tester.getSize(last).height, greaterThanOrEqualTo(48));
      expect(tester.getSize(last).width, greaterThanOrEqualTo(48));
      // The root has scrolled off to the left to make room.
      expect(
        root.evaluate().isEmpty || tester.getRect(root).right <= 0,
        isTrue,
      );
    });

    testWidgets('show in folder is a step that Back undoes', (tester) async {
      final daemon = FakeFilesDaemon(
        files: {'/repo/lib/a.dart': 'void main() {}', '/repo/README.md': 'r'},
      );
      final nav = GlobalKey<NavigatorState>();
      await tester.pumpWidget(
        filesApp(daemon, navigatorKey: nav, home: const Text('home')),
      );
      nav.currentState!.push(
        MaterialPageRoute(
          settings: const RouteSettings(name: fileBrowserRoute),
          builder: (_) => const FileBrowserScreen(
            hostId: fakeHost,
            rootPath: '/repo',
            startPath: '/repo/lib',
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('a.dart'));
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('Show in folder'));
      await tester.pumpAndSettle();
      expect(find.byType(FileViewerScreen), findsNothing);

      await nav.currentState!.maybePop();
      await tester.pumpAndSettle();
      expect(find.byType(FileViewerScreen), findsOneWidget);
    });
  });
}
