import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'cache_effects.dart';
import 'daemon_providers.dart';

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
    case CacheTarget.notifications:
      ref.invalidate(notificationsProvider(host));
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
      // No git event names a path, and a commit moves status, log, diff and
      // worktrees together, so the whole family goes.
      ref.invalidate(gitStatusProvider);
      ref.invalidate(gitLogProvider);
      ref.invalidate(gitDiffProvider);
      ref.invalidate(gitChangesProvider);
      ref.invalidate(gitWorktreesProvider);
    case CacheTarget.files:
      ref.invalidate(listFilesProvider);
      // readFile is deliberately left alone: a viewer may hold an edited
      // buffer, and dropping the entry under it would mark a dirty file clean.
      break;
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
