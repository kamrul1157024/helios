import 'dart:convert';
import '../models/notification.dart';

/// Payload accessors for HeliosNotification, keyed on the kind of request
/// rather than on who raised it.
///
/// None of these is specific to one agent in substance: a permission request
/// carries a tool name and a tool input whoever asked, so the card that
/// renders it is the same card. Keying on [HeliosNotification.kind] is what
/// lets a second provider reuse all of it.
extension NotificationPayload on HeliosNotification {
  // Kind checks
  bool get isPermission => kind == 'permission';
  bool get isQuestion => kind == 'question';
  bool get isElicitationForm => kind == 'elicitation.form';
  bool get isElicitationUrl => kind == 'elicitation.url';
  bool get isElicitation => kind.startsWith('elicitation.');
  bool get isTrust => kind == 'trust';
  bool get isDone => kind == 'done';
  bool get isError => kind == 'error';

  /// Whether this notification needs user action.
  bool get needsAction =>
      isPending &&
      (isPermission || isQuestion || isElicitation || isTrust || isError);

  // ==================== Error payload ====================

  /// The API error text the agent recorded for the failed turn.
  String? get errorText => payload?['error'] as String?;

  /// The session the failed turn belongs to. Carried in the payload because
  /// that is what the retry action handler reads.
  String? get errorSessionId => payload?['session_id'] as String?;

  bool get isRateLimit => payload?['is_rate_limit'] == true;
  bool get isRetryable => payload?['retryable'] == true;

  /// When a usage limit lifts, or null when the error carried no reset time.
  /// An unknown window is not a reason to lock the user out of retrying.
  DateTime? get rateLimitResetAt {
    final raw = payload?['reset_at'] as String?;
    if (raw == null) return null;
    return DateTime.tryParse(raw)?.toUtc();
  }

  // Payload accessors for a permission request
  String? get toolName => payload?['tool_name'] as String?;
  String? get toolInput {
    final ti = payload?['tool_input'];
    if (ti is String) return ti;
    if (ti is Map) return jsonEncode(ti);
    return null;
  }

  List<dynamic>? get permissionSuggestions =>
      payload?['permission_suggestions'] as List?;

  /// Whether Codex raised this. The card is the same card whoever raised it,
  /// but the words on it name the agent the user is talking to.
  bool get isCodex => source == 'codex';

  /// A plan waiting for approval. On the wire it is a permission like any
  /// other, but it is not a yes-or-no question: the answer picks the mode the
  /// session continues in, or sends the plan back in words.
  bool get isPlan => toolName == 'ExitPlanMode';

  /// The plan as Claude wrote it, markdown and all.
  String? get planText {
    final ti = payload?['tool_input'];
    if (ti is Map) return ti['plan'] as String?;
    return null;
  }

  /// Where the whole plan lives on the machine running the agent.
  String? get planFilePath {
    final ti = payload?['tool_input'];
    if (ti is Map) return ti['planFilePath'] as String?;
    return null;
  }

  // Payload accessors for a question
  List<dynamic>? get questions => payload?['questions'] as List?;

  // Payload accessors for an elicitation
  String? get mcpServerName => payload?['mcp_server_name'] as String?;
  String? get elicitationMessage => payload?['message'] as String?;
  Map<String, dynamic>? get requestedSchema =>
      payload?['requested_schema'] as Map<String, dynamic>?;
  String? get elicitationUrl => payload?['url'] as String?;
}
