import 'package:flutter/material.dart';
import '../models/notification.dart';
import '../services/sse_service.dart';
import 'claude/cards.dart';
import 'claude/notification_ext.dart';

/// Signature for a notification card builder.
typedef CardBuilder = Widget Function({
  required HeliosNotification notification,
  required SSEService sse,
  required Set<String> selected,
  required VoidCallback onSelectionChanged,
});

/// Maps notification type → card builder widget.
/// Returns null if no card is registered for the type.
Widget? buildCardForType({
  required HeliosNotification notification,
  required SSEService sse,
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
    default:
      return null;
  }
}

/// Whether this notification needs user action (checks all registered providers).
bool needsAction(HeliosNotification n) {
  return n.needsClaudeAction;
}
