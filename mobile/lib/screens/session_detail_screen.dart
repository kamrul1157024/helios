import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../models/message.dart';
import '../models/notification.dart';
import '../providers/card_registry.dart' as registry;
import '../providers/claude/notification_ext.dart';
import '../services/sse_service.dart';
import '../widgets/message_card.dart';

class SessionDetailScreen extends StatefulWidget {
  final Session session;

  const SessionDetailScreen({super.key, required this.session});

  @override
  State<SessionDetailScreen> createState() => _SessionDetailScreenState();
}

class _SessionDetailScreenState extends State<SessionDetailScreen> {
  final _promptController = TextEditingController();
  final _scrollController = ScrollController();
  List<Message> _messages = [];
  bool _loading = true;
  bool _sending = false;
  int _total = 0;
  bool _hasMore = false;
  StreamSubscription<SSEEvent>? _eventSub;

  @override
  void initState() {
    super.initState();
    _loadTranscript();
    _eventSub = context.read<SSEService>().events.listen((event) {
      if (event.data is Map) {
        final data = event.data as Map;
        // Refresh on session status changes and notification events for this session
        if (event.type == 'session_status' &&
            data['session_id'] == widget.session.sessionId) {
          _loadTranscript();
        }
        if (event.type == 'notification' || event.type == 'notification_resolved') {
          // Notifications refresh via SSEService.fetchNotifications — just rebuild
          if (mounted) setState(() {});
        }
      }
    });
  }

  @override
  void dispose() {
    _promptController.dispose();
    _scrollController.dispose();
    _eventSub?.cancel();
    super.dispose();
  }

  Future<void> _loadTranscript() async {
    final sse = context.read<SSEService>();
    final result = await sse.fetchTranscript(widget.session.sessionId, limit: 200);
    if (result != null && mounted) {
      setState(() {
        _messages = result.messages;
        _total = result.total;
        _hasMore = result.hasMore;
        _loading = false;
      });
      _scrollToBottom();
    } else if (mounted) {
      setState(() => _loading = false);
    }
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.animateTo(
          _scrollController.position.maxScrollExtent,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        );
      }
    });
  }

  Future<void> _sendPrompt() async {
    final text = _promptController.text.trim();
    if (text.isEmpty) return;

    setState(() => _sending = true);
    final sse = context.read<SSEService>();
    final ok = await sse.sendSessionPrompt(widget.session.sessionId, text);
    if (ok && mounted) {
      _promptController.clear();
      await Future.delayed(const Duration(milliseconds: 500));
      await _loadTranscript();
    }
    if (mounted) setState(() => _sending = false);
  }

  Future<void> _stop() async {
    final sse = context.read<SSEService>();
    await sse.stopSession(widget.session.sessionId);
  }

  Future<void> _suspend() async {
    final sse = context.read<SSEService>();
    await sse.suspendSession(widget.session.sessionId);
  }

  Future<void> _resume() async {
    final sse = context.read<SSEService>();
    await sse.resumeSession(widget.session.sessionId);
  }

  /// Get pending notifications for this session.
  List<HeliosNotification> _pendingNotifications(SSEService sse) {
    return sse.notifications
        .where((n) => n.sourceSession == widget.session.sessionId && n.isPending)
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<SSEService>(
      builder: (context, sse, _) {
        final session = sse.sessions.firstWhere(
          (s) => s.sessionId == widget.session.sessionId,
          orElse: () => widget.session,
        );
        final pendingNotifs = _pendingNotifications(sse);

        return Scaffold(
          appBar: AppBar(
            title: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(session.shortCwd, style: const TextStyle(fontSize: 14)),
                Text(
                  '${_statusLabel(session.status)} ${session.model ?? ''}',
                  style: TextStyle(
                    fontSize: 11,
                    color: _statusColor(session.status, Theme.of(context)),
                  ),
                ),
              ],
            ),
            actions: _buildActions(session),
          ),
          body: Column(
            children: [
              // Messages
              Expanded(
                child: _loading
                    ? const Center(child: CircularProgressIndicator())
                    : _messages.isEmpty && pendingNotifs.isEmpty
                        ? _buildEmptyTranscript()
                        : _buildMessageList(),
              ),
              // Inline HITL: pending notifications for this session
              if (pendingNotifs.isNotEmpty)
                _buildInlineNotifications(pendingNotifs, sse),
              // Prompt bar
              _buildPromptBar(session),
            ],
          ),
        );
      },
    );
  }

  Widget _buildInlineNotifications(List<HeliosNotification> notifs, SSEService sse) {
    final theme = Theme.of(context);
    return Container(
      decoration: BoxDecoration(
        color: theme.colorScheme.errorContainer.withValues(alpha: 0.3),
        border: Border(
          top: BorderSide(color: Colors.orange.withValues(alpha: 0.5)),
        ),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Batch approve all button
          if (notifs.length > 1)
            Padding(
              padding: const EdgeInsets.only(left: 12, right: 12, top: 8),
              child: SizedBox(
                width: double.infinity,
                child: FilledButton.tonal(
                  onPressed: () {
                    final ids = notifs
                        .where((n) => n.isClaudePermission)
                        .map((n) => n.id)
                        .toList();
                    if (ids.isNotEmpty) {
                      sse.batchAction(ids, {'action': 'approve'});
                    }
                  },
                  child: Text('Approve All (${notifs.length})'),
                ),
              ),
            ),
          // Individual notification cards
          ...notifs.map((n) => _buildInlineNotifCard(n, sse)),
        ],
      ),
    );
  }

  Widget _buildInlineNotifCard(HeliosNotification n, SSEService sse) {
    // Try to use the provider-specific card
    final card = registry.buildCardForType(
      notification: n,
      sse: sse,
      selected: const {},
      onSelectionChanged: () {},
    );
    if (card != null) {
      return Padding(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        child: card,
      );
    }

    // Fallback: simple approve/deny card
    final theme = Theme.of(context);
    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      color: theme.colorScheme.surface,
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              n.displayTitle,
              style: TextStyle(
                fontSize: 13,
                fontWeight: FontWeight.w600,
                color: theme.colorScheme.onSurface,
              ),
            ),
            const SizedBox(height: 4),
            Text(
              n.displayDetail,
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(height: 8),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                OutlinedButton(
                  onPressed: () => sse.sendAction(n.id, {'action': 'deny'}),
                  child: const Text('Deny'),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: () => sse.sendAction(n.id, {'action': 'approve'}),
                  child: const Text('Approve'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  List<Widget> _buildActions(Session session) {
    final actions = <Widget>[];

    if (session.canStop) {
      actions.add(
        IconButton(
          icon: const Icon(Icons.stop),
          tooltip: 'Stop (Escape)',
          onPressed: _stop,
        ),
      );
    }

    if (session.canSuspend) {
      actions.add(
        IconButton(
          icon: const Icon(Icons.pause),
          tooltip: 'Suspend (Ctrl+C)',
          onPressed: _suspend,
        ),
      );
    }

    if (session.canResume) {
      actions.add(
        IconButton(
          icon: const Icon(Icons.play_arrow),
          tooltip: 'Resume',
          onPressed: _resume,
        ),
      );
    }

    return actions;
  }

  Widget _buildEmptyTranscript() {
    return Center(
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(
            Icons.chat_bubble_outline,
            size: 48,
            color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
          ),
          const SizedBox(height: 16),
          Text(
            'No messages yet.',
            style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
          ),
          if (_total > 0) ...[
            const SizedBox(height: 4),
            Text(
              'Transcript has $_total entries but none could be loaded.',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildMessageList() {
    return ListView.builder(
      controller: _scrollController,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      itemCount: _messages.length + (_hasMore ? 1 : 0),
      itemBuilder: (context, index) {
        if (_hasMore && index == 0) {
          return Center(
            child: Padding(
              padding: const EdgeInsets.all(8),
              child: Text(
                '${_total - _messages.length} earlier messages',
                style: TextStyle(
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          );
        }
        final msgIndex = _hasMore ? index - 1 : index;
        return MessageCard(message: _messages[msgIndex]);
      },
    );
  }

  Widget _buildPromptBar(Session session) {
    final canSend = session.canSendPrompt;
    final theme = Theme.of(context);

    return Container(
      padding: EdgeInsets.only(
        left: 12,
        right: 8,
        top: 8,
        bottom: MediaQuery.of(context).padding.bottom + 8,
      ),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        border: Border(
          top: BorderSide(color: theme.colorScheme.outlineVariant),
        ),
      ),
      child: Row(
        children: [
          Expanded(
            child: TextField(
              controller: _promptController,
              enabled: canSend && !_sending,
              textInputAction: TextInputAction.send,
              onSubmitted: (_) => canSend ? _sendPrompt() : null,
              maxLines: 3,
              minLines: 1,
              decoration: InputDecoration(
                hintText: canSend
                    ? 'Send a prompt...'
                    : session.isActive
                        ? 'Session is busy...'
                        : 'Session ${session.status}',
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(24),
                  borderSide: BorderSide.none,
                ),
                filled: true,
                fillColor: theme.colorScheme.surfaceContainerHighest,
                contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                isDense: true,
              ),
              style: const TextStyle(fontSize: 14),
            ),
          ),
          const SizedBox(width: 8),
          IconButton.filled(
            onPressed: canSend && !_sending ? _sendPrompt : null,
            icon: _sending
                ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                : const Icon(Icons.send, size: 20),
          ),
        ],
      ),
    );
  }

  Color _statusColor(String status, ThemeData theme) {
    switch (status) {
      case 'active':
        return Colors.green;
      case 'waiting_permission':
        return Colors.orange;
      case 'idle':
        return Colors.blue;
      case 'error':
        return theme.colorScheme.error;
      case 'suspended':
        return Colors.purple;
      case 'ended':
        return theme.colorScheme.outline;
      default:
        return theme.colorScheme.outline;
    }
  }

  String _statusLabel(String status) {
    switch (status) {
      case 'active':
        return 'Active';
      case 'waiting_permission':
        return 'Needs Approval';
      case 'idle':
        return 'Idle';
      case 'error':
        return 'Error';
      case 'suspended':
        return 'Suspended';
      case 'ended':
        return 'Ended';
      default:
        return status;
    }
  }
}
