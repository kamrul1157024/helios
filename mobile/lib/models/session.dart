class Session {
  final String hostId;
  final String sessionId;
  final String source;
  final String cwd;
  final String project;
  final String? title;
  final String? transcriptPath;
  final String? model;
  final String status;
  final String? lastEvent;
  final String? lastEventAt;
  final String? lastUserMessage;
  final bool pinned;
  final bool archived;
  final bool managed;

  /// The agent's permission mode, or null for a session that has not reported
  /// one yet. Switching it restarts the agent, so it is only offered when the
  /// session is idle.
  final String? permissionMode;

  /// Handle of the session's live terminal host, or null when the session is
  /// cold and has to be resumed before it can be driven.
  final String? terminal;
  final bool supportsPromptQueue;
  final String createdAt;
  final String? endedAt;

  Session({
    this.hostId = '',
    required this.sessionId,
    required this.source,
    required this.cwd,
    required this.project,
    this.title,
    this.transcriptPath,
    this.model,
    required this.status,
    this.lastEvent,
    this.lastEventAt,
    this.lastUserMessage,
    this.pinned = false,
    this.archived = false,
    this.managed = false,
    this.permissionMode,
    this.terminal,
    this.supportsPromptQueue = false,
    required this.createdAt,
    this.endedAt,
  });

  factory Session.fromJson(Map<String, dynamic> json, {String hostId = ''}) {
    return Session(
      hostId: hostId,
      sessionId: json['session_id'] as String,
      source: json['source'] as String? ?? 'claude',
      cwd: json['cwd'] as String? ?? '',
      project: json['project'] as String? ?? '',
      title: json['title'] as String?,
      transcriptPath: json['transcript_path'] as String?,
      model: json['model'] as String?,
      status: json['status'] as String,
      lastEvent: json['last_event'] as String?,
      lastEventAt: json['last_event_at'] as String?,
      lastUserMessage: json['last_user_message'] as String?,
      pinned: json['pinned'] == true || json['pinned'] == 1,
      archived: json['archived'] == true || json['archived'] == 1,
      managed: json['managed'] == true || json['managed'] == 1,
      permissionMode: json['permission_mode'] as String?,
      terminal: json['terminal'] as String?,
      supportsPromptQueue: json['supports_prompt_queue'] == true,
      createdAt: json['created_at'] as String,
      endedAt: json['ended_at'] as String?,
    );
  }

  bool get isStarting => status == 'starting';
  bool get isActive =>
      status == 'active' ||
      status == 'waiting_permission' ||
      status == 'compacting' ||
      status == 'starting';
  bool get isCompacting => status == 'compacting';
  bool get isIdle => status == 'idle';
  bool get isTerminated => status == 'terminated';
  bool get canSendPrompt {
    if (status == 'idle') return true;
    // A turn that died on an API error leaves a live, idle agent. The daemon
    // accepts a prompt in this state — handleSessionSend treats only active,
    // waiting_permission and terminated as unsendable — and typing "continue"
    // is exactly the terminal recovery. Blocking it here stranded the session.
    if (status == 'error') return true;
    if (supportsPromptQueue && isActive) return true;
    return false;
  }

  bool get isQueueing => supportsPromptQueue && isActive;

  String get displayTitle => title ?? lastUserMessage ?? shortCwd;
  bool get hasTerminal => terminal != null && terminal!.isNotEmpty;

  /// A session with no live terminal has to be woken before it can do
  /// anything. Being helios-managed does not spare it that: nothing resurrects
  /// a host on its own.
  bool get needsRecovery => !hasTerminal && !isTerminated;
  bool get canStop => isActive;
  bool get canTerminate => isActive || isIdle;
  bool get canResume => isTerminated;

  /// Switching the mode restarts the agent, which would discard a turn in
  /// flight and strand any pending permission prompt.
  bool get canSwitchPermissionMode => source == 'claude' && isIdle;

  Session copyWith({
    String? title,
    bool? pinned,
    bool? archived,
    String? permissionMode,
  }) {
    return Session(
      hostId: hostId,
      sessionId: sessionId,
      source: source,
      cwd: cwd,
      project: project,
      title: title ?? this.title,
      transcriptPath: transcriptPath,
      model: model,
      status: status,
      lastEvent: lastEvent,
      lastEventAt: lastEventAt,
      lastUserMessage: lastUserMessage,
      pinned: pinned ?? this.pinned,
      archived: archived ?? this.archived,
      managed: managed,
      permissionMode: permissionMode ?? this.permissionMode,
      terminal: terminal,
      supportsPromptQueue: supportsPromptQueue,
      createdAt: createdAt,
      endedAt: endedAt,
    );
  }

  String get shortId {
    if (sessionId.length > 8) return sessionId.substring(0, 8);
    return sessionId;
  }

  String get shortCwd {
    final parts = cwd.split('/');
    if (parts.length <= 3) return cwd;
    return '.../${parts.sublist(parts.length - 2).join('/')}';
  }

  String get timeAgo {
    final ts = lastEventAt ?? createdAt;
    try {
      final normalized = ts.contains('T') ? ts : '${ts.replaceAll(' ', 'T')}Z';
      final d = DateTime.parse(normalized);
      final diff = DateTime.now().toUtc().difference(d);
      if (diff.inSeconds < 60) return 'just now';
      if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
      if (diff.inHours < 24) return '${diff.inHours}h ago';
      return '${d.month}/${d.day}';
    } catch (_) {
      return ts;
    }
  }
}

class Subagent {
  final String agentId;
  final String parentSessionId;
  final String? agentType;
  final String? description;
  final String status;
  final String? transcriptPath;
  final String createdAt;
  final String? endedAt;

  Subagent({
    required this.agentId,
    required this.parentSessionId,
    this.agentType,
    this.description,
    required this.status,
    this.transcriptPath,
    required this.createdAt,
    this.endedAt,
  });

  factory Subagent.fromJson(Map<String, dynamic> json) {
    return Subagent(
      agentId: json['agent_id'] as String,
      parentSessionId: json['parent_session_id'] as String,
      agentType: json['agent_type'] as String?,
      description: json['description'] as String?,
      status: json['status'] as String,
      transcriptPath: json['transcript_path'] as String?,
      createdAt: json['created_at'] as String,
      endedAt: json['ended_at'] as String?,
    );
  }
}
