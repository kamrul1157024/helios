import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/services/desktop_bridge.dart';

import 'support/fake_desktop.dart';

/// Phone-sized pictures of the remote desktop screens, for the PR to show
/// (spec 66, D8 and D9).
///
/// Pictures, not a regression check, so this only runs when asked —
///
///     flutter test --update-goldens test/remote_desktop_screenshots_test.dart
///
/// — or with HELIOS_SCREENSHOTS=1 to compare against the saved ones. The
/// desktop itself is a stand-in drawn here: a widget test has no WebView.
final _enabled =
    autoUpdateGoldenFiles || Platform.environment['HELIOS_SCREENSHOTS'] == '1';

const _portrait = Size(412, 915);
const _landscape = Size(915, 412);
const _ratio = 2.0;

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
  await load('monospace', [
    'assets/fonts/JetBrainsMono-Regular.ttf',
    'assets/fonts/JetBrainsMono-Bold.ttf',
  ]);
}

/// An XFCE-ish screen at fit-to-width, letterboxed the way the page draws it.
class _StandIn extends StatelessWidget {
  const _StandIn();

  @override
  Widget build(BuildContext context) {
    return ColoredBox(
      color: const Color(0xFF1E1E1E),
      child: Align(
        alignment: Alignment.topCenter,
        child: LayoutBuilder(
          builder: (context, box) {
            final tall = box.maxWidth / box.maxHeight < 16 / 10;
            return Padding(
              padding: EdgeInsets.only(
                top: tall ? (box.maxHeight - box.maxWidth * 10 / 16) / 2 : 0,
              ),
              child: AspectRatio(
                aspectRatio: 16 / 10,
                child: Container(
                  decoration: const BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.topLeft,
                      end: Alignment.bottomRight,
                      colors: [Color(0xFF2B4C7E), Color(0xFF1B2F4F)],
                    ),
                  ),
                  child: Stack(
                    children: [
                      Container(height: 10, color: const Color(0xFF3C3C3C)),
                      Positioned(
                        left: box.maxWidth * 0.1,
                        top: 24,
                        width: box.maxWidth * 0.55,
                        height: box.maxWidth * 0.3,
                        child: Container(
                          decoration: BoxDecoration(
                            color: const Color(0xFF101010),
                            border: Border.all(color: const Color(0xFF5A5A5A)),
                          ),
                          padding: const EdgeInsets.all(4),
                          child: const Text(
                            '\$ npm run dev\n  ready on :5173',
                            style: TextStyle(
                              color: Color(0xFF8AE234),
                              fontFamily: 'monospace',
                              fontSize: 7,
                            ),
                          ),
                        ),
                      ),
                      Positioned(
                        right: box.maxWidth * 0.08,
                        top: box.maxWidth * 0.18,
                        child: const Icon(
                          Icons.circle,
                          size: 10,
                          color: Color(0x994285F4),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}

Future<FakeBridge> _show(
  WidgetTester tester,
  Map<String, dynamic>? status, {
  Size size = _portrait,
  Brightness brightness = Brightness.light,
  FakeDesktopDaemon? daemon,
}) async {
  tester.view.physicalSize = size * _ratio;
  tester.view.devicePixelRatio = _ratio;
  addTearDown(tester.view.reset);
  final bridge = FakeBridge(picture: const _StandIn());
  await tester.pumpWidget(
    desktopApp(
      daemon ?? FakeDesktopDaemon(status),
      bridge,
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(
          seedColor: Colors.indigo,
          brightness: brightness,
        ),
      ),
    ),
  );
  await _settle(tester);
  return bridge;
}

Future<void> _settle(WidgetTester tester) async {
  for (var i = 0; i < 10; i++) {
    await tester.pump(const Duration(milliseconds: 20));
  }
}

Future<void> _snap(WidgetTester tester, String name) => expectLater(
  find.byType(MaterialApp),
  matchesGoldenFile('goldens/remote_desktop/$name.png'),
);

void main() {
  setUpAll(_loadFonts);
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('install', skip: !_enabled, (tester) async {
    await _show(
      tester,
      desktopBody('missing', missing: ['xrdp', 'xfce', 'guacd']),
    );
    await _snap(tester, 'install');
  });

  testWidgets('install, dark', skip: !_enabled, (tester) async {
    await _show(
      tester,
      desktopBody('missing', missing: ['xrdp', 'xfce', 'guacd']),
      brightness: Brightness.dark,
    );
    await _snap(tester, 'install_dark');
  });

  testWidgets('needs root', skip: !_enabled, (tester) async {
    await _show(tester, desktopBody('missing', canInstall: false));
    await _snap(tester, 'needs_root');
  });

  testWidgets('installing', skip: !_enabled, (tester) async {
    await _show(
      tester,
      desktopBody(
        'installing',
        steps: ['done', 'skipped', 'running'],
        runningLine: 'Writing /usr/lib/helios/desktop-auth',
      ),
    );
    await _snap(tester, 'installing');
  });

  testWidgets('failed', skip: !_enabled, (tester) async {
    await _show(
      tester,
      desktopBody(
        'failed',
        error: 'Another install is running. Try again when it ends.',
        steps: ['failed'],
        runningLine: 'E: Could not get lock /var/lib/dpkg/lock-frontend',
      ),
    );
    await _snap(tester, 'failed');
  });

  testWidgets('unsupported', skip: !_enabled, (tester) async {
    await _show(tester, desktopBody('unsupported'));
    await _snap(tester, 'unsupported');
  });

  testWidgets('connecting', skip: !_enabled, (tester) async {
    await _show(tester, desktopBody('ready'));
    await _snap(tester, 'connecting');
  });

  testWidgets('connected', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'));
    bridge.emit('connected');
    await _settle(tester);
    await _snap(tester, 'connected');
  });

  testWidgets('connected, ctrl armed', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'));
    bridge.emit('connected');
    await _settle(tester);
    await tester.tap(find.text('ctrl'));
    await _settle(tester);
    await _snap(tester, 'connected_ctrl');
  });

  testWidgets('too many viewers', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'));
    bridge
      ..emit('connected')
      ..emit('closed', code: closeTooManyViewers);
    await _settle(tester);
    await _snap(tester, 'too_many_viewers');
  });

  testWidgets('landscape', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'), size: _landscape);
    bridge.emit('connected');
    await _settle(tester);
    await _snap(tester, 'landscape');
  });

  testWidgets('full screen', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'), size: _landscape);
    bridge.emit('connected');
    await _settle(tester);
    await tester.tap(find.byTooltip('Full screen'));
    await _settle(tester);
    await _snap(tester, 'landscape_full_screen');
  });

  testWidgets('display sheet', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'));
    bridge.emit('connected');
    await _settle(tester);
    await tester.tap(find.byTooltip('Screen size and DPI'));
    await _settle(tester);
    await _snap(tester, 'display_sheet');
  });

  testWidgets('display sheet, custom', skip: !_enabled, (tester) async {
    final bridge = await _show(tester, desktopBody('ready'));
    bridge.emit('connected');
    await _settle(tester);
    await tester.tap(find.byTooltip('Screen size and DPI'));
    await _settle(tester);
    await tester.tap(find.text('Custom').first);
    await tester.tap(find.text('Custom').last);
    await _settle(tester);
    await _snap(tester, 'display_sheet_custom');
  });
}
