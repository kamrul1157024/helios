import 'dart:async';
import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import 'package:provider/provider.dart';
import '../models/session.dart';
import '../models/message.dart';
import '../models/notification.dart';
import '../models/provider.dart';
import '../providers/card_registry.dart' as registry;
import '../providers/claude/notification_ext.dart';
import '../providers/claude/verbs.dart';
import '../services/api_client.dart';
import '../services/host_manager.dart';
import '../services/daemon_api_service.dart';
import '../widgets/message_card.dart';
import '../services/speech_input_service.dart';
import '../utils/large_paste.dart';
import '../widgets/skeleton.dart';
import 'file_browser_screen.dart';
import 'git_status_screen.dart';

class SessionDetailScreen extends StatefulWidget {
  final Session session;

  const SessionDetailScreen({super.key, required this.session});

  @override
  State<SessionDetailScreen> createState() => _SessionDetailScreenState();
}

class _SessionDetailScreenState extends State<SessionDetailScreen>
    with SingleTickerProviderStateMixin {
  // Persisted across session switches (static = app-lifetime)
  static final _worktreeSelections =
      <String, String>{}; // sessionId → worktreePath
  static final _lastSubRoute =
      <String, _SubRoute>{}; // sessionId → last sub-screen

  final _promptController = TextEditingController();
  final List<UploadFile> _attachments = [];
  /// The block just pasted into the composer, while the offer to file it is up.
  String? _pastedBlock;
  String _lastPrompt = '';
  final _scrollController = ScrollController();
  List<Message> _messages = [];
  bool _loading = true;
  bool _sending = false;
  int _total = 0;
  bool _hasMore = false;
  bool _loadingOlder = false;

  /// Which parse the held seq numbers count against, quoted back when asking
  /// for a delta so the daemon can say when it no longer holds.
  String _epoch = '';

  /// Messages per request. A page is what fills a screen and a little more,
  /// not the whole conversation: the rest arrives as the reader scrolls.
  static const int _pageSize = 50;
  StreamSubscription<SSEEvent>? _eventSub;
  String _currentVerb = randomClaudeVerb();
  Timer? _verbTimer;
  Timer? _transcriptDebounce;
  List<Timer> _resendReads = [];
  late final AnimationController _breathController;
  bool _breathingActive = false;
  bool _isRecording = false;
  GitStatus? _gitStatus;
  List<Worktree> _worktrees = [];
  Map<String, GitStatus> _worktreeStatuses = {};
  String? _selectedWorktreePath;

  String get _effectiveCwd => _selectedWorktreePath ?? widget.session.cwd;

  @override
  void initState() {
    super.initState();
    _promptController.addListener(_watchForLargePaste);
    // Restore persisted worktree selection
    _selectedWorktreePath = _worktreeSelections[widget.session.sessionId];
    _breathController = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 2500),
    );
    if (widget.session.isActive) {
      _breathController.repeat(reverse: true);
      _breathingActive = true;
    }
    _loadTranscript();
    _loadGitStatus();
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
          debugPrint(
            '[Transcript][${widget.session.sessionId}] SSE session_status → read new messages (status=${data['status']})',
          );
          _transcriptDebounce?.cancel();
          _transcriptDebounce = Timer(const Duration(milliseconds: 500), () {
            _loadNewMessages();
          });
        }
        if (event.type == 'notification' ||
            event.type == 'notification_resolved') {
          if (mounted) setState(() {});
        }
      }
    });
    // Restore last sub-route after first frame
    _restoreSubRoute();
  }

  @override
  void dispose() {
    _breathController.dispose();
    _promptController.dispose();
    _scrollController.dispose();
    _eventSub?.cancel();
    _verbTimer?.cancel();
    _transcriptDebounce?.cancel();
    for (final timer in _resendReads) {
      timer.cancel();
    }
    if (_isRecording) SpeechInputService.instance.stopListening();
    super.dispose();
  }

  void _restoreSubRoute() {
    final saved = _lastSubRoute[widget.session.sessionId];
    if (saved == null) return;
    // Push after current frame so the base screen is mounted
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      switch (saved.type) {
        case _SubRouteType.gitStatus:
          _openGitStatus(widget.session);
          break;
        case _SubRouteType.fileBrowser:
          _openFileBrowser(widget.session);
          break;
      }
    });
  }

  void _updateBreathAnimation(Session session) {
    final shouldAnimate = session.isActive;
    if (shouldAnimate && !_breathingActive) {
      _breathController.repeat(reverse: true);
      _breathingActive = true;
    } else if (!shouldAnimate && _breathingActive) {
      _breathController.stop();
      _breathController.reset();
      _breathingActive = false;
    }
  }

  DaemonAPIService? get _sse =>
      context.read<HostManager>().serviceFor(widget.session.hostId);

  Future<void> _loadTranscript() async {
    final sid = widget.session.sessionId;
    debugPrint(
      '[Transcript][$sid] _loadTranscript start, _loading=$_loading messages=${_messages.length}',
    );
    final sse = _sse;
    if (sse == null) {
      debugPrint('[Transcript][$sid] no SSE service, aborting');
      return;
    }
    final result = await sse.fetchTranscript(sid, limit: _pageSize);
    debugPrint(
      '[Transcript][$sid] fetchTranscript result=${result == null ? "null" : "total=${result.total} returned=${result.messages.length} hasMore=${result.hasMore}"}',
    );
    if (result != null && mounted) {
      setState(() {
        _messages = result.messages;
        _total = result.total;
        _hasMore = result.hasMore;
        _epoch = result.epoch;
        _loading = false;
      });
    } else if (mounted) {
      debugPrint(
        '[Transcript][$sid] result null — setting loading=false, messages unchanged (${_messages.length})',
      );
      setState(() => _loading = false);
    }
  }

  /// Pulls what the agent has written since the last message held.
  ///
  /// A status event fires several times a turn, and the answer to it is
  /// usually one message. Asking for the page again would rebuild the list and
  /// throw away where the reader was.
  Future<void> _loadNewMessages() async {
    final sse = _sse;
    if (sse == null) return;
    if (_messages.isEmpty || _epoch.isEmpty) {
      await _loadTranscript();
      return;
    }

    final result = await sse.fetchTranscript(
      widget.session.sessionId,
      limit: _pageSize,
      afterSeq: _messages.last.seq,
      epoch: _epoch,
    );
    if (result == null || !mounted) return;

    setState(() {
      if (result.epochChanged) {
        // The transcript those seq numbers counted against is gone — forked,
        // or replaced. Start from what is there now.
        _messages = result.messages;
        _epoch = result.epoch;
        _hasMore = result.hasMore;
      } else if (result.messages.isNotEmpty) {
        _messages = [..._messages, ...result.messages];
      }
      _total = result.total;
      _loading = false;
    });
  }

  /// Reads the page before the oldest message held, for a reader scrolling
  /// back through the conversation.
  Future<void> _loadOlder() async {
    final sse = _sse;
    if (sse == null || _loadingOlder || !_hasMore) return;
    setState(() => _loadingOlder = true);

    final result = await sse.fetchTranscript(
      widget.session.sessionId,
      limit: _pageSize,
      offset: _messages.length,
    );
    if (!mounted) return;

    setState(() {
      if (result != null) {
        _messages = [...result.messages, ..._messages];
        _hasMore = result.hasMore;
        _total = result.total;
      }
      _loadingOlder = false;
    });
  }

  Future<void> _loadGitStatus() async {
    final svc = _sse;
    if (svc == null) return;
    // First get git status — server resolves to git root from any subdirectory
    final status = await svc.gitStatus(_effectiveCwd);
    if (!mounted) return;
    setState(() => _gitStatus = status);

    // Use resolved git root for worktree listing
    final gitRoot = status?.root ?? widget.session.cwd;
    final worktrees = await svc.gitWorktrees(gitRoot);
    if (!mounted) return;
    setState(() => _worktrees = worktrees);

    // Fetch git status for each worktree in parallel (for diff counts)
    if (worktrees.length > 1) {
      final statuses = await Future.wait(
        worktrees.map((wt) => svc.gitStatus(wt.path)),
      );
      if (!mounted) return;
      final map = <String, GitStatus>{};
      for (var i = 0; i < worktrees.length; i++) {
        final s = statuses[i];
        if (s != null) map[worktrees[i].path] = s;
      }
      setState(() => _worktreeStatuses = map);
    }
  }

  /// Picks attachments, from the gallery, the camera, or the file browser.
  Future<void> _attach(_AttachSource source) async {
    try {
      final picked = <UploadFile>[];
      if (source == _AttachSource.files) {
        final result = await FilePicker.pickFiles(
          allowMultiple: true,
          withData: true,
        );
        for (final file in result?.files ?? <PlatformFile>[]) {
          if (file.bytes != null) {
            picked.add(UploadFile(name: file.name, bytes: file.bytes!));
          }
        }
      } else {
        final image = await ImagePicker().pickImage(
          source: source == _AttachSource.camera
              ? ImageSource.camera
              : ImageSource.gallery,
        );
        if (image != null) {
          picked.add(
            UploadFile(name: image.name, bytes: await image.readAsBytes()),
          );
        }
      }
      if (picked.isNotEmpty && mounted) {
        setState(() => _attachments.addAll(picked));
      }
    } catch (e) {
      debugPrint('[attach] $e');
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Could not read that file')),
        );
      }
    }
  }

  void _showAttachSheet() {
    showModalBottomSheet(
      context: context,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.photo_library_outlined),
              title: const Text('Photo library'),
              onTap: () {
                Navigator.pop(ctx);
                _attach(_AttachSource.gallery);
              },
            ),
            ListTile(
              leading: const Icon(Icons.photo_camera_outlined),
              title: const Text('Camera'),
              onTap: () {
                Navigator.pop(ctx);
                _attach(_AttachSource.camera);
              },
            ),
            ListTile(
              leading: const Icon(Icons.attach_file),
              title: const Text('File'),
              onTap: () {
                Navigator.pop(ctx);
                _attach(_AttachSource.files);
              },
            ),
          ],
        ),
      ),
    );
  }

  /// Notices a block arriving in one edit, which on a phone means a paste.
  void _watchForLargePaste() {
    final before = _lastPrompt;
    final after = _promptController.text;
    _lastPrompt = after;
    final inserted = insertedText(before, after);
    if (inserted == null || !isLargePaste(inserted)) return;
    setState(() => _pastedBlock = inserted);
  }

  /// Moves the pasted block out of the composer and into an attachment.
  void _filePastedBlock() {
    final block = _pastedBlock;
    if (block == null) return;
    setState(() {
      _attachments.add(pastedTextFile(block));
      _promptController.text = removeFirst(_promptController.text, block);
      _promptController.selection = TextSelection.fromPosition(
        TextPosition(offset: _promptController.text.length),
      );
      _pastedBlock = null;
    });
  }

  Future<void> _sendPrompt() async {
    final text = _promptController.text.trim();
    if (text.isEmpty && _attachments.isEmpty) return;

    setState(() => _sending = true);
    final sse = _sse;

    // Upload first. A prompt naming a path the daemon never stored sends the
    // agent looking for a file that is not there. Only what has not been
    // stored yet: the send below may have failed once already, and uploading
    // the same bytes again would leave a numbered copy behind per attempt.
    var message = text;
    final pending = _attachments.where((f) => f.storedPath == null).toList();
    if (pending.isNotEmpty) {
      final paths = sse == null
          ? null
          : await sse.uploadSessionFiles(widget.session.sessionId, pending);
      if (paths == null || paths.length != pending.length) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Could not upload the attachments')),
          );
          setState(() => _sending = false);
        }
        return;
      }
      // Recorded before the send, which is the call that fails.
      for (var i = 0; i < pending.length; i++) {
        pending[i].storedPath = paths[i];
      }
    }
    if (_attachments.isNotEmpty) {
      message = [
        // Backticked: pasted bare into the agent's composer, a path to an
        // image is taken for an attachment to make rather than text to keep,
        // and vanishes from the prompt with nothing in its place.
        ..._attachments.map((f) => 'Attached: `${f.storedPath}`'),
        '',
        text,
      ].join('\n').trim();
    }

    final error = sse == null
        ? 'Not connected'
        : await sse.sendSessionPrompt(widget.session.sessionId, message);
    if (error == null && mounted) {
      _promptController.clear();
      setState(() {
        _attachments.clear();
        _pastedBlock = null;
      });
      await Future.delayed(const Duration(milliseconds: 500));
      await _loadTranscript();
      // The agent writes the prompt to its transcript a moment after accepting
      // it, so the read above can land before the line exists — and a turn
      // that does nothing hook-worthy afterwards never prompts another.
      for (final timer in _resendReads) {
        timer.cancel();
      }
      _resendReads = [5, 10]
          .map((seconds) => Timer(Duration(seconds: seconds), () {
                if (mounted) _loadTranscript();
              }))
          .toList();
    } else if (mounted) {
      // The prompt stays in the box: it never reached the session, so the
      // user should not have to retype it.
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(error!), duration: const Duration(seconds: 3)),
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
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(8),
              ),
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

  Future<void> _stop() async {
    await _sse?.stopSession(widget.session.sessionId);
  }

  Future<void> _terminate() async {
    await _sse?.terminateSession(widget.session.sessionId);
  }

  Future<void> _resume() async {
    await _sse?.resumeSession(widget.session.sessionId);
  }

  /// Get pending notifications for this session.
  List<HeliosNotification> _pendingNotifications(DaemonAPIService sse) {
    return sse.notifications
        .where(
          (n) => n.sourceSession == widget.session.sessionId && n.isPending,
        )
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        final sse = hm.serviceFor(widget.session.hostId);
        final session =
            sse?.sessions.firstWhere(
              (s) => s.sessionId == widget.session.sessionId,
              orElse: () => widget.session,
            ) ??
            widget.session;
        _updateBreathAnimation(session);
        final pendingNotifs = sse != null
            ? _pendingNotifications(sse)
            : <HeliosNotification>[];

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
            actions: _buildActions(session, sse),
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
              // Git status bar
              if (_gitStatus != null) _buildGitBar(session),
              // Prompt bar
              _buildPromptBar(session),
            ],
          ),
        );
      },
    );
  }

  Widget _buildInlineNotifications(
    List<HeliosNotification> notifs,
    DaemonAPIService sse,
  ) {
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
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onSurfaceVariant,
              ),
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

  void _toggleRecording() {
    debugPrint('[SessionDetail] _toggleRecording() _isRecording=$_isRecording');
    if (_isRecording) {
      SpeechInputService.instance.stopListening();
      setState(() => _isRecording = false);
      // If there's text in the field, send it
      final text = _promptController.text.trim();
      debugPrint('[SessionDetail] stopped recording, text in field: "$text"');
      if (text.isNotEmpty) {
        _sendPrompt();
      }
      return;
    }

    debugPrint('[SessionDetail] calling startListening...');
    SpeechInputService.instance
        .startListening(
          onResult: (text, finalResult) {
            debugPrint('[SessionDetail] onResult: "$text" final=$finalResult');
            if (!mounted) return;
            setState(() {
              _promptController.text = text;
              _promptController.selection = TextSelection.fromPosition(
                TextPosition(offset: text.length),
              );
            });
            // Auto-send when speech engine detects a pause (finalResult)
            if (finalResult && text.trim().isNotEmpty) {
              debugPrint('[SessionDetail] finalResult detected, auto-sending');
              setState(() => _isRecording = false);
              _sendPrompt();
            }
          },
          onDone: () {
            debugPrint('[SessionDetail] onDone called');
            if (mounted) setState(() => _isRecording = false);
          },
          onError: (error) {
            debugPrint('[SessionDetail] onError: $error');
            if (mounted) {
              setState(() => _isRecording = false);
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                  content: Text('Voice input error: $error'),
                  duration: const Duration(seconds: 2),
                ),
              );
            }
          },
        )
        .then((started) {
          debugPrint('[SessionDetail] startListening returned: $started');
          if (mounted) {
            if (started) {
              setState(() => _isRecording = true);
            } else {
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(
                  content: Text('Microphone permission denied'),
                  duration: Duration(seconds: 2),
                ),
              );
            }
          }
        });
  }

  void _openFileBrowser(Session session) {
    _lastSubRoute[session.sessionId] = _SubRoute(_SubRouteType.fileBrowser);
    Navigator.of(context)
        .push(
          MaterialPageRoute(
            settings: const RouteSettings(name: '/file-browser'),
            builder: (_) => FileBrowserScreen(
              hostId: session.hostId,
              rootPath: _effectiveCwd,
              sessionId: session.sessionId,
            ),
          ),
        )
        .then((_) {
          _lastSubRoute.remove(session.sessionId);
        });
  }

  List<Widget> _buildActions(Session session, DaemonAPIService? sse) {
    final actions = <Widget>[];

    // File browser
    actions.add(
      IconButton(
        icon: const Icon(Icons.folder_outlined),
        tooltip: 'Browse files',
        onPressed: () => _openFileBrowser(session),
      ),
    );

    // Permission mode. Shown always so the mode is visible at a glance;
    // tappable only when idle, because switching restarts the agent.
    if (session.source == 'claude') {
      final mode = PermissionMode.of(session.permissionMode ?? '');
      actions.add(
        IconButton(
          icon: Icon(
            mode.isRisky ? Icons.lock_open : Icons.tune,
            color: mode.isRisky ? Colors.orange.shade700 : null,
          ),
          tooltip: session.canSwitchPermissionMode
              ? 'Permission mode: ${mode.label}'
              : 'Permission mode: ${mode.label} (stop the session to change)',
          onPressed: session.canSwitchPermissionMode
              ? () => _showPermissionModeSheet(session)
              : null,
        ),
      );
    }

    if (session.memoryLabel.isNotEmpty) {
      actions.add(
        Tooltip(
          message: 'Memory this terminal holds',
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Center(
              child: Text(
                session.memoryLabel,
                style: TextStyle(
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ),
        ),
      );
    }

    if (session.needsRecovery) {
      actions.add(
        IconButton(
          icon: Icon(Icons.link_off, color: Colors.amber.shade700),
          tooltip: 'Cold — tap to resume',
          onPressed: _resume,
        ),
      );
    }

    if (session.canTerminate) {
      actions.add(
        IconButton(
          icon: const Icon(Icons.close),
          tooltip: 'Terminate session',
          onPressed: _terminate,
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
            color: Theme.of(
              context,
            ).colorScheme.onSurfaceVariant.withValues(alpha: 0.5),
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
        // Last item in the reversed list, so the top of the screen: building
        // it means the reader has scrolled to the start of what is held, and
        // the page before it is what they are reaching for.
        if (_hasMore && index == itemCount - 1) {
          WidgetsBinding.instance.addPostFrameCallback((_) => _loadOlder());
          return const Center(
            child: Padding(
              padding: EdgeInsets.all(12),
              child: SizedBox(
                width: 18,
                height: 18,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            ),
          );
        }
        // Reverse the index: index 0 → last message, index N → first message
        final msgIndex = _messages.length - 1 - index;
        return MessageCard(
          message: _messages[msgIndex],
          hostId: widget.session.hostId,
          sessionCwd: widget.session.cwd,
        );
      },
    );
  }

  /// Offers the modes the daemon says this provider has. Picking one
  /// restarts the agent, so the sheet says so rather than letting the restart
  /// look like a crash.
  void _showPermissionModeSheet(Session session) {
    final sse = _sse;
    final provider = sse?.providers
        .where((p) => p.id == session.source)
        .firstOrNull;
    final ids = provider?.permissionModes ?? const <String>[];
    if (ids.isEmpty) {
      // Either the provider list has not loaded yet or the daemon predates
      // permission modes. Say so rather than making the tap do nothing.
      sse?.fetchProviders();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Permission modes unavailable — try again in a moment'),
          duration: Duration(seconds: 3),
        ),
      );
      return;
    }
    // Empty matches none of the provider's modes, which is the point: a session
    // Helios never set a mode on has nothing to show as selected.
    final current = session.permissionMode ?? '';

    showModalBottomSheet(
      context: context,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
                child: Text(
                  'Permission mode',
                  style: Theme.of(ctx).textTheme.titleSmall,
                ),
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                child: Text(
                  'Changing this restarts the agent. The conversation is kept; '
                  'the terminal scrollback is not.',
                  style: Theme.of(ctx).textTheme.bodySmall,
                ),
              ),
              Flexible(
                child: ListView(
                  shrinkWrap: true,
                  children: [
                    ...ids.map((id) {
                      final mode = PermissionMode.of(id);
                      final selected = id == current;
                      return ListTile(
                        leading: Icon(
                          selected
                              ? Icons.radio_button_checked
                              : Icons.radio_button_unchecked,
                          color: mode.isRisky ? Colors.orange.shade700 : null,
                        ),
                        title: Text(mode.label),
                        subtitle: mode.description.isEmpty
                            ? null
                            : Text(
                                mode.description,
                                style: const TextStyle(fontSize: 12),
                              ),
                        onTap: selected
                            ? null
                            : () {
                                Navigator.pop(ctx);
                                _setPermissionMode(session, mode);
                              },
                      );
                    }),
                    const SizedBox(height: 8),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  Future<void> _setPermissionMode(Session session, PermissionMode mode) async {
    if (mode.isRisky && !await _confirmRiskyMode(mode)) return;
    if (!mounted) return;

    final sse = _sse;
    final error = sse == null
        ? 'Not connected'
        : await sse.setPermissionMode(session.sessionId, mode.id);
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(error ?? 'Permission mode set to ${mode.label}'),
        duration: const Duration(seconds: 3),
      ),
    );
  }

  Future<bool> _confirmRiskyMode(PermissionMode mode) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(mode.label),
        content: Text(
          '${mode.description}\n\n'
          'You will not be asked to approve anything from your phone while '
          'this session is in this mode.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Continue'),
          ),
        ],
      ),
    );
    return ok ?? false;
  }

  void _openGitStatus(Session session) {
    _lastSubRoute[session.sessionId] = _SubRoute(_SubRouteType.gitStatus);
    // Use resolved git root so diffs work from any subdirectory
    final gitRoot = _gitStatus?.root ?? _effectiveCwd;
    Navigator.of(context)
        .push(
          MaterialPageRoute(
            settings: const RouteSettings(name: '/git-status'),
            builder: (_) => GitStatusScreen(
              hostId: session.hostId,
              cwd: gitRoot,
              sessionId: session.sessionId,
            ),
          ),
        )
        .then((_) {
          _lastSubRoute.remove(session.sessionId);
        });
  }

  Widget _buildGitBar(Session session) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final g = _gitStatus!;
    final hasWorktrees = _worktrees.length > 1;

    // Git-themed colors
    const gitOrange = Color(0xFFF05033);
    final barBg = isDark ? const Color(0xFF1B1F23) : const Color(0xFFF6F8FA);
    final borderColor = isDark
        ? const Color(0xFF30363D)
        : const Color(0xFFD0D7DE);
    final branchColor = isDark
        ? const Color(0xFF58A6FF)
        : const Color(0xFF0969DA);
    final textMuted = isDark
        ? const Color(0xFF8B949E)
        : const Color(0xFF656D76);

    return GestureDetector(
      onTap: () => _openGitStatus(session),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
        decoration: BoxDecoration(
          color: barBg,
          border: Border(top: BorderSide(color: borderColor)),
        ),
        child: Row(
          children: [
            // Git branch icon
            Icon(Icons.fork_right, size: 14, color: gitOrange),
            const SizedBox(width: 6),
            // Branch name — long press for worktree picker
            GestureDetector(
              onTap: hasWorktrees
                  ? () => _showWorktreePicker(session)
                  : () => _openGitStatus(session),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 140),
                    child: Text(
                      g.branch,
                      style: TextStyle(
                        fontSize: 12,
                        fontFamily: 'monospace',
                        color: branchColor,
                        fontWeight: FontWeight.w600,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  if (hasWorktrees) ...[
                    const SizedBox(width: 2),
                    Icon(Icons.arrow_drop_down, size: 16, color: textMuted),
                  ],
                ],
              ),
            ),
            if (g.staged.isNotEmpty) ...[
              const SizedBox(width: 10),
              const Icon(
                Icons.check_circle_outline,
                size: 12,
                color: Color(0xFF3FB950),
              ),
              const SizedBox(width: 2),
              Text(
                '${g.staged.length}',
                style: const TextStyle(fontSize: 11, color: Color(0xFF3FB950)),
              ),
            ],
            if (g.unstaged.isNotEmpty) ...[
              const SizedBox(width: 8),
              const Icon(
                Icons.edit_outlined,
                size: 12,
                color: Color(0xFFD29922),
              ),
              const SizedBox(width: 2),
              Text(
                '${g.unstaged.length}',
                style: const TextStyle(fontSize: 11, color: Color(0xFFD29922)),
              ),
            ],
            if (g.untracked.isNotEmpty) ...[
              const SizedBox(width: 8),
              Icon(Icons.add_circle_outline, size: 12, color: textMuted),
              const SizedBox(width: 2),
              Text(
                '${g.untracked.length}',
                style: TextStyle(fontSize: 11, color: textMuted),
              ),
            ],
            if (g.ahead > 0) ...[
              const SizedBox(width: 10),
              const Icon(
                Icons.arrow_upward,
                size: 12,
                color: Color(0xFF3FB950),
              ),
              Text(
                '${g.ahead}',
                style: const TextStyle(fontSize: 11, color: Color(0xFF3FB950)),
              ),
            ],
            if (g.behind > 0) ...[
              const SizedBox(width: 6),
              const Icon(
                Icons.arrow_downward,
                size: 12,
                color: Color(0xFFD29922),
              ),
              Text(
                '${g.behind}',
                style: const TextStyle(fontSize: 11, color: Color(0xFFD29922)),
              ),
            ],
            const Spacer(),
            Icon(Icons.chevron_right, size: 16, color: textMuted),
          ],
        ),
      ),
    );
  }

  void _showWorktreePicker(Session session) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      constraints: BoxConstraints(
        maxHeight: MediaQuery.of(context).size.height * 0.6,
      ),
      builder: (ctx) {
        return _WorktreePickerSheet(
          worktrees: _worktrees,
          worktreeStatuses: _worktreeStatuses,
          effectiveCwd: _effectiveCwd,
          selectedWorktreePath: _selectedWorktreePath,
          isDark: isDark,
          theme: theme,
          onSelected: (wt) {
            Navigator.pop(ctx);
            final path = wt.isMain ? null : wt.path;
            setState(() => _selectedWorktreePath = path);
            if (path != null) {
              _worktreeSelections[widget.session.sessionId] = path;
            } else {
              _worktreeSelections.remove(widget.session.sessionId);
            }
            _loadGitStatus();
          },
        );
      },
    );
  }

  Widget _buildPromptBar(Session session) {
    final canSend = session.canSendPrompt;
    final isQueueing = session.isQueueing;
    final theme = Theme.of(context);

    if (session.isTerminated) {
      return Container(
        padding: EdgeInsets.only(
          left: 16,
          right: 8,
          top: 12,
          bottom: MediaQuery.of(context).padding.bottom + 12,
        ),
        decoration: BoxDecoration(
          color: theme.colorScheme.surface,
          border: Border(
            top: BorderSide(color: theme.colorScheme.outlineVariant),
          ),
        ),
        child: Row(
          children: [
            Icon(
              Icons.stop_circle_outlined,
              size: 18,
              color: theme.colorScheme.outline,
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                'Session terminated — resume to continue',
                style: TextStyle(
                  fontSize: 13,
                  color: theme.colorScheme.outline,
                ),
              ),
            ),
            const SizedBox(width: 8),
            FilledButton.tonal(onPressed: _resume, child: const Text('Resume')),
          ],
        ),
      );
    }

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
                      final slideIn =
                          Tween<Offset>(
                            begin: const Offset(0, 0.5),
                            end: Offset.zero,
                          ).animate(
                            CurvedAnimation(
                              parent: animation,
                              curve: Curves.easeOut,
                            ),
                          );
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
          if (_pastedBlock != null &&
              _promptController.text.contains(_pastedBlock!))
            _PasteOffer(
              bytes: pastedTextFile(_pastedBlock!).size,
              onAttach: _sending ? null : _filePastedBlock,
              onDismiss: () => setState(() => _pastedBlock = null),
            ),
          if (_attachments.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Wrap(
                spacing: 6,
                runSpacing: 6,
                children: _attachments
                    .map(
                      (file) => _AttachmentChip(
                        file: file,
                        onRemove: _sending
                            ? null
                            : () => setState(() => _attachments.remove(file)),
                      ),
                    )
                    .toList(),
              ),
            ),
          Row(
            children: [
              IconButton(
                onPressed: canSend && !_sending ? _showAttachSheet : null,
                icon: const Icon(Icons.add_photo_alternate_outlined, size: 22),
                tooltip: 'Attach a photo or file',
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
                            fillColor:
                                theme.colorScheme.surfaceContainerHighest,
                            contentPadding: const EdgeInsets.symmetric(
                              horizontal: 16,
                              vertical: 10,
                            ),
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
                                  final slideIn =
                                      Tween<Offset>(
                                        begin: const Offset(0, 0.5),
                                        end: Offset.zero,
                                      ).animate(
                                        CurvedAnimation(
                                          parent: animation,
                                          curve: Curves.easeOut,
                                        ),
                                      );
                                  return SlideTransition(
                                    position: slideIn,
                                    child: FadeTransition(
                                      opacity: animation,
                                      child: child,
                                    ),
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
              Padding(
                padding: const EdgeInsets.only(left: 4),
                child: IconButton(
                  onPressed: canSend && !_sending ? _toggleRecording : null,
                  icon: Icon(
                    _isRecording ? Icons.stop : Icons.mic,
                    color: _isRecording ? theme.colorScheme.error : null,
                    size: 22,
                  ),
                  tooltip: _isRecording ? 'Stop recording' : 'Voice input',
                ),
              ),
              const SizedBox(width: 8),
              if (session.canStop) ...[
                IconButton.filled(
                  onPressed: _stop,
                  style: IconButton.styleFrom(
                    backgroundColor: theme.colorScheme.errorContainer,
                  ),
                  icon: Icon(
                    Icons.stop,
                    size: 20,
                    color: theme.colorScheme.onErrorContainer,
                  ),
                  tooltip: 'Stop generation',
                ),
                const SizedBox(width: 6),
              ],
              IconButton.filled(
                onPressed: canSend && !_sending ? _sendPrompt : null,
                icon: _sending
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
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
      case 'terminated':
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
      case 'terminated':
        return 'Terminated';
      default:
        return status;
    }
  }
}

class _WorktreePickerSheet extends StatefulWidget {
  final List<Worktree> worktrees;
  final Map<String, GitStatus> worktreeStatuses;
  final String effectiveCwd;
  final String? selectedWorktreePath;
  final bool isDark;
  final ThemeData theme;
  final ValueChanged<Worktree> onSelected;

  const _WorktreePickerSheet({
    required this.worktrees,
    required this.worktreeStatuses,
    required this.effectiveCwd,
    required this.selectedWorktreePath,
    required this.isDark,
    required this.theme,
    required this.onSelected,
  });

  @override
  State<_WorktreePickerSheet> createState() => _WorktreePickerSheetState();
}

class _WorktreePickerSheetState extends State<_WorktreePickerSheet> {
  String _query = '';

  List<Worktree> get _filtered {
    if (_query.isEmpty) return widget.worktrees;
    final q = _query.toLowerCase();
    return widget.worktrees
        .where(
          (wt) =>
              wt.branch.toLowerCase().contains(q) ||
              wt.path.toLowerCase().contains(q),
        )
        .toList();
  }

  @override
  Widget build(BuildContext context) {
    final filtered = _filtered;
    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
            child: Text(
              'Switch worktree',
              style: TextStyle(
                fontSize: 14,
                fontWeight: FontWeight.w600,
                color: widget.theme.colorScheme.onSurface,
              ),
            ),
          ),
          if (widget.worktrees.length > 5)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: TextField(
                autofocus: false,
                style: const TextStyle(fontSize: 13),
                decoration: InputDecoration(
                  hintText: 'Search branch or path...',
                  hintStyle: TextStyle(
                    fontSize: 13,
                    color: widget.theme.colorScheme.onSurfaceVariant,
                  ),
                  prefixIcon: const Icon(Icons.search, size: 18),
                  isDense: true,
                  contentPadding: const EdgeInsets.symmetric(vertical: 8),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(8),
                  ),
                ),
                onChanged: (v) => setState(() => _query = v),
              ),
            ),
          const SizedBox(height: 4),
          Flexible(
            child: ListView.builder(
              shrinkWrap: true,
              itemCount: filtered.length,
              itemBuilder: (ctx, i) {
                final wt = filtered[i];
                final isSelected =
                    wt.path == widget.effectiveCwd ||
                    (wt.isMain && widget.selectedWorktreePath == null);
                final wtStatus = widget.worktreeStatuses[wt.path];
                // The worktree listing already carries a dirty count; the
                // per-worktree status is more precise but arrives later.
                final changes = wtStatus?.totalChanges ?? wt.dirty;

                return ListTile(
                  leading: Icon(
                    isSelected
                        ? Icons.radio_button_checked
                        : Icons.radio_button_unchecked,
                    size: 20,
                    color: isSelected
                        ? widget.theme.colorScheme.primary
                        : widget.theme.colorScheme.onSurfaceVariant,
                  ),
                  title: Row(
                    children: [
                      Flexible(
                        child: Text(
                          wt.branch,
                          style: TextStyle(
                            fontSize: 13,
                            fontFamily: 'monospace',
                            fontWeight: isSelected
                                ? FontWeight.w600
                                : FontWeight.normal,
                            color: widget.theme.colorScheme.onSurface,
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      if (changes > 0) ...[
                        const SizedBox(width: 8),
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 6,
                            vertical: 1,
                          ),
                          decoration: BoxDecoration(
                            color: widget.isDark
                                ? const Color(0xFF2D1B00)
                                : const Color(0xFFFFF3CD),
                            borderRadius: BorderRadius.circular(8),
                          ),
                          child: Text(
                            '$changes',
                            style: TextStyle(
                              fontSize: 10,
                              fontWeight: FontWeight.w600,
                              color: widget.isDark
                                  ? const Color(0xFFD29922)
                                  : const Color(0xFF9A6700),
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                  subtitle: Text(
                    [
                      wt.shortPath,
                      if (wt.ahead > 0) '↑${wt.ahead}',
                      if (wt.behind > 0) '↓${wt.behind}',
                      if (wt.subject.isNotEmpty) wt.subject,
                    ].join('  '),
                    style: TextStyle(
                      fontSize: 11,
                      color: widget.theme.colorScheme.onSurfaceVariant,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  trailing: wt.isMain
                      ? Text(
                          'main',
                          style: TextStyle(
                            fontSize: 10,
                            color: widget.theme.colorScheme.onSurfaceVariant,
                          ),
                        )
                      : null,
                  dense: true,
                  onTap: () => widget.onSelected(wt),
                );
              },
            ),
          ),
          const SizedBox(height: 8),
        ],
      ),
    );
  }
}

enum _SubRouteType { gitStatus, fileBrowser }

class _SubRoute {
  final _SubRouteType type;
  _SubRoute(this.type);
}

enum _AttachSource { gallery, camera, files }

/// One pending attachment. A thumbnail for images, because the point of
/// sending a screenshot is knowing which screenshot went.
/// Offers to turn a large paste into a file. An offer, not an interception:
/// ignoring it sends the prompt exactly as it was pasted.
class _PasteOffer extends StatelessWidget {
  final int bytes;
  final VoidCallback? onAttach;
  final VoidCallback onDismiss;

  const _PasteOffer({
    required this.bytes,
    required this.onAttach,
    required this.onDismiss,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.only(left: 12, right: 2, top: 2, bottom: 2),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Row(
        children: [
          Expanded(
            child: Text(
              'Pasted ${_AttachmentChip._size(bytes)} of text',
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          TextButton(
            onPressed: onAttach,
            style: TextButton.styleFrom(
              visualDensity: VisualDensity.compact,
              padding: const EdgeInsets.symmetric(horizontal: 10),
            ),
            child: const Text('Attach as file', style: TextStyle(fontSize: 12)),
          ),
          IconButton(
            onPressed: onDismiss,
            icon: const Icon(Icons.close, size: 14),
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            padding: EdgeInsets.zero,
            tooltip: 'Keep it in the prompt',
          ),
        ],
      ),
    );
  }
}

class _AttachmentChip extends StatelessWidget {
  final UploadFile file;
  final VoidCallback? onRemove;

  const _AttachmentChip({required this.file, required this.onRemove});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.only(left: 6, right: 2, top: 3, bottom: 3),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (file.isImage)
            ClipRRect(
              borderRadius: BorderRadius.circular(4),
              child: Image.memory(
                file.bytes,
                width: 20,
                height: 20,
                fit: BoxFit.cover,
                gaplessPlayback: true,
              ),
            )
          else
            Icon(
              Icons.insert_drive_file_outlined,
              size: 16,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          const SizedBox(width: 6),
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 140),
            child: Text(
              file.name,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12),
            ),
          ),
          const SizedBox(width: 6),
          Text(
            _size(file.size),
            style: TextStyle(
              fontSize: 11,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          IconButton(
            onPressed: onRemove,
            icon: const Icon(Icons.close, size: 14),
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            padding: EdgeInsets.zero,
            tooltip: 'Remove',
          ),
        ],
      ),
    );
  }

  static String _size(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).round()} KB';
    return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
  }
}
