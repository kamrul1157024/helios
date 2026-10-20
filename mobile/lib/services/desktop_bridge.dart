/// The phone's side of the desktop page (spec 66, D8).
///
/// The page is served by the daemon at `/desktop/` and does the drawing and
/// the touch with guacamole-common-js. This file is the thin seam between it
/// and the app: [DesktopBridge] is what the page can be asked to do, and
/// [DesktopLink] is the ticket-and-reconnect logic, kept apart from the WebView
/// so it can be tested without one.
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:webview_flutter/webview_flutter.dart';

import '../models/desktop.dart';

/// Close codes the tunnel uses (see the contract in spec 66, D2).
const closeBadTicket = 4401;
const closeNotReady = 4503;
const closeTooManyViewers = 4429;
const closeGuacdFailed = 1011;

/// One `{"type":"state"}` message from the page.
class DesktopPageEvent {
  /// connecting, connected, closed or error from the page; unreachable when
  /// the page itself did not load.
  final String state;
  final int code;
  final String message;

  const DesktopPageEvent(this.state, {this.code = 0, this.message = ''});

  static DesktopPageEvent? parse(String raw) {
    try {
      final data = jsonDecode(raw);
      if (data is! Map || data['type'] != 'state') return null;
      return DesktopPageEvent(
        data['state'] as String? ?? '',
        code: (data['code'] as num?)?.toInt() ?? 0,
        message: data['message'] as String? ?? '',
      );
    } on FormatException {
      return null;
    }
  }
}

/// What a connect needs. The ticket is the only credential the page ever sees.
class DesktopConnectParams {
  final String ticket;
  final int width;
  final int height;
  final int dpi;

  const DesktopConnectParams({
    required this.ticket,
    required this.width,
    required this.height,
    required this.dpi,
  });

  Map<String, dynamic> toJson() => {
    'ticket': ticket,
    'width': width,
    'height': height,
    'dpi': dpi,
  };
}

/// `window.heliosDesktop` and its event channel, as the app sees them.
abstract class DesktopBridge {
  Stream<DesktopPageEvent> get events;

  /// Loads the page. Its fragment carries the first ticket, so the page
  /// connects as soon as it has loaded.
  Future<void> open(Uri page);

  /// Connects again in the loaded page, with a fresh ticket.
  Future<void> connect(DesktopConnectParams params);
  Future<void> resize(int width, int height);
  Future<void> setKeyboard(bool open);

  /// Presses the keys in order and releases them in reverse: one chord.
  Future<void> sendKeys(List<int> keysyms);

  /// Several chords in one trip, for typed text.
  Future<void> sendChords(List<List<int>> chords);
  Future<void> disconnect();

  Widget view();
  void dispose();
}

/// The real bridge: a WebView on the daemon's page.
class WebViewDesktopBridge implements DesktopBridge {
  final _events = StreamController<DesktopPageEvent>.broadcast();
  late final WebViewController _controller;
  Uri? _page;

  WebViewDesktopBridge() {
    _controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(const Color(0xFF1E1E1E))
      ..addJavaScriptChannel(
        'HeliosDesktop',
        onMessageReceived: (message) {
          final event = DesktopPageEvent.parse(message.message);
          if (event != null && !_events.isClosed) _events.add(event);
        },
      )
      ..setNavigationDelegate(
        NavigationDelegate(
          // The page and nothing else. It has no links, and anything that
          // tries to leave it would leave with no way back.
          onNavigationRequest: (request) {
            final page = _page;
            final target = Uri.tryParse(request.url);
            final same =
                page != null &&
                target != null &&
                target.origin == page.origin &&
                target.path == page.path;
            return same
                ? NavigationDecision.navigate
                : NavigationDecision.prevent;
          },
          onWebResourceError: (error) {
            if (error.isForMainFrame ?? true) {
              _events.add(
                DesktopPageEvent('unreachable', message: error.description),
              );
            }
          },
        ),
      );
  }

  @override
  Stream<DesktopPageEvent> get events => _events.stream;

  @override
  Future<void> open(Uri page) {
    _page = page;
    return _controller.loadRequest(page);
  }

  Future<void> _call(String method, [List<Object> args = const []]) =>
      _controller.runJavaScript(
        'window.heliosDesktop && window.heliosDesktop.$method('
        '${args.map(jsonEncode).join(',')});',
      );

  @override
  Future<void> connect(DesktopConnectParams params) =>
      _call('connect', [params.toJson()]);

  @override
  Future<void> resize(int width, int height) =>
      _call('resize', [width, height]);

  @override
  Future<void> setKeyboard(bool open) => _call('setKeyboard', [open]);

  @override
  Future<void> sendKeys(List<int> keysyms) => _call('sendKeys', [keysyms]);

  @override
  Future<void> sendChords(List<List<int>> chords) => _controller.runJavaScript(
    'if (window.heliosDesktop) { ${[for (final c in chords) 'window.heliosDesktop.sendKeys(${jsonEncode(c)});'].join(' ')} }',
  );

  @override
  Future<void> disconnect() => _call('disconnect');

  @override
  Widget view() => WebViewWidget(controller: _controller);

  @override
  void dispose() {
    unawaited(disconnect().catchError((Object _) {}));
    _events.close();
  }
}

/// Where the link is. The screen draws one of these over the page.
enum DesktopLinkPhase { ticketing, connecting, connected, failed }

/// Gets tickets, opens the page, and decides what a closed tunnel means.
///
/// A 4401 is a ticket that was spent or ran out — the phone slept between
/// getting it and using it, for one — so it is answered with a fresh ticket,
/// a few times in a row at most. The other close codes are put into words.
class DesktopLink extends ChangeNotifier {
  final DesktopBridge bridge;
  final Future<DesktopTicket> Function() ticket;
  final Uri Function(DesktopTicket ticket, DesktopConnectParams params) pageFor;

  /// Called when the host says the desktop is not ready, so the screen can
  /// re-read the state and show the install or start progress instead.
  final VoidCallback? onNotReady;

  /// Called when the host refuses because remote desktop is turned off.
  final VoidCallback? onDisabled;

  static const maxReticket = 3;

  DesktopLinkPhase _phase = DesktopLinkPhase.ticketing;
  String _message = '';

  /// Whether the page has loaded and spoken. Until it has, a connect means
  /// loading it; after, it means calling into it.
  bool _pageLive = false;
  int _reticket = 0;
  ({int width, int height, int dpi})? _size;
  StreamSubscription<DesktopPageEvent>? _sub;
  bool _disposed = false;

  DesktopLink({
    required this.bridge,
    required this.ticket,
    required this.pageFor,
    this.onNotReady,
    this.onDisabled,
  }) {
    _sub = bridge.events.listen(_onEvent);
  }

  DesktopLinkPhase get phase => _phase;

  /// Why the link failed, in words. Empty unless [phase] is failed.
  String get message => _message;

  /// Connects at this size, or reconnects after a failure.
  Future<void> start({
    required int width,
    required int height,
    required int dpi,
  }) async {
    _size = (width: width, height: height, dpi: dpi);
    _reticket = 0;
    await _connect();
  }

  /// Asks the page for a new remote size, over the open tunnel.
  Future<void> resize(int width, int height) async {
    final size = _size;
    if (size == null) return;
    _size = (width: width, height: height, dpi: size.dpi);
    if (_phase == DesktopLinkPhase.connected) {
      await bridge.resize(width, height);
    }
  }

  Future<void> _connect() async {
    final size = _size;
    if (size == null) return;
    _set(DesktopLinkPhase.ticketing);
    final DesktopTicket pass;
    try {
      pass = await ticket();
    } on DesktopActionException catch (e) {
      if (e.disabled) {
        onDisabled?.call();
      } else if (e.statusCode == 409 || e.code == 'not_ready') {
        onNotReady?.call();
      }
      _fail(e.words);
      return;
    } catch (_) {
      _fail('Could not reach the host.');
      return;
    }
    if (_disposed) return;
    final params = DesktopConnectParams(
      ticket: pass.ticket,
      width: size.width,
      height: size.height,
      dpi: size.dpi,
    );
    _set(DesktopLinkPhase.connecting);
    if (_pageLive) {
      await bridge.connect(params);
    } else {
      await bridge.open(pageFor(pass, params));
    }
  }

  void _onEvent(DesktopPageEvent event) {
    _pageLive = event.state != 'unreachable';
    switch (event.state) {
      case 'connecting':
        _set(DesktopLinkPhase.connecting);
      case 'connected':
        _reticket = 0;
        _set(DesktopLinkPhase.connected);
      case 'closed':
      case 'error':
        _onClosed(event);
      case 'unreachable':
        _fail('Could not load the desktop page from the host.');
    }
  }

  void _onClosed(DesktopPageEvent event) {
    if (event.code == closeBadTicket && _reticket < maxReticket) {
      _reticket++;
      unawaited(_connect());
      return;
    }
    if (event.code == closeNotReady) onNotReady?.call();
    _fail(closeMessage(event.code));
  }

  void _fail(String message) {
    _message = message;
    _set(DesktopLinkPhase.failed);
  }

  void _set(DesktopLinkPhase phase) {
    if (_disposed) return;
    if (phase != DesktopLinkPhase.failed) _message = '';
    _phase = phase;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _sub?.cancel();
    super.dispose();
  }
}

/// A closed tunnel, in words a person can act on.
///
/// Never the page's own detail: that is the library's or the daemon's text,
/// and a person can do nothing with it.
String closeMessage(int code) {
  switch (code) {
    case closeBadTicket:
      return 'The host did not accept this phone\'s pass to the desktop. '
          'Try again.';
    case closeNotReady:
      return 'The desktop is not ready yet.';
    case closeTooManyViewers:
      return 'Four people are already watching this desktop. '
          'Close one of the other views and try again.';
    case closeGuacdFailed:
      return 'The desktop service stopped. Try again.';
    case 1000:
    case 1005:
      return 'The desktop was disconnected.';
  }
  return 'The connection to the desktop was lost.';
}
