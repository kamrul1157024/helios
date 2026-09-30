import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/channel.dart';
import '../services/daemon_api_service.dart';
import 'daemon_providers.dart';

/// The page shown on open, and the size of every page fetched after.
const channelPageSize = 20;

/// The most messages held at once. Past this, a page loaded at one end drops a
/// page off the other, so a conversation of any length holds a fixed slice.
const channelMaxHeld = 200;

/// A bounded window over a channel's spine, oldest first.
///
/// A channel opens on the newest page and pages back from there. The window is
/// held to [channelMaxHeld]: scrolling up drops the newest page off the bottom to
/// make room for older, and scrolling back down reloads it.
///
/// See docs/specs/60-group-chat.md.
class ChannelWindow {
  final List<ChannelMessage> messages;

  /// Older messages sit above the top of what is held.
  final bool hasOlder;

  /// Newer messages were dropped off the bottom to hold the window's size;
  /// scrolling back down reloads them.
  final bool hasNewer;

  const ChannelWindow({
    this.messages = const [],
    this.hasOlder = false,
    this.hasNewer = false,
  });

  ChannelWindow copyWith({
    List<ChannelMessage>? messages,
    bool? hasOlder,
    bool? hasNewer,
  }) => ChannelWindow(
    messages: messages ?? this.messages,
    hasOlder: hasOlder ?? this.hasOlder,
    hasNewer: hasNewer ?? this.hasNewer,
  );
}

/// The window a newest page opens onto: a short page is the whole start, a full
/// one has more behind it.
ChannelWindow openingWindow(List<ChannelMessage> newest) => ChannelWindow(
  messages: newest,
  hasOlder: newest.length >= channelPageSize,
  hasNewer: false,
);

/// Prepends an older page, recording whether more remain above it. Prepending
/// alone can carry the window past its cap; [trimmedNewest] brings it back.
ChannelWindow withOlder(ChannelWindow held, List<ChannelMessage> older) {
  if (older.isEmpty) return held;
  return held.copyWith(
    messages: [...older, ...held.messages],
    hasOlder: older.length >= channelPageSize,
  );
}

/// How many messages are held over the cap and want dropping.
int overflowOf(ChannelWindow held) {
  final over = held.messages.length - channelMaxHeld;
  return over > 0 ? over : 0;
}

/// Drops the newest [count] messages to hold the window's size, marking they can
/// be pulled back down.
ChannelWindow trimmedNewest(ChannelWindow held, int count) {
  if (count <= 0) return held;
  return held.copyWith(
    messages: held.messages.sublist(0, held.messages.length - count),
    hasNewer: true,
  );
}

/// Appends a newer page and drops the oldest past the cap, recording whether
/// more remain below the new bottom.
ChannelWindow withNewer(ChannelWindow held, List<ChannelMessage> newer) {
  if (newer.isEmpty) return held;
  var combined = [...held.messages, ...newer];
  var hasOlder = held.hasOlder;
  if (combined.length > channelMaxHeld) {
    combined = combined.sublist(combined.length - channelMaxHeld);
    hasOlder = true;
  }
  return ChannelWindow(
    messages: combined,
    hasOlder: hasOlder,
    hasNewer: newer.length >= channelPageSize,
  );
}

/// One channel's spine, paged backwards from the newest message and forwards
/// again, holding a bounded window.
///
/// Fetching and applying a page are split so the screen can hold the reader's
/// place across the two: prepending older is smooth on its own in a reversed
/// list, but the trim that follows removes extent from the bottom, and only the
/// screen — which has the scroll position — can compensate for that. The notifier
/// owns what is held and whether more exists; the screen owns where the eye is.
class ChannelMessagesNotifier
    extends FamilyAsyncNotifier<ChannelWindow, (String, String)> {
  bool _busyOlder = false;
  bool _busyNewer = false;
  bool _gone = false;

  @override
  Future<ChannelWindow> build((String, String) arg) async {
    _gone = false;
    ref.onDispose(() => _gone = true);
    final (hostId, channelId) = arg;
    if (channelId.isEmpty) return const ChannelWindow();
    final service = ref.watch(serviceProvider(hostId));
    if (service == null) return const ChannelWindow();

    // The newest page. Reading it marks the channel read, which is what clears
    // the badge — the one fetch that should, since the reader has now seen the
    // latest. The paging fetches below leave the receipt alone.
    final page = await service.channelMessages(
      channelId,
      limit: channelPageSize,
    );
    return openingWindow(page);
  }

  DaemonAPIService? get _service => ref.read(serviceProvider(arg.$1));
  String get _channelId => arg.$2;

  /// Reads the page older than the top of the window. Empty when there is
  /// nothing more, a fetch is already running, or the service is gone. The
  /// caller applies it with [applyOlder].
  Future<List<ChannelMessage>> fetchOlder() async {
    final held = state.valueOrNull;
    final service = _service;
    if (service == null ||
        held == null ||
        _busyOlder ||
        !held.hasOlder ||
        held.messages.isEmpty) {
      return const [];
    }
    _busyOlder = true;
    try {
      return await service.channelMessages(
        _channelId,
        before: held.messages.first.id,
        limit: channelPageSize,
        markRead: false,
      );
    } catch (_) {
      return const [];
    } finally {
      _busyOlder = false;
    }
  }

  /// Prepends an older page and records whether more remain above it.
  void applyOlder(List<ChannelMessage> older) {
    final held = state.valueOrNull;
    if (held == null || _gone || older.isEmpty) return;
    state = AsyncData(withOlder(held, older));
  }

  /// How many messages are held over the cap and want dropping.
  int get overflow {
    final held = state.valueOrNull;
    return held == null ? 0 : overflowOf(held);
  }

  /// Drops the newest [count] messages to hold the window's size.
  void trimNewest(int count) {
    final held = state.valueOrNull;
    if (held == null || _gone || count <= 0) return;
    state = AsyncData(trimmedNewest(held, count));
  }

  /// Reads the page newer than the bottom of the window. Empty when the bottom
  /// is already the live tail, a fetch is running, or the service is gone. The
  /// caller applies it with [applyNewer].
  Future<List<ChannelMessage>> fetchNewer() async {
    final held = state.valueOrNull;
    final service = _service;
    if (service == null ||
        held == null ||
        _busyNewer ||
        !held.hasNewer ||
        held.messages.isEmpty) {
      return const [];
    }
    _busyNewer = true;
    try {
      return await service.channelMessages(
        _channelId,
        after: held.messages.last.id,
        limit: channelPageSize,
        markRead: false,
      );
    } catch (_) {
      return const [];
    } finally {
      _busyNewer = false;
    }
  }

  /// Appends a newer page and drops the oldest to hold the window.
  void applyNewer(List<ChannelMessage> newer) {
    final held = state.valueOrNull;
    if (held == null || _gone || newer.isEmpty) return;
    state = AsyncData(withNewer(held, newer));
  }
}

/// One channel's spine, as a bounded window. Reading the newest page marks it
/// read, so this is asked for when the conversation opens.
final channelMessagesProvider =
    AsyncNotifierProvider.family<
      ChannelMessagesNotifier,
      ChannelWindow,
      (String, String)
    >(ChannelMessagesNotifier.new);
