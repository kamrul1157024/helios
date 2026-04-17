import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../services/host_manager.dart';
import '../services/daemon_api_service.dart';
import '../widgets/skeleton.dart';
import 'session_detail_screen.dart';

enum SessionFilter { all, pinned, archived }

class SessionsScreen extends StatefulWidget {
  const SessionsScreen({super.key});

  @override
  State<SessionsScreen> createState() => _SessionsScreenState();
}

class _SessionsScreenState extends State<SessionsScreen> {
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

  List<Session> _filterSessions(List<Session> sessions) {
    // When search or CWD filter is active, API already filtered — pass through.
    if (_searchExpanded && _searchController.text.trim().isNotEmpty || _cwdFilter != null) {
      return sessions;
    }
    switch (_filter) {
      case SessionFilter.all:
        return sessions.where((s) => !s.archived).toList();
      case SessionFilter.pinned:
        return sessions.where((s) => s.pinned && !s.archived).toList();
      case SessionFilter.archived:
        return sessions.where((s) => s.archived).toList();
    }
  }

  int _statusOrder(Session s) {
    if (s.isActive) return 0;
    if (s.isIdle) return 1;
    if (s.pinned) return 2;
    if (s.isTerminated) return 3;
    if (s.archived) return 4;
    return 3;
  }

  List<Session> _sortSessions(List<Session> sessions) {
    sessions.sort((a, b) {
      final orderCmp = _statusOrder(a).compareTo(_statusOrder(b));
      if (orderCmp != 0) return orderCmp;
      final aTime = a.lastEventAt ?? a.createdAt;
      final bTime = b.lastEventAt ?? b.createdAt;
      return bTime.compareTo(aTime);
    });
    return sessions;
  }

  String get _filterParam {
    switch (_filter) {
      case SessionFilter.all:
        return 'all';
      case SessionFilter.pinned:
        return 'pinned';
      case SessionFilter.archived:
        return 'archived';
    }
  }

  void _triggerSearch() {
    _debounce?.cancel();
    _debounce = Timer(const Duration(milliseconds: 300), () {
      if (!mounted) return;
      final hm = context.read<HostManager>();
      final q = _searchController.text.trim();
      if (hm.activeHostId != null) {
        hm.serviceFor(hm.activeHostId!)?.fetchSessions(
          q: q.isNotEmpty ? q : null,
          filter: _filterParam,
          cwd: _cwdFilter,
          updateFilters: true,
        );
      } else {
        for (final host in hm.hosts) {
          hm.serviceFor(host.id)?.fetchSessions(
            q: q.isNotEmpty ? q : null,
            filter: _filterParam,
            cwd: _cwdFilter,
            updateFilters: true,
          );
        }
      }
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
    final hm = context.read<HostManager>();
    final service = hm.activeHostId != null ? hm.serviceFor(hm.activeHostId!) : null;
    if (service == null) return;

    final dirs = await service.fetchDirectories();
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
                  style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
                ),
              ),
              const Divider(height: 1),
              ...dirs.map((d) => ListTile(
                leading: const Icon(Icons.folder_outlined),
                title: Text(d.project.isNotEmpty ? d.project : d.shortCwd),
                subtitle: Text(
                  d.shortCwd,
                  style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: theme.colorScheme.onSurfaceVariant),
                ),
                trailing: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (d.activeCount > 0)
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: Colors.green.withValues(alpha: 0.12),
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(
                          '${d.activeCount} active',
                          style: const TextStyle(fontSize: 11, color: Colors.green, fontWeight: FontWeight.w600),
                        ),
                      ),
                    const SizedBox(width: 6),
                    Text(
                      '${d.sessionCount}',
                      style: TextStyle(fontSize: 13, color: theme.colorScheme.onSurfaceVariant),
                    ),
                  ],
                ),
                onTap: () {
                  Navigator.pop(ctx);
                  _setCwdFilter(d.cwd, d.project);
                },
              )),
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
    final hm = context.read<HostManager>();
    for (final key in _selected.toList()) {
      final parts = key.split(':');
      if (parts.length == 2) {
        hm.serviceFor(parts[0])?.patchSession(parts[1], pinned: pin);
      }
    }
    _exitMultiSelect();
  }

  Future<void> _batchArchive(bool archive) async {
    final hm = context.read<HostManager>();
    for (final key in _selected.toList()) {
      final parts = key.split(':');
      if (parts.length == 2) {
        hm.serviceFor(parts[0])?.patchSession(parts[1], archived: archive);
      }
    }
    _exitMultiSelect();
  }

  Future<void> _batchDelete() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete sessions'),
        content: Text('Delete ${_selected.length} session(s)? This cannot be undone.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: FilledButton.styleFrom(backgroundColor: Theme.of(ctx).colorScheme.error),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    final hm = context.read<HostManager>();
    for (final key in _selected.toList()) {
      final parts = key.split(':');
      if (parts.length == 2) {
        hm.serviceFor(parts[0])?.deleteSession(parts[1]);
      }
    }
    _exitMultiSelect();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        final sessions = hm.filteredSessions;
        final loaded = hm.sessionsLoaded;

        if (!loaded) {
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

        final isSearchActive = _searchExpanded && _searchController.text.trim().isNotEmpty;
        final isFilterActive = isSearchActive || _cwdFilter != null || _filter != SessionFilter.all;

        if (sessions.isEmpty && !isFilterActive) {
          return _buildEmptyState();
        }

        final filtered = _sortSessions(_filterSessions(sessions));

        final activeService = hm.activeHostId != null ? hm.serviceFor(hm.activeHostId!) : null;
        final tmux = activeService?.tmuxStatus;
        final banners = <Widget>[];
        if (tmux != null && !tmux.installed && !(activeService?.tmuxMissingBannerDismissed ?? true)) {
          banners.add(_buildTmuxMissingBanner(activeService!));
        } else if (tmux != null &&
            (!tmux.resurrectPlugin || !tmux.continuumPlugin) &&
            !(activeService?.pluginBannerDismissed ?? true)) {
          banners.add(_buildPluginBanner(tmux, activeService!));
        }

        return Column(
          children: [
            if (_multiSelect) _buildMultiSelectBar(),
            _buildFilterRow(sessions),
            if (_cwdFilter != null) _buildActiveFiltersRow(),
            Expanded(
              child: filtered.isEmpty
                  ? _buildEmptyFilterState()
                  : RefreshIndicator(
                      onRefresh: () => hm.activeHostId != null
                          ? hm.refreshHost(hm.activeHostId!)
                          : hm.refreshAll(),
                      child: ListView.builder(
                        padding: const EdgeInsets.symmetric(horizontal: 12),
                        itemCount: filtered.length + banners.length,
                        itemBuilder: (context, index) {
                          if (index < banners.length) return banners[index];
                          return _buildSwipeableCard(filtered[index - banners.length], hm);
                        },
                      ),
                    ),
            ),
          ],
        );
      },
    );
  }

  Widget _buildMultiSelectBar() {
    final theme = Theme.of(context);
    final isArchiveTab = _filter == SessionFilter.archived;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      color: theme.colorScheme.surfaceContainerHighest,
      child: Row(
        children: [
          IconButton(icon: const Icon(Icons.close), onPressed: _exitMultiSelect),
          Text('${_selected.length} selected', style: const TextStyle(fontWeight: FontWeight.w600)),
          const Spacer(),
          if (!isArchiveTab)
            IconButton(icon: const Icon(Icons.push_pin_outlined), tooltip: 'Pin', onPressed: () => _batchPin(true)),
          IconButton(
            icon: Icon(isArchiveTab ? Icons.unarchive_outlined : Icons.archive_outlined),
            tooltip: isArchiveTab ? 'Unarchive' : 'Archive',
            onPressed: () => _batchArchive(!isArchiveTab),
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

  Widget _buildFilterRow(List<Session> allSessions) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: AnimatedCrossFade(
        duration: const Duration(milliseconds: 200),
        crossFadeState: _searchExpanded ? CrossFadeState.showSecond : CrossFadeState.showFirst,
        firstChild: _buildFilterChips(allSessions),
        secondChild: _buildSearchBar(),
      ),
    );
  }

  Widget _buildFilterChips(List<Session> allSessions) {
    final allCount = allSessions.where((s) => !s.archived).length;
    final pinnedCount = allSessions.where((s) => s.pinned && !s.archived).length;
    final archivedCount = allSessions.where((s) => s.archived).length;

    return Row(
      children: [
        _filterChip('All', allCount, SessionFilter.all),
        const SizedBox(width: 8),
        _filterChip('Pinned', pinnedCount, SessionFilter.pinned),
        const SizedBox(width: 8),
        _filterChip('Archived', archivedCount, SessionFilter.archived),
        const Spacer(),
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

  Widget _buildSwipeableCard(Session session, HostManager hm) {
    final theme = Theme.of(context);
    final isArchived = session.archived;
    final service = hm.serviceFor(session.hostId);

    return Dismissible(
      key: ValueKey(_compositeKey(session)),
      background: Container(
        margin: const EdgeInsets.only(bottom: 8),
        decoration: BoxDecoration(
          color: isArchived ? Colors.green : Colors.teal,
          borderRadius: BorderRadius.circular(12),
        ),
        alignment: Alignment.centerLeft,
        padding: const EdgeInsets.only(left: 20),
        child: Row(
          children: [
            Icon(isArchived ? Icons.unarchive : Icons.archive, color: Colors.white),
            const SizedBox(width: 8),
            Text(
              isArchived ? 'Unarchive' : 'Archive',
              style: const TextStyle(color: Colors.white, fontWeight: FontWeight.w600),
            ),
          ],
        ),
      ),
      secondaryBackground: Container(
        margin: const EdgeInsets.only(bottom: 8),
        decoration: BoxDecoration(color: theme.colorScheme.error, borderRadius: BorderRadius.circular(12)),
        alignment: Alignment.centerRight,
        padding: const EdgeInsets.only(right: 20),
        child: const Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            Text('Delete', style: TextStyle(color: Colors.white, fontWeight: FontWeight.w600)),
            SizedBox(width: 8),
            Icon(Icons.delete, color: Colors.white),
          ],
        ),
      ),
      confirmDismiss: (direction) async {
        if (direction == DismissDirection.startToEnd) {
          service?.patchSession(session.sessionId, archived: !isArchived);
          return false;
        } else {
          final confirmed = await showDialog<bool>(
            context: context,
            builder: (ctx) => AlertDialog(
              title: const Text('Delete session'),
              content: const Text('Delete this session? This cannot be undone.'),
              actions: [
                TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
                FilledButton(
                  onPressed: () => Navigator.pop(ctx, true),
                  style: FilledButton.styleFrom(backgroundColor: theme.colorScheme.error),
                  child: const Text('Delete'),
                ),
              ],
            ),
          );
          if (confirmed == true) {
            service?.deleteSession(session.sessionId);
          }
          return false;
        }
      },
      child: _buildSessionCard(session, hm),
    );
  }

  Widget _buildSessionCard(Session session, HostManager hm) {
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
              MaterialPageRoute(builder: (_) => SessionDetailScreen(session: session)),
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
                                  _PulsingIcon(icon: statusIcon, color: statusColor, size: 14)
                                else
                                  Icon(statusIcon, size: 14, color: statusColor),
                                const SizedBox(width: 6),
                                Container(
                                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                                  decoration: BoxDecoration(
                                    color: statusColor.withValues(alpha: 0.12),
                                    borderRadius: BorderRadius.circular(4),
                                  ),
                                  child: Text(
                                    _statusLabel(session.status),
                                    style: TextStyle(fontSize: 11, color: statusColor, fontWeight: FontWeight.w600),
                                  ),
                                ),
                                if (session.needsRecovery) ...[
                                  const SizedBox(width: 6),
                                  Tooltip(
                                    message: 'No tmux pane — tap to recover',
                                    child: Icon(Icons.link_off, size: 14, color: Colors.amber.shade700),
                                  ),
                                ],
                                if (session.pinned) ...[
                                  const SizedBox(width: 6),
                                  Icon(Icons.push_pin, size: 14, color: theme.colorScheme.primary),
                                ],
                                const Spacer(),
                                Text(
                                  session.timeAgo,
                                  style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
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
                                    style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                                Text(
                                  hostLabel,
                                  style: TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: hostColor),
                                ),
                              ],
                            ),
                          ],
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
    final isArchived = session.archived;
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
                      style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: theme.colorScheme.onSurfaceVariant),
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
                leading: Icon(session.pinned ? Icons.push_pin : Icons.push_pin_outlined),
                title: Text(session.pinned ? 'Unpin' : 'Pin'),
                onTap: () {
                  Navigator.pop(ctx);
                  service?.patchSession(sessionId, pinned: !session.pinned);
                },
              ),
              ListTile(
                leading: Icon(isArchived ? Icons.unarchive_outlined : Icons.archive_outlined),
                title: Text(isArchived ? 'Unarchive' : 'Archive'),
                onTap: () {
                  Navigator.pop(ctx);
                  service?.patchSession(sessionId, archived: !isArchived);
                },
              ),
              ListTile(
                leading: Icon(Icons.delete_outline, color: theme.colorScheme.error),
                title: Text('Delete', style: TextStyle(color: theme.colorScheme.error)),
                onTap: () async {
                  Navigator.pop(ctx);
                  if (!mounted) return;
                  final confirmed = await showDialog<bool>(
                    context: context,
                    builder: (dCtx) => AlertDialog(
                      title: const Text('Delete session'),
                      content: const Text('Delete this session? This cannot be undone.'),
                      actions: [
                        TextButton(onPressed: () => Navigator.pop(dCtx, false), child: const Text('Cancel')),
                        FilledButton(
                          onPressed: () => Navigator.pop(dCtx, true),
                          style: FilledButton.styleFrom(backgroundColor: theme.colorScheme.error),
                          child: const Text('Delete'),
                        ),
                      ],
                    ),
                  );
                  if (confirmed == true) {
                    service?.deleteSession(sessionId);
                  }
                },
              ),
              const Divider(height: 1),
              ListTile(
                leading: Icon(session.managed ? Icons.link_off : Icons.shield_outlined),
                title: Text(session.managed ? 'Detach from Helios' : 'Hand off to Helios'),
                onTap: () {
                  Navigator.pop(ctx);
                  service?.patchSession(sessionId, managed: !session.managed);
                },
              ),
              if (session.canStop || session.canTerminate || session.canResume) ...[
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
    final service = hm.serviceFor(session.hostId);
    if (service == null) return;

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
              border: OutlineInputBorder(borderRadius: BorderRadius.circular(8)),
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
        service.patchSession(sessionId, title: title);
      }
    });
  }

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.terminal, size: 48, color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.5)),
          const SizedBox(height: 16),
          Text(
            'No sessions yet.',
            style: Theme.of(context).textTheme.bodyLarge?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant),
          ),
          const SizedBox(height: 4),
          Text(
            'Start a Claude session:\nhelios new "your prompt"',
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
                  fontFamily: 'monospace',
                ),
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyFilterState() {
    final isSearchActive = _searchExpanded && _searchController.text.trim().isNotEmpty;

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
        SessionFilter.archived => 'No archived sessions.',
        SessionFilter.all => 'No sessions.',
      };
      hint = switch (_filter) {
        SessionFilter.pinned => 'Long-press a session to pin it.',
        SessionFilter.archived => 'Swipe right on a session to archive it.',
        SessionFilter.all => '',
      };
      icon = _filter == SessionFilter.pinned ? Icons.push_pin_outlined : Icons.archive_outlined;
    }

    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            icon,
            size: 48,
            color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
          ),
          const SizedBox(height: 16),
          Text(
            label,
            style: Theme.of(context).textTheme.bodyLarge?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant),
          ),
          if (hint.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(
              hint,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
                  ),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildTmuxMissingBanner(DaemonAPIService service) {
    final theme = Theme.of(context);
    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      color: theme.colorScheme.errorContainer,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.warning_amber, color: theme.colorScheme.onErrorContainer, size: 20),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'tmux not installed',
                    style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13, color: theme.colorScheme.onErrorContainer),
                  ),
                ),
                GestureDetector(
                  onTap: () => service.dismissTmuxMissingBanner(),
                  child: Icon(Icons.close, size: 18, color: theme.colorScheme.onErrorContainer),
                ),
              ],
            ),
            const SizedBox(height: 6),
            Text(
              'Session management (send, stop, resume) requires tmux. Install it on your server:',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onErrorContainer),
            ),
            const SizedBox(height: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
              decoration: BoxDecoration(
                color: theme.colorScheme.onErrorContainer.withValues(alpha: 0.08),
                borderRadius: BorderRadius.circular(6),
              ),
              child: Text(
                'brew install tmux',
                style: TextStyle(fontFamily: 'monospace', fontSize: 12, color: theme.colorScheme.onErrorContainer),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildPluginBanner(TmuxStatus tmux, DaemonAPIService service) {
    final theme = Theme.of(context);
    final missing = <String>[];
    if (!tmux.resurrectPlugin) missing.add('tmux-resurrect');
    if (!tmux.continuumPlugin) missing.add('tmux-continuum');

    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      color: Colors.orange.withValues(alpha: 0.1),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: Colors.orange.withValues(alpha: 0.3)),
      ),
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.tips_and_updates, color: Colors.orange, size: 20),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'Recommended: ${missing.join(" & ")}',
                    style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13, color: theme.colorScheme.onSurface),
                  ),
                ),
                GestureDetector(
                  onTap: () => service.dismissPluginBanner(),
                  child: Icon(Icons.close, size: 18, color: theme.colorScheme.onSurfaceVariant),
                ),
              ],
            ),
            const SizedBox(height: 6),
            Text(
              'These plugins save and auto-restore your tmux sessions '
              'after crashes or reboots, so Claude sessions survive restarts.',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(height: 6),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
              decoration: BoxDecoration(
                color: theme.colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(6),
              ),
              child: Text(
                'git clone https://github.com/tmux-plugins/tpm ~/.tmux/plugins/tpm',
                style: TextStyle(fontFamily: 'monospace', fontSize: 11, color: theme.colorScheme.onSurface),
              ),
            ),
          ],
        ),
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

  const _PulsingIcon({required this.icon, required this.color, required this.size});

  @override
  State<_PulsingIcon> createState() => _PulsingIconState();
}

class _PulsingIconState extends State<_PulsingIcon> with SingleTickerProviderStateMixin {
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
          child: Icon(widget.icon, size: widget.size, color: widget.color.withValues(alpha: opacity)),
        );
      },
    );
  }
}
