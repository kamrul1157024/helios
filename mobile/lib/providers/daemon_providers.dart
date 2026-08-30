import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'cache_effects.dart';
import '../models/notification.dart';
import '../models/provider.dart';
import '../models/session.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';

/// Every keyed read the app makes, one family per resource.
///
/// Keys carry the host id first, because two daemons hold the same paths and
/// session ids and must not answer for each other. A family given an empty
/// argument answers empty rather than calling the daemon — the mobile
/// equivalent of a query that is not enabled yet.

/// The bridge between the `provider` tree and the Riverpod one.
///
/// `HostManager` owns the per-host `DaemonAPIService` instances and stays a
/// `ChangeNotifier`, but a Riverpod provider body has no `BuildContext` to read
/// it from. So the single instance built in `main` is handed to both trees, and
/// this is overridden at the `ProviderScope` that wraps the app.
final hostManagerProvider = Provider<HostManager>(
  (ref) => throw UnimplementedError('override hostManagerProvider in ProviderScope'),
);

final serviceProvider = Provider.family<DaemonAPIService?, String>(
  (ref, hostId) => ref.watch(hostManagerProvider).serviceFor(hostId),
);

// ─── Sessions and notifications ─────────────────────────────────────────────

/// A host's session list under one set of filters.
///
/// An `AsyncNotifier` rather than a `FutureProvider` for one reason: a
/// `session_status` event carries the new status in its payload, and painting
/// that row immediately is what stops the list flickering between the event
/// and the refetch landing. A `FutureProvider`'s state cannot be written.
///
/// Not auto-disposed: the dashboard counts sessions across every host, so a
/// list has to outlive the screen that first asked for it.
class SessionsNotifier
    extends FamilyAsyncNotifier<List<Session>, (String, SessionQuery)> {
  @override
  Future<List<Session>> build((String, SessionQuery) arg) async {
    final (hostId, query) = arg;
    final service = ref.watch(serviceProvider(hostId));
    if (service == null) return const [];
    return service.listSessions(query);
  }

  /// Applies a status the server just announced, without a round trip.
  ///
  /// A no-op when the list has not loaded yet, or when it does not hold the
  /// row — a filtered list legitimately does not contain every session.
  void patchRow(String sessionId, String status, String? terminal) {
    final held = state.valueOrNull;
    if (held == null) return;
    state = AsyncData(patchSessionRow(held, sessionId, status, terminal));
  }
}

final sessionsProvider = AsyncNotifierProvider.family<SessionsNotifier,
    List<Session>, (String, SessionQuery)>(SessionsNotifier.new);

/// The unfiltered list — what most screens want, and the one the disk cache
/// seeds.
///
/// An alias, not a second provider: it resolves to the same family entry, so
/// the whole list is never held twice.
AsyncNotifierProviderFamily<SessionsNotifier, List<Session>,
        (String, SessionQuery)>
    get allSessions => sessionsProvider;

/// The key for a host's unfiltered list.
(String, SessionQuery) allSessionsKey(String hostId) =>
    (hostId, SessionQuery.all);

final notificationsProvider =
    FutureProvider.family<List<HeliosNotification>, String>((ref, hostId) async {
  final service = ref.watch(serviceProvider(hostId));
  if (service == null) return const [];
  return service.listNotifications();
});

// ─── Providers, models, directories, settings ───────────────────────────────

final providersProvider =
    FutureProvider.family<List<ProviderInfo>, String>((ref, hostId) async {
  final service = ref.watch(serviceProvider(hostId));
  if (service == null) return const [];
  return service.listProviders();
});

/// Providers a session can actually be started with.
///
/// An unready agent — not installed, or hooks missing — produces a session that
/// runs and is never heard from, which reads as a hang.
final readyProvidersProvider =
    FutureProvider.family<List<ProviderInfo>, String>((ref, hostId) async {
  final all = await ref.watch(providersProvider(hostId).future);
  return all.where((p) => p.ready).toList(growable: false);
});

final modelsProvider =
    FutureProvider.family<List<ModelInfo>, (String, String)>((ref, key) async {
  final (hostId, providerId) = key;
  if (providerId.isEmpty) return const [];
  final service = ref.watch(serviceProvider(hostId));
  if (service == null) return const [];
  return service.fetchModels(providerId);
});

final directoriesProvider =
    FutureProvider.family<List<DirectoryInfo>, String>((ref, hostId) async {
  final service = ref.watch(serviceProvider(hostId));
  if (service == null) return const [];
  return service.fetchDirectories();
});

/// A host's settings, and the writes against them.
///
/// The writes paint before the daemon answers and put the old value back when
/// it refuses — which is what the service's `_writeHostSetting` did, moved
/// rather than reinvented. `copyWith` is what keeps them merging by field:
/// several screens own disjoint parts of this document and must not blank each
/// other's.
class HostSettingsNotifier extends FamilyAsyncNotifier<HostSettings, String> {
  @override
  Future<HostSettings> build(String hostId) async {
    final service = ref.watch(serviceProvider(hostId));
    if (service == null) return const HostSettings();
    return service.loadHostSettings();
  }

  Future<bool> _write(
    String key,
    String value,
    HostSettings Function(HostSettings) apply,
  ) async {
    final service = ref.read(serviceProvider(arg));
    final previous = state.valueOrNull;
    if (service == null || previous == null) return false;

    state = AsyncData(apply(previous));
    if (await service.updateSettings({key: value})) return true;
    state = AsyncData(previous);
    return false;
  }

  Future<bool> setAutoTitleEnabled(bool value) => _write(
        DaemonAPIService.settingAutoTitle,
        value ? 'true' : 'false',
        (s) => s.copyWith(autoTitleEnabled: value),
      );

  Future<bool> setAutoTitleEmoji(bool value) => _write(
        DaemonAPIService.settingAutoTitleEmoji,
        value ? 'true' : 'false',
        (s) => s.copyWith(autoTitleEmoji: value),
      );

  Future<bool> setEvictEnabled(bool value) => _write(
        DaemonAPIService.settingEvict,
        value ? 'true' : 'false',
        (s) => s.copyWith(evictEnabled: value),
      );

  Future<bool> setBudgetFraction(double value) {
    final clamped = value.clamp(
      DaemonAPIService.budgetMin,
      DaemonAPIService.budgetMax,
    );
    return _write(
      DaemonAPIService.settingBudgetFraction,
      clamped.toStringAsFixed(2),
      (s) => s.copyWith(budgetFraction: clamped),
    );
  }

  Future<bool> setManualOrder(bool value) => _write(
        DaemonAPIService.settingSortMode,
        value ? 'manual' : 'activity',
        (s) => s.copyWith(manualOrder: value),
      );
}

final hostSettingsProvider = AsyncNotifierProvider.family<HostSettingsNotifier,
    HostSettings, String>(HostSettingsNotifier.new);

/// Whether any host is sorting by hand.
///
/// One switch for every host: the arrangement is stored per daemon, but a list
/// that sorts itself on one host and holds still on another is neither.
final manualOrderProvider = Provider<bool>((ref) {
  final hosts = ref.watch(hostManagerProvider).hosts;
  return hosts.any(
    (h) => ref.watch(hostSettingsProvider(h.id)).valueOrNull?.manualOrder ?? false,
  );
});

// ─── Git ────────────────────────────────────────────────────────────────────

final gitStatusProvider =
    FutureProvider.family<GitStatus?, (String, String)>((ref, key) async {
  final (hostId, cwd) = key;
  if (cwd.isEmpty) return null;
  return ref.watch(serviceProvider(hostId))?.gitStatus(cwd);
});

final gitWorktreesProvider =
    FutureProvider.family<List<Worktree>, (String, String)>((ref, key) async {
  final (hostId, cwd) = key;
  if (cwd.isEmpty) return const [];
  final service = ref.watch(serviceProvider(hostId));
  if (service == null) return const [];
  return service.gitWorktrees(cwd);
});

/// What a log read varies by. Part of the key, because two different logs are
/// two different answers and cannot share an entry.
class GitLogKey {
  final String hostId;
  final String cwd;
  final String? base;
  final bool all;
  final int limit;
  final int skip;

  const GitLogKey(
    this.hostId,
    this.cwd, {
    this.base,
    this.all = false,
    this.limit = 50,
    this.skip = 0,
  });

  @override
  bool operator ==(Object other) =>
      other is GitLogKey &&
      other.hostId == hostId &&
      other.cwd == cwd &&
      other.base == base &&
      other.all == all &&
      other.limit == limit &&
      other.skip == skip;

  @override
  int get hashCode => Object.hash(hostId, cwd, base, all, limit, skip);
}

final gitLogProvider = FutureProvider.family<GitLog?, GitLogKey>((ref, key) async {
  if (key.cwd.isEmpty) return null;
  return ref.watch(serviceProvider(key.hostId))?.gitLog(
        key.cwd,
        base: key.base,
        all: key.all,
        limit: key.limit,
        skip: key.skip,
      );
});

class GitDiffKey {
  final String hostId;
  final String cwd;
  final String file;
  final bool staged;
  final String? from;
  final String? to;
  final bool untracked;

  const GitDiffKey(
    this.hostId,
    this.cwd,
    this.file, {
    this.staged = false,
    this.from,
    this.to,
    this.untracked = false,
  });

  @override
  bool operator ==(Object other) =>
      other is GitDiffKey &&
      other.hostId == hostId &&
      other.cwd == cwd &&
      other.file == file &&
      other.staged == staged &&
      other.from == from &&
      other.to == to &&
      other.untracked == untracked;

  @override
  int get hashCode =>
      Object.hash(hostId, cwd, file, staged, from, to, untracked);
}

final gitDiffProvider = FutureProvider.family<GitDiff?, GitDiffKey>((ref, key) async {
  if (key.cwd.isEmpty || key.file.isEmpty) return null;
  return ref.watch(serviceProvider(key.hostId))?.gitDiff(
        key.cwd,
        key.file,
        staged: key.staged,
        from: key.from,
        to: key.to,
        untracked: key.untracked,
      );
});

class GitChangesKey {
  final String hostId;
  final String cwd;
  final String to;
  final String? from;

  const GitChangesKey(this.hostId, this.cwd, this.to, {this.from});

  @override
  bool operator ==(Object other) =>
      other is GitChangesKey &&
      other.hostId == hostId &&
      other.cwd == cwd &&
      other.to == to &&
      other.from == from;

  @override
  int get hashCode => Object.hash(hostId, cwd, to, from);
}

final gitChangesProvider =
    FutureProvider.family<GitChanges?, GitChangesKey>((ref, key) async {
  if (key.cwd.isEmpty || key.to.isEmpty) return null;
  return ref
      .watch(serviceProvider(key.hostId))
      ?.gitChanges(key.cwd, key.to, from: key.from);
});

// ─── Files ──────────────────────────────────────────────────────────────────

final listFilesProvider =
    FutureProvider.family<FileListing?, (String, String)>((ref, key) async {
  final (hostId, path) = key;
  if (path.isEmpty) return null;
  return ref.watch(serviceProvider(hostId))?.listFiles(path);
});

/// A file's contents.
///
/// Never refreshed on its own. A viewer copies this into a buffer and compares
/// against it to decide whether the file is dirty, so a background refetch
/// would move the thing the comparison is against. Freshness comes from an
/// explicit reload.
final readFileProvider =
    FutureProvider.family<FileReadResult?, (String, String)>((ref, key) async {
  final (hostId, path) = key;
  if (path.isEmpty) return null;
  return ref.watch(serviceProvider(hostId))?.readFile(path);
});

// ─── Subagents ──────────────────────────────────────────────────────────────

final subagentsProvider =
    FutureProvider.family<List<Subagent>, (String, String)>((ref, key) async {
  final (hostId, sessionId) = key;
  if (sessionId.isEmpty) return const [];
  final service = ref.watch(serviceProvider(hostId));
  if (service == null) return const [];
  return service.fetchSubagents(sessionId);
});
