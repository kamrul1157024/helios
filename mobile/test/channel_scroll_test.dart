import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/channel.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/screens/channel_detail_screen.dart';

/// A channel opened on its first message, so the newest thing said was a scroll
/// away — the one thing a reader opens a conversation for.

ChannelMessage _message(int n) => ChannelMessage(
  id: 'm$n',
  author: n.isEven ? 'user' : 'session:s1',
  from: n.isEven ? 'user' : 'agent',
  body: 'message $n',
  createdAt: '2026-09-14T10:00:0${n % 10}Z',
);

Widget _app(List<ChannelMessage> messages) {
  const channel = Channel(id: 'c1', name: 'testing', members: ['s1']);
  return rp.ProviderScope(
    overrides: [
      channelsProvider('h1').overrideWith((ref) async => [channel]),
      channelMessagesProvider((
        'h1',
        'c1',
      )).overrideWith((ref) async => messages),
    ],
    child: const MaterialApp(
      home: ChannelDetailScreen(hostId: 'h1', channelId: 'c1'),
    ),
  );
}

void main() {
  testWidgets('a channel opens on the newest message', (tester) async {
    final messages = [for (var n = 0; n < 40; n++) _message(n)];
    await tester.pumpWidget(_app(messages));
    await tester.pumpAndSettle();

    expect(find.text('message 39'), findsOneWidget);
    expect(find.text('message 0'), findsNothing);
  });
}
