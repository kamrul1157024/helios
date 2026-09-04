/// Generic, provider-agnostic transcript message.
class Message {
  /// Position in the whole transcript, not in the page it arrived in. Asking
  /// for what came after it is how the app follows a running session.
  final int seq;
  final String role; // user, assistant, tool_use, tool_result
  final String? content;
  final String? tool;
  final String? summary;
  final bool? success;
  final Map<String, dynamic>? metadata;
  final String timestamp;

  Message({
    this.seq = 0,
    required this.role,
    this.content,
    this.tool,
    this.summary,
    this.success,
    this.metadata,
    required this.timestamp,
  });

  factory Message.fromJson(Map<String, dynamic> json) {
    return Message(
      seq: json['seq'] as int? ?? 0,
      role: json['role'] as String,
      content: json['content'] as String?,
      tool: json['tool'] as String?,
      summary: json['summary'] as String?,
      success: json['success'] as bool?,
      metadata: json['metadata'] as Map<String, dynamic>?,
      timestamp: json['timestamp'] as String? ?? '',
    );
  }

  bool get isUser => role == 'user';
  bool get isAssistant => role == 'assistant';
  bool get isToolUse => role == 'tool_use';
  bool get isToolResult => role == 'tool_result';

  String get timeAgo {
    try {
      final normalized = timestamp.contains('T')
          ? timestamp
          : '${timestamp.replaceAll(' ', 'T')}Z';
      final d = DateTime.parse(normalized);
      final diff = DateTime.now().toUtc().difference(d);
      if (diff.inSeconds < 60) return 'just now';
      if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
      if (diff.inHours < 24) return '${diff.inHours}h ago';
      return '${d.month}/${d.day}';
    } catch (_) {
      return timestamp;
    }
  }
}

/// Paginated transcript result from the API.
class TranscriptResult {
  final List<Message> messages;
  final int total;
  final int returned;
  final int offset;
  final bool hasMore;

  /// Which parse the seq numbers count against.
  final String epoch;

  /// Set when a delta was asked for under an epoch that no longer holds: the
  /// messages are a fresh newest page and replace what is held.
  final bool epochChanged;

  /// Set when the limit cut a delta short and more messages follow the last
  /// one here. Distinct from [hasMore], which is about older pages.
  final bool moreAfter;

  TranscriptResult({
    required this.messages,
    required this.total,
    required this.returned,
    required this.offset,
    required this.hasMore,
    this.epoch = '',
    this.epochChanged = false,
    this.moreAfter = false,
  });

  factory TranscriptResult.fromJson(Map<String, dynamic> json) {
    final list = (json['messages'] as List?) ?? [];
    return TranscriptResult(
      messages: list
          .map((m) => Message.fromJson(m as Map<String, dynamic>))
          .toList(),
      total: json['total'] as int? ?? 0,
      returned: json['returned'] as int? ?? 0,
      offset: json['offset'] as int? ?? 0,
      hasMore: json['has_more'] as bool? ?? false,
      epoch: json['epoch'] as String? ?? '',
      epochChanged: json['epoch_changed'] as bool? ?? false,
      moreAfter: json['more_after'] as bool? ?? false,
    );
  }
}
