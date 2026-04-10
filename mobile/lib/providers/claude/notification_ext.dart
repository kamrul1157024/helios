import 'dart:convert';
import '../../models/notification.dart';

/// Claude-specific accessors for HeliosNotification.
extension ClaudeNotification on HeliosNotification {
  // Type checks
  bool get isClaudePermission => type == 'claude.permission';
  bool get isClaudeQuestion => type == 'claude.question';
  bool get isClaudeElicitationForm => type == 'claude.elicitation.form';
  bool get isClaudeElicitationUrl => type == 'claude.elicitation.url';
  bool get isClaudeElicitation => type.startsWith('claude.elicitation.');
  bool get isClaudeDone => type == 'claude.done';
  bool get isClaudeError => type == 'claude.error';

  /// Whether this Claude notification needs user action.
  bool get needsClaudeAction =>
      isPending && (isClaudePermission || isClaudeQuestion || isClaudeElicitation);

  String get claudeDisplayTitle => title ?? _claudeTypeLabel;

  String get _claudeTypeLabel {
    switch (type) {
      case 'claude.permission':
        return 'Permission request';
      case 'claude.question':
        return 'Question';
      case 'claude.elicitation.form':
        return 'Input requested';
      case 'claude.elicitation.url':
        return 'Authentication required';
      case 'claude.done':
        return 'Session completed';
      case 'claude.error':
        return 'Session error';
      default:
        return type;
    }
  }

  // Payload accessors for claude.permission
  String? get toolName => payload?['tool_name'] as String?;
  String? get toolInput {
    final ti = payload?['tool_input'];
    if (ti is String) return ti;
    if (ti is Map) return jsonEncode(ti);
    return null;
  }

  List<dynamic>? get permissionSuggestions =>
      payload?['permission_suggestions'] as List?;

  // Payload accessors for claude.question
  List<dynamic>? get questions => payload?['questions'] as List?;

  // Payload accessors for claude.elicitation
  String? get mcpServerName => payload?['mcp_server_name'] as String?;
  String? get elicitationMessage => payload?['message'] as String?;
  Map<String, dynamic>? get requestedSchema =>
      payload?['requested_schema'] as Map<String, dynamic>?;
  String? get elicitationUrl => payload?['url'] as String?;
}
