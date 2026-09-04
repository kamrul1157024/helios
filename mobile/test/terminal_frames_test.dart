import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:helios/services/terminal/frames.dart';

/// The same fixtures Go writes and the desktop app reads — see
/// internal/terminal/golden_test.go and desktop/test/frames.test.ts. Read from
/// the repo rather than copied, so there is nothing to fall out of step with.
Uint8List fixture(String name) {
  final file = File('../internal/terminal/testdata/frames/$name.bin');
  if (!file.existsSync()) {
    fail(
      'missing ${file.path} — run: go test ./internal/terminal -run Golden -update',
    );
  }
  return file.readAsBytesSync();
}

void main() {
  group('encoding matches the golden fixtures', () {
    test('hello', () {
      final encoded = encodeHello(
        role: Role.interactive,
        cols: 120,
        rows: 34,
        since: 4096,
        name: 'desktop',
      );
      expect(encoded, equals(fixture('hello')));
    });

    test('resize', () {
      expect(
        encodeFrame(FrameType.resize, encodeResize(120, 34)),
        equals(fixture('resize')),
      );
    });

    test('snapshot', () {
      final ansi = Uint8List.fromList(
        utf8.encode('\x1b[2J\x1b[H\x1b[32mready\x1b[0m'),
      );
      final encoded = encodeFrame(
        FrameType.snapshot,
        encodeSnapshot(1 << 40, ansi),
      );
      expect(encoded, equals(fixture('snapshot')));
    });

    test('input', () {
      final encoded = encodeFrame(
        FrameType.input,
        Uint8List.fromList([0x1b, 0x5b, 0x5a]),
      );
      expect(encoded, equals(fixture('input')));
    });

    test('ping and pong carry no payload', () {
      expect(encodeFrame(FrameType.ping), equals(fixture('ping')));
      expect(encodeFrame(FrameType.pong), equals(fixture('pong')));
    });
  });

  group('decoding the golden fixtures', () {
    test('snapshot carries the sequence to resume from', () {
      final frames = FrameParser().push(fixture('snapshot'));
      expect(frames, hasLength(1));
      expect(frames.single.type, FrameType.snapshot);

      final decoded = decodeSnapshot(frames.single.payload);
      expect(decoded.seq, 1 << 40);
      expect(utf8.decode(decoded.ansi), '\x1b[2J\x1b[H\x1b[32mready\x1b[0m');
    });

    test('status names the writer and the shared size', () {
      final frames = FrameParser().push(fixture('status'));
      final status = decodeStatus(frames.single.payload);
      expect(status.state, 'ready');
      expect(status.writer, 'phone');
      expect(status.viewers, 2);
      expect(status.cols, 120);
      expect(status.rows, 34);
    });

    test('exit carries a signed code', () {
      final frames = FrameParser().push(fixture('exit'));
      expect(decodeExit(frames.single.payload), -1);
    });

    test('output survives multibyte text', () {
      final frames = FrameParser().push(fixture('output'));
      expect(
        utf8.decode(frames.single.payload),
        '\x1b[1mhello\x1b[0m 🎉 中文\r\n',
      );
    });

    test('resize round-trips', () {
      final frames = FrameParser().push(fixture('resize'));
      final size = decodeResize(frames.single.payload);
      expect(size.cols, 120);
      expect(size.rows, 34);
    });
  });

  group('the parser handles what a socket actually delivers', () {
    // The fixture holds three frames in one blob: a reader that assumed one
    // frame per chunk would drop two of them.
    test('several frames in one chunk', () {
      final frames = FrameParser().push(fixture('stream'));
      expect(frames.map((f) => f.type), [
        FrameType.output,
        FrameType.ping,
        FrameType.output,
      ]);
      expect(utf8.decode(frames[0].payload), 'first');
      expect(utf8.decode(frames[2].payload), 'second');
    });

    test('one frame split across chunks', () {
      final blob = fixture('stream');
      final parser = FrameParser();
      final collected = <Frame>[];
      // A byte at a time is the worst case a stream can hand over.
      for (final byte in blob) {
        collected.addAll(parser.push([byte]));
      }
      expect(collected.map((f) => f.type), [
        FrameType.output,
        FrameType.ping,
        FrameType.output,
      ]);
      expect(utf8.decode(collected[2].payload), 'second');
    });

    test('a length prefix past the maximum is refused, not allocated', () {
      final absurd = Uint8List(headerSize);
      ByteData.view(absurd.buffer).setUint32(0, maxFrameSize + 1);
      expect(
        () => FrameParser().push(absurd),
        throwsA(isA<FrameTooLargeException>()),
      );
    });
  });
}
