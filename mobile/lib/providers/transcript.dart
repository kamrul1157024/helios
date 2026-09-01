import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/message.dart';
import '../services/daemon_api_service.dart';
import 'daemon_providers.dart';

/// A conversation as the reader has it: the messages held, how many exist, and
/// which parse the seq numbers count against.
class Transcript {
  final List<Message> messages;
  final int total;
  final bool hasMore;

  /// The parse the held seq numbers belong to, quoted back when asking for a
  /// delta so the daemon can say when it no longer holds.
  final String epoch;

  const Transcript({
    this.messages = const [],
    this.total = 0,
    this.hasMore = false,
    this.epoch = '',
  });
}

/// Adds what the agent has written since the last message held.
///
/// An epoch change means the transcript those seq numbers counted against is
/// gone — forked, or replaced — so the delta *replaces* what is held rather
/// than being appended to it. Appending across a fork would interleave two
/// different conversations.
Transcript appendDelta(Transcript held, TranscriptResult delta) {
  if (delta.epochChanged) {
    return Transcript(
      messages: delta.messages,
      total: delta.total,
      hasMore: delta.hasMore,
      epoch: delta.epoch,
    );
  }
  return Transcript(
    messages: delta.messages.isEmpty
        ? held.messages
        : [...held.messages, ...delta.messages],
    total: delta.total,
    hasMore: held.hasMore,
    epoch: held.epoch,
  );
}

/// Adds the page before the oldest message held, for a reader scrolling back.
Transcript prependPage(Transcript held, TranscriptResult older) => Transcript(
      messages: [...older.messages, ...held.messages],
      total: older.total,
      hasMore: older.hasMore,
      epoch: held.epoch,
    );

/// One session's conversation, paged backwards from the newest message.
///
/// Never refreshed wholesale. The reader's place in a long transcript is worth
/// more than freshness, and the delta keeps the tail current — refetching the
/// first page would throw away every older page they had scrolled back to.
class TranscriptNotifier
    extends FamilyAsyncNotifier<Transcript, (String, String)> {
  static const pageSize = 50;

  bool _loadingOlder = false;
  bool get loadingOlder => _loadingOlder;

  /// The pull in flight, and whether a trigger arrived while it was running.
  ///
  /// A trigger that lands mid-flight is exactly the case this provider gets
  /// wrong when it is dropped, so it schedules one more pull instead. What is
  /// promised is that every trigger is followed by a pull that *started* after
  /// it, which a single trailing flag is enough to give.
  Future<void>? _pulling;
  bool _pullAgain = false;

  /// Set when the notifier is torn down or rebuilt. A pull is several awaits
  /// long, and assigning `state` after that has happened throws.
  bool _gone = false;

  @override
  Future<Transcript> build((String, String) arg) async {
    _gone = false;
    ref.onDispose(() => _gone = true);
    final (hostId, sessionId) = arg;
    if (sessionId.isEmpty) return const Transcript();
    final service = ref.watch(serviceProvider(hostId));
    if (service == null) return const Transcript();

    final result = await service.fetchTranscript(sessionId, limit: pageSize);
    if (result == null) return const Transcript();
    return Transcript(
      messages: result.messages,
      total: result.total,
      hasMore: result.hasMore,
      epoch: result.epoch,
    );
  }

  DaemonAPIService? get _service => ref.read(serviceProvider(arg.$1));

  /// Pulls what the agent has written since the last message held.
  ///
  /// A status event fires several times a turn and the answer is usually one
  /// message, so asking for the page again would rebuild the list and lose the
  /// reader's place. Nothing here touches the loading state: the whole
  /// transition is `AsyncData` to `AsyncData`, so no caller can put the
  /// skeleton back over a conversation the reader is looking at.
  Future<void> pullNew() {
    if (_pulling != null) {
      _pullAgain = true;
      return _pulling!;
    }
    return _pulling = _pull().whenComplete(() {
      _pulling = null;
      if (_pullAgain && !_gone) {
        _pullAgain = false;
        pullNew();
      }
    });
  }

  Future<void> _pull() async {
    final service = _service;
    if (service == null) return;

    // The daemon answers a delta up to a limit, so catching up after the app
    // was away is several requests. Each one advances the cursor to the last
    // seq it was given, which is only contiguous because the daemon hands back
    // the oldest of what is new rather than the newest.
    while (!_gone) {
      final held = state.valueOrNull;
      if (held == null || held.messages.isEmpty || held.epoch.isEmpty) {
        // Nothing to quote an epoch from. This reads the first page again,
        // which is a refresh rather than a delta — harmless, because there is
        // nothing on screen to lose.
        ref.invalidateSelf();
        return;
      }

      final result = await service.fetchTranscript(
        arg.$2,
        limit: pageSize,
        afterSeq: held.messages.last.seq,
        epoch: held.epoch,
      );
      if (result == null || _gone) return;

      // Read again rather than reusing `held`: the await is long enough for
      // loadOlder to have prepended a page underneath.
      state = AsyncData(appendDelta(state.valueOrNull ?? held, result));

      // An empty answer cannot advance the cursor, so asking again would ask
      // the same question forever.
      if (!result.moreAfter || result.messages.isEmpty) return;
    }
  }

  /// Reads the page before the oldest message held.
  Future<void> loadOlder() async {
    final held = state.valueOrNull;
    final service = _service;
    if (service == null || held == null || _loadingOlder || !held.hasMore) {
      return;
    }
    _loadingOlder = true;
    ref.notifyListeners();
    try {
      final result = await service.fetchTranscript(
        arg.$2,
        limit: pageSize,
        offset: held.messages.length,
      );
      if (result != null) state = AsyncData(prependPage(held, result));
    } finally {
      _loadingOlder = false;
      ref.notifyListeners();
    }
  }
}

final transcriptProvider = AsyncNotifierProvider.family<TranscriptNotifier,
    Transcript, (String, String)>(TranscriptNotifier.new);
