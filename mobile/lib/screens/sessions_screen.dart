import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../services/sse_service.dart';
import '../widgets/skeleton.dart';
import 'session_detail_screen.dart';

class SessionsScreen extends StatefulWidget {
  const SessionsScreen({super.key});

  @override
  State<SessionsScreen> createState() => _SessionsScreenState();
}

class _SessionsScreenState extends State<SessionsScreen> {
  @override
  void initState() {
    super.initState();
    context.read<SSEService>().fetchSessions();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<SSEService>(
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

        // Backend already returns sessions sorted by last activity (most recent first)
        return RefreshIndicator(
          onRefresh: sse.fetchSessions,
          child: ListView.builder(
            padding: const EdgeInsets.all(12),
            itemCount: sessions.length,
            itemBuilder: (context, index) => _buildSessionCard(sessions[index]),
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

  Widget _buildSessionCard(Session session) {
    final theme = Theme.of(context);
    final statusColor = _statusColor(session.status, theme);
    final statusIcon = _statusIcon(session.status);

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: session.isActive
            ? BorderSide(color: statusColor.withValues(alpha: 0.4), width: 1.5)
            : BorderSide.none,
      ),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: () {
          Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) => SessionDetailScreen(session: session),
            ),
          );
        },
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // Top row: status badge + model + time
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
                  if (session.model != null) ...[
                    const SizedBox(width: 8),
                    Text(
                      session.model!,
                      style: TextStyle(
                        fontSize: 11,
                        color: theme.colorScheme.onSurfaceVariant,
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
              // CWD
              Text(
                session.shortCwd,
                style: TextStyle(
                  fontSize: 13,
                  fontFamily: 'monospace',
                  color: theme.colorScheme.onSurface,
                ),
                overflow: TextOverflow.ellipsis,
              ),
              // Last event
              if (session.lastEvent != null) ...[
                const SizedBox(height: 4),
                Text(
                  session.lastEvent!,
                  style: TextStyle(
                    fontSize: 12,
                    color: theme.colorScheme.onSurfaceVariant,
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
      ),
    );
  }

  Color _statusColor(String status, ThemeData theme) {
    switch (status) {
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
