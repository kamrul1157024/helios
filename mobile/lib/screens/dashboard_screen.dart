import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/notification.dart';
import '../providers/card_registry.dart' as registry;
import '../providers/claude/notification_ext.dart';
import '../services/daemon_api_service.dart';
import '../widgets/skeleton.dart';
import 'session_detail_screen.dart';

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({super.key});

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  final Set<String> _selected = {};

  @override
  Widget build(BuildContext context) {
    return Consumer<DaemonAPIService>(
      builder: (context, sse, _) {
        if (!sse.notificationsLoaded) {
          return ListView(
            padding: const EdgeInsets.all(16),
            children: const [
              NotificationCardSkeleton(),
              NotificationCardSkeleton(),
              NotificationCardSkeleton(),
            ],
          );
        }

        final notifications = sse.notifications;

        final pendingActions = notifications
            .where((n) => registry.needsAction(n))
            .toList();
        final activeStatuses = notifications
            .where((n) => n.isPending && !registry.needsAction(n))
            .toList();
        final resolved = notifications
            .where((n) => !n.isPending)
            .toList();

        if (pendingActions.isEmpty && activeStatuses.isEmpty && resolved.isEmpty) {
          return _buildEmptyState();
        }

        return RefreshIndicator(
          onRefresh: sse.fetchNotifications,
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              if (pendingActions.isNotEmpty)
                _buildBatchActions(pendingActions),

              if (pendingActions.isNotEmpty) ...[
                _sectionHeader('Pending (${pendingActions.length})'),
                ...pendingActions.map(_buildNotificationCard),
                const SizedBox(height: 16),
              ],

              if (activeStatuses.isNotEmpty) ...[
                _sectionHeader('Active Sessions'),
                ...activeStatuses.map(_buildStatusCard),
                const SizedBox(height: 16),
              ],

              if (resolved.isNotEmpty) ...[
                _sectionHeader('History'),
                ...resolved.map(_buildHistoryCard),
              ],
            ],
          ),
        );
      },
    );
  }

  void _navigateToSession(String sourceSession) {
    if (sourceSession.isEmpty) return;
    final sse = context.read<DaemonAPIService>();
    final match = sse.sessions.where((s) => s.sessionId == sourceSession);
    if (match.isEmpty) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => SessionDetailScreen(session: match.first),
      ),
    );
  }

  // ==================== Card Routing ====================

  Widget _buildNotificationCard(HeliosNotification n) {
    final sse = context.read<DaemonAPIService>();
    final card = registry.buildCardForType(
      notification: n,
      sse: sse,
      selected: _selected,
      onSelectionChanged: () => setState(() {}),
    );
    return card ?? _buildStatusCard(n);
  }

  // ==================== Shared Widgets ====================

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            Icons.notifications_none,
            size: 48,
            color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
          ),
          const SizedBox(height: 16),
          Text(
            'No notifications yet.',
            style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
          ),
          const SizedBox(height: 4),
          Text(
            'Start a Claude session with helios hooks installed.',
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.7),
                ),
          ),
        ],
      ),
    );
  }

  Widget _sectionHeader(String title) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Text(
        title,
        style: Theme.of(context).textTheme.labelMedium?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
      ),
    );
  }

  Widget _buildBatchActions(List<HeliosNotification> pending) {
    final sse = context.read<DaemonAPIService>();
    final permissionIds = pending.where((n) => n.isClaudePermission).map((n) => n.id).toList();
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        children: [
          if (permissionIds.isNotEmpty)
            FilledButton.tonal(
              onPressed: () {
                sse.batchAction(permissionIds, {'action': 'approve'});
              },
              child: Text('Approve All (${permissionIds.length})'),
            ),
          if (_selected.isNotEmpty) ...[
            const SizedBox(width: 8),
            OutlinedButton(
              onPressed: () {
                sse.batchAction(_selected.toList(), {'action': 'approve'});
                setState(() => _selected.clear());
              },
              child: Text('Approve (${_selected.length})'),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildStatusCard(HeliosNotification n) {
    final sse = context.read<DaemonAPIService>();
    final isError = n.type.endsWith('.error');
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: isError
              ? Theme.of(context).colorScheme.error.withValues(alpha: 0.3)
              : Theme.of(context).colorScheme.primary.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: ListTile(
        onTap: () => _navigateToSession(n.sourceSession),
        title: Row(
          children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: isError
                    ? Theme.of(context).colorScheme.errorContainer
                    : Theme.of(context).colorScheme.secondaryContainer,
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                n.displayTitle,
                style: TextStyle(
                  fontSize: 11,
                  color: isError
                      ? Theme.of(context).colorScheme.onErrorContainer
                      : Theme.of(context).colorScheme.onSecondaryContainer,
                ),
              ),
            ),
            const Spacer(),
            IconButton(
              icon: const Icon(Icons.close, size: 16),
              onPressed: () => sse.dismissNotification(n.id),
              constraints: const BoxConstraints(),
              padding: EdgeInsets.zero,
            ),
          ],
        ),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const SizedBox(height: 4),
            Text(n.displayDetail, style: const TextStyle(fontSize: 13)),
            const SizedBox(height: 4),
            Text(
              '${n.cwd}  ${n.timeAgo}',
              style: TextStyle(
                fontFamily: 'monospace',
                fontSize: 11,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHistoryCard(HeliosNotification n) {
    Color badgeColor;
    switch (n.status) {
      case 'approved':
        badgeColor = Colors.green;
        break;
      case 'denied':
        badgeColor = Theme.of(context).colorScheme.error;
        break;
      case 'answered':
        badgeColor = Colors.blue;
        break;
      default:
        badgeColor = Theme.of(context).colorScheme.outline;
    }

    return Card(
      margin: const EdgeInsets.only(bottom: 4),
      child: Opacity(
        opacity: 0.7,
        child: ListTile(
          onTap: () => _navigateToSession(n.sourceSession),
          dense: true,
          title: Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: badgeColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  n.status,
                  style: TextStyle(fontSize: 11, color: badgeColor),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  n.displayTitle,
                  style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w500),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          subtitle: Text(
            '${n.displayDetail}  ${n.timeAgo}${n.resolvedSource != null ? '  via ${n.resolvedSource}' : ''}',
            style: TextStyle(
              fontSize: 12,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ),
    );
  }
}
