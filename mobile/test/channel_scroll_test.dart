import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/channel.dart';
import 'package:helios/providers/channel_messages.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/screens/channel_detail_screen.dart';

/// A channel opened on its newest message, so the last thing said is in view —
/// the one thing a reader opens a conversation for — not a scroll away.

ChannelMessage _message(int n) => ChannelMessage(
  id: 'm$n',
  author: n.isEven ? 'user' : 'session:s1',
  from: n.isEven ? 'user' : 'agent',
  body: 'message $n',
  createdAt: '2026-09-14T10:00:0${n % 10}Z',
);

/// Serves a fixed window, so the screen renders without a daemon.
class _StubMessages extends ChannelMessagesNotifier {
  _StubMessages(this.window);
  final ChannelWindow window;

  @override
  Future<ChannelWindow> build((String, String) arg) async => window;
}

Widget _app(ChannelWindow window) {
  const channel = Channel(id: 'c1', name: 'testing', members: ['s1']);
  return rp.ProviderScope(
    overrides: [
      channelsProvider('h1').overrideWith((ref) async => [channel]),
      channelMessagesProvider.overrideWith(() => _StubMessages(window)),
    ],
    child: const MaterialApp(
      home: ChannelDetailScreen(hostId: 'h1', channelId: 'c1'),
    ),
  );
}

void main() {
  testWidgets('a channel opens on the newest message', (tester) async {
    final window = ChannelWindow(
      messages: [for (var n = 0; n < 40; n++) _message(n)],
    );
    await tester.pumpWidget(_app(window));
    await tester.pumpAndSettle();

    expect(find.text('message 39'), findsOneWidget);
    expect(find.text('message 0'), findsNothing);
  });
}
