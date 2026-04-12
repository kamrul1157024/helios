import 'message.dart';
import 'notification.dart';
import 'tts_persona.dart';

/// A narration-worthy event sent to the backend for AI narration.
class NarrationEvent {
  final String type; // "tool_use", "tool_result", "assistant", "notification", "status"
  final String? tool;
  final String? target;
  final String? summary;
  final String? content; // assistant text (pre-truncated to 500 chars)
  final bool? success;
  final String? status;

  const NarrationEvent({
    required this.type,
    this.tool,
    this.target,
    this.summary,
    this.content,
    this.success,
    this.status,
  });

  Map<String, dynamic> toJson() => {
        'type': type,
        if (tool != null) 'tool': tool,
        if (target != null) 'target': target,
        if (summary != null) 'summary': summary,
        if (content != null) 'content': content,
        if (success != null) 'success': success,
        if (status != null) 'status': status,
      };

  /// Build from a transcript Message.
  factory NarrationEvent.fromMessage(Message msg) {
    switch (msg.role) {
      case 'assistant':
        final text = msg.content ?? '';
        return NarrationEvent(
          type: 'assistant',
          content: text.length > 500 ? text.substring(0, 500) : text,
        );
      case 'tool_use':
        return NarrationEvent(
          type: 'tool_use',
          tool: msg.tool,
          target: _shorten(msg.tool, msg.summary),
          summary: msg.summary,
        );
      case 'tool_result':
        return NarrationEvent(
          type: 'tool_result',
          tool: msg.tool,
          success: msg.success,
        );
      case 'user':
        final text = msg.content ?? '';
        return NarrationEvent(
          type: 'user',
          content: text.length > 200 ? text.substring(0, 200) : text,
        );
      default:
        return NarrationEvent(type: msg.role);
    }
  }

  /// Build from a session status change.
  factory NarrationEvent.fromStatus(String status) =>
      NarrationEvent(type: 'status', status: status);

  /// Build from a notification.
  factory NarrationEvent.fromNotification(HeliosNotification n) =>
      NarrationEvent(
        type: 'notification',
        tool: n.payload?['tool_name'] as String?,
        summary: n.detail,
      );

  static String? _shorten(String? tool, String? raw) {
    if (raw == null) return null;
    switch (tool) {
      case 'Bash':
        return shortenCommand(raw);
      case 'Read':
      case 'Write':
      case 'Edit':
        return shortenFilePath(raw);
      default:
        return raw;
    }
  }
}
