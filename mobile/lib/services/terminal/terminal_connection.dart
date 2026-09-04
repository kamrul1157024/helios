import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/io.dart';

import 'frames.dart';

/// Where the connection is, as far as the user needs to know. Named for the
/// link rather than the terminal, which xterm already has a state of its own for.
enum LinkState { connecting, live, reconnecting, closed }

/// Backoff schedule from spec 31: 0.5s, 1s, 2s, 4s, then 8s forever.
const _reconnectDelays = [
  Duration(milliseconds: 500),
  Duration(seconds: 1),
  Duration(seconds: 2),
  Duration(seconds: 4),
  Duration(seconds: 8),
];

const _pingInterval = Duration(seconds: 10);
const _pongTimeout = Duration(seconds: 5);

/// One viewer connection to a session's terminal host, over the same frame
/// protocol the desktop speaks — see desktop/src/main/conn.ts, which this
/// mirrors, and internal/terminal/protocol.go, which defines it.
///
/// The phone connects as an observer by default. The host adopts the smallest
/// size any *interactive* viewer declares, so an observer can watch a desktop's
/// terminal without reflowing it; claiming the size means reconnecting in the
/// other role.
class TerminalConnection {
  TerminalConnection({
    required this.serverUrl,
    required this.terminalId,
    required this.getToken,
    required this.cols,
    required this.rows,
    this.role = Role.observer,
    this.name = 'phone',
  });

  final String serverUrl;
  final String terminalId;
  final Future<String> Function() getToken;
  final String role;
  final String name;

  int cols;
  int rows;

  /// Bytes for the emulator: live output, and the ANSI resync of a snapshot.
  final _output = StreamController<Uint8List>.broadcast();

  /// Fires when the host resyncs us, so the emulator can clear first.
  final _snapshots = StreamController<void>.broadcast();
  final _statuses = StreamController<TerminalStatus>.broadcast();
  final _states = StreamController<LinkState>.broadcast();

  Stream<Uint8List> get output => _output.stream;
  Stream<void> get snapshots => _snapshots.stream;
  Stream<TerminalStatus> get status => _statuses.stream;
  Stream<LinkState> get states => _states.stream;

  /// Why the connection ended, when it ended for good rather than dropped.
  String? closeReason;

  IOWebSocketChannel? _channel;
  StreamSubscription<dynamic>? _socketSub;
  final _parser = FrameParser();

  /// Bytes consumed, counted exactly as internal/terminal/client.go does: add
  /// every Output payload's length, adopt a Snapshot's sequence wholesale. It
  /// is what lets a reconnect resume rather than resync.
  int _seq = 0;
  int _attempt = 0;
  bool _closed = false;
  Timer? _pingTimer;
  Timer? _pongTimer;
  Timer? _reconnectTimer;
  int _sentCols = -1;
  int _sentRows = -1;

  Future<void> start() => _connect();

  Future<void> _connect() async {
    if (_closed) return;
    _emitState(LinkState.connecting);

    try {
      final token = await getToken();
      final url = serverUrl.replaceFirst(RegExp(r'^http'), 'ws');
      final channel = IOWebSocketChannel.connect(
        Uri.parse('$url/api/terminals/${Uri.encodeComponent(terminalId)}'),
        headers: {'Authorization': 'Bearer $token'},
        connectTimeout: const Duration(seconds: 5),
      );
      await channel.ready;
      if (_closed) {
        await channel.sink.close();
        return;
      }

      _channel = channel;
      _parser.reset();
      _socketSub = channel.stream.listen(
        _consume,
        onError: (Object err) => _dropped('$err'),
        onDone: () => _dropped(channel.closeReason ?? 'connection closed'),
        cancelOnError: true,
      );

      _sendHello();
      _startHeartbeat();
      _attempt = 0;
      _emitState(LinkState.live);
    } catch (err) {
      _dropped('$err');
    }
  }

  void _sendHello() {
    _write(
      encodeHello(role: role, cols: cols, rows: rows, since: _seq, name: name),
    );
    _sentCols = cols;
    _sentRows = rows;
  }

  void _consume(dynamic message) {
    if (message is! List<int>) return;
    List<Frame> frames;
    try {
      frames = _parser.push(message);
    } catch (err) {
      // A framing error means the stream is no longer trustworthy; resync.
      _dropped('$err');
      return;
    }

    for (final frame in frames) {
      switch (frame.type) {
        case FrameType.output:
          _seq += frame.payload.length;
          _output.add(frame.payload);
        case FrameType.snapshot:
          final decoded = decodeSnapshot(frame.payload);
          _seq = decoded.seq;
          _snapshots.add(null);
          _output.add(decoded.ansi);
        case FrameType.status:
          _statuses.add(decodeStatus(frame.payload));
        case FrameType.exit:
          _fail('the process exited (${decodeExit(frame.payload)})');
        case FrameType.pong:
          _clearPongTimer();
        default:
        // Hello, Input and Resize travel the other way; a host that sends one
        // is a host we do not understand, and ignoring it is safe.
      }
    }
  }

  void send(Uint8List bytes) => _write(encodeFrame(FrameType.input, bytes));

  /// Delivers text as a paste rather than as keystrokes. Typed in, an
  /// application can mistake the trailing Enter for part of the burst and lose
  /// the submit — see docs/specs/37-prompt-delivery-reliability.md.
  void paste(String text) =>
      _write(encodeFrame(FrameType.paste, Uint8List.fromList(text.codeUnits)));

  /// Declares this viewer's size. Only meaningful as an interactive viewer:
  /// the host ignores an observer's vote, which is the whole reason the phone
  /// can watch a desktop terminal without reflowing it.
  void resize(int newCols, int newRows) {
    if (newCols == _sentCols && newRows == _sentRows) return;
    _sentCols = newCols;
    _sentRows = newRows;
    if (newCols > 0 && newRows > 0) {
      // Remembered so a reconnect's Hello carries the real geometry.
      cols = newCols;
      rows = newRows;
    }
    _write(encodeFrame(FrameType.resize, encodeResize(newCols, newRows)));
  }

  void _write(Uint8List frame) {
    final channel = _channel;
    if (channel == null) return;
    try {
      channel.sink.add(frame);
    } catch (err) {
      _dropped('$err');
    }
  }

  /// Catches the zombie from spec 31: a viewer dropped for overrunning its
  /// frame queue keeps a socket that reads fine and writes nowhere. It answers
  /// no pings, and that is the only thing that gives it away.
  void _startHeartbeat() {
    _stopHeartbeat();
    _pingTimer = Timer.periodic(_pingInterval, (_) {
      if (_pongTimer != null) return; // a ping is already outstanding
      _write(encodeFrame(FrameType.ping));
      _pongTimer = Timer(_pongTimeout, () {
        _pongTimer = null;
        _dropped('the terminal stopped answering');
      });
    });
  }

  void _stopHeartbeat() {
    _pingTimer?.cancel();
    _pingTimer = null;
    _clearPongTimer();
  }

  void _clearPongTimer() {
    _pongTimer?.cancel();
    _pongTimer = null;
  }

  void _dropped(String reason) {
    if (_closed) return;
    _teardown();
    if (_reconnectTimer != null) return;
    final delay =
        _reconnectDelays[_attempt < _reconnectDelays.length
            ? _attempt
            : _reconnectDelays.length - 1];
    _attempt++;
    debugPrint(
      '[Terminal] $terminalId dropped: $reason — retrying in ${delay.inMilliseconds}ms',
    );
    _emitState(LinkState.reconnecting);
    _reconnectTimer = Timer(delay, () {
      _reconnectTimer = null;
      _connect();
    });
  }

  /// Gives up for good: the terminal is gone, not merely unreachable.
  void _fail(String reason) {
    if (_closed) return;
    closeReason = reason;
    dispose();
  }

  void _teardown() {
    _stopHeartbeat();
    _socketSub?.cancel();
    _socketSub = null;
    _channel?.sink.close();
    _channel = null;
  }

  void _emitState(LinkState state) {
    if (!_states.isClosed) _states.add(state);
  }

  /// Detaches this viewer. It never kills the host: leaving the screen is
  /// closing a window, and `helios ptyhost` runs detached so it survives.
  void dispose() {
    if (_closed) return;
    _closed = true;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _teardown();
    _emitState(LinkState.closed);
    _output.close();
    _snapshots.close();
    _statuses.close();
    _states.close();
  }
}
