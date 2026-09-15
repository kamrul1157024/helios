import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/session_group.dart';
import '../services/daemon_api_service.dart';
import '../utils/grouping.dart';
import 'daemon_providers.dart';

/// The groups a host holds, and the choices this device makes about them.
///
/// The catalogue comes from the daemon so every client agrees on it. How the
/// list is arranged — grouped or flat, by directory or by hand, and which
/// headers are closed — stays here: the desktop keeps the same choices in
/// `localStorage`, and a phone that reached across and folded somebody's
/// sidebar would be a surprise.

/// A host's groups. A daemon from before grouping answers 404, which reads as
/// unsupported rather than as a failure: the sessions are fine.
class GroupsNotifier extends FamilyAsyncNotifier<GroupCatalog, String> {
  @override
  Future<GroupCatalog> build(String hostId) async {
    final service = ref.watch(serviceProvider(hostId));
    if (service == null) return GroupCatalog.empty;
    return _read(service);
  }

  Future<GroupCatalog> _read(DaemonAPIService service) async {
    try {
      return GroupCatalog(groups: await service.listGroups());
    } on HeliosApiException catch (e) {
      if (e.statusCode == 404) return GroupCatalog.missing;
      rethrow;
    }
  }

  Future<void> _reread() async {
    final service = ref.read(serviceProvider(arg));
    if (service == null) return;
    state = AsyncData(await _read(service));
  }

  /// Makes a group. An empty [parent] makes it a root.
  Future<SessionGroup?> create(String name, {String parent = ''}) async {
    final service = ref.read(serviceProvider(arg));
    if (service == null) return null;
    final made = await service.createGroup(name, parent: parent);
    if (made != null) await _reread();
    return made;
  }

  Future<bool> rename(String key, String name) async {
    final service = ref.read(serviceProvider(arg));
    if (service == null) return false;
    if (!await service.patchGroup(key, name: name)) return false;
    await _reread();
    return true;
  }

  /// Moves a group under another, or to the root with an empty [parent].
  ///
  /// The sessions move with it, so their list is dropped too: each one carries
  /// the positions of the groups above it, and those have just changed.
  Future<bool> move(String key, String parent) async {
    final service = ref.read(serviceProvider(arg));
    if (service == null) return false;
    if (!await service.patchGroup(key, parent: parent)) return false;
    await _reread();
    ref.invalidate(sessionsProvider);
    return true;
  }

  /// Deletes a group. Its subgroups and sessions come up a level, which is the
  /// daemon's doing — the list is re-read rather than guessed at.
  Future<bool> delete(String key) async {
    final service = ref.read(serviceProvider(arg));
    if (service == null) return false;
    if (!await service.deleteGroup(key)) return false;
    await _reread();
    ref.invalidate(sessionsProvider);
    return true;
  }

  /// Arranges one parent's children, painting the new order first so a move
  /// lands without waiting for the round trip.
  Future<bool> reorder(String parent, List<String> keys) async {
    final service = ref.read(serviceProvider(arg));
    final previous = state.valueOrNull;
    if (service == null || previous == null) return false;

    final positions = {for (var i = 0; i < keys.length; i++) keys[i]: i};
    state = AsyncData(
      GroupCatalog(
        groups: [
          for (final g in previous.groups)
            if (positions.containsKey(g.key))
              SessionGroup(
                key: g.key,
                name: g.name,
                parent: g.parent,
                position: positions[g.key]!,
              )
            else
              g,
        ],
        unsupported: previous.unsupported,
      ),
    );

    if (await service.setGroupOrder(parent, keys)) {
      ref.invalidate(sessionsProvider);
      return true;
    }
    state = AsyncData(previous);
    return false;
  }
}

final groupsProvider =
    AsyncNotifierProvider.family<GroupsNotifier, GroupCatalog, String>(
      GroupsNotifier.new,
    );

/// This device's choices about the arrangement.
class GroupingPrefs {
  final GroupMode mode;

  /// Only read in [GroupMode.auto]: a made group sits where it was put.
  final GroupOrder order;

  /// Where each directory has been moved to, by path.
  final PathOrder dirOrder;

  /// The headers that are closed, as `"$hostId:$path"`.
  final Set<String> folded;

  const GroupingPrefs({
    this.mode = GroupMode.off,
    this.order = GroupOrder.activity,
    this.dirOrder = const {},
    this.folded = const {},
  });

  GroupingPrefs copyWith({
    GroupMode? mode,
    GroupOrder? order,
    PathOrder? dirOrder,
    Set<String>? folded,
  }) => GroupingPrefs(
    mode: mode ?? this.mode,
    order: order ?? this.order,
    dirOrder: dirOrder ?? this.dirOrder,
    folded: folded ?? this.folded,
  );

  bool isFolded(String hostId, List<String> path) =>
      folded.contains(foldKey(hostId, path));

  /// The same key the desktop computes, so the two read the same tree the same
  /// way: a group can be folded at one place in the tree and open at another.
  static String foldKey(String hostId, List<String> path) =>
      '$hostId:${path.join('/')}';
}

/// Written straight to `shared_preferences` under the names the desktop uses.
class GroupingPrefsNotifier extends AsyncNotifier<GroupingPrefs> {
  static const _modeKey = 'helios.grouping';
  static const _orderKey = 'helios.groupOrder';
  static const _dirOrderKey = 'helios.dirOrder';
  static const _foldedKey = 'helios.foldedGroups';

  @override
  Future<GroupingPrefs> build() async {
    final store = await SharedPreferences.getInstance();
    return GroupingPrefs(
      mode: GroupMode.values.firstWhere(
        (m) => m.name == store.getString(_modeKey),
        orElse: () => GroupMode.off,
      ),
      order: GroupOrder.values.firstWhere(
        (o) => o.name == store.getString(_orderKey),
        orElse: () => GroupOrder.activity,
      ),
      dirOrder: _readDirOrder(store.getString(_dirOrderKey)),
      folded: (store.getStringList(_foldedKey) ?? const []).toSet(),
    );
  }

  static PathOrder _readDirOrder(String? raw) {
    if (raw == null || raw.isEmpty) return const {};
    final decoded = jsonDecode(raw);
    if (decoded is! Map) return const {};
    return {
      for (final entry in decoded.entries)
        if (entry.value is num) '${entry.key}': (entry.value as num).toInt(),
    };
  }

  Future<void> _put(
    GroupingPrefs next,
    Future<void> Function(SharedPreferences) write,
  ) async {
    state = AsyncData(next);
    await write(await SharedPreferences.getInstance());
  }

  Future<void> setMode(GroupMode mode) async {
    final held = state.valueOrNull ?? const GroupingPrefs();
    await _put(
      held.copyWith(mode: mode),
      (store) => store.setString(_modeKey, mode.name),
    );
  }

  Future<void> setOrder(GroupOrder order) async {
    final held = state.valueOrNull ?? const GroupingPrefs();
    await _put(
      held.copyWith(order: order),
      (store) => store.setString(_orderKey, order.name),
    );
  }

  /// Records where the directories have been put, which is also the only way
  /// [GroupOrder.manual] is ever chosen: a drag is the choice.
  Future<void> setDirOrder(List<String> paths) async {
    final held = state.valueOrNull ?? const GroupingPrefs();
    final placed = {for (var i = 0; i < paths.length; i++) paths[i]: i};
    await _put(held.copyWith(dirOrder: placed, order: GroupOrder.manual), (
      store,
    ) async {
      await store.setString(_dirOrderKey, jsonEncode(placed));
      await store.setString(_orderKey, GroupOrder.manual.name);
    });
  }

  Future<void> toggleFold(String hostId, List<String> path) async {
    final held = state.valueOrNull ?? const GroupingPrefs();
    final key = GroupingPrefs.foldKey(hostId, path);
    final next = {...held.folded};
    if (!next.remove(key)) next.add(key);
    await _put(
      held.copyWith(folded: next),
      (store) => store.setStringList(_foldedKey, next.toList()),
    );
  }
}

final groupingPrefsProvider =
    AsyncNotifierProvider<GroupingPrefsNotifier, GroupingPrefs>(
      GroupingPrefsNotifier.new,
    );

/// The arrangement as the list reads it, with the preferences still loading
/// counting as off — a tree that appears a frame later is worse than one that
/// appears with the rest of the screen.
final groupingProvider = Provider<GroupingPrefs>(
  (ref) =>
      ref.watch(groupingPrefsProvider).valueOrNull ?? const GroupingPrefs(),
);
