class Session {
  final String hostId;
  final String sessionId;
  final String source;
  final String cwd;
  final String project;
  final String? transcriptPath;
  final String? model;
  final String status;
  final String? lastEvent;
  final String? lastEventAt;
  final String? lastUserMessage;
  final bool pinned;
  final bool archived;
  final String? tmuxPane;
  final int? tmuxPid;
  final String createdAt;
  final String? endedAt;

  Session({
    this.hostId = '',
    required this.sessionId,
    required this.source,
    required this.cwd,
    required this.project,
    this.transcriptPath,
    this.model,
    required this.status,
    this.lastEvent,
    this.lastEventAt,
    this.lastUserMessage,
    this.pinned = false,
    this.archived = false,
    this.tmuxPane,
    this.tmuxPid,
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
      transcriptPath: json['transcript_path'] as String?,
      model: json['model'] as String?,
      status: json['status'] as String,
      lastEvent: json['last_event'] as String?,
      lastEventAt: json['last_event_at'] as String?,
      lastUserMessage: json['last_user_message'] as String?,
      pinned: json['pinned'] == true || json['pinned'] == 1,
      archived: json['archived'] == true || json['archived'] == 1,
      tmuxPane: json['tmux_pane'] as String?,
      tmuxPid: json['tmux_pid'] as int?,
      createdAt: json['created_at'] as String,
      endedAt: json['ended_at'] as String?,
    );
  }

  bool get isStarting => status == 'starting';
  bool get isActive => status == 'active' || status == 'waiting_permission' || status == 'compacting' || status == 'starting';
  bool get isCompacting => status == 'compacting';
  bool get isIdle => status == 'idle';
  bool get isEnded => status == 'ended';
  bool get isSuspended => status == 'suspended';
  bool get isStale => status == 'stale';
  bool get canSendPrompt => status == 'idle' || status == 'ended' || status == 'suspended' || status == 'stale';
  bool get canStop => status == 'active' || status == 'waiting_permission' || status == 'compacting';
  bool get canSuspend => isActive || isIdle;
  bool get canResume => isEnded || isSuspended || isStale;

  Session copyWith({
    bool? pinned,
    bool? archived,
  }) {
    return Session(
      hostId: hostId,
      sessionId: sessionId,
      source: source,
      cwd: cwd,
      project: project,
      transcriptPath: transcriptPath,
      model: model,
      status: status,
      lastEvent: lastEvent,
      lastEventAt: lastEventAt,
      lastUserMessage: lastUserMessage,
      pinned: pinned ?? this.pinned,
      archived: archived ?? this.archived,
      tmuxPane: tmuxPane,
      tmuxPid: tmuxPid,
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
