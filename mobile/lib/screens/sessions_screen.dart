import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
// Both packages export Provider, ChangeNotifierProvider and Consumer.
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../providers/daemon_providers.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';
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

  List<Session> _sortSessions(List<Session> sessions, {bool manual = false}) {
    sessions.sort((a, b) {
      if (manual) {
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
        final matching = _filterSessions(sessions);
        final filtered = _sortSessions(
          _hidingTerminated
              ? matching.where((s) => !s.isTerminated).toList()
              : matching,
          manual: manual,
        );

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
                      child: manual && orderable != null
                          ? ReorderableListView.builder(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 12,
                              ),
                              itemCount: filtered.length,
                              onReorder: (from, to) =>
                                  _onReorder(orderable, filtered, from, to),
                              // The cards carry their own handle, so the list's
                              // long-press drag would only fight the options
                              // sheet that a long press already opens.
                              buildDefaultDragHandles: false,
                              itemBuilder: (context, index) =>
                                  _buildSwipeableCard(
                                    filtered[index],
                                    hm,
                                    reorderIndex: index,
                                  ),
                            )
                          : ListView.builder(
                              padding: const EdgeInsets.symmetric(
                                horizontal: 12,
                              ),
                              itemCount: filtered.length,
                              itemBuilder: (context, index) =>
                                  _buildSwipeableCard(filtered[index], hm),
                            ),
                    ),
            ),
          ],
        );
      },
    );
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
    // Counting what the tab would show, not what exists: a chip reading 155
    // over a list of twelve is a chip that has to be explained. Terminated
    // sessions have their own tab, and it holds them on purpose.
    bool counted(Session s) => !(_hidingTerminated && s.isTerminated);
    final allCount = allSessions.where(counted).length;
    final pinnedCount = allSessions.where((s) => s.pinned && counted(s)).length;
    final terminatedCount = allSessions.where((s) => s.isTerminated).length;

    return Row(
      children: [
        _filterChip('All', allCount, SessionFilter.all),
        const SizedBox(width: 8),
        _filterChip('Pinned', pinnedCount, SessionFilter.pinned),
        const SizedBox(width: 8),
        _filterChip('Terminated', terminatedCount, SessionFilter.terminated),
        const Spacer(),
        IconButton(
          icon: Icon(
            ref.watch(manualOrderProvider) ? Icons.swap_vert : Icons.sort,
            size: 20,
            color: ref.watch(manualOrderProvider)
                ? Theme.of(context).colorScheme.primary
                : null,
          ),
          tooltip: ref.watch(manualOrderProvider)
              ? 'Sort: Manual — long-press a session to move it. Tap to sort by activity instead.'
              : 'Sort: Activity — active first, then most recent. Tap to arrange them by hand instead.',
          visualDensity: VisualDensity.compact,
          onPressed: () => _toggleManualOrder(hm, visible),
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

  Widget _filterChip(String label, int count, SessionFilter filter) {
    final isSelected = _filter == filter;
    return FilterChip(
      label: Text(count > 0 ? '$label ($count)' : label),
      selected: isSelected,
      onSelected: (_) {
        setState(() {
          _filter = filter;
          _exitMultiSelect();
        });
        _triggerSearch();
      },
      showCheckmark: false,
      visualDensity: VisualDensity.compact,
    );
  }

  /// [reorderIndex] is the card's place in a list arranged by hand, and null
  /// when the list sorts itself. The drag handle needs it to say which card is
  /// being moved.
  Widget _buildSwipeableCard(
    Session session,
    HostManager hm, {
    int? reorderIndex,
  }) {
    final theme = Theme.of(context);
    // Terminated is the archival state: putting a session away is ending it,
    // and the way back out is Resume rather than an unarchive.
    final isTerminated = session.isTerminated;
    final service = hm.serviceFor(session.hostId);

    return Dismissible(
      key: ValueKey(_compositeKey(session)),
      background: Container(
        margin: const EdgeInsets.only(bottom: 8),
        decoration: BoxDecoration(
          color: isTerminated ? Colors.green : Colors.teal,
          borderRadius: BorderRadius.circular(12),
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
        decoration: BoxDecoration(
          color: theme.colorScheme.error,
          borderRadius: BorderRadius.circular(12),
        ),
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
      child: _buildSessionCard(session, hm, reorderIndex: reorderIndex),
    );
  }

  Widget _buildSessionCard(
    Session session,
    HostManager hm, {
    int? reorderIndex,
  }) {
    final theme = Theme.of(context);
    final statusColor = _statusColor(session.status, theme);
    final statusIcon = _statusIcon(session.status);
    final isSelected = _selected.contains(_compositeKey(session));
    final host = hm.hostById(session.hostId);
    final hostColor = host?.color ?? theme.colorScheme.primary;
    final hostLabel = host?.label ?? '';

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      clipBehavior: Clip.antiAlias,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: isSelected
            ? BorderSide(color: theme.colorScheme.primary, width: 2)
            : session.isActive
            ? BorderSide(color: statusColor.withValues(alpha: 0.4), width: 1.5)
            : BorderSide.none,
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
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
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.all(12),
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
                                    color: statusColor.withValues(alpha: 0.12),
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
                                      color: theme.colorScheme.onSurfaceVariant,
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
                                fontSize: 14,
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
                              mainAxisAlignment: MainAxisAlignment.spaceBetween,
                              children: [
                                Flexible(
                                  child: Text(
                                    session.model ?? '',
                                    style: TextStyle(
                                      fontSize: 11,
                                      color: theme.colorScheme.onSurfaceVariant,
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
              ListTile(
                leading: const Icon(Icons.folder_outlined),
                title: const Text('Filter this directory'),
                onTap: () {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  _setCwdFilter(session.cwd, session.project);
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
