import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import '../models/session.dart';
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

  @override
  void initState() {
    super.initState();
    context.read<DaemonAPIService>().fetchSessions();
  }

  List<Session> _filterSessions(List<Session> sessions) {
    List<Session> filtered;
    switch (_filter) {
      case SessionFilter.all:
        filtered = sessions.where((s) => !s.archived).toList();
      case SessionFilter.pinned:
        filtered = sessions.where((s) => s.pinned && !s.archived).toList();
      case SessionFilter.archived:
        filtered = sessions.where((s) => s.archived).toList();
    }
    // Active sessions always float to the top.
    filtered.sort((a, b) {
      if (a.isActive && !b.isActive) return -1;
      if (!a.isActive && b.isActive) return 1;
      return 0; // preserve server order otherwise
    });
    return filtered;
  }

  void _exitMultiSelect() {
    setState(() {
      _multiSelect = false;
      _selected.clear();
    });
  }

  void _toggleSelection(String sessionId) {
    setState(() {
      if (_selected.contains(sessionId)) {
        _selected.remove(sessionId);
        if (_selected.isEmpty) _multiSelect = false;
      } else {
        _selected.add(sessionId);
      }
    });
  }

  Future<void> _batchPin(DaemonAPIService sse, bool pin) async {
    for (final id in _selected.toList()) {
      await sse.patchSession(id, pinned: pin);
    }
    _exitMultiSelect();
  }

  Future<void> _batchArchive(DaemonAPIService sse, bool archive) async {
    for (final id in _selected.toList()) {
      await sse.patchSession(id, archived: archive);
    }
    _exitMultiSelect();
  }

  Future<void> _batchDelete(DaemonAPIService sse) async {
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
    if (confirmed != true) return;

    for (final id in _selected.toList()) {
      await sse.deleteSession(id);
    }
    _exitMultiSelect();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<DaemonAPIService>(
      builder: (context, sse, _) {
        final sessions = sse.sessions;

        if (!sse.sessionsLoaded) {
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

        if (sessions.isEmpty) {
          return _buildEmptyState();
        }

        final filtered = _filterSessions(sessions);

        final tmux = sse.tmuxStatus;
        final banners = <Widget>[];
        if (tmux != null && !tmux.installed && !sse.tmuxMissingBannerDismissed) {
          banners.add(_buildTmuxMissingBanner(sse));
        } else if (tmux != null && (!tmux.resurrectPlugin || !tmux.continuumPlugin) && !sse.pluginBannerDismissed) {
          banners.add(_buildPluginBanner(tmux, sse));
        }

        return Column(
          children: [
            // Multi-select action bar
            if (_multiSelect)
              _buildMultiSelectBar(sse),
            // Filter chips
            _buildFilterChips(sessions),
            // Session list
            Expanded(
              child: filtered.isEmpty
                  ? _buildEmptyFilterState()
                  : RefreshIndicator(
                      onRefresh: sse.fetchSessions,
                      child: ListView.builder(
                        padding: const EdgeInsets.symmetric(horizontal: 12),
                        itemCount: filtered.length + banners.length,
                        itemBuilder: (context, index) {
                          if (index < banners.length) return banners[index];
                          return _buildSwipeableCard(filtered[index - banners.length], sse);
                        },
                      ),
                    ),
            ),
          ],
        );
      },
    );
  }

  Widget _buildMultiSelectBar(DaemonAPIService sse) {
    final theme = Theme.of(context);
    final isArchiveTab = _filter == SessionFilter.archived;

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
          if (!isArchiveTab)
            IconButton(
              icon: const Icon(Icons.push_pin_outlined),
              tooltip: 'Pin',
              onPressed: () => _batchPin(sse, true),
            ),
          IconButton(
            icon: Icon(isArchiveTab ? Icons.unarchive_outlined : Icons.archive_outlined),
            tooltip: isArchiveTab ? 'Unarchive' : 'Archive',
            onPressed: () => _batchArchive(sse, !isArchiveTab),
          ),
          IconButton(
            icon: Icon(Icons.delete_outline, color: theme.colorScheme.error),
            tooltip: 'Delete',
            onPressed: () => _batchDelete(sse),
          ),
        ],
      ),
    );
  }

  Widget _buildFilterChips(List<Session> allSessions) {
    final allCount = allSessions.where((s) => !s.archived).length;
    final pinnedCount = allSessions.where((s) => s.pinned && !s.archived).length;
    final archivedCount = allSessions.where((s) => s.archived).length;

    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: Row(
        children: [
          _filterChip('All', allCount, SessionFilter.all),
          const SizedBox(width: 8),
          _filterChip('Pinned', pinnedCount, SessionFilter.pinned),
          const SizedBox(width: 8),
          _filterChip('Archived', archivedCount, SessionFilter.archived),
        ],
      ),
    );
  }

  Widget _filterChip(String label, int count, SessionFilter filter) {
    final isSelected = _filter == filter;
    return FilterChip(
      label: Text(count > 0 ? '$label ($count)' : label),
      selected: isSelected,
      onSelected: (_) => setState(() {
        _filter = filter;
        _exitMultiSelect();
      }),
      showCheckmark: false,
      visualDensity: VisualDensity.compact,
    );
  }

  Widget _buildSwipeableCard(Session session, DaemonAPIService sse) {
    final theme = Theme.of(context);
    final isArchived = session.archived;

    return Dismissible(
      key: ValueKey(session.sessionId),
      // Swipe right → archive/unarchive
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
      // Swipe left → delete
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
            Text('Delete', style: TextStyle(color: Colors.white, fontWeight: FontWeight.w600)),
            SizedBox(width: 8),
            Icon(Icons.delete, color: Colors.white),
          ],
        ),
      ),
      confirmDismiss: (direction) async {
        if (direction == DismissDirection.startToEnd) {
          // Archive/unarchive — no confirm needed
          await sse.patchSession(session.sessionId, archived: !isArchived);
          if (mounted) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(isArchived ? 'Session unarchived' : 'Session archived'),
                action: SnackBarAction(
                  label: 'Undo',
                  onPressed: () => sse.patchSession(session.sessionId, archived: isArchived),
                ),
                duration: const Duration(seconds: 3),
              ),
            );
          }
          return false; // don't remove from list, fetchSessions handles it
        } else {
          // Delete — confirm
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
            await sse.deleteSession(session.sessionId);
          }
          return false;
        }
      },
      child: _buildSessionCard(session, sse),
    );
  }

  Widget _buildSessionCard(Session session, DaemonAPIService sse) {
    final theme = Theme.of(context);
    final statusColor = _statusColor(session.status, theme);
    final statusIcon = _statusIcon(session.status);
    final isSelected = _selected.contains(session.sessionId);

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
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
            _toggleSelection(session.sessionId);
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
          _showContextMenu(session, sse);
        },
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              // Selection checkbox in multi-select mode
              if (_multiSelect) ...[
                Checkbox(
                  value: isSelected,
                  onChanged: (_) => _toggleSelection(session.sessionId),
                  visualDensity: VisualDensity.compact,
                ),
                const SizedBox(width: 4),
              ],
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Top row: status badge + pin icon + model + time
                    Row(
                      children: [
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
                        if (session.pinned) ...[
                          const SizedBox(width: 6),
                          Icon(Icons.push_pin, size: 14, color: theme.colorScheme.primary),
                        ],
                        if (session.model != null) ...[
                          const SizedBox(width: 8),
                          Flexible(
                            child: Text(
                              session.model!,
                              style: TextStyle(
                                fontSize: 11,
                                color: theme.colorScheme.onSurfaceVariant,
                              ),
                              overflow: TextOverflow.ellipsis,
                            ),
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
                    // Last user message — bold for visibility
                    if (session.lastUserMessage != null) ...[
                      Text(
                        session.lastUserMessage!,
                        style: TextStyle(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                          color: theme.colorScheme.onSurface,
                        ),
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                      ),
                      const SizedBox(height: 4),
                    ],
                    // CWD
                    Text(
                      session.shortCwd,
                      style: TextStyle(
                        fontSize: 12,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                    // Last event
                    if (session.lastEvent != null) ...[
                      const SizedBox(height: 4),
                      Text(
                        session.lastEvent!,
                        style: TextStyle(
                          fontSize: 11,
                          color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ],
                    // Session ID
                    const SizedBox(height: 4),
                    Text(
                      session.shortId,
                      style: TextStyle(
                        fontSize: 10,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.6),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _showContextMenu(Session session, DaemonAPIService sse) {
    final theme = Theme.of(context);
    final isArchived = session.archived;

    showModalBottomSheet(
      context: context,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              // Header
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        session.lastUserMessage ?? session.shortCwd,
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
              // Select
              ListTile(
                leading: const Icon(Icons.check_box_outlined),
                title: const Text('Select'),
                onTap: () {
                  Navigator.pop(ctx);
                  setState(() {
                    _multiSelect = true;
                    _selected.add(session.sessionId);
                  });
                },
              ),
              // Pin / Unpin
              ListTile(
                leading: Icon(session.pinned ? Icons.push_pin : Icons.push_pin_outlined),
                title: Text(session.pinned ? 'Unpin' : 'Pin'),
                onTap: () {
                  Navigator.pop(ctx);
                  sse.patchSession(session.sessionId, pinned: !session.pinned);
                },
              ),
              // Archive / Unarchive
              ListTile(
                leading: Icon(isArchived ? Icons.unarchive_outlined : Icons.archive_outlined),
                title: Text(isArchived ? 'Unarchive' : 'Archive'),
                onTap: () {
                  Navigator.pop(ctx);
                  sse.patchSession(session.sessionId, archived: !isArchived);
                  ScaffoldMessenger.of(context).showSnackBar(
                    SnackBar(
                      content: Text(isArchived ? 'Session unarchived' : 'Session archived'),
                      action: SnackBarAction(
                        label: 'Undo',
                        onPressed: () => sse.patchSession(session.sessionId, archived: isArchived),
                      ),
                      duration: const Duration(seconds: 3),
                    ),
                  );
                },
              ),
              // Delete
              ListTile(
                leading: Icon(Icons.delete_outline, color: theme.colorScheme.error),
                title: Text('Delete', style: TextStyle(color: theme.colorScheme.error)),
                onTap: () async {
                  Navigator.pop(ctx);
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
                    await sse.deleteSession(session.sessionId);
                  }
                },
              ),
              // Session control actions
              if (session.canStop || session.canSuspend || session.canResume) ...[
                const Divider(height: 1),
                if (session.canStop)
                  ListTile(
                    leading: const Icon(Icons.stop),
                    title: const Text('Stop'),
                    onTap: () {
                      Navigator.pop(ctx);
                      sse.stopSession(session.sessionId);
                    },
                  ),
                if (session.canSuspend)
                  ListTile(
                    leading: const Icon(Icons.pause),
                    title: const Text('Suspend'),
                    onTap: () {
                      Navigator.pop(ctx);
                      sse.suspendSession(session.sessionId);
                    },
                  ),
                if (session.canResume)
                  ListTile(
                    leading: const Icon(Icons.play_arrow),
                    title: const Text('Resume'),
                    onTap: () {
                      Navigator.pop(ctx);
                      sse.resumeSession(session.sessionId);
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

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            Icons.terminal,
            size: 48,
            color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
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
                  color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
                  fontFamily: 'monospace',
                ),
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyFilterState() {
    final label = switch (_filter) {
      SessionFilter.pinned => 'No pinned sessions.',
      SessionFilter.archived => 'No archived sessions.',
      SessionFilter.all => 'No sessions.',
    };
    final hint = switch (_filter) {
      SessionFilter.pinned => 'Long-press a session to pin it.',
      SessionFilter.archived => 'Swipe right on a session to archive it.',
      SessionFilter.all => '',
    };

    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            _filter == SessionFilter.pinned ? Icons.push_pin_outlined : Icons.archive_outlined,
            size: 48,
            color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
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
                    color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
                  ),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildTmuxMissingBanner(DaemonAPIService sse) {
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
                    style: TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 13,
                      color: theme.colorScheme.onErrorContainer,
                    ),
                  ),
                ),
                GestureDetector(
                  onTap: () => sse.dismissTmuxMissingBanner(),
                  child: Icon(Icons.close, size: 18, color: theme.colorScheme.onErrorContainer),
                ),
              ],
            ),
            const SizedBox(height: 6),
            Text(
              'Session management (send, stop, resume) requires tmux. '
              'Install it on your server:',
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
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 12,
                  color: theme.colorScheme.onErrorContainer,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildPluginBanner(TmuxStatus tmux, DaemonAPIService sse) {
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
                    style: TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 13,
                      color: theme.colorScheme.onSurface,
                    ),
                  ),
                ),
                GestureDetector(
                  onTap: () => sse.dismissPluginBanner(),
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
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 11,
                  color: theme.colorScheme.onSurface,
                ),
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
      case 'suspended':
        return Colors.purple;
      case 'stale':
        return Colors.grey;
      case 'ended':
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
      case 'suspended':
        return Icons.stop_circle;
      case 'stale':
        return Icons.help_outline;
      case 'ended':
        return Icons.check_circle;
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
      case 'suspended':
        return 'Suspended';
      case 'stale':
        return 'Stale';
      case 'ended':
        return 'Ended';
      default:
        return status;
    }
  }
}
