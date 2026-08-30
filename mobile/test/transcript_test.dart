import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/message.dart';
import 'package:helios/providers/transcript.dart';

/// How a conversation grows at both ends.
///
/// The interesting rule is the epoch: a delta that comes back under a new one
/// describes a different parse of the transcript, and appending it would
/// interleave two conversations.

Message msg(int seq) => Message(
      seq: seq,
      role: 'assistant',
      content: 'm$seq',
      timestamp: '2026-01-01T00:00:00Z',
    );

TranscriptResult result(
  List<Message> messages, {
  int total = 0,
  bool hasMore = false,
  String epoch = 'e1',
  bool epochChanged = false,
}) =>
    TranscriptResult(
      messages: messages,
      total: total,
      returned: messages.length,
      offset: 0,
      hasMore: hasMore,
      epoch: epoch,
      epochChanged: epochChanged,
    );

void main() {
  const held = Transcript(
    messages: [],
    total: 2,
    hasMore: true,
    epoch: 'e1',
  );

  group('appendDelta', () {
    test('adds new messages to the end', () {
      final base = Transcript(messages: [msg(1), msg(2)], total: 2, epoch: 'e1');
      final next = appendDelta(base, result([msg(3)], total: 3));
      expect(next.messages.map((m) => m.seq), [1, 2, 3]);
      expect(next.total, 3);
      expect(next.epoch, 'e1');
    });

    test('an empty delta leaves the messages alone', () {
      final base = Transcript(messages: [msg(1)], total: 1, epoch: 'e1');
      final next = appendDelta(base, result(const [], total: 1));
      expect(next.messages.map((m) => m.seq), [1]);
    });

    // The transcript those seq numbers counted against is gone — forked, or
    // replaced. Appending across that would interleave two conversations.
    test('an epoch change replaces rather than appends', () {
      final base = Transcript(messages: [msg(1), msg(2)], total: 2, epoch: 'e1');
      final next = appendDelta(
        base,
        result([msg(9)], total: 1, epoch: 'e2', epochChanged: true),
      );
      expect(next.messages.map((m) => m.seq), [9]);
      expect(next.epoch, 'e2', reason: 'the new parse is adopted');
      expect(next.total, 1);
    });

    test('an epoch change carries the new hasMore', () {
      final next = appendDelta(
        held,
        result([msg(9)], hasMore: true, epoch: 'e2', epochChanged: true),
      );
      expect(next.hasMore, isTrue);
    });

    // Only the reader scrolling back changes whether older pages exist, so a
    // delta must not answer that question.
    test('a normal delta leaves hasMore alone', () {
      final base = Transcript(messages: [msg(1)], hasMore: true, epoch: 'e1');
      final next = appendDelta(base, result([msg(2)], hasMore: false));
      expect(next.hasMore, isTrue);
    });
  });

  group('prependPage', () {
    test('puts the older page in front', () {
      final base = Transcript(messages: [msg(5), msg(6)], epoch: 'e1');
      final next = prependPage(base, result([msg(3), msg(4)], total: 4));
      expect(next.messages.map((m) => m.seq), [3, 4, 5, 6]);
    });

    test('takes the page hasMore, which is what says whether to keep going', () {
      final base = Transcript(messages: [msg(5)], hasMore: true, epoch: 'e1');
      final next = prependPage(base, result([msg(4)], hasMore: false));
      expect(next.hasMore, isFalse);
    });

    test('keeps the epoch: paging back is the same parse', () {
      final base = Transcript(messages: [msg(5)], epoch: 'e1');
      final next = prependPage(base, result([msg(4)], epoch: 'ignored'));
      expect(next.epoch, 'e1');
    });
  });

  // A delta appended to a page gives what a full fetch would have.
  test('a page plus its delta equals the whole conversation', () {
    final page = Transcript(messages: [msg(1), msg(2)], total: 2, epoch: 'e1');
    final whole = appendDelta(page, result([msg(3), msg(4)], total: 4));
    expect(whole.messages.map((m) => m.seq), [1, 2, 3, 4]);
    expect(whole.total, 4);
  });
}
