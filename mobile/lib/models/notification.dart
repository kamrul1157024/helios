class HeliosNotification {
  final String id;
  final String claudeSessionId;
  final String cwd;
  final String type;
  final String status;
  final String? toolName;
  final String? toolInput;
  final String? detail;
  final String? resolvedAt;
  final String? resolvedSource;
  final String createdAt;

  HeliosNotification({
    required this.id,
    required this.claudeSessionId,
    required this.cwd,
    required this.type,
    required this.status,
    this.toolName,
    this.toolInput,
    this.detail,
    this.resolvedAt,
    this.resolvedSource,
    required this.createdAt,
  });

  factory HeliosNotification.fromJson(Map<String, dynamic> json) {
    return HeliosNotification(
      id: json['id'] as String,
      claudeSessionId: json['claude_session_id'] as String,
      cwd: json['cwd'] as String,
      type: json['type'] as String,
      status: json['status'] as String,
      toolName: json['tool_name'] as String?,
      toolInput: json['tool_input'] as String?,
      detail: json['detail'] as String?,
      resolvedAt: json['resolved_at'] as String?,
      resolvedSource: json['resolved_source'] as String?,
      createdAt: json['created_at'] as String,
    );
  }

  bool get isPending => status == 'pending';
  bool get isPermission => type == 'permission';

  String get displayDetail => detail ?? toolInput ?? 'No details';
  String get displayName => toolName ?? _statusLabel;

  String get _statusLabel {
    switch (type) {
      case 'idle':
        return 'Waiting for input';
      case 'done':
        return 'Session completed';
      case 'error':
        return 'Session error';
      case 'permission':
        return 'Permission request';
      default:
        return type;
    }
  }

  String get timeAgo {
    try {
      final ts = createdAt.contains('T') ? createdAt : '${createdAt.replaceAll(' ', 'T')}Z';
      final d = DateTime.parse(ts);
      final diff = DateTime.now().toUtc().difference(d);
      if (diff.inSeconds < 60) return 'just now';
      if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
      if (diff.inHours < 24) return '${diff.inHours}h ago';
      return '${d.month}/${d.day}';
    } catch (_) {
      return createdAt;
    }
  }
}
