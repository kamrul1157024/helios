import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'cache_effects.dart';
import 'daemon_providers.dart';
import 'desktop_providers.dart';
import 'grouping_providers.dart';

/// Subscribes the cache to the daemon's event stream.
///
/// Watched once at the top of the app, and nothing reads its value — it exists
/// for the subscription. Every host's events arrive on one stream, so a host
/// paired later is covered without re-wiring.
///
/// Invalidation is per host and per key, so a host nobody is looking at simply
/// has no listener and refetches nothing until a screen asks. That is why there
/// is no active-host condition anywhere in this path.
final cacheInvalidatorProvider = Provider<void>((ref) {
  final subscription = ref.watch(hostManagerProvider).sseEvents.listen((e) {
    applyCacheEffects(ref, effectsFor(e.hostId, e.event.type, e.event.data));
  });
  ref.onDispose(subscription.cancel);
});

/// Turns the pure effect list into invalidations against the container.
///
/// Kept apart from `effectsFor` so the mapping stays assertable without a
/// container: this half is the boring half, and it is the only place that
/// knows both what an event means and which family holds it.
void applyCacheEffects(Ref ref, List<CacheEffect> effects) {
  for (final effect in effects) {
    switch (effect) {
      case PatchSession():
        _patchSession(ref, effect);
      case InvalidateTarget():
        _invalidate(ref, effect);
      case InvalidateFiles():
        _invalidateFiles(ref, effect);
      case SetDesktopState():
        // Only a host somebody has asked about. Reading the notifier of one
        // nobody has would start a fetch just to throw its answer away.
        if (ref.exists(desktopProvider(effect.hostId))) {
          ref.read(desktopProvider(effect.hostId).notifier).apply(effect.body);
        }
    }
  }
}

void _invalidate(Ref ref, InvalidateTarget effect) {
  final host = effect.hostId;
  switch (effect.target) {
    case CacheTarget.sessions:
      // Every filter variant, not just the unfiltered one: the search results
      // moved too, and naming a single argument would miss whatever the user
      // has since typed. Invalidating the family covers all of them.
      ref.invalidate(sessionsProvider);
      // A group made, renamed or deleted anywhere announces itself as
      // `session_updated` and nothing else (internal/server/groups.go), so the
      // catalogue rides along with the list. Without this, a group made on the
      // desktop reaches the phone as a session whose key names nothing.
      ref.invalidate(groupsProvider(host));
    case CacheTarget.notifications:
      ref.invalidate(notificationsProvider(host));
    case CacheTarget.schedules:
      ref.invalidate(schedulesProvider(host));
    case CacheTarget.channels:
      ref.invalidate(channelsProvider(host));
      // The families are keyed by channel and by thread, and the event names
      // neither in a form worth threading through — the lists are short and a
      // refetch is one request.
      ref.invalidate(channelMessagesProvider);
      ref.invalidate(channelThreadProvider);
    case CacheTarget.providers:
      ref.invalidate(providersProvider(host));
      ref.invalidate(readyProvidersProvider(host));
    case CacheTarget.settings:
      ref.invalidate(hostSettingsProvider(host));
    case CacheTarget.directories:
      ref.invalidate(directoriesProvider(host));
    case CacheTarget.subagents:
      final id = effect.sessionId;
      if (id != null) ref.invalidate(subagentsProvider((host, id)));
    case CacheTarget.transcript:
      break;
    case CacheTarget.git:
      // The whole family: a commit moves status, log, diff and worktrees
      // together, and `file_changed` names a working-tree path rather than the
      // cwd these are keyed by.
      ref.invalidate(gitStatusProvider);
      ref.invalidate(gitLogProvider);
      ref.invalidate(gitDiffProvider);
      ref.invalidate(gitChangesProvider);
      ref.invalidate(gitWorktreesProvider);
    case CacheTarget.files:
      // The whole family, which only a reconnect asks for: the gap could have
      // hidden any change. A `file_changed` event arrives as
      // [InvalidateFiles] instead and takes out only what it names.
      ref.invalidate(listFilesProvider);
      // Content too, which the desktop cannot do: this client has no PUT at
      // all (`api_client.dart`), so no buffer here can be dirty and there is
      // nothing to lose. An editor on this side would have to take the
      // desktop's rule instead — refetch, compare, and leave a dirty buffer
      // alone. See docs/specs/54-file-change-events.md.
      ref.invalidate(readFileProvider);
    case CacheTarget.touched:
      final id = effect.sessionId;
      if (id != null) ref.invalidate(touchedProvider((host, id)));
    case CacheTarget.desktop:
      ref.invalidate(desktopProvider(host));
  }
}

/// The folders and files one `file_changed` named.
///
/// Riverpod refetches only the entries something watches, so a file is read
/// again only while a viewer shows it, and that read carries the ETag
/// (`readFileProvider`). An entry nothing watches is marked stale and read on
/// the next visit, which is the desktop's `refetchType: 'none'`.
void _invalidateFiles(Ref ref, InvalidateFiles effect) {
  for (final dir in effect.listings) {
    ref.invalidate(listFilesProvider((effect.hostId, dir)));
  }
  for (final file in effect.files) {
    ref.invalidate(readFileProvider((effect.hostId, file)));
  }
}

/// Paints one row from the event's own payload.
///
/// Only the unfiltered list is patched. A filtered list may no longer match the
/// row at all once its status changes — a search for active sessions should
/// drop one that just went idle — and deciding that here would be
/// re-implementing the daemon's filter on the client. The invalidation that
/// follows re-reads those properly.
void _patchSession(Ref ref, PatchSession effect) {
  ref
      .read(sessionsProvider(allSessionsKey(effect.hostId)).notifier)
      .patchRow(effect.sessionId, effect.status, effect.terminal);
}
