import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/providers/transcript.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';

/// Catching up on a transcript after the event stream lost something.
///
/// The stream is best-effort — the daemon drops a broadcast to a client whose
/// buffer is full and keeps nothing across a disconnect — so `pullNew` is the
/// one path back to current, and every property it needs is asserted here:
/// it walks a truncated delta to the end, it never runs twice at once, and it
/// never puts the reader back on a loading state.

const _host = 'h1';
const _session = 's1';
const _key = (_host, _session);

Map<String, dynamic> msg(int seq) => {
      'seq': seq,
      'role': 'assistant',
      'content': 'm$seq',
      'timestamp': '2026-01-01T00:00:00Z',
    };

/// The daemon's answer: `seqs`, and whether the limit cut it short.
String body(List<int> seqs, {String epoch = 'e1', bool moreAfter = false}) =>
    jsonEncode({
      'messages': seqs.map(msg).toList(),
      'total': 99,
      'returned': seqs.length,
      'offset': 0,
      'has_more': false,
      'epoch': epoch,
      'more_after': moreAfter,
    });

/// Records every transcript request, and answers from [answers] in order.
class Daemon {
  Daemon(this.answers);

  final List<String> answers;
  final List<Uri> asked = [];
  int _next = 0;

  /// Completed by the test to release the answer, when the test needs to
  /// observe the state of a request that is still in flight.
  List<Future<void>?> gates = [];

  Future<http.Response> handle(http.Request req) async {
    if (req.url.path.contains('/auth/')) {
      return http.Response(
        jsonEncode({
          'token': 't',
          'expires_at': DateTime.now()
              .toUtc()
              .add(const Duration(hours: 1))
              .toIso8601String(),
        }),
        200,
      );
    }
    asked.add(req.url);
    final i = _next++;
    if (i < gates.length && gates[i] != null) await gates[i];
    return http.Response(answers[i.clamp(0, answers.length - 1)], 200);
  }

  List<Uri> get deltas =>
      asked.where((u) => u.queryParameters.containsKey('after_seq')).toList();
}

ProviderContainer containerFor(Daemon daemon) {
  final service = DaemonAPIService(
    hostId: _host,
    serverUrl: 'http://localhost:1',
    api: ApiClient(
      serverUrl: 'http://localhost:1',
      deviceId: 'd1',
      privateKeySeed: Uint8List(32),
      client: MockClient(daemon.handle),
    ),
  );
  final container = ProviderContainer(
    overrides: [serviceProvider(_host).overrideWithValue(service)],
  );
  addTearDown(container.dispose);
  return container;
}

TranscriptNotifier notifierOf(ProviderContainer c) =>
    c.read(transcriptProvider(_key).notifier);

List<int> seqsOf(ProviderContainer c) => c
    .read(transcriptProvider(_key))
    .valueOrNull!
    .messages
    .map((m) => m.seq)
    .toList();

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('walks a truncated delta to the end of the transcript', () async {
    // Away long enough to miss more than one page: the daemon answers the
    // oldest of what is new and says there is more behind it.
    final daemon = Daemon([
      body([1, 2]),
      body([3, 4], moreAfter: true),
      body([5, 6], moreAfter: true),
      body([7]),
    ]);
    final c = containerFor(daemon);
    await c.read(transcriptProvider(_key).future);

    await notifierOf(c).pullNew();

    expect(seqsOf(c), [1, 2, 3, 4, 5, 6, 7],
        reason: 'a delta cut short must be followed to the end, not left as a hole');
    expect(
      daemon.deltas.map((u) => u.queryParameters['after_seq']),
      ['2', '4', '6'],
      reason: 'each request advances the cursor to the last seq it was given',
    );
  });

  test('one delta is enough when nothing was cut short', () async {
    final daemon = Daemon([
      body([1, 2]),
      body([3]),
    ]);
    final c = containerFor(daemon);
    await c.read(transcriptProvider(_key).future);

    await notifierOf(c).pullNew();

    expect(seqsOf(c), [1, 2, 3]);
    expect(daemon.deltas, hasLength(1));
  });

  test('a trigger arriving mid-flight is run once afterwards, not dropped',
      () async {
    final release = Completer<void>();
    final daemon = Daemon([
      body([1, 2]),
      body([3]),
      body([4]),
    ])
      ..gates = [null, release.future];

    final c = containerFor(daemon);
    await c.read(transcriptProvider(_key).future);

    final first = notifierOf(c).pullNew();
    await pumpEventQueue();
    expect(daemon.deltas, hasLength(1), reason: 'the first read is out');

    // A second event lands while that read is still waiting on the daemon.
    notifierOf(c).pullNew();
    await pumpEventQueue();
    expect(daemon.deltas, hasLength(1),
        reason: 'the second trigger must not open a second request');

    release.complete();
    await first;
    await pumpEventQueue();

    expect(daemon.deltas, hasLength(2),
        reason: 'the trigger that arrived mid-flight is the one this provider '
            'used to lose; it must produce a read that started after it');
    expect(seqsOf(c), [1, 2, 3, 4]);
  });

  test('a failed read leaves the conversation alone and does not wedge the next',
      () async {
    final daemon = Daemon([body([1, 2])]);
    final c = containerFor(daemon);
    await c.read(transcriptProvider(_key).future);

    // fetchTranscript swallows the failure and answers null.
    daemon.answers.add('not json');
    await notifierOf(c).pullNew();
    expect(seqsOf(c), [1, 2]);

    daemon.answers.add(body([3]));
    await notifierOf(c).pullNew();
    expect(seqsOf(c), [1, 2, 3], reason: 'the next pull must still run');
  });

  test('an epoch change replaces the conversation and stops the walk', () async {
    final daemon = Daemon([
      body([1, 2]),
      jsonEncode({
        'messages': [msg(9)],
        'total': 1,
        'returned': 1,
        'offset': 0,
        'has_more': false,
        'epoch': 'e2',
        'epoch_changed': true,
      }),
    ]);
    final c = containerFor(daemon);
    await c.read(transcriptProvider(_key).future);

    await notifierOf(c).pullNew();

    expect(seqsOf(c), [9],
        reason: 'the seq numbers held counted against a parse that is gone');
    expect(daemon.deltas, hasLength(1));
  });

  test('catching up never returns the cell to a loading state', () async {
    final daemon = Daemon([
      body([1, 2]),
      body([3, 4], moreAfter: true),
      body([5]),
    ]);
    final c = containerFor(daemon);
    await c.read(transcriptProvider(_key).future);

    final seen = <bool>[];
    c.listen(transcriptProvider(_key), (_, next) => seen.add(next.isLoading));

    await notifierOf(c).pullNew();

    expect(seen, isNotEmpty);
    expect(seen, everyElement(isFalse),
        reason: 'a loading state here draws the skeleton over messages the '
            'reader is looking at');
  });
}
