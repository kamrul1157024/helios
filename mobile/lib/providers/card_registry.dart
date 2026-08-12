import 'package:flutter/material.dart';
import '../models/notification.dart';
import '../services/daemon_api_service.dart';
import 'claude/cards.dart';
import 'claude/notification_ext.dart';

/// Signature for a notification card builder.
typedef CardBuilder = Widget Function({
  required HeliosNotification notification,
  required DaemonAPIService sse,
  required Set<String> selected,
  required VoidCallback onSelectionChanged,
});

/// Maps notification type → card builder widget.
/// Returns null if no card is registered for the type.
Widget? buildCardForType({
  required HeliosNotification notification,
  required DaemonAPIService sse,
  required Set<String> selected,
  required VoidCallback onSelectionChanged,
}) {
  switch (notification.type) {
    case 'claude.permission':
      return ClaudePermissionCard(
        notification: notification,
        sse: sse,
        selected: selected,
        onSelectionChanged: onSelectionChanged,
      );
    case 'claude.question':
      return ClaudeQuestionCard(
        notification: notification,
        sse: sse,
      );
    case 'claude.elicitation.form':
      return ClaudeElicitationFormCard(
        notification: notification,
        sse: sse,
      );
    case 'claude.elicitation.url':
      return ClaudeElicitationUrlCard(
        notification: notification,
        sse: sse,
      );
    case 'claude.trust':
      return ClaudeTrustCard(
        notification: notification,
        sse: sse,
      );
    default:
      return null;
  }
}

/// Whether this notification needs user action (checks all registered providers).
bool needsAction(HeliosNotification n) {
  return n.needsClaudeAction;
}

/// Whether this notification type blocks the agent until someone answers.
/// Type-level counterpart to [needsAction], for when only the type string is in
/// hand (an SSE payload) rather than a parsed notification. Kept in sync with
/// `needsClaudeAction` minus its `isPending` term.
bool isActionableType(String type) =>
    type == 'claude.permission' ||
    type == 'claude.question' ||
    type == 'claude.trust' ||
    type.startsWith('claude.elicitation.');

/// Whether an incoming notification event should raise an OS notification.
///
/// [status] is the status carried by the event, [alreadyPosted] whether a
/// notification for the same id is already in the tray.
bool shouldRaiseNotification({
  required String type,
  String? status,
  required bool alreadyPosted,
}) {
  // An already-answered approval must not be raised: a reconnect can replay the
  // original event, leaving a prompt on screen that no action can clear. Only
  // actionable types are gated — claude.done is created with status
  // "dismissed", so a blanket status check would kill "Task completed".
  if (isActionableType(type) && status != null && status != 'pending') {
    return false;
  }
  return !alreadyPosted;
}
