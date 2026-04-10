import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/notification.dart';
import '../services/auth_service.dart';
import '../services/sse_service.dart';
import '../services/notification_service.dart';
import 'setup_screen.dart';

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({super.key});

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> with WidgetsBindingObserver {
  late SSEService _sse;
  StreamSubscription<SSEEvent>? _eventSub;
  final Set<String> _selected = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    _sse = context.read<SSEService>();
    final auth = context.read<AuthService>();
    _sse.attach(auth);
    _sse.fetchNotifications();
    _sse.start();

    // Request notification permission
    NotificationService.instance.requestPermission();

    // Listen to SSE events for local notifications
    NotificationService.instance.onAction = _handleNotificationAction;
    _eventSub = _sse.events.listen(_handleSSEEvent);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _sse.fetchNotifications();
    }
  }

  void _handleSSEEvent(SSEEvent event) {
    if (event.type == 'notification' && event.data is Map) {
      final data = event.data as Map;
      final type = data['type']?.toString() ?? '';
      final id = data['id']?.toString() ?? '';

      if (type == 'permission') {
        NotificationService.instance.showPermissionNotification(
          id: id,
          toolName: data['tool_name']?.toString() ?? 'Unknown tool',
          detail: data['detail']?.toString() ?? data['tool_input']?.toString() ?? 'Permission requested',
        );
      } else {
        NotificationService.instance.showNotification(
          id: id,
          title: 'helios',
          body: data['detail']?.toString() ?? type,
        );
      }
    }
  }

  void _handleNotificationAction(String notificationId, String action) {
    if (action == 'approve') {
      _sse.approveNotification(notificationId);
    } else if (action == 'deny') {
      _sse.denyNotification(notificationId);
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _eventSub?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('helios'),
        centerTitle: true,
        actions: [
          // Connection indicator
          Consumer<SSEService>(
            builder: (_, sse, _) => Padding(
              padding: const EdgeInsets.only(right: 8),
              child: Icon(
                Icons.circle,
                size: 10,
                color: sse.connected ? Colors.green : Colors.grey,
              ),
            ),
          ),
          PopupMenuButton<String>(
            onSelected: (value) async {
              if (value == 'logout') {
                _sse.stop();
                final auth = context.read<AuthService>();
                final nav = Navigator.of(context);
                await auth.logout();
                if (mounted) {
                  nav.pushReplacement(
                    MaterialPageRoute(builder: (_) => const SetupScreen()),
                  );
                }
              }
            },
            itemBuilder: (_) => [
              const PopupMenuItem(value: 'logout', child: Text('Disconnect')),
            ],
          ),
        ],
      ),
      body: Consumer<SSEService>(
        builder: (context, sse, _) {
          final notifications = sse.notifications;

          final pendingPermissions = notifications
              .where((n) => n.isPending && n.isPermission)
              .toList();
          final activeStatuses = notifications
              .where((n) => n.isPending && !n.isPermission)
              .toList();
          final resolved = notifications
              .where((n) => !n.isPending)
              .toList();

          if (pendingPermissions.isEmpty && activeStatuses.isEmpty && resolved.isEmpty) {
            return _buildEmptyState();
          }

          return RefreshIndicator(
            onRefresh: sse.fetchNotifications,
            child: ListView(
              padding: const EdgeInsets.all(16),
              children: [
                // Batch actions
                if (pendingPermissions.isNotEmpty)
                  _buildBatchActions(pendingPermissions),

                // Pending permissions
                if (pendingPermissions.isNotEmpty) ...[
                  _sectionHeader('Pending Permissions (${pendingPermissions.length})'),
                  ...pendingPermissions.map(_buildPermissionCard),
                  const SizedBox(height: 16),
                ],

                // Active sessions
                if (activeStatuses.isNotEmpty) ...[
                  _sectionHeader('Active Sessions'),
                  ...activeStatuses.map(_buildStatusCard),
                  const SizedBox(height: 16),
                ],

                // History
                if (resolved.isNotEmpty) ...[
                  _sectionHeader('History'),
                  ...resolved.map(_buildHistoryCard),
                ],
              ],
            ),
          );
        },
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

  Widget _buildBatchActions(List<HeliosNotification> pending) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        children: [
          FilledButton.tonal(
            onPressed: () {
              final ids = pending.map((n) => n.id).toList();
              _sse.batchAction(ids, 'approve');
            },
            child: Text('Approve All (${pending.length})'),
          ),
          if (_selected.isNotEmpty) ...[
            const SizedBox(width: 8),
            OutlinedButton(
              onPressed: () {
                _sse.batchAction(_selected.toList(), 'approve');
                setState(() => _selected.clear());
              },
              child: Text('Approve (${_selected.length})'),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildPermissionCard(HeliosNotification n) {
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: Colors.orange.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Checkbox(
                  value: _selected.contains(n.id),
                  onChanged: (v) {
                    setState(() {
                      if (v == true) {
                        _selected.add(n.id);
                      } else {
                        _selected.remove(n.id);
                      }
                    });
                  },
                ),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: Colors.orange.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.orange.withValues(alpha: 0.3)),
                  ),
                  child: const Text('permission', style: TextStyle(fontSize: 11, color: Colors.orange)),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.displayName,
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(8),
              ),
              constraints: const BoxConstraints(maxHeight: 100),
              child: SingleChildScrollView(
                child: Text(
                  n.displayDetail,
                  style: TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 12,
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: Text(
                    n.cwd,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                Text(
                  n.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: FilledButton(
                    onPressed: () => _sse.approveNotification(n.id),
                    child: const Text('Approve'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: FilledButton(
                    onPressed: () => _sse.denyNotification(n.id),
                    style: FilledButton.styleFrom(
                      backgroundColor: Theme.of(context).colorScheme.error,
                      foregroundColor: Theme.of(context).colorScheme.onError,
                    ),
                    child: const Text('Deny'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildStatusCard(HeliosNotification n) {
    final isError = n.type == 'error';
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
                n.displayName,
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
              onPressed: () => _sse.dismissNotification(n.id),
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
      default:
        badgeColor = Theme.of(context).colorScheme.outline;
    }

    return Card(
      margin: const EdgeInsets.only(bottom: 4),
      child: Opacity(
        opacity: 0.7,
        child: ListTile(
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
                  n.displayName,
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
