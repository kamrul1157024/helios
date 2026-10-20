import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/screens/file_browser_screen.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/services/host_manager.dart';
import 'package:helios/utils/code_highlight.dart';
import 'package:helios/widgets/git_diff_view.dart';

import 'support/fake_files_daemon.dart';

/// Phone-sized pictures of the files screens, for a PR to show what changed
/// (spec 65, Rollout: "each UI PR posts before and after screenshots").
///
/// Pictures, not a regression check: pixels move with every Flutter release,
/// and the layout tests in fast_files_test.dart are what guard behaviour. So
/// this only runs when asked —
///
///     flutter test --update-goldens test/fast_files_screenshots_test.dart
///
/// — or with HELIOS_SCREENSHOTS=1 to compare against the saved ones.
final _enabled =
    autoUpdateGoldenFiles || Platform.environment['HELIOS_SCREENSHOTS'] == '1';

const _phone = Size(412, 915);
const _ratio = 2.0;

const _goSource = '''package server

import (
	"net/http"
	"strconv"
)

// handleFileRaw serves a file's bytes with a weak ETag built from one stat, so an unchanged file costs a 304 and nothing else.
func (s *Server) handleFileRaw(w http.ResponseWriter, r *http.Request) {
	path, err := s.resolvePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_path", err)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	}
	etag := `W/"` + strconv.FormatInt(info.Size(), 10) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + `"`
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	http.ServeFile(w, r, path)
}
''';

const _readme = '''# Helios

Helios is a daemon, a TUI and a phone app for running AI coding agents.

## Fast files

The files panel must feel instant. Opening a file, expanding a folder and
seeing a change from the agent must each take less time than a person can
notice. See [the spec](docs/specs/65-fast-files.md).

> Send less, rebuild less, block nothing, show more.

```go
etag := fmt.Sprintf(`W/"%d-%d"`, info.Size(), info.ModTime().UnixNano())
```

- Raw bytes with an ETag
- Narrow invalidation
- Highlight off the UI thread
''';

FakeFilesDaemon _daemon({String base = ''}) => FakeFilesDaemon(
  files: {
    '/repo/cmd/helios/main.go': 'package main',
    '/repo/internal/server/files.go': _goSource,
    '/repo/mobile/lib/main.dart': 'void main() {}',
    '/repo/node_modules/left-pad/index.js': '',
    '/repo/.gitignore': 'node_modules/\n',
    '/repo/AGENTS.md': '# Helios',
    '/repo/README.md': _readme,
    '/repo/go.mod': 'module helios',
    '/repo/go.sum': '',
    '/repo/Makefile': 'build:',
    '/repo/logo.svg': '<svg/>',
  },
  gitChanges: {
    'internal/server/files.go': 'M',
    'README.md': 'M',
    'docs/notes.txt': '?',
    'go.sum': 'M',
  },
  ignored: {'/repo/node_modules'},
  base: base,
  commits: [
    {
      'sha': 'c0ffee1234567',
      'short': 'c0ffee1',
      'author': 'agent',
      'date': '2026-09-01T09:58:00Z',
      'subject': 'Serve file bytes with a weak ETag',
      'files': 2,
      'insertions': 30,
      'deletions': 4,
    },
  ],
  reviewChanges: {
    'internal/server/files.go': 'M',
    'internal/server/api.go': 'M',
    'docs/specs/65-fast-files.md': 'A',
  },
  touched: [
    {
      'path': '/repo/internal/server/files.go',
      'last_at': '2026-09-01T09:58:00Z',
      'tool': 'Edit',
      'count': 3,
      'line': 12,
    },
    {
      'path': '/repo/README.md',
      'last_at': '2026-09-01T09:41:00Z',
      'tool': 'Write',
      'count': 1,
      'line': 1,
    },
  ],
);

Future<void> _loadFonts() async {
  final flutterRoot =
      Platform.environment['FLUTTER_ROOT'] ??
      File(
        Platform.resolvedExecutable,
      ).parent.parent.parent.parent.parent.parent.path;
  final material = '$flutterRoot/bin/cache/artifacts/material_fonts';
  Future<void> load(String family, List<String> paths) async {
    final loader = FontLoader(family);
    for (final path in paths) {
      final bytes = File(path).readAsBytesSync();
      loader.addFont(Future.value(ByteData.sublistView(bytes)));
    }
    await loader.load();
  }

  await load('Roboto', [
    '$material/Roboto-Regular.ttf',
    '$material/Roboto-Medium.ttf',
    '$material/Roboto-Bold.ttf',
  ]);
  await load('MaterialIcons', ['$material/MaterialIcons-Regular.otf']);
  // The viewer asks for "monospace", which is the platform's face on a phone.
  // The app's own mono face stands in for it here.
  await load('monospace', [
    'assets/fonts/JetBrainsMono-Regular.ttf',
    'assets/fonts/JetBrainsMono-Bold.ttf',
  ]);
}

Future<void> _show(
  WidgetTester tester,
  Widget screen, {
  Brightness brightness = Brightness.light,
  String base = '',
}) async {
  tester.view.physicalSize = _phone * _ratio;
  tester.view.devicePixelRatio = _ratio;
  addTearDown(tester.view.reset);
  final daemon = _daemon(base: base);
  await tester.pumpWidget(
    rp.ProviderScope(
      overrides: [
        hostManagerProvider.overrideWithValue(HostManager()),
        serviceProvider(fakeHost).overrideWithValue(daemon.service()),
      ],
      child: MaterialApp(
        debugShowCheckedModeBanner: false,
        theme: ThemeData(
          colorScheme: ColorScheme.fromSeed(
            seedColor: Colors.indigo,
            brightness: brightness,
          ),
        ),
        home: screen,
      ),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> _snap(WidgetTester tester, String name) => expectLater(
  find.byType(MaterialApp),
  matchesGoldenFile('goldens/fast_files/$name.png'),
);

void main() {
  setUpAll(_loadFonts);
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    highlightCache.clear();
    highlightRunner = (request) async => highlightLines(request);
  });

  testWidgets('browser', skip: !_enabled, (tester) async {
    await _show(
      tester,
      const FileBrowserScreen(
        hostId: fakeHost,
        rootPath: '/repo',
        sessionId: 's1',
      ),
    );
    await _snap(tester, 'browser');
  });

  testWidgets('changes', skip: !_enabled, (tester) async {
    await _show(
      tester,
      const FileBrowserScreen(
        hostId: fakeHost,
        rootPath: '/repo',
        sessionId: 's1',
      ),
    );
    await tester.tap(find.byType(FilterChip));
    await tester.pumpAndSettle();
    await _snap(tester, 'changes');
  });

  testWidgets('changes, review scope', skip: !_enabled, (tester) async {
    await _show(
      tester,
      const FileBrowserScreen(
        hostId: fakeHost,
        rootPath: '/repo',
        sessionId: 's2',
        startInChanges: true,
      ),
      base: 'main',
    );
    await _snap(tester, 'changes_review_scope');
  });

  testWidgets('code, diff', skip: !_enabled, (tester) async {
    await _show(
      tester,
      const FileViewerScreen(
        hostId: fakeHost,
        path: '/repo/internal/server/files.go',
        rootPath: '/repo',
        sessionId: 's1',
        diff: DiffTarget(
          root: '/repo',
          change: GitChange(path: 'internal/server/files.go', status: 'M'),
        ),
      ),
      brightness: Brightness.dark,
    );
    await _snap(tester, 'code_diff');
  });

  testWidgets('code, wrap off', skip: !_enabled, (tester) async {
    await _show(
      tester,
      const FileViewerScreen(
        hostId: fakeHost,
        path: '/repo/internal/server/files.go',
        rootPath: '/repo',
        sessionId: 's1',
        line: 12,
      ),
      brightness: Brightness.dark,
    );
    await tester.tap(find.text('16'));
    await tester.pumpAndSettle();
    await _snap(tester, 'code_wrap_off');
  });

  testWidgets('code, wrap on', skip: !_enabled, (tester) async {
    SharedPreferences.setMockInitialValues({'file_viewer_wrap': true});
    await _show(
      tester,
      const FileViewerScreen(
        hostId: fakeHost,
        path: '/repo/internal/server/files.go',
        rootPath: '/repo',
        sessionId: 's1',
      ),
      brightness: Brightness.dark,
    );
    await _snap(tester, 'code_wrap_on');
  });

  testWidgets('markdown', skip: !_enabled, (tester) async {
    await _show(
      tester,
      const FileViewerScreen(
        hostId: fakeHost,
        path: '/repo/README.md',
        rootPath: '/repo',
        sessionId: 's1',
      ),
    );
    await _snap(tester, 'markdown');
  });
}
