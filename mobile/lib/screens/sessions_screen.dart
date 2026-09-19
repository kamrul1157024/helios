import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
// Both packages export Provider, ChangeNotifierProvider and Consumer.
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../models/session_group.dart';
import '../providers/daemon_providers.dart';
import '../providers/grouping_providers.dart';
import '../providers/theme_provider.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';
import '../utils/grouping.dart';
import '../widgets/group_header.dart';
import '../widgets/group_picker_sheet.dart';
import '../widgets/provider_mark.dart';
import '../widgets/skeleton.dart';
import 'session_detail_screen.dart';

enum SessionFilter { all, pinned, terminated }

class SessionsScreen extends rp.ConsumerStatefulWidget {
  const SessionsScreen({super.key});

  @override
  rp.ConsumerState<SessionsScreen> createState() => _SessionsScreenState();
}

class _SessionsScreenState extends rp.ConsumerState<SessionsScreen> {
  SessionFilter _filter = SessionFilter.all;
  final Set<String> _selected = {};
  bool _multiSelect = false;
  bool _searchExpanded = false;
  final _searchController = TextEditingController();
  final _searchFocusNode = FocusNode();
  Timer? _debounce;
  String? _cwdFilter;
  String? _cwdFilterProject;

  @override
  void dispose() {
    _searchController.dispose();
    _searchFocusNode.dispose();
    _debounce?.cancel();
    super.dispose();
  }

  String _compositeKey(Session s) => '${s.hostId}:${s.sessionId}';

  /// A search or a directory filter is a request for particular sessions, and
  /// answering it while holding some of them back would be a lie. The
  /// Terminated tab is left alone too: showing what has ended is its whole job.
  ///
  /// Off by default everywhere else: a machine that has run a few hundred
  /// agents is mostly finished ones, and they are history rather than work.
  bool get _hidingTerminated =>
      _cwdFilter == null &&
      _filter != SessionFilter.terminated &&
      !(_searchExpanded && _searchController.text.trim().isNotEmpty);

  List<Session> _filterSessions(List<Session> sessions) {
    // When search or CWD filter is active, API already filtered — pass through.
    if (_searchExpanded && _searchController.text.trim().isNotEmpty ||
        _cwdFilter != null) {
      return sessions;
    }
    switch (_filter) {
      case SessionFilter.all:
        return sessions;
      case SessionFilter.pinned:
        return sessions.where((s) => s.pinned).toList();
      case SessionFilter.terminated:
        return sessions.where((s) => s.isTerminated).toList();
    }
  }

  int _statusOrder(Session s) {
    if (s.isActive) return 0;
    if (s.isIdle) return 1;
    if (s.pinned) return 2;
    return 3;
  }

  List<Session> _sortSessions(
    List<Session> sessions, {
    bool manual = false,
    bool grouped = false,
  }) {
    // Inside a tree, a session's place is the positions of the groups above it
    // with its own order last. Comparing only `sortOrder` would arrange the
    // whole host as one list and then hang it on a tree that disagrees.
    final depth = grouped ? depthOf(sessions) : 0;
    sessions.sort((a, b) {
      if (manual) {
        if (grouped) {
          final rankCmp = byRank(rankOf(a, depth), rankOf(b, depth));
          if (rankCmp != 0) return rankCmp;
          return b.createdAt.compareTo(a.createdAt);
        }
        final handCmp = a.sortOrder.compareTo(b.sortOrder);
        if (handCmp != 0) return handCmp;
        return b.createdAt.compareTo(a.createdAt);
      }
      final orderCmp = _statusOrder(a).compareTo(_statusOrder(b));
      if (orderCmp != 0) return orderCmp;
      final aTime = a.lastEventAt ?? a.createdAt;
      final bTime = b.lastEventAt ?? b.createdAt;
      return bTime.compareTo(aTime);
    });
    return sessions;
  }

  /// Dragging needs one host in view: the arrangement lives per host, and a
  /// list mixing two of them has no order either daemon could be told about.
  DaemonAPIService? _orderableService(HostManager hm) {
    if (hm.activeHostId == null) return null;
    if (_cwdFilter != null) return null;
    if (_searchExpanded && _searchController.text.trim().isNotEmpty)
      return null;
    return hm.serviceFor(hm.activeHostId!);
  }

  /// Flips every host between sorting itself and holding still.
  ///
  /// Switching to manual freezes what is on screen as the starting
  /// arrangement, so the list does not jump the moment it stops sorting.
  Future<void> _toggleManualOrder(HostManager hm, List<Session> visible) async {
    final byHost = <String, List<String>>{};
    for (final session in visible) {
      byHost.putIfAbsent(session.hostId, () => []).add(session.sessionId);
    }
    final manual = !ref.read(manualOrderProvider);
    await Future.wait(
      hm.hosts.map((host) async {
        final order = byHost[host.id] ?? const <String>[];
        if (manual && order.isNotEmpty) {
          await ref
              .read(sessionsProvider(allSessionsKey(host.id)).notifier)
              .reorder(order);
        }
        await ref
            .read(hostSettingsProvider(host.id).notifier)
            .setManualOrder(manual);
      }),
    );
  }

  Future<void> _onReorder(
    DaemonAPIService service,
    List<Session> visible,
    int from,
    int to,
  ) async {
    final ids = visible.map((s) => s.sessionId).toList();
    if (to > from) to -= 1;
    ids.insert(to, ids.removeAt(from));
    await ref
        .read(sessionsProvider(allSessionsKey(service.hostId)).notifier)
        .reorder(ids);
  }

  /// A card moved inside one group.
  ///
  /// The whole tree is posted, not the node: the daemon holds one flat order
  /// per host, so naming only these sessions would renumber them from zero and
  /// scatter every other group around them.
  Future<void> _onNodeReorder(
    DaemonAPIService service,
    List<GroupNode> tree,
    GroupNode node,
    int from,
    int to,
  ) async {
    if (to > from) to -= 1;
    node.sessions.insert(to, node.sessions.removeAt(from));
    await ref
        .read(sessionsProvider(allSessionsKey(service.hostId)).notifier)
        .reorder(flattenSessions(tree).map((s) => s.sessionId).toList());
  }

  String get _filterParam {
    switch (_filter) {
      case SessionFilter.all:
        return 'all';
      case SessionFilter.pinned:
        return 'pinned';
      case SessionFilter.terminated:
        return 'terminated';
    }
  }

  /// What the list is currently asking the daemon for. Also its cache key, so
  /// changing the search re-reads under a different entry rather than
  /// overwriting the unfiltered one.
  SessionQuery get _query {
    final q = _searchController.text.trim();
    return SessionQuery(
      q: q.isNotEmpty ? q : null,
      filter: _filterParam,
      cwd: _cwdFilter,
    );
  }

  /// Debounced so a keystroke does not cost a request, and a setState because
  /// the query is the key: rebuilding under the new one is the fetch.
  void _triggerSearch() {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 300), () {
      if (mounted) setState(() {});
    });
  }

  void _setCwdFilter(String cwd, String project) {
    setState(() {
      _cwdFilter = cwd;
      _cwdFilterProject = project;
    });
    _triggerSearch();
  }

  void _clearCwdFilter() {
    setState(() {
      _cwdFilter = null;
      _cwdFilterProject = null;
    });
    _triggerSearch();
  }

  void _openDirectoryPicker() async {
    final hostId = context.read<HostManager>().activeHostId;
    if (hostId == null) return;

    final dirs = await ref.read(directoriesProvider(hostId).future);
    if (!mounted || dirs.isEmpty) return;

    showModalBottomSheet(
      context: context,
      builder: (ctx) {
        final theme = Theme.of(ctx);
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Text(
                  'Filter by directory',
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              const Divider(height: 1),
              ...dirs.map(
                (d) => ListTile(
                  leading: const Icon(Icons.folder_outlined),
                  title: Text(d.project.isNotEmpty ? d.project : d.shortCwd),
                  subtitle: Text(
                    d.shortCwd,
                    style: TextStyle(
                      fontSize: 11,
                      fontFamily: 'monospace',
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (d.activeCount > 0)
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 6,
                            vertical: 2,
                          ),
                          decoration: BoxDecoration(
                            color: Colors.green.withValues(alpha: 0.12),
                            borderRadius: BorderRadius.circular(4),
                          ),
                          child: Text(
                            '${d.activeCount} active',
                            style: const TextStyle(
                              fontSize: 11,
                              color: Colors.green,
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                        ),
                      const SizedBox(width: 6),
                      Text(
                        '${d.sessionCount}',
                        style: TextStyle(
                          fontSize: 13,
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ),
                  onTap: () {
                    Navigator.pop(ctx);
                    _setCwdFilter(d.cwd, d.project);
                  },
                ),
              ),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  void _exitMultiSelect() {
    setState(() {
      _multiSelect = false;
      _selected.clear();
    });
  }

  void _toggleSelection(Session session) {
    final key = _compositeKey(session);
    setState(() {
      if (_selected.contains(key)) {
        _selected.remove(key);
        if (_selected.isEmpty) _multiSelect = false;
      } else {
        _selected.add(key);
      }
    });
  }

  Future<void> _batchPin(bool pin) async {
    for (final key in _selected.toList()) {
      final parts = key.split(':');
      if (parts.length == 2) {
        ref
            .read(sessionsProvider(allSessionsKey(parts[0])).notifier)
            .patch(parts[1], pinned: pin);
      }
    }
    _exitMultiSelect();
  }

  Future<bool> _confirmTerminate(List<Session> sessions) async {
    if (!needsTerminateConfirm(sessions)) return true;
    final busy = sessions.where((s) => s.isActive).length;
    final many = sessions.length > 1;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(many ? 'Terminate sessions' : 'Terminate session'),
        content: Text(
          many
              ? '$busy of ${sessions.length} are mid-turn. Terminating loses the work in flight. Resume starts them again.'
              : 'The agent is mid-turn. Terminating loses the work in flight. Resume starts it again.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Terminate'),
          ),
        ],
      ),
    );
    return confirmed == true;
  }

  Future<void> _batchTerminate(HostManager hm) async {
    final chosen = (ref.read(visibleSessionsProvider).valueOrNull ?? const [])
        .where((s) => _selected.contains(_compositeKey(s)))
        .toList();
    if (!await _confirmTerminate(chosen)) return;
    for (final session in chosen) {
      hm.serviceFor(session.hostId)?.terminateSession(session.sessionId);
    }
    if (mounted) _exitMultiSelect();
  }

  Future<void> _batchResume(HostManager hm) async {
    for (final key in _selected.toList()) {
      final parts = key.split(':');
      if (parts.length == 2) {
        hm.serviceFor(parts[0])?.resumeSession(parts[1]);
      }
    }
    _exitMultiSelect();
  }

  Future<void> _batchDelete() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete sessions'),
        content: Text(
          'Delete ${_selected.length} session(s)? This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    for (final key in _selected.toList()) {
      final parts = key.split(':');
      if (parts.length == 2) {
        ref
            .read(sessionsProvider(allSessionsKey(parts[0])).notifier)
            .delete(parts[1]);
      }
    }
    _exitMultiSelect();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        final held = ref.watch(visibleSessionsForProvider(_query));
        final sessions = held.valueOrNull ?? const <Session>[];

        if (held.valueOrNull == null) {
          return ListView(
            padding: const EdgeInsets.all(12),
            children: const [
              SessionCardSkeleton(),
              SessionCardSkeleton(),
              SessionCardSkeleton(),
              SessionCardSkeleton(),
            ],
          );
        }

        final isSearchActive =
            _searchExpanded && _searchController.text.trim().isNotEmpty;
        final isFilterActive =
            isSearchActive ||
            _cwdFilter != null ||
            _filter != SessionFilter.all;

        if (sessions.isEmpty && !isFilterActive) {
          return _buildEmptyState();
        }

        final manual = ref.watch(manualOrderProvider);
        final orderable = manual ? _orderableService(hm) : null;
        final grouping = _groupingFor(hm);
        final matching = _filterSessions(sessions);
        final filtered = _sortSessions(
          _hidingTerminated
              ? matching.where((s) => !s.isTerminated).toList()
              : matching,
          manual: manual,
          grouped: grouping != null,
        );
        // The flat list draws families too, so it iterates roots and lets each
        // one bring its own forks. Its reorder posts roots only, which is what
        // the daemon keeps an order of.
        final flatForks = forksByParent(filtered);
        final flatPresent = idsOf(filtered);
        final flatRoots = [
          for (final session in filtered)
            if (!isNestedFork(session, flatPresent)) session,
        ];

        return Column(
          children: [
            if (_multiSelect) _buildMultiSelectBar(hm),
            _buildFilterRow(sessions, hm, filtered),
            if (_cwdFilter != null) _buildActiveFiltersRow(),
            Expanded(
              child: filtered.isEmpty
                  ? _buildEmptyFilterState()
                  : RefreshIndicator(
                      onRefresh: () => hm.activeHostId != null
                          ? ref.refreshHost(hm.activeHostId!)
                          : ref.refreshAllHosts(),
                      child: grouping != null
                          ? _buildGroupedList(
                              grouping,
                              filtered,
                              hm,
                              orderable,
                            )
                          : manual && orderable != null
                          ? ReorderableListView.builder(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 12,
                              ),
                              itemCount: flatRoots.length,
                              onReorder: (from, to) =>
                                  _onReorder(orderable, flatRoots, from, to),
                              // The cards carry their own handle, so the list's
                              // long-press drag would only fight the options
                              // sheet that a long press already opens.
                              buildDefaultDragHandles: false,
                              itemBuilder: (context, index) => _buildFamily(
                                flatRoots[index],
                                hm,
                                flatForks,
                                hm.activeHostId ?? '',
                                reorderIndex: index,
                              ),
                            )
                          : ListView.builder(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 12,
                              ),
                              itemCount: flatRoots.length,
                              itemBuilder: (context, index) => _buildFamily(
                                flatRoots[index],
                                hm,
                                flatForks,
                                hm.activeHostId ?? '',
                              ),
                            ),
                    ),
            ),
          ],
        );
      },
    );
  }

  /// Whether the list is drawn as a tree, and what it needs to draw one.
  ///
  /// Null means flat. Three things force that: the mode is off; more than one
  /// host is in view, and a group key from one daemon means nothing on
  /// another; or the mode is Groups against a daemon that has none.
  _Grouping? _groupingFor(HostManager hm) {
    final prefs = ref.watch(groupingProvider);
    if (prefs.mode == GroupMode.off) return null;
    final hostId = hm.activeHostId;
    if (hostId == null) return null;

    if (prefs.mode == GroupMode.auto) {
      return _Grouping(hostId, prefs, GroupCatalog.empty);
    }
    final catalog =
        ref.watch(groupsProvider(hostId)).valueOrNull ?? GroupCatalog.empty;
    if (catalog.unsupported) return null;
    return _Grouping(hostId, prefs, catalog);
  }

  IconData _groupingIcon(GroupMode mode) => switch (mode) {
    GroupMode.off => Icons.folder_open_outlined,
    GroupMode.manual => Icons.folder_copy,
    GroupMode.auto => Icons.snippet_folder,
  };

  void _openGroupingSheet(HostManager hm, List<Session> visible) {
    final hostId = hm.activeHostId;
    final catalog = hostId == null
        ? GroupCatalog.empty
        : ref.read(groupsProvider(hostId)).valueOrNull ?? GroupCatalog.empty;
    showGroupingSheet(
      context,
      ref,
      unsupported: catalog.unsupported,
      hostName: hostId == null
          ? 'This machine'
          : hm.hostById(hostId)?.label ?? 'This machine',
      manualOrder: ref.read(manualOrderProvider),
      onManualOrder: (manual) {
        if (manual == ref.read(manualOrderProvider)) return;
        _toggleManualOrder(hm, visible);
      },
    );
  }

  /// Whether this host can be filed into at all: the mode has to be Groups,
  /// and the daemon has to hold them.
  bool _canFile(String hostId) {
    if (ref.read(groupingProvider).mode != GroupMode.manual) return false;
    final catalog = ref.read(groupsProvider(hostId)).valueOrNull;
    return catalog != null && !catalog.unsupported;
  }

  GroupCatalog _catalogOf(String hostId) =>
      ref.read(groupsProvider(hostId)).valueOrNull ?? GroupCatalog.empty;

  /// Makes a group and answers with its key, or null if the name was left
  /// empty or the daemon refused.
  Future<String?> _createGroup(String hostId, {String parent = ''}) async {
    final name = await promptForGroupName(
      context,
      title: parent.isEmpty ? 'New group' : 'New subgroup',
      action: 'Create',
    );
    if (name == null || !mounted) return null;
    final made = await ref
        .read(groupsProvider(hostId).notifier)
        .create(name, parent: parent);
    return made?.key;
  }

  Future<void> _fileSession(Session session) async {
    final chosen = await showGroupPicker(
      context,
      catalog: _catalogOf(session.hostId),
      current: session.groupKey,
      rootLabel: kUngroupedName,
      onCreate: () => _createGroup(session.hostId),
    );
    if (chosen == null || !mounted) return;
    await _fileSessionUnder(session, chosen);
  }

  /// Files [session] under [groupKey], or unfiles it when that is empty.
  ///
  /// The ancestry goes with the key because the row sorts by the positions in
  /// it: painting the key alone would drop the session to the end of the tree
  /// until the refetch landed.
  Future<void> _fileSessionUnder(Session session, String groupKey) async {
    if (groupKey == session.groupKey) return;
    await ref
        .read(sessionsProvider(allSessionsKey(session.hostId)).notifier)
        .patch(
          session.sessionId,
          group: groupKey,
          groupPath: _catalogOf(session.hostId).ancestryOf(groupKey),
        );
  }

  /// The grip that drags a session onto a group.
  ///
  /// Its own target rather than a long press on the card: a long press already
  /// opens the options sheet, and two long-press gestures on one widget is a
  /// coin toss. This one drags on contact, and it is only there in the mode
  /// where there is something to drop onto.
  Widget _fileHandle(Session session, ThemeData theme) {
    final icon = Padding(
      padding: const EdgeInsets.only(left: 4),
      child: Icon(
        Icons.drive_file_move_outline,
        size: 20,
        color: theme.colorScheme.onSurfaceVariant,
        semanticLabel: 'Drag onto a group to file',
      ),
    );

    return Draggable<Session>(
      data: session,
      dragAnchorStrategy: pointerDragAnchorStrategy,
      feedback: Material(
        elevation: 4,
        color: theme.colorScheme.surfaceContainerHighest,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Text(
            session.displayTitle,
            style: const TextStyle(fontSize: 13),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ),
      childWhenDragging: Opacity(opacity: 0.3, child: icon),
      child: icon,
    );
  }

  /// The group a node sits in, which is what its siblings share.
  String _parentOf(GroupNode node) =>
      node.path.length > 1 ? node.path[node.path.length - 2] : '';

  Future<void> _nudgeGroup(GroupNode node, String hostId, int by) async {
    final parent = _parentOf(node);
    final keys = _catalogOf(
      hostId,
    ).childrenOf(parent).map((g) => g.key).toList();
    final at = keys.indexOf(node.key);
    final to = at + by;
    if (at < 0 || to < 0 || to >= keys.length) return;
    keys.insert(to, keys.removeAt(at));
    await ref.read(groupsProvider(hostId).notifier).reorder(parent, keys);
  }

  Future<void> _renameGroup(GroupNode node, String hostId) async {
    final name = await promptForGroupName(
      context,
      title: 'Rename group',
      initial: node.name,
    );
    if (name == null || !mounted) return;
    await ref.read(groupsProvider(hostId).notifier).rename(node.key, name);
  }

  Future<void> _moveGroup(GroupNode node, String hostId) async {
    final chosen = await showGroupPicker(
      context,
      catalog: _catalogOf(hostId),
      current: _parentOf(node),
      excludeSubtreeOf: node.key,
      title: 'Move ${node.name} into',
      rootLabel: 'Top level',
      onCreate: () => _createGroup(hostId),
    );
    if (chosen == null || !mounted) return;
    await ref.read(groupsProvider(hostId).notifier).move(node.key, chosen);
  }

  Future<void> _deleteGroup(GroupNode node, String hostId) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Delete ${node.name}'),
        content: const Text(
          'The sessions and subgroups inside it move up a level. Nothing is '
          'deleted but the group itself.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.error,
            ),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await ref.read(groupsProvider(hostId).notifier).delete(node.key);
  }

  /// What a group header offers. Nothing here applies to Ungrouped, which is
  /// synthetic, or to a directory, whose key is a path nobody stored.
  void _showGroupMenu(GroupNode node, String hostId) {
    final siblings = _catalogOf(hostId).childrenOf(_parentOf(node));
    final at = siblings.indexWhere((g) => g.key == node.key);

    showModalBottomSheet(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      node.name,
                      style: Theme.of(ctx).textTheme.titleSmall,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  Text(
                    '${node.total}',
                    style: TextStyle(
                      fontSize: 11,
                      color: Theme.of(ctx).colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            ListTile(
              leading: const Icon(Icons.edit_outlined),
              title: const Text('Rename'),
              onTap: () {
                Navigator.pop(ctx);
                if (mounted) _renameGroup(node, hostId);
              },
            ),
            ListTile(
              leading: const Icon(Icons.create_new_folder_outlined),
              title: const Text('New subgroup'),
              onTap: () {
                Navigator.pop(ctx);
                if (mounted) _createGroup(hostId, parent: node.key);
              },
            ),
            ListTile(
              leading: const Icon(Icons.drive_file_move_outlined),
              title: const Text('Move to…'),
              onTap: () {
                Navigator.pop(ctx);
                if (mounted) _moveGroup(node, hostId);
              },
            ),
            if (at > 0)
              ListTile(
                leading: const Icon(Icons.arrow_upward),
                title: const Text('Move up'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (mounted) _nudgeGroup(node, hostId, -1);
                },
              ),
            if (at >= 0 && at < siblings.length - 1)
              ListTile(
                leading: const Icon(Icons.arrow_downward),
                title: const Text('Move down'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (mounted) _nudgeGroup(node, hostId, 1);
                },
              ),
            ListTile(
              leading: Icon(
                Icons.delete_outline,
                color: Theme.of(ctx).colorScheme.error,
              ),
              title: Text(
                'Delete',
                style: TextStyle(color: Theme.of(ctx).colorScheme.error),
              ),
              onTap: () {
                Navigator.pop(ctx);
                if (mounted) _deleteGroup(node, hostId);
              },
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildGroupedList(
    _Grouping grouping,
    List<Session> visible,
    HostManager hm,
    DaemonAPIService? orderable,
  ) {
    final tree = grouping.prefs.mode == GroupMode.auto
        ? buildCwdTree(visible, grouping.prefs.order, grouping.prefs.dirOrder)
        : buildTree(visible, grouping.catalog.groups);
    // Built once over the whole list rather than per node: a fork is filed
    // with its root, which may be in a different directory from its own.
    final forks = forksByParent(visible);

    return CustomScrollView(
      slivers: [
        for (final node in tree)
          ..._nodeSlivers(node, tree, 0, grouping, hm, orderable, forks),
        // The only way to make the first group: until one exists there is no
        // header to long-press, and the tree is a single Ungrouped bucket.
        if (grouping.prefs.mode == GroupMode.manual)
          SliverToBoxAdapter(
            child: Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                icon: const Icon(Icons.add, size: 18),
                label: const Text('New group'),
                onPressed: () => _createGroup(grouping.hostId),
              ),
            ),
          ),
        const SliverToBoxAdapter(child: SizedBox(height: 24)),
      ],
    );
  }

  /// One node: its header, then the groups inside it, then the sessions that
  /// stop here. Children first, as the desktop draws them — a subgroup is a
  /// heading over its sessions, not a footnote under them.
  ///
  /// A list per node rather than one list of mixed rows. The daemon holds one
  /// flat order per host and a card may only be dropped inside the node it
  /// started in, so giving each node its own index space is what makes that
  /// rule hold without policing a drag across a header.
  List<Widget> _nodeSlivers(
    GroupNode node,
    List<GroupNode> tree,
    int depth,
    _Grouping grouping,
    HostManager hm,
    DaemonAPIService? orderable,
    Map<String, List<Session>> forks,
  ) {
    final folded = grouping.prefs.isFolded(grouping.hostId, node.path);
    // A directory node's key is where the sessions run, and Ungrouped is not a
    // group at all, so neither has anything a menu could change.
    final editable =
        grouping.prefs.mode == GroupMode.manual && !node.isUngrouped;
    Widget header({bool highlighted = false}) => GroupHeader(
      node: node,
      depth: depth,
      folded: folded,
      highlighted: highlighted,
      onTap: () => ref
          .read(groupingPrefsProvider.notifier)
          .toggleFold(grouping.hostId, node.path),
      onMenu: editable ? () => _showGroupMenu(node, grouping.hostId) : null,
    );

    final slivers = <Widget>[
      SliverToBoxAdapter(
        // A directory is where a session runs, so it is not a place anything
        // can be dropped. Ungrouped is: dropping there unfiles the session.
        child: grouping.prefs.mode != GroupMode.manual
            ? header()
            : DragTarget<Session>(
                onWillAcceptWithDetails: (details) =>
                    details.data.hostId == grouping.hostId &&
                    details.data.groupKey != node.key,
                onAcceptWithDetails: (details) =>
                    _fileSessionUnder(details.data, node.key),
                builder: (context, candidate, _) =>
                    header(highlighted: candidate.isNotEmpty),
              ),
      ),
    ];
    if (folded) return slivers;

    for (final child in node.children) {
      slivers.addAll(
        _nodeSlivers(child, tree, depth + 1, grouping, hm, orderable, forks),
      );
    }

    if (node.sessions.isNotEmpty) {
      final padding = EdgeInsets.fromLTRB(12.0 + depth * 8, 0, 12, 0);
      final fileable = grouping.prefs.mode == GroupMode.manual;
      // node.sessions holds roots only; the forks are drawn by the family they
      // belong to, so the list's index space stays one entry per movable thing.
      slivers.add(
        SliverPadding(
          padding: padding,
          sliver: orderable == null
              ? SliverList.builder(
                  itemCount: node.sessions.length,
                  itemBuilder: (context, index) => _buildFamily(
                    node.sessions[index],
                    hm,
                    forks,
                    grouping.hostId,
                    fileable: fileable,
                  ),
                )
              : SliverReorderableList(
                  itemCount: node.sessions.length,
                  onReorder: (from, to) =>
                      _onNodeReorder(orderable, tree, node, from, to),
                  itemBuilder: (context, index) => _buildFamily(
                    node.sessions[index],
                    hm,
                    forks,
                    grouping.hostId,
                    reorderIndex: index,
                    fileable: fileable,
                  ),
                ),
        ),
      );
    }
    return slivers;
  }

  Widget _buildMultiSelectBar(HostManager hm) {
    final theme = Theme.of(context);
    final isTerminatedTab = _filter == SessionFilter.terminated;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      color: theme.colorScheme.surfaceContainerHighest,
      child: Row(
        children: [
          IconButton(
            icon: const Icon(Icons.close),
            onPressed: _exitMultiSelect,
          ),
          Text(
            '${_selected.length} selected',
            style: const TextStyle(fontWeight: FontWeight.w600),
          ),
          const Spacer(),
          if (!isTerminatedTab)
            IconButton(
              icon: const Icon(Icons.push_pin_outlined),
              tooltip: 'Pin',
              onPressed: () => _batchPin(true),
            ),
          IconButton(
            icon: Icon(
              isTerminatedTab
                  ? Icons.play_arrow_outlined
                  : Icons.stop_circle_outlined,
            ),
            tooltip: isTerminatedTab ? 'Resume' : 'Terminate',
            onPressed: () =>
                isTerminatedTab ? _batchResume(hm) : _batchTerminate(hm),
          ),
          IconButton(
            icon: Icon(Icons.delete_outline, color: theme.colorScheme.error),
            tooltip: 'Delete',
            onPressed: _batchDelete,
          ),
        ],
      ),
    );
  }

  Widget _buildFilterRow(
    List<Session> allSessions,
    HostManager hm,
    List<Session> visible,
  ) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: AnimatedCrossFade(
        duration: const Duration(milliseconds: 200),
        crossFadeState: _searchExpanded
            ? CrossFadeState.showSecond
            : CrossFadeState.showFirst,
        firstChild: _buildFilterChips(allSessions, hm, visible),
        secondChild: _buildSearchBar(),
      ),
    );
  }

  Widget _buildFilterChips(
    List<Session> allSessions,
    HostManager hm,
    List<Session> visible,
  ) {
    bool counted(Session s) => !(_hidingTerminated && s.isTerminated);
    final allCount = allSessions.where(counted).length;
    final pinnedCount = allSessions.where((s) => s.pinned && counted(s)).length;
    final terminatedCount = allSessions.where((s) => s.isTerminated).length;
    final theme = Theme.of(context);
    final isFiltered = _filter != SessionFilter.all;
    final filterLabel = switch (_filter) {
      SessionFilter.all => 'All ($allCount)',
      SessionFilter.pinned => 'Pinned ($pinnedCount)',
      SessionFilter.terminated => 'Terminated ($terminatedCount)',
    };

    return Row(
      children: [
        PopupMenuButton<SessionFilter>(
          tooltip: 'Filter sessions',
          onSelected: (value) {
            setState(() {
              _filter = value;
              _exitMultiSelect();
            });
            _triggerSearch();
          },
          itemBuilder: (_) => [
            _filterMenuItem('All', allCount, SessionFilter.all),
            _filterMenuItem('Pinned', pinnedCount, SessionFilter.pinned),
            _filterMenuItem(
              'Terminated',
              terminatedCount,
              SessionFilter.terminated,
            ),
          ],
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(
                  Icons.filter_list,
                  size: 20,
                  color: isFiltered ? theme.colorScheme.primary : null,
                ),
                const SizedBox(width: 6),
                Text(
                  filterLabel,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight: isFiltered ? FontWeight.w600 : FontWeight.normal,
                    color: isFiltered
                        ? theme.colorScheme.primary
                        : theme.colorScheme.onSurface,
                  ),
                ),
              ],
            ),
          ),
        ),
        const Spacer(),
        // One control for both questions, as the desktop has it: grouping and
        // sorting both arrange the list, and two buttons that mean "order"
        // make the reader guess which one they want.
        Builder(
          builder: (context) {
            final mode = ref.watch(groupingProvider).mode;
            final manual = ref.watch(manualOrderProvider);
            final on = mode != GroupMode.off || manual;
            return IconButton(
              icon: Icon(
                _groupingIcon(mode),
                size: 20,
                color: on ? theme.colorScheme.primary : null,
              ),
              tooltip: 'Arrange — grouping, and what the list sorts by',
              visualDensity: VisualDensity.compact,
              onPressed: () => _openGroupingSheet(hm, visible),
            );
          },
        ),
        IconButton(
          icon: const Icon(Icons.folder_outlined, size: 20),
          tooltip: 'Filter by directory',
          visualDensity: VisualDensity.compact,
          onPressed: _openDirectoryPicker,
        ),
        IconButton(
          icon: const Icon(Icons.search, size: 20),
          tooltip: 'Search',
          visualDensity: VisualDensity.compact,
          onPressed: () {
            setState(() => _searchExpanded = true);
            WidgetsBinding.instance.addPostFrameCallback((_) {
              _searchFocusNode.requestFocus();
            });
          },
        ),
      ],
    );
  }

  Widget _buildSearchBar() {
    return Row(
      children: [
        Expanded(
          child: TextField(
            controller: _searchController,
            focusNode: _searchFocusNode,
            onChanged: (_) => _triggerSearch(),
            decoration: InputDecoration(
              hintText: 'Search sessions...',
              prefixIcon: const Icon(Icons.search, size: 20),
              suffixIcon: IconButton(
                icon: const Icon(Icons.close, size: 20),
                onPressed: () {
                  _searchController.clear();
                  setState(() => _searchExpanded = false);
                  _triggerSearch();
                },
              ),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(24),
                borderSide: BorderSide.none,
              ),
              filled: true,
              fillColor: Theme.of(context).colorScheme.surfaceContainerHighest,
              contentPadding: const EdgeInsets.symmetric(vertical: 8),
              isDense: true,
            ),
            style: const TextStyle(fontSize: 14),
          ),
        ),
      ],
    );
  }

  Widget _buildActiveFiltersRow() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 4),
      child: Row(
        children: [
          if (_cwdFilter != null)
            InputChip(
              avatar: const Icon(Icons.folder_outlined, size: 16),
              label: Text(
                _cwdFilterProject ?? _cwdFilter!.split('/').last,
                style: const TextStyle(fontSize: 12),
              ),
              onDeleted: _clearCwdFilter,
              visualDensity: VisualDensity.compact,
            ),
        ],
      ),
    );
  }

  PopupMenuItem<SessionFilter> _filterMenuItem(
    String label,
    int count,
    SessionFilter filter,
  ) {
    final isSelected = _filter == filter;
    return PopupMenuItem<SessionFilter>(
      value: filter,
      child: Row(
        children: [
          if (isSelected)
            Icon(Icons.check, size: 18, color: Theme.of(context).colorScheme.primary)
          else
            const SizedBox(width: 18),
          const SizedBox(width: 8),
          Text(count > 0 ? '$label ($count)' : label),
        ],
      ),
    );
  }

  /// [reorderIndex] is the card's place in a list arranged by hand, and null
  /// when the list sorts itself. The drag handle needs it to say which card is
  /// being moved.
  ///
  /// [fileable] adds the grip that drags the session onto a group.
  /// A root and the forks hanging off it, drawn as one item.
  ///
  /// One reorderable index is one family. The alternative — fork rows as
  /// siblings inside the list — puts undraggable rows into a reorderable index
  /// space, and every `onReorder` then has to subtract them. This is the
  /// arithmetic spec 62 avoided by giving each node its own list, and the same
  /// answer applies here: make the unit of the list the thing that moves.
  Widget _buildFamily(
    Session root,
    HostManager hm,
    Map<String, List<Session>> forks,
    String hostId, {
    int? reorderIndex,
    bool fileable = false,
  }) {
    final prefs = ref.watch(groupingProvider);
    final rows = familyRows(
      root,
      forks,
      (id) => prefs.isForkFolded(hostId, id),
    );
    if (rows.length == 1) {
      return _buildSwipeableCard(
        root,
        hm,
        reorderIndex: reorderIndex,
        fileable: fileable,
      );
    }
    return Column(
      key: ValueKey('family-${root.sessionId}'),
      mainAxisSize: MainAxisSize.min,
      children: [
        for (final row in rows)
          _buildSwipeableCard(
            row.session,
            hm,
            // Only the root carries the drag: the family moves as a unit
            // because it is one item, and a handle on a fork would offer a
            // move the daemon discards.
            reorderIndex: row.depth == 0 ? reorderIndex : null,
            fileable: fileable && row.depth == 0,
            family: row,
          ),
      ],
    );
  }

  Widget _buildSwipeableCard(
    Session session,
    HostManager hm, {
    int? reorderIndex,
    bool fileable = false,
    FamilyRow? family,
  }) {
    final theme = Theme.of(context);
    // Terminated is the archival state: putting a session away is ending it,
    // and the way back out is Resume rather than an unarchive.
    final isTerminated = session.isTerminated;
    final service = hm.serviceFor(session.hostId);

    return Dismissible(
      key: ValueKey(_compositeKey(session)),
      // Square like the card it sits behind: a rounded corner here would show
      // as a sliver of colour outside the square one being swiped away.
      background: Container(
        margin: const EdgeInsets.only(bottom: 8),
        decoration: BoxDecoration(
          color: isTerminated ? Colors.green : Colors.teal,
        ),
        alignment: Alignment.centerLeft,
        padding: const EdgeInsets.only(left: 20),
        child: Row(
          children: [
            Icon(
              isTerminated ? Icons.play_arrow : Icons.stop_circle_outlined,
              color: Colors.white,
            ),
            const SizedBox(width: 8),
            Text(
              isTerminated ? 'Resume' : 'Terminate',
              style: const TextStyle(
                color: Colors.white,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
      secondaryBackground: Container(
        margin: const EdgeInsets.only(bottom: 8),
        decoration: BoxDecoration(color: theme.colorScheme.error),
        alignment: Alignment.centerRight,
        padding: const EdgeInsets.only(right: 20),
        child: const Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            Text(
              'Delete',
              style: TextStyle(
                color: Colors.white,
                fontWeight: FontWeight.w600,
              ),
            ),
            SizedBox(width: 8),
            Icon(Icons.delete, color: Colors.white),
          ],
        ),
      ),
      confirmDismiss: (direction) async {
        if (direction == DismissDirection.startToEnd) {
          if (isTerminated) {
            service?.resumeSession(session.sessionId);
          } else if (await _confirmTerminate([session])) {
            service?.terminateSession(session.sessionId);
          }
          return false;
        } else {
          final confirmed = await showDialog<bool>(
            context: context,
            builder: (ctx) => AlertDialog(
              title: const Text('Delete session'),
              content: const Text(
                'Delete this session? This cannot be undone.',
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx, false),
                  child: const Text('Cancel'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(ctx, true),
                  style: FilledButton.styleFrom(
                    backgroundColor: theme.colorScheme.error,
                  ),
                  child: const Text('Delete'),
                ),
              ],
            ),
          );
          if (confirmed == true) {
            ref
                .read(sessionsProvider(allSessionsKey(session.hostId)).notifier)
                .delete(session.sessionId);
          }
          return false;
        }
      },
      child: _buildSessionCard(
        session,
        hm,
        reorderIndex: reorderIndex,
        fileable: fileable,
        family: family,
      ),
    );
  }

  Widget _buildSessionCard(
    Session session,
    HostManager hm, {
    int? reorderIndex,
    bool fileable = false,
    FamilyRow? family,
  }) {
    final theme = Theme.of(context);
    final statusColor = _statusColor(session.status, theme);
    final statusIcon = _statusIcon(session.status);
    final isSelected = _selected.contains(_compositeKey(session));
    final host = hm.hostById(session.hostId);
    final hostColor = host?.color ?? theme.colorScheme.primary;
    final hostLabel = host?.label ?? '';
    final appearance = context.watch<ThemeProvider>();
    final compact = appearance.compactSessions;
    final titleSize = appearance.titleSize;

    // Square, at either density. A rounded card says "this is one object,
    // lifted off the page", which is true of a four-line card and a lie about a
    // one-line row — a column of them reads as a stack of pills rather than a
    // list. The corner is what carried that reading, so it goes.
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      clipBehavior: Clip.antiAlias,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.zero,
        side: isSelected
            ? BorderSide(color: theme.colorScheme.primary, width: 2)
            : session.isActive
            ? BorderSide(color: statusColor.withValues(alpha: 0.4), width: 1.5)
            : BorderSide.none,
      ),
      child: InkWell(
        onTap: () {
          if (_multiSelect) {
            _toggleSelection(session);
          } else {
            Navigator.of(context).push(
              MaterialPageRoute(
                builder: (_) => SessionDetailScreen(session: session),
              ),
            );
          }
        },
        onLongPress: () {
          HapticFeedback.mediumImpact();
          _showContextMenu(session, hm);
        },
        child: IntrinsicHeight(
          child: Row(
            children: [
              Container(width: 2, color: hostColor.withValues(alpha: 0.4)),
              // The tree's own lines, one column per level of indent. Columns
              // rather than a margin: an indent alone leaves the reader to
              // guess which row above a deep fork belongs to, and the trunk is
              // what answers that. Each column is a line running through, blank
              // where that ancestor's last child has already been drawn, or
              // this row's own elbow.
              if (family != null && family.trunk.isNotEmpty)
                _ForkGuides(
                  trunk: family.trunk,
                  last: family.last,
                  colour: theme.colorScheme.onSurfaceVariant,
                ),
              Expanded(
                child: Padding(
                  padding: compact
                      ? const EdgeInsets.symmetric(horizontal: 12, vertical: 10)
                      : const EdgeInsets.all(12),
                  child: Row(
                    children: [
                      if (_multiSelect) ...[
                        Checkbox(
                          value: isSelected,
                          onChanged: (_) => _toggleSelection(session),
                          visualDensity: VisualDensity.compact,
                        ),
                        const SizedBox(width: 4),
                      ],
                      // The count and the fold in one control, as on the
                      // desktop. A chevron on one side and a count on the other
                      // were two affordances for one action, and on a phone the
                      // chevron alone is too small to be a target.
                      if (session.forkCount > 0) ...[
                        _ForkToggle(
                          count: session.forkCount,
                          folded: ref
                              .watch(groupingProvider)
                              .isForkFolded(session.hostId, session.sessionId),
                          onTap: () => ref
                              .read(groupingPrefsProvider.notifier)
                              .toggleForkFold(
                                session.hostId,
                                session.sessionId,
                              ),
                        ),
                        const SizedBox(width: 6),
                      ],
                      if (compact)
                        Expanded(
                          child: _compactBody(
                            session,
                            statusColor,
                            theme,
                            titleSize,
                          ),
                        )
                      else
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              // Row 1: Status + pin + time
                              Row(
                                children: [
                                  if (session.isActive)
                                    _PulsingIcon(
                                      icon: statusIcon,
                                      color: statusColor,
                                      size: 14,
                                    )
                                  else
                                    Icon(
                                      statusIcon,
                                      size: 14,
                                      color: statusColor,
                                    ),
                                  const SizedBox(width: 6),
                                  Container(
                                    padding: const EdgeInsets.symmetric(
                                      horizontal: 8,
                                      vertical: 2,
                                    ),
                                    decoration: BoxDecoration(
                                      color: statusColor.withValues(
                                        alpha: 0.12,
                                      ),
                                      borderRadius: BorderRadius.circular(4),
                                    ),
                                    child: Text(
                                      _statusLabel(session.status),
                                      style: TextStyle(
                                        fontSize: 11,
                                        color: statusColor,
                                        fontWeight: FontWeight.w600,
                                      ),
                                    ),
                                  ),
                                  if (session.memoryLabel.isNotEmpty) ...[
                                    const SizedBox(width: 6),
                                    Text(
                                      session.memoryLabel,
                                      style: TextStyle(
                                        fontSize: 11,
                                        color:
                                            theme.colorScheme.onSurfaceVariant,
                                      ),
                                    ),
                                  ],
                                  if (session.needsRecovery) ...[
                                    const SizedBox(width: 6),
                                    Tooltip(
                                      message: 'Cold — tap to resume',
                                      child: Icon(
                                        Icons.link_off,
                                        size: 14,
                                        color: Colors.amber.shade700,
                                      ),
                                    ),
                                  ],
                                  if (session.pinned) ...[
                                    const SizedBox(width: 6),
                                    Icon(
                                      Icons.push_pin,
                                      size: 14,
                                      color: theme.colorScheme.primary,
                                    ),
                                  ],
                                  const Spacer(),
                                  Text(
                                    session.timeAgo,
                                    style: TextStyle(
                                      fontSize: 11,
                                      color: theme.colorScheme.onSurfaceVariant,
                                    ),
                                  ),
                                ],
                              ),
                              const SizedBox(height: 8),
                              // Row 2: Title / Prompt
                              Text(
                                session.displayTitle,
                                style: TextStyle(
                                  fontSize: titleSize,
                                  fontWeight: FontWeight.w600,
                                  color: theme.colorScheme.onSurface,
                                ),
                                maxLines: 2,
                                overflow: TextOverflow.ellipsis,
                              ),
                              const SizedBox(height: 6),
                              // Row 3: Workspace
                              Text(
                                session.shortCwd,
                                style: TextStyle(
                                  fontSize: 12,
                                  fontFamily: 'monospace',
                                  color: theme.colorScheme.onSurfaceVariant,
                                ),
                                overflow: TextOverflow.ellipsis,
                              ),
                              const SizedBox(height: 4),
                              // Row 4: Model + host name
                              Row(
                                mainAxisAlignment:
                                    MainAxisAlignment.spaceBetween,
                                children: [
                                  Flexible(
                                    child: Text(
                                      session.model ?? '',
                                      style: TextStyle(
                                        fontSize: 11,
                                        color:
                                            theme.colorScheme.onSurfaceVariant,
                                      ),
                                      overflow: TextOverflow.ellipsis,
                                    ),
                                  ),
                                  Text(
                                    hostLabel,
                                    style: TextStyle(
                                      fontSize: 11,
                                      fontWeight: FontWeight.w600,
                                      color: hostColor,
                                    ),
                                  ),
                                ],
                              ),
                            ],
                          ),
                        ),
                      // A handle rather than the whole card, because a card
                      // already answers a long press by opening its options:
                      // the drag would never win that gesture. Dragging starts
                      // the moment the handle is touched.
                      if (fileable) _fileHandle(session, theme),
                      if (reorderIndex != null)
                        ReorderableDragStartListener(
                          index: reorderIndex,
                          child: Padding(
                            padding: const EdgeInsets.only(left: 4),
                            child: Icon(
                              Icons.drag_handle,
                              size: 22,
                              color: theme.colorScheme.onSurfaceVariant,
                              semanticLabel: 'Drag to reorder',
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// How this session should be branched.
  ///
  /// One field and a button is the whole ordinary path. A worktree of its own
  /// is the default and needs no control: a fork exists to try a second answer
  /// to the same question, and two agents editing one checkout is not a second
  /// answer, it is a race. Sharing the parent's folder is behind a disclosure,
  /// because on the face it invites a shared checkout by accident.
  void _showForkSheet(Session session, HostManager hm) {
    final branch = TextEditingController(text: _suggestBranch(session));
    final prompt = TextEditingController();
    var sameFolder = false;
    var showMore = false;
    var busy = false;
    String? error;

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setSheet) {
          Future<void> submit() async {
            setSheet(() {
              busy = true;
              error = null;
            });
            final service = hm.serviceFor(session.hostId);
            if (service == null) {
              setSheet(() {
                busy = false;
                error = 'That host is not connected.';
              });
              return;
            }
            final result = await service.forkSession(
              session.sessionId,
              workspace: sameFolder ? 'same' : 'worktree',
              branch: sameFolder ? null : branch.text.trim(),
              prompt: prompt.text.trim(),
            );
            if (!ctx.mounted) return;
            if (!result.ok) {
              // Stays open on failure: a 409 means this session has no
              // conversation to fork yet, which the user has to read.
              setSheet(() {
                busy = false;
                error = result.error ?? 'Failed to fork the session';
              });
              return;
            }
            Navigator.pop(ctx);
            if (!mounted) return;
            for (final warning in result.warnings) {
              ScaffoldMessenger.of(
                context,
              ).showSnackBar(SnackBar(content: Text(warning)));
            }
            await ref.refreshHost(session.hostId);
          }

          return Padding(
            padding: EdgeInsets.fromLTRB(
              16,
              16,
              16,
              16 + MediaQuery.of(ctx).viewInsets.bottom,
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  'Fork "${session.displayTitle}"',
                  style: Theme.of(ctx).textTheme.titleSmall,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 4),
                Text(
                  sameFolder
                      ? 'The fork keeps everything said so far and shares this folder.'
                      : 'The fork keeps everything said so far and gets a worktree of its own.',
                  style: TextStyle(
                    fontSize: 12,
                    color: Theme.of(ctx).colorScheme.onSurfaceVariant,
                  ),
                ),
                const SizedBox(height: 12),
                if (!sameFolder)
                  TextField(
                    controller: branch,
                    autofocus: true,
                    decoration: const InputDecoration(
                      labelText: 'Branch',
                      isDense: true,
                      border: OutlineInputBorder(),
                    ),
                  ),
                if (!sameFolder) const SizedBox(height: 8),
                TextField(
                  controller: prompt,
                  minLines: 2,
                  maxLines: 4,
                  decoration: const InputDecoration(
                    labelText: 'First message',
                    hintText: 'Optional — what should the fork try instead?',
                    isDense: true,
                    border: OutlineInputBorder(),
                  ),
                ),
                if (!sameFolder) ...[
                  const SizedBox(height: 8),
                  Text(
                    'The fork starts from the last commit, so anything uncommitted here stays here.',
                    style: TextStyle(
                      fontSize: 11,
                      color: Theme.of(ctx).colorScheme.tertiary,
                    ),
                  ),
                ],
                const SizedBox(height: 4),
                TextButton(
                  onPressed: () => setSheet(() => showMore = !showMore),
                  child: Text(showMore ? 'Fewer options' : 'Other options'),
                ),
                if (showMore)
                  CheckboxListTile(
                    contentPadding: EdgeInsets.zero,
                    controlAffinity: ListTileControlAffinity.leading,
                    value: sameFolder,
                    onChanged: (value) =>
                        setSheet(() => sameFolder = value ?? false),
                    title: const Text(
                      "Work in this session's folder",
                      style: TextStyle(fontSize: 13),
                    ),
                    subtitle: const Text(
                      'Both agents will edit the same files.',
                      style: TextStyle(fontSize: 11),
                    ),
                  ),
                if (error != null) ...[
                  const SizedBox(height: 8),
                  Text(
                    error!,
                    style: TextStyle(
                      fontSize: 12,
                      color: Theme.of(ctx).colorScheme.error,
                    ),
                  ),
                ],
                const SizedBox(height: 12),
                Row(
                  mainAxisAlignment: MainAxisAlignment.end,
                  children: [
                    TextButton(
                      onPressed: busy ? null : () => Navigator.pop(ctx),
                      child: const Text('Cancel'),
                    ),
                    const SizedBox(width: 8),
                    FilledButton(
                      onPressed: busy ? null : submit,
                      child: Text(busy ? 'Forking…' : 'Fork'),
                    ),
                  ],
                ),
              ],
            ),
          );
        },
      ),
    );
  }

  /// The branch name the daemon would pick, so the field is filled rather than
  /// empty. The daemon still has the last word: it walks past a name already
  /// taken, which a client cannot know about.
  String _suggestBranch(Session session) {
    final slug = session.displayTitle
        .toLowerCase()
        .replaceAll(RegExp(r'[^a-z0-9._-]+'), '-')
        .replaceAll(RegExp(r'-{2,}'), '-')
        .replaceAll(RegExp(r'^[-.]+|[-.]+$'), '');
    if (slug.isEmpty) return 'fork';
    final trimmed = slug.length > 40 ? slug.substring(0, 40) : slug;
    return trimmed.replaceAll(RegExp(r'[-.]+$'), '');
  }

  /// The card with everything but the answer to "which one is this?" removed.
  ///
  /// Which agent it runs and how it is doing are glyphs at the head of the
  /// title, in place of the four lines that say them in words. The directory,
  /// the model and the memory are a tap away in the session itself, and this
  /// list is read to pick a session out of. How long ago stays: it costs no
  /// line of its own, and it is the only thing here that says which of these
  /// have gone cold.
  Widget _compactBody(
    Session session,
    Color statusColor,
    ThemeData theme,
    double titleSize,
  ) {
    return Row(
      children: [
        ProviderMark(source: session.source),
        const SizedBox(width: 8),
        if (session.isActive)
          _PulsingIcon(icon: Icons.circle, color: statusColor, size: 8)
        else
          Icon(Icons.circle, size: 8, color: statusColor),
        const SizedBox(width: 8),
        Expanded(
          child: Text(
            session.displayTitle,
            style: TextStyle(
              fontSize: titleSize,
              fontWeight: FontWeight.w600,
              color: theme.colorScheme.onSurface,
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
        const SizedBox(width: 8),
        Text(
          session.timeAgo,
          style: TextStyle(
            fontSize: 11,
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }

  void _showContextMenu(Session session, HostManager hm) {
    final theme = Theme.of(context);
    final isTerminated = session.isTerminated;
    final hostId = session.hostId;
    final sessionId = session.sessionId;
    final service = hm.serviceFor(hostId);

    showModalBottomSheet(
      context: context,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        session.displayTitle,
                        style: theme.textTheme.titleSmall,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    Text(
                      session.shortId,
                      style: TextStyle(
                        fontSize: 11,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
              const Divider(height: 1),
              ListTile(
                leading: const Icon(Icons.check_box_outlined),
                title: const Text('Select'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  setState(() {
                    _multiSelect = true;
                    _selected.add(_compositeKey(session));
                  });
                },
              ),
              ListTile(
                leading: const Icon(Icons.edit_outlined),
                title: const Text('Rename'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  _showRenameDialog(session, hm);
                },
              ),
              // Branching keeps everything said so far, so it sits with the
              // actions that act on the conversation rather than the ones that
              // end it.
              ListTile(
                leading: const Icon(Icons.call_split),
                title: const Text('Fork…'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  _showForkSheet(session, hm);
                },
              ),
              ListTile(
                leading: const Icon(Icons.folder_outlined),
                title: const Text('Filter this directory'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  _setCwdFilter(session.cwd, session.project);
                },
              ),
              // Only against a daemon that holds groups. A directory group is
              // where the session runs, which is not something a menu moves.
              if (_canFile(hostId))
                ListTile(
                  leading: const Icon(Icons.drive_file_move_outlined),
                  title: const Text('Move to group…'),
                  onTap: () {
                    Navigator.pop(ctx);
                    if (!mounted) return;
                    _fileSession(session);
                  },
                ),
              ListTile(
                leading: Icon(
                  session.pinned ? Icons.push_pin : Icons.push_pin_outlined,
                ),
                title: Text(session.pinned ? 'Unpin' : 'Pin'),
                onTap: () {
                  Navigator.pop(ctx);
                  ref
                      .read(
                        sessionsProvider(
                          allSessionsKey(session.hostId),
                        ).notifier,
                      )
                      .patch(sessionId, pinned: !session.pinned);
                },
              ),
              ListTile(
                leading: Icon(
                  isTerminated
                      ? Icons.play_arrow_outlined
                      : Icons.stop_circle_outlined,
                ),
                title: Text(isTerminated ? 'Resume' : 'Terminate'),
                onTap: () async {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  if (isTerminated) {
                    service?.resumeSession(sessionId);
                  } else if (await _confirmTerminate([session])) {
                    service?.terminateSession(sessionId);
                  }
                },
              ),
              ListTile(
                leading: Icon(
                  Icons.delete_outline,
                  color: theme.colorScheme.error,
                ),
                title: Text(
                  'Delete',
                  style: TextStyle(color: theme.colorScheme.error),
                ),
                onTap: () async {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  final confirmed = await showDialog<bool>(
                    context: context,
                    builder: (dCtx) => AlertDialog(
                      title: const Text('Delete session'),
                      content: const Text(
                        'Delete this session? This cannot be undone.',
                      ),
                      actions: [
                        TextButton(
                          onPressed: () => Navigator.pop(dCtx, false),
                          child: const Text('Cancel'),
                        ),
                        FilledButton(
                          onPressed: () => Navigator.pop(dCtx, true),
                          style: FilledButton.styleFrom(
                            backgroundColor: theme.colorScheme.error,
                          ),
                          child: const Text('Delete'),
                        ),
                      ],
                    ),
                  );
                  if (confirmed == true) {
                    ref
                        .read(
                          sessionsProvider(
                            allSessionsKey(session.hostId),
                          ).notifier,
                        )
                        .delete(sessionId);
                  }
                },
              ),
              if (session.canStop ||
                  session.canTerminate ||
                  session.canResume) ...[
                const Divider(height: 1),
                if (session.canStop)
                  ListTile(
                    leading: const Icon(Icons.stop),
                    title: const Text('Stop'),
                    onTap: () {
                      Navigator.pop(ctx);
                      service?.stopSession(session.sessionId);
                    },
                  ),
                if (session.canTerminate)
                  ListTile(
                    leading: const Icon(Icons.close),
                    title: const Text('Terminate'),
                    onTap: () {
                      Navigator.pop(ctx);
                      service?.terminateSession(session.sessionId);
                    },
                  ),
                if (session.canResume)
                  ListTile(
                    leading: const Icon(Icons.play_arrow),
                    title: const Text('Resume'),
                    onTap: () {
                      Navigator.pop(ctx);
                      service?.resumeSession(session.sessionId);
                    },
                  ),
              ],
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  void _showRenameDialog(Session session, HostManager hm) {
    final sessionId = session.sessionId;
    final hostId = session.hostId;

    showDialog<String>(
      context: context,
      builder: (ctx) {
        final controller = TextEditingController(text: session.title ?? '');
        return AlertDialog(
          title: const Text('Rename session'),
          content: TextField(
            controller: controller,
            autofocus: true,
            decoration: InputDecoration(
              hintText: session.lastUserMessage ?? 'Session title',
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
              ),
            ),
            onSubmitted: (value) => Navigator.pop(ctx, value.trim()),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, controller.text.trim()),
              child: const Text('Save'),
            ),
          ],
        );
      },
    ).then((title) {
      if (title != null && title.isNotEmpty) {
        ref
            .read(sessionsProvider(allSessionsKey(hostId)).notifier)
            .patch(sessionId, title: title);
      }
    });
  }

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            Icons.terminal,
            size: 48,
            color: Theme.of(
              context,
            ).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
          ),
          const SizedBox(height: 16),
          Text(
            'No sessions yet.',
            style: Theme.of(context).textTheme.bodyLarge?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            'Start a Claude session:\nhelios new "your prompt"',
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
              color: Theme.of(
                context,
              ).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
              fontFamily: 'monospace',
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyFilterState() {
    final isSearchActive =
        _searchExpanded && _searchController.text.trim().isNotEmpty;

    final String label;
    final String hint;
    final IconData icon;

    if (isSearchActive) {
      label = 'No matching sessions.';
      hint = 'Try a different search term.';
      icon = Icons.search_off;
    } else if (_cwdFilter != null) {
      label = 'No sessions in this directory.';
      hint = '';
      icon = Icons.folder_off_outlined;
    } else {
      label = switch (_filter) {
        SessionFilter.pinned => 'No pinned sessions.',
        SessionFilter.terminated => 'No terminated sessions.',
        SessionFilter.all => 'No sessions.',
      };
      hint = switch (_filter) {
        SessionFilter.pinned => 'Long-press a session to pin it.',
        SessionFilter.terminated => 'Swipe right on a session to terminate it.',
        SessionFilter.all => '',
      };
      icon = _filter == SessionFilter.pinned
          ? Icons.push_pin_outlined
          : Icons.stop_circle_outlined;
    }

    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            icon,
            size: 48,
            color: Theme.of(
              context,
            ).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
          ),
          const SizedBox(height: 16),
          Text(
            label,
            style: Theme.of(context).textTheme.bodyLarge?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
          if (hint.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(
              hint,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: Theme.of(
                  context,
                ).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
              ),
            ),
          ],
        ],
      ),
    );
  }

  Color _statusColor(String status, ThemeData theme) {
    switch (status) {
      case 'starting':
        return Colors.teal;
      case 'active':
        return Colors.green;
      case 'compacting':
        return Colors.indigo;
      case 'waiting_permission':
        return Colors.orange;
      case 'idle':
        return Colors.blue;
      case 'error':
        return theme.colorScheme.error;
      case 'terminated':
        return theme.colorScheme.outline;
      default:
        return theme.colorScheme.outline;
    }
  }

  IconData _statusIcon(String status) {
    switch (status) {
      case 'starting':
        return Icons.rocket_launch;
      case 'active':
        return Icons.play_circle_filled;
      case 'compacting':
        return Icons.compress;
      case 'waiting_permission':
        return Icons.warning_amber;
      case 'idle':
        return Icons.pause_circle_filled;
      case 'error':
        return Icons.error;
      case 'terminated':
        return Icons.cancel_outlined;
      default:
        return Icons.circle;
    }
  }

  String _statusLabel(String status) {
    switch (status) {
      case 'starting':
        return 'Starting';
      case 'active':
        return 'Active';
      case 'compacting':
        return 'Compacting';
      case 'waiting_permission':
        return 'Needs Approval';
      case 'idle':
        return 'Idle';
      case 'error':
        return 'Error';
      case 'terminated':
        return 'Terminated';
      default:
        return status;
    }
  }
}

class _PulsingIcon extends StatefulWidget {
  final IconData icon;
  final Color color;
  final double size;

  const _PulsingIcon({
    required this.icon,
    required this.color,
    required this.size,
  });

  @override
  State<_PulsingIcon> createState() => _PulsingIconState();
}

class _PulsingIconState extends State<_PulsingIcon>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    _controller = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 2000),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _controller,
      builder: (context, child) {
        final opacity = 0.4 + 0.6 * _controller.value;
        final scale = 1.0 + 0.15 * _controller.value;
        return Transform.scale(
          scale: scale,
          child: Icon(
            widget.icon,
            size: widget.size,
            color: widget.color.withValues(alpha: opacity),
          ),
        );
      },
    );
  }
}

/// What the list needs to draw a tree: whose groups, the reader's choices, and
/// the catalogue those choices are read against.
class _Grouping {
  final String hostId;
  final GroupingPrefs prefs;
  final GroupCatalog catalog;

  const _Grouping(this.hostId, this.prefs, this.catalog);
}

/// The tree lines to the left of a forked row.
///
/// One column per level of indent, each 14 logical pixels wide with its line
/// down the centre, so an elbow and the trunk below it land on the same pixel.
/// Drawn rather than indented: a deep fork on its own leaves the reader
/// counting whitespace to work out which row above it came from.
class _ForkGuides extends StatelessWidget {
  /// One entry per level, outermost first: whether that level's line keeps
  /// running past this row. The last entry is this row's own column.
  final List<bool> trunk;

  /// Whether this row is its parent's last fork, so its elbow closes.
  final bool last;

  final Color colour;

  const _ForkGuides({
    required this.trunk,
    required this.last,
    required this.colour,
  });

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        for (var level = 0; level < trunk.length; level++)
          SizedBox(
            width: 14,
            child: CustomPaint(
              painter: _ForkGuidePainter(
                // The last column is this row's elbow; the ones before it are
                // ancestors, drawn only while they still have a child to come.
                elbow: level == trunk.length - 1,
                closes: last,
                running: trunk[level],
                colour: colour.withValues(alpha: 0.55),
              ),
            ),
          ),
      ],
    );
  }
}

class _ForkGuidePainter extends CustomPainter {
  final bool elbow;
  final bool closes;
  final bool running;
  final Color colour;

  _ForkGuidePainter({
    required this.elbow,
    required this.closes,
    required this.running,
    required this.colour,
  });

  @override
  void paint(Canvas canvas, Size size) {
    final pen = Paint()
      ..color = colour
      ..strokeWidth = 1.5
      ..strokeCap = StrokeCap.round;

    final x = size.width / 2;
    final middle = size.height / 2;

    if (elbow) {
      // Down to the middle, then right into the row. A last fork stops at the
      // turn; one with siblings below carries on so the next elbow meets the
      // same line.
      canvas.drawLine(Offset(x, 0), Offset(x, closes ? middle : size.height), pen);
      canvas.drawLine(Offset(x, middle), Offset(size.width, middle), pen);
      return;
    }
    // An ancestor with another child still to come. Blank once its last child
    // has been drawn, but the column is still held so everything stays aligned.
    if (running) {
      canvas.drawLine(Offset(x, 0), Offset(x, size.height), pen);
    }
  }

  @override
  bool shouldRepaint(_ForkGuidePainter old) =>
      old.elbow != elbow ||
      old.closes != closes ||
      old.running != running ||
      old.colour != colour;
}


/// A session's fork count, and the control that shows or hides them.
///
/// Always visible, so a folded family cannot be mistaken for a session with
/// nothing under it — folded, this is the only thing saying the branches are
/// still there. Sized as a chip rather than a bare icon: it is the one target
/// on the row that is not the row itself.
class _ForkToggle extends StatelessWidget {
  final int count;
  final bool folded;
  final VoidCallback onTap;

  const _ForkToggle({
    required this.count,
    required this.folded,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Semantics(
      button: true,
      label: folded ? 'Show $count forks' : 'Hide $count forks',
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(10),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                folded ? Icons.chevron_right : Icons.expand_more,
                size: 16,
                color: theme.colorScheme.onSurfaceVariant,
              ),
              const SizedBox(width: 2),
              Text(
                '$count \u2442',
                style: TextStyle(
                  fontSize: 11,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
