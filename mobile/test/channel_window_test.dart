import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/channel.dart';
import 'package:helios/providers/channel_messages.dart';

/// The window slides rather than grows: a page loaded at one end drops a page off
/// the other, so a channel of any length holds a fixed slice, and every drop is
/// reversible by scrolling back the way you came.

ChannelMessage _m(int n) =>
    ChannelMessage(id: 'm$n', author: 'user', from: 'user', body: 'message $n');

List<ChannelMessage> _range(int from, int to) => [
  for (var n = from; n < to; n++) _m(n),
];

void main() {
  test('a full opening page has more behind it, a short one is the start', () {
    expect(openingWindow(_range(0, channelPageSize)).hasOlder, isTrue);
    expect(openingWindow(_range(0, channelPageSize)).hasNewer, isFalse);
    expect(openingWindow(_range(0, 5)).hasOlder, isFalse);
  });

  test('older prepends and keeps oldest-first order', () {
    final held = openingWindow(_range(80, 100));
    final grown = withOlder(held, _range(60, 80));

    expect(grown.messages.first.body, 'message 60');
    expect(grown.messages.last.body, 'message 99');
    expect(grown.messages.length, 40);
    // A full older page means more still sits above it.
    expect(grown.hasOlder, isTrue);
  });

  test('a short older page means the top has been reached', () {
    final held = ChannelWindow(messages: _range(5, 25), hasOlder: true);
    expect(withOlder(held, _range(0, 5)).hasOlder, isFalse);
  });

  test('loading older past the cap trims the newest, reversibly', () {
    // A full window, then a page older prepended: 20 over the cap.
    var window = ChannelWindow(messages: _range(0, channelMaxHeld));
    window = withOlder(window, _range(-channelPageSize, 0));
    expect(window.messages.length, channelMaxHeld + channelPageSize);

    final over = overflowOf(window);
    expect(over, channelPageSize);

    window = trimmedNewest(window, over);
    expect(window.messages.length, channelMaxHeld);
    // The oldest just loaded stayed; the newest were dropped and can return.
    expect(window.messages.first.body, 'message -$channelPageSize');
    expect(
      window.messages.last.body,
      'message ${channelMaxHeld - 1 - channelPageSize}',
    );
    expect(window.hasNewer, isTrue);
  });

  test('newer appends and drops the oldest past the cap', () {
    // A full window whose newest were trimmed, so more sit below it.
    final held = ChannelWindow(
      messages: _range(0, channelMaxHeld),
      hasOlder: true,
      hasNewer: true,
    );
    final grown = withNewer(
      held,
      _range(channelMaxHeld, channelMaxHeld + channelPageSize),
    );

    expect(grown.messages.length, channelMaxHeld);
    // The newest page landed at the bottom; the oldest page fell off the top.
    expect(
      grown.messages.last.body,
      'message ${channelMaxHeld + channelPageSize - 1}',
    );
    expect(grown.messages.first.body, 'message $channelPageSize');
    expect(grown.hasOlder, isTrue);
    // A full newer page means more may still sit below.
    expect(grown.hasNewer, isTrue);
  });

  test('a short newer page means the live tail is back in view', () {
    final held = ChannelWindow(messages: _range(0, 40), hasNewer: true);
    final grown = withNewer(held, _range(40, 45));
    expect(grown.hasNewer, isFalse);
  });

  test('empty pages leave the window untouched', () {
    final held = openingWindow(_range(0, 20));
    expect(withOlder(held, const []).messages.length, 20);
    expect(withNewer(held, const []).messages.length, 20);
    expect(trimmedNewest(held, 0).messages.length, 20);
  });
}
