import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/screens/desktop_screen.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/services/desktop_bridge.dart';
import 'package:helios/services/host_manager.dart';

const desktopHost = 'h1';

const installSteps = [
  ('packages', 'Install xrdp and XFCE'),
  ('xrdp-config', 'Configure xrdp'),
  ('pam', 'Let Helios sign in to the desktop'),
  ('session', 'Choose the XFCE session'),
  ('guacd', 'Get the desktop service'),
  ('start', 'Start the desktop'),
];

/// A `GET /api/desktop` body. [steps] gives each step's state in order; the
/// rest are pending.
Map<String, dynamic> desktopBody(
  String state, {
  bool enabled = true,
  List<String> missing = const [],
  bool canInstall = true,
  int dpi = 120,
  String error = '',
  List<String> steps = const [],
  String runningLine = '',
}) => {
  'state': state,
  'enabled': enabled,
  'missing': missing,
  'can_install': canInstall,
  'install_command': 'helios setup desktop',
  'display': state == 'ready' ? ':10' : '',
  'dpi': dpi,
  'viewers': 0,
  'error': error,
  'steps': [
    for (var i = 0; i < installSteps.length; i++)
      {
        'id': installSteps[i].$1,
        'label': installSteps[i].$2,
        'state': i < steps.length ? steps[i] : 'pending',
        'line':
            i < steps.length && (steps[i] == 'running' || steps[i] == 'failed')
            ? runningLine
            : '',
      },
  ],
};

/// The desktop routes of a daemon, in memory. Every request is recorded.
class FakeDesktopDaemon {
  /// What `GET /api/desktop` answers; null for a daemon that predates it.
  Map<String, dynamic>? status;

  int tickets = 0;
  int installStatus = 202;
  String installCode = '';

  /// The daemon's own sentence on a refusal: what the app must never show.
  String refusalText = 'refused';

  /// Refuse install and tickets as turned off, whatever the last read said.
  bool refuseDisabled = false;
  int dpiStatus = 200;
  String dpiCode = '';
  final List<http.Request> requests = [];

  FakeDesktopDaemon(this.status);

  Future<http.Response> handle(http.Request request) async {
    requests.add(request);
    switch ('${request.method} ${request.url.path}') {
      case 'GET /api/desktop':
        final body = status;
        return body == null
            ? http.Response('404 page not found', 404)
            : http.Response(jsonEncode(body), 200);
      case 'POST /api/desktop/enable':
        status?['enabled'] = true;
        refuseDisabled = false;
        return http.Response(jsonEncode(status), 200);
      case 'POST /api/desktop/install' || 'POST /api/desktop/ticket'
          when refuseDisabled || status?['enabled'] == false:
        return http.Response(
          jsonEncode({
            'error': 'remote desktop is turned off (setting desktop.enabled)',
            'code': 'disabled',
          }),
          409,
        );
      case 'POST /api/desktop/install':
        return http.Response(
          jsonEncode(
            installStatus == 202
                ? {'started': true}
                : {
                    'error': refusalText,
                    'code': installCode,
                    'install_command': 'helios setup desktop',
                  },
          ),
          installStatus,
        );
      case 'POST /api/desktop/ticket':
        tickets++;
        return http.Response(
          jsonEncode({
            'ticket': 't$tickets',
            'expires_at': '2026-10-20T10:00:30Z',
          }),
          200,
        );
      case 'PUT /api/desktop/display':
        if (dpiStatus != 200) {
          return http.Response(
            jsonEncode({'error': refusalText, 'code': dpiCode}),
            dpiStatus,
          );
        }
        final dpi = (jsonDecode(request.body) as Map)['dpi'] as int;
        status?['dpi'] = dpi;
        return http.Response(jsonEncode({'dpi': dpi}), 200);
    }
    return http.Response('{}', 404);
  }

  /// "METHOD /path" for every request, in order.
  List<String> get calls => [
    for (final r in requests) '${r.method} ${r.url.path}',
  ];

  /// The DPI of every display write, in order.
  List<int> get dpiWrites => [
    for (final r in requests)
      if (r.method == 'PUT') (jsonDecode(r.body) as Map)['dpi'] as int,
  ];

  DaemonAPIService service() => DaemonAPIService(
    hostId: desktopHost,
    serverUrl: 'http://host.test:7654',
    api: ApiClient(
      serverUrl: 'http://host.test:7654',
      deviceId: 'd1',
      privateKeySeed: Uint8List(32),
      client: MockClient(handle),
    ),
  );
}

/// The page, without a WebView: records what the app asks of it and lets a
/// test say what the page reports back.
class FakeBridge implements DesktopBridge {
  final _events = StreamController<DesktopPageEvent>.broadcast();
  final opened = <Uri>[];
  final connects = <DesktopConnectParams>[];
  final resizes = <(int, int)>[];
  final keys = <List<int>>[];
  final keyboard = <bool>[];
  int disconnects = 0;

  /// What the view shows, for pictures.
  final Widget? picture;

  FakeBridge({this.picture});

  void emit(String state, {int code = 0, String message = ''}) =>
      _events.add(DesktopPageEvent(state, code: code, message: message));

  @override
  Stream<DesktopPageEvent> get events => _events.stream;

  @override
  Future<void> open(Uri page) async => opened.add(page);

  @override
  Future<void> connect(DesktopConnectParams params) async =>
      connects.add(params);

  @override
  Future<void> resize(int width, int height) async =>
      resizes.add((width, height));

  @override
  Future<void> setKeyboard(bool open) async => keyboard.add(open);

  @override
  Future<void> sendKeys(List<int> keysyms) async => keys.add(keysyms);

  @override
  Future<void> sendChords(List<List<int>> chords) async => keys.addAll(chords);

  @override
  Future<void> disconnect() async => disconnects++;

  @override
  Widget view() =>
      picture ??
      const ColoredBox(key: Key('fake-desktop'), color: Color(0xFF1E1E1E));

  @override
  void dispose() => _events.close();
}

/// The app around a [DesktopScreen], with [daemon] as the host and [bridge]
/// as the page.
Widget desktopApp(
  FakeDesktopDaemon daemon,
  FakeBridge bridge, {
  Widget? home,
  HostManager? hosts,
  ThemeData? theme,
}) => rp.ProviderScope(
  overrides: [
    hostManagerProvider.overrideWithValue(hosts ?? HostManager()),
    serviceProvider(desktopHost).overrideWithValue(daemon.service()),
    desktopBridgeFactoryProvider.overrideWithValue(() => bridge),
  ],
  child: MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: theme,
    home: home ?? const DesktopScreen(hostId: desktopHost),
  ),
);
