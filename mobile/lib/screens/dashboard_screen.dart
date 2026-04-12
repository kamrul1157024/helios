import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/notification.dart';
import '../providers/card_registry.dart' as registry;
import '../providers/claude/notification_ext.dart';
import '../services/host_manager.dart';
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
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        if (!hm.notificationsLoaded) {
          return ListView(
            padding: const EdgeInsets.all(16),
            children: const [
              NotificationCardSkeleton(),
              NotificationCardSkeleton(),
              NotificationCardSkeleton(),
            ],
          );
        }

        final notifications = hm.filteredNotifications;

        final pendingActions = <HeliosNotification>[];
        final activeStatuses = <HeliosNotification>[];
        final resolved = <HeliosNotification>[];
        for (final n in notifications) {
          if (!n.isPending) {
            resolved.add(n);
          } else if (registry.needsAction(n)) {
            pendingActions.add(n);
          } else {
            activeStatuses.add(n);
          }
        }

        if (pendingActions.isEmpty && activeStatuses.isEmpty && resolved.isEmpty) {
          return _buildEmptyState();
        }

        return RefreshIndicator(
          onRefresh: () => hm.activeHostId != null
              ? hm.refreshHost(hm.activeHostId!)
              : hm.refreshAll(),
          child: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              if (pendingActions.isNotEmpty) _buildBatchActions(pendingActions, hm),
              if (pendingActions.isNotEmpty) ...[
                _sectionHeader('Pending (${pendingActions.length})'),
                ...pendingActions.map((n) => _buildNotificationCard(n, hm)),
                const SizedBox(height: 16),
              ],
              if (activeStatuses.isNotEmpty) ...[
                _sectionHeader('Active Sessions'),
                ...activeStatuses.map((n) => _buildStatusCard(n, hm)),
                const SizedBox(height: 16),
              ],
              if (resolved.isNotEmpty) ...[
                _sectionHeader('History'),
                ...resolved.map((n) => _buildHistoryCard(n, hm)),
              ],
            ],
          ),
        );
      },
    );
  }

  void _navigateToSession(String hostId, String sourceSession) {
    if (sourceSession.isEmpty) return;
    final hm = context.read<HostManager>();
    final service = hm.serviceFor(hostId);
    if (service == null) return;
    final match = service.sessions.where((s) => s.sessionId == sourceSession);
    if (match.isEmpty) return;
    Navigator.of(context).push(
      MaterialPageRoute(builder: (_) => SessionDetailScreen(session: match.first)),
    );
  }

  Widget _buildNotificationCard(HeliosNotification n, HostManager hm) {
    final service = hm.serviceFor(n.hostId);
    if (service == null) return const SizedBox.shrink();
    final card = registry.buildCardForType(
      notification: n,
      sse: service,
      selected: _selected,
      onSelectionChanged: () => setState(() {}),
    );
    return _wrapWithHostBar(n, hm, card ?? _buildStatusCard(n, hm));
  }

  Widget _wrapWithHostBar(HeliosNotification n, HostManager hm, Widget child, {double opacity = 0.4}) {
    final host = hm.hostById(n.hostId);
    final hostColor = host?.color ?? Theme.of(context).colorScheme.primary;

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(12),
        child: IntrinsicHeight(
          child: Row(
            children: [
              Container(width: 2, color: hostColor.withValues(alpha: opacity)),
              Expanded(child: child),
            ],
          ),
        ),
      ),
    );
  }

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

  Widget _buildBatchActions(List<HeliosNotification> pending, HostManager hm) {
    final permissionIds = pending.where((n) => n.isClaudePermission).toList();

    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        children: [
          if (permissionIds.isNotEmpty)
            FilledButton.tonal(
              onPressed: () {
                // Group by host and send batch to each
                final byHost = <String, List<String>>{};
                for (final n in permissionIds) {
                  byHost.putIfAbsent(n.hostId, () => []).add(n.id);
                }
                for (final entry in byHost.entries) {
                  hm.serviceFor(entry.key)?.batchAction(entry.value, {'action': 'approve'});
                }
              },
              child: Text('Approve All (${permissionIds.length})'),
            ),
          if (_selected.isNotEmpty) ...[
            const SizedBox(width: 8),
            OutlinedButton(
              onPressed: () {
                // Group selected by host
                final byHost = <String, List<String>>{};
                for (final id in _selected) {
                  final n = pending.where((n) => n.id == id).firstOrNull;
                  if (n != null) {
                    byHost.putIfAbsent(n.hostId, () => []).add(n.id);
                  }
                }
                for (final entry in byHost.entries) {
                  hm.serviceFor(entry.key)?.batchAction(entry.value, {'action': 'approve'});
                }
                setState(() => _selected.clear());
              },
              child: Text('Approve (${_selected.length})'),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildStatusCard(HeliosNotification n, HostManager hm) {
    final service = hm.serviceFor(n.hostId);
    final isError = n.type.endsWith('.error');
    final host = hm.hostById(n.hostId);
    final hostColor = host?.color ?? Theme.of(context).colorScheme.primary;
    final hostLabel = host?.label ?? '';

    return Card(
      margin: EdgeInsets.zero,
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
        onTap: () => _navigateToSession(n.hostId, n.sourceSession),
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
              onPressed: () => service?.dismissNotification(n.id),
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
            Row(
              children: [
                Expanded(
                  child: Text(
                    '${n.cwd}  ${n.timeAgo}',
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
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
    );
  }

  Widget _buildHistoryCard(HeliosNotification n, HostManager hm) {
    final host = hm.hostById(n.hostId);
    final hostColor = host?.color ?? Theme.of(context).colorScheme.primary;
    final hostLabel = host?.label ?? '';

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
      clipBehavior: Clip.antiAlias,
      child: Opacity(
        opacity: 0.7,
        child: IntrinsicHeight(
          child: Row(
            children: [
              Container(width: 2, color: hostColor.withValues(alpha: 0.3)),
              Expanded(
                child: ListTile(
                  onTap: () => _navigateToSession(n.hostId, n.sourceSession),
                  dense: true,
                  title: Row(
                    children: [
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                        decoration: BoxDecoration(
                          color: badgeColor.withValues(alpha: 0.15),
                          borderRadius: BorderRadius.circular(4),
                        ),
                        child: Text(n.status, style: TextStyle(fontSize: 11, color: badgeColor)),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          n.displayTitle,
                          style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w500),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      Text(
                        hostLabel,
                        style: TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: hostColor),
                      ),
                    ],
                  ),
                  subtitle: Text(
                    '${n.displayDetail}  ${n.timeAgo}${n.resolvedSource != null ? '  via ${n.resolvedSource}' : ''}',
                    style: TextStyle(fontSize: 12, color: Theme.of(context).colorScheme.onSurfaceVariant),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
