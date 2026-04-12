import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../models/message.dart';
import '../models/notification.dart';
import '../providers/card_registry.dart' as registry;
import '../providers/claude/notification_ext.dart';
import '../providers/claude/verbs.dart';
import '../services/host_manager.dart';
import '../services/daemon_api_service.dart';
import '../widgets/message_card.dart';
import '../widgets/skeleton.dart';

class SessionDetailScreen extends StatefulWidget {
  final Session session;

  const SessionDetailScreen({super.key, required this.session});

  @override
  State<SessionDetailScreen> createState() => _SessionDetailScreenState();
}

class _SessionDetailScreenState extends State<SessionDetailScreen>
    with SingleTickerProviderStateMixin {
  final _promptController = TextEditingController();
  final _scrollController = ScrollController();
  List<Message> _messages = [];
  bool _loading = true;
  bool _sending = false;
  int _total = 0;
  bool _hasMore = false;
  StreamSubscription<SSEEvent>? _eventSub;
  String _currentVerb = randomClaudeVerb();
  Timer? _verbTimer;
  late final AnimationController _breathController;

  @override
  void initState() {
    super.initState();
    _breathController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 2500),
    )..repeat(reverse: true);
    _loadTranscript();
    _verbTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      if (mounted) setState(() => _currentVerb = randomClaudeVerb());
    });
    final sse = context.read<HostManager>().serviceFor(widget.session.hostId);
    _eventSub = sse?.events.listen((event) {
      if (event.data is Map) {
        final data = event.data as Map;
        // Refresh on session status changes and notification events for this session
        if (event.type == 'session_status' &&
            data['session_id'] == widget.session.sessionId) {
          _loadTranscript();
        }
        if (event.type == 'notification' || event.type == 'notification_resolved') {
          // Notifications refresh via DaemonAPIService.fetchNotifications — just rebuild
          if (mounted) setState(() {});
        }
      }
    });
  }

  @override
  void dispose() {
    _breathController.dispose();
    _promptController.dispose();
    _scrollController.dispose();
    _eventSub?.cancel();
    _verbTimer?.cancel();
    super.dispose();
  }

  DaemonAPIService? get _sse => context.read<HostManager>().serviceFor(widget.session.hostId);

  Future<void> _loadTranscript() async {
    final sse = _sse;
    if (sse == null) return;
    final result = await sse.fetchTranscript(widget.session.sessionId, limit: 200);
    if (result != null && mounted) {
      setState(() {
        _messages = result.messages;
        _total = result.total;
        _hasMore = result.hasMore;
        _loading = false;
      });
    } else if (mounted) {
      setState(() => _loading = false);
    }
  }

  Future<void> _sendPrompt() async {
    final text = _promptController.text.trim();
    if (text.isEmpty) return;

    setState(() => _sending = true);
    final sse = _sse;
    final ok = sse != null && await sse.sendSessionPrompt(widget.session.sessionId, text);
    if (ok && mounted) {
      _promptController.clear();
      await Future.delayed(const Duration(milliseconds: 500));
      await _loadTranscript();
    } else if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to send prompt'), duration: Duration(seconds: 2)),
      );
    }
    if (mounted) setState(() => _sending = false);
  }

  void _showRenameDialog(Session session) {
    final controller = TextEditingController(text: session.title ?? '');
    showDialog(
      context: context,
      builder: (ctx) {
        return AlertDialog(
          title: const Text('Rename session'),
          content: TextField(
            controller: controller,
            autofocus: true,
            decoration: InputDecoration(
              hintText: session.lastUserMessage ?? 'Session title',
              border: OutlineInputBorder(borderRadius: BorderRadius.circular(8)),
            ),
            onSubmitted: (_) {
              Navigator.pop(ctx);
              final title = controller.text.trim();
              _sse?.patchSession(widget.session.sessionId, title: title);
            },
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                Navigator.pop(ctx);
                final title = controller.text.trim();
                _sse?.patchSession(widget.session.sessionId, title: title);
              },
              child: const Text('Save'),
            ),
          ],
        );
      },
    ).then((_) => controller.dispose());
  }

  void _showNoTmuxInfo() {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        icon: Icon(Icons.warning_amber, color: Colors.amber.shade700),
        title: const Text('No tmux pane'),
        content: const Text(
          'This session was started outside Helios, so there is no tmux pane attached.\n\n'
          'Stop and pause controls are unavailable.\n\n'
          'Sending a prompt will open a new tmux pane, but live bidirectional updates '
          'won\'t be available until then.',
        ),
        actions: [
          FilledButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }

  Future<void> _stop() async {
    await _sse?.stopSession(widget.session.sessionId);
  }

  Future<void> _suspend() async {
    await _sse?.suspendSession(widget.session.sessionId);
  }

  Future<void> _resume() async {
    await _sse?.resumeSession(widget.session.sessionId);
  }

  /// Get pending notifications for this session.
  List<HeliosNotification> _pendingNotifications(DaemonAPIService sse) {
    return sse.notifications
        .where((n) => n.sourceSession == widget.session.sessionId && n.isPending)
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        final sse = hm.serviceFor(widget.session.hostId);
        final session = sse?.sessions.firstWhere(
          (s) => s.sessionId == widget.session.sessionId,
          orElse: () => widget.session,
        ) ?? widget.session;
        final pendingNotifs = sse != null ? _pendingNotifications(sse) : <HeliosNotification>[];

        return Scaffold(
          appBar: AppBar(
            title: GestureDetector(
              onTap: () => _showRenameDialog(session),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    session.displayTitle,
                    style: const TextStyle(fontSize: 14),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  Text(
                    '${_statusLabel(session.status)} ${session.model ?? ''} · ${session.shortCwd}',
                    style: TextStyle(
                      fontSize: 11,
                      color: _statusColor(session.status, Theme.of(context)),
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            actions: _buildActions(session),
          ),
          body: Column(
            children: [
              // Messages
              Expanded(
                child: _loading
                    ? const MessageListSkeleton()
                    : _messages.isEmpty && pendingNotifs.isEmpty
                        ? _buildEmptyTranscript()
                        : _buildMessageList(),
              ),
              // Inline HITL: pending notifications for this session
              if (pendingNotifs.isNotEmpty && sse != null)
                _buildInlineNotifications(pendingNotifs, sse),
              // Prompt bar
              _buildPromptBar(session),
            ],
          ),
        );
      },
    );
  }

  Widget _buildInlineNotifications(List<HeliosNotification> notifs, DaemonAPIService sse) {
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

  Widget _buildInlineNotifCard(HeliosNotification n, DaemonAPIService sse) {
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

    if (!session.hasTmux && !session.isEnded) {
      actions.add(
        IconButton(
          icon: Icon(Icons.warning_amber, color: Colors.amber.shade700),
          tooltip: 'No tmux pane',
          onPressed: _showNoTmuxInfo,
        ),
      );
    }

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
    // reverse: true renders from bottom up — newest messages visible immediately.
    // Items are indexed in reverse order, so index 0 = last message.
    final itemCount = _messages.length + (_hasMore ? 1 : 0);
    return ListView.builder(
      controller: _scrollController,
      reverse: true,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      itemCount: itemCount,
      itemBuilder: (context, index) {
        // Last item in reversed list = "earlier messages" banner
        if (_hasMore && index == itemCount - 1) {
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
        // Reverse the index: index 0 → last message, index N → first message
        final msgIndex = _messages.length - 1 - index;
        return MessageCard(message: _messages[msgIndex]);
      },
    );
  }

  void _showCommandSheet() {
    final commands = _sse?.commands ?? [];
    if (commands.isEmpty) return;

    showModalBottomSheet(
      context: context,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Padding(
                padding: const EdgeInsets.all(16),
                child: Text(
                  'Commands',
                  style: Theme.of(ctx).textTheme.titleSmall,
                ),
              ),
              ...commands.map((cmd) => ListTile(
                leading: Icon(_iconForCommand(cmd.icon)),
                title: Text(cmd.name, style: const TextStyle(fontFamily: 'monospace', fontWeight: FontWeight.w600)),
                subtitle: Text(cmd.description, style: const TextStyle(fontSize: 12)),
                onTap: () {
                  Navigator.pop(ctx);
                  _sendCommand(cmd.name);
                },
              )),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  Future<void> _sendCommand(String command) async {
    setState(() => _sending = true);
    final sse = _sse;
    final ok = sse != null && await sse.sendSessionPrompt(widget.session.sessionId, command);
    if (!ok && mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Failed to send command'), duration: Duration(seconds: 2)),
      );
    }
    if (mounted) setState(() => _sending = false);
  }

  IconData _iconForCommand(String icon) {
    switch (icon) {
      case 'compress':
        return Icons.compress;
      case 'rate_review':
        return Icons.rate_review;
      case 'payments':
        return Icons.payments;
      case 'info':
        return Icons.info_outline;
      case 'health_and_safety':
        return Icons.health_and_safety;
      case 'memory':
        return Icons.memory;
      case 'clear_all':
        return Icons.clear_all;
      case 'swap_horiz':
        return Icons.swap_horiz;
      default:
        return Icons.terminal;
    }
  }

  Widget _buildPromptBar(Session session) {
    final canSend = session.canSendPrompt;
    final isQueueing = session.isQueueing;
    final theme = Theme.of(context);
    final hasCommands = (_sse?.commands ?? []).isNotEmpty;

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
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Verb animation above the input when queueing
          if (isQueueing)
            AnimatedBuilder(
              animation: _breathController,
              builder: (context, _) {
                final t = _breathController.value;
                final accentColor = theme.colorScheme.primary;
                final verbColor = Color.lerp(
                  theme.colorScheme.onSurfaceVariant,
                  accentColor,
                  0.3 + 0.3 * t,
                )!;
                return Padding(
                  padding: const EdgeInsets.only(bottom: 6),
                  child: AnimatedSwitcher(
                    duration: const Duration(milliseconds: 400),
                    transitionBuilder: (child, animation) {
                      final slideIn = Tween<Offset>(
                        begin: const Offset(0, 0.5),
                        end: Offset.zero,
                      ).animate(CurvedAnimation(parent: animation, curve: Curves.easeOut));
                      return SlideTransition(
                        position: slideIn,
                        child: FadeTransition(opacity: animation, child: child),
                      );
                    },
                    child: Text(
                      '$_currentVerb...',
                      key: ValueKey(_currentVerb),
                      style: TextStyle(
                        fontSize: 12,
                        color: verbColor,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
                );
              },
            ),
          Row(
            children: [
              if (hasCommands)
                IconButton(
                  onPressed: canSend && !_sending ? _showCommandSheet : null,
                  icon: const Text('/', style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold)),
                  tooltip: 'Commands',
                ),
              Expanded(
                child: AnimatedBuilder(
                  animation: _breathController,
                  builder: (context, _) {
                    final isBreathing = session.isActive && !canSend;
                    final t = _breathController.value;

                    // Morph border radius: pill (24) -> squircle (16) -> pill
                    final breathingAnim = isBreathing || isQueueing;
                    final radius = breathingAnim ? 24.0 - 8.0 * t : 24.0;

                    final accentColor = theme.colorScheme.primary;

                    // Verb text: fade between muted and slightly tinted
                    final verbColor = isBreathing
                        ? Color.lerp(
                            theme.colorScheme.onSurfaceVariant,
                            accentColor,
                            0.3 + 0.3 * t,
                          )!
                        : theme.colorScheme.onSurfaceVariant;

                    return Stack(
                      alignment: Alignment.centerLeft,
                      children: [
                        TextField(
                          controller: _promptController,
                          enabled: canSend && !_sending,
                          textInputAction: TextInputAction.send,
                          onSubmitted: (_) => canSend ? _sendPrompt() : null,
                          maxLines: 3,
                          minLines: 1,
                          decoration: InputDecoration(
                            hintText: isQueueing
                                ? 'Queue a prompt...'
                                : canSend
                                    ? 'Send a prompt...'
                                    : session.isActive
                                        ? ''
                                        : 'Session ${session.status}',
                            border: OutlineInputBorder(
                              borderRadius: BorderRadius.circular(radius),
                              borderSide: BorderSide.none,
                            ),
                            enabledBorder: OutlineInputBorder(
                              borderRadius: BorderRadius.circular(radius),
                              borderSide: BorderSide.none,
                            ),
                            disabledBorder: OutlineInputBorder(
                              borderRadius: BorderRadius.circular(radius),
                              borderSide: BorderSide.none,
                            ),
                            filled: true,
                            fillColor: theme.colorScheme.surfaceContainerHighest,
                            contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                            isDense: true,
                          ),
                          style: const TextStyle(fontSize: 14),
                        ),
                        // Verb animation inside the disabled field (non-queue active sessions)
                        if (isBreathing)
                          Padding(
                            padding: const EdgeInsets.only(left: 16),
                            child: IgnorePointer(
                              child: AnimatedSwitcher(
                                duration: const Duration(milliseconds: 400),
                                transitionBuilder: (child, animation) {
                                  final slideIn = Tween<Offset>(
                                    begin: const Offset(0, 0.5),
                                    end: Offset.zero,
                                  ).animate(CurvedAnimation(parent: animation, curve: Curves.easeOut));
                                  return SlideTransition(
                                    position: slideIn,
                                    child: FadeTransition(opacity: animation, child: child),
                                  );
                                },
                                child: Text(
                                  '$_currentVerb...',
                                  key: ValueKey(_currentVerb),
                                  style: TextStyle(
                                    fontSize: 14,
                                    color: verbColor,
                                    fontWeight: FontWeight.w500,
                                  ),
                                ),
                              ),
                            ),
                          ),
                      ],
                    );
                  },
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
