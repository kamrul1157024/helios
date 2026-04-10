/// Generic, provider-agnostic transcript message.
class Message {
  final String role; // user, assistant, tool_use, tool_result
  final String? content;
  final String? tool;
  final String? summary;
  final bool? success;
  final Map<String, dynamic>? metadata;
  final String timestamp;

  Message({
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
      final normalized =
          timestamp.contains('T') ? timestamp : '${timestamp.replaceAll(' ', 'T')}Z';
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

  TranscriptResult({
    required this.messages,
    required this.total,
    required this.returned,
    required this.offset,
    required this.hasMore,
  });

  factory TranscriptResult.fromJson(Map<String, dynamic> json) {
    final list = (json['messages'] as List?) ?? [];
    return TranscriptResult(
      messages: list.map((m) => Message.fromJson(m as Map<String, dynamic>)).toList(),
      total: json['total'] as int? ?? 0,
      returned: json['returned'] as int? ?? 0,
      offset: json['offset'] as int? ?? 0,
      hasMore: json['has_more'] as bool? ?? false,
    );
  }
}
