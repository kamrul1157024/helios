import 'message.dart';
import 'notification.dart';

/// A narration-worthy event sent to the backend for AI narration.
/// Sends raw data — the backend handles truncation and prompt building.
class NarrationEvent {
  final String type; // "tool_use", "tool_result", "assistant", "notification", "status"
  final String? tool;
  final String? content;
  final bool? success;
  final String? status;
  final Map<String, dynamic>? payload; // raw notification payload

  const NarrationEvent({
    required this.type,
    this.tool,
    this.content,
    this.success,
    this.status,
    this.payload,
  });

  Map<String, dynamic> toJson() => {
        'type': type,
        if (tool != null) 'tool': tool,
        if (content != null) 'content': content,
        if (success != null) 'success': success,
        if (status != null) 'status': status,
        if (payload != null) 'payload': payload,
      };

  /// Build from a transcript Message — send raw data.
  factory NarrationEvent.fromMessage(Message msg) {
    return NarrationEvent(
      type: msg.role,
      tool: msg.tool,
      content: msg.content,
      success: msg.success,
    );
  }

  /// Build from a session status change.
  factory NarrationEvent.fromStatus(String status) =>
      NarrationEvent(type: 'status', status: status);

  /// Build from a notification — send raw payload.
  factory NarrationEvent.fromNotification(HeliosNotification n) =>
      NarrationEvent(
        type: 'notification',
        content: n.type,
        payload: n.payload,
      );
}
