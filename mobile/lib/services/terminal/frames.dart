/// Port of internal/terminal/protocol.go, alongside desktop/src/shared/frames.ts.
/// All three are pinned to the fixtures in internal/terminal/testdata/frames,
/// so a change to the wire format fails in whichever language forgets it.
///
/// Length-prefixed: uint32 len(type + payload), uint8 type, payload.
library;

import 'dart:convert';
import 'dart:typed_data';

class FrameType {
  static const int hello = 0x01;
  static const int snapshot = 0x02;
  static const int output = 0x03;
  static const int input = 0x04;
  static const int resize = 0x05;
  static const int status = 0x06;
  static const int exit = 0x07;
  static const int ping = 0x08;
  static const int pong = 0x09;
  static const int overlaySet = 0x0a;
  static const int overlayClear = 0x0b;
  static const int overlayInput = 0x0c;
  static const int paste = 0x0d;
}

/// Bounds a single frame so a corrupt length prefix cannot exhaust memory.
const int maxFrameSize = 8 << 20;

const int headerSize = 5;

/// Whether a viewer votes on the PTY size. The phone joins as an observer, so
/// it cannot shrink the terminal a desktop is watching; claiming the size
/// means reconnecting as interactive.
class Role {
  static const String interactive = 'interactive';
  static const String observer = 'observer';
}

class Frame {
  const Frame(this.type, this.payload);

  final int type;
  final Uint8List payload;
}

class FrameTooLargeException implements Exception {
  const FrameTooLargeException(this.size);

  final int size;

  @override
  String toString() => 'terminal: frame of $size bytes exceeds $maxFrameSize';
}

/// The status the host broadcasts: who is typing, how many are watching, and
/// the size everyone shares.
class TerminalStatus {
  const TerminalStatus({
    required this.state,
    required this.viewers,
    required this.cols,
    required this.rows,
    this.writer,
  });

  factory TerminalStatus.fromJson(Map<String, dynamic> json) => TerminalStatus(
        state: json['state'] as String? ?? 'ready',
        viewers: (json['viewers'] as num?)?.toInt() ?? 0,
        cols: (json['cols'] as num?)?.toInt() ?? 0,
        rows: (json['rows'] as num?)?.toInt() ?? 0,
        writer: json['writer'] as String?,
      );

  final String state;
  final int viewers;
  final int cols;
  final int rows;
  final String? writer;
}

Uint8List encodeFrame(int type, [Uint8List? payload]) {
  final body = payload ?? Uint8List(0);
  if (body.length + 1 > maxFrameSize) {
    throw FrameTooLargeException(body.length + 1);
  }
  final out = Uint8List(headerSize + body.length);
  final view = ByteData.view(out.buffer);
  view.setUint32(0, body.length + 1);
  out[4] = type;
  out.setRange(headerSize, headerSize + body.length, body);
  return out;
}

Uint8List encodeJsonFrame(int type, Object value) =>
    encodeFrame(type, Uint8List.fromList(utf8.encode(jsonEncode(value))));

/// The first frame a viewer sends. [since] asks for replay from a sequence
/// number; zero means "snapshot me".
Uint8List encodeHello({
  required String role,
  required int cols,
  required int rows,
  int since = 0,
  String? name,
}) =>
    encodeJsonFrame(FrameType.hello, {
      'role': role,
      'cols': cols,
      'rows': rows,
      'since': since,
      if (name != null && name.isNotEmpty) 'name': name,
    });

Uint8List encodeResize(int cols, int rows) {
  final out = Uint8List(4);
  final view = ByteData.view(out.buffer);
  view.setUint16(0, cols);
  view.setUint16(2, rows);
  return out;
}

({int cols, int rows}) decodeResize(Uint8List payload) {
  if (payload.length < 4) {
    throw const FormatException('terminal: resize payload too short');
  }
  final view = ByteData.view(payload.buffer, payload.offsetInBytes);
  return (cols: view.getUint16(0), rows: view.getUint16(2));
}

Uint8List encodeSnapshot(int seq, Uint8List ansi) {
  final out = Uint8List(8 + ansi.length);
  ByteData.view(out.buffer).setUint64(0, seq);
  out.setRange(8, out.length, ansi);
  return out;
}

/// The sequence the ANSI resync corresponds to, and the bytes themselves.
({int seq, Uint8List ansi}) decodeSnapshot(Uint8List payload) {
  if (payload.length < 8) {
    throw const FormatException('terminal: snapshot payload too short');
  }
  final view = ByteData.view(payload.buffer, payload.offsetInBytes);
  return (
    seq: view.getUint64(0),
    ansi: Uint8List.sublistView(payload, 8),
  );
}

int decodeExit(Uint8List payload) {
  if (payload.length < 4) {
    throw const FormatException('terminal: exit payload too short');
  }
  return ByteData.view(payload.buffer, payload.offsetInBytes).getInt32(0);
}

TerminalStatus decodeStatus(Uint8List payload) =>
    TerminalStatus.fromJson(jsonDecode(utf8.decode(payload)) as Map<String, dynamic>);

/// Reassembles frames from a byte stream, which arrives in whatever chunks the
/// socket felt like: a frame can span several, and one chunk can hold many.
class FrameParser {
  final List<int> _buffer = [];

  List<Frame> push(List<int> chunk) {
    _buffer.addAll(chunk);
    final frames = <Frame>[];

    while (_buffer.length >= headerSize) {
      final header = Uint8List.fromList(_buffer.sublist(0, headerSize));
      final length = ByteData.view(header.buffer).getUint32(0);
      if (length == 0) {
        throw const FormatException('terminal: zero-length frame');
      }
      if (length > maxFrameSize) {
        throw FrameTooLargeException(length);
      }
      final total = 4 + length;
      if (_buffer.length < total) break;

      frames.add(Frame(
        _buffer[4],
        Uint8List.fromList(_buffer.sublist(headerSize, total)),
      ));
      _buffer.removeRange(0, total);
    }
    return frames;
  }

  void reset() => _buffer.clear();
}
