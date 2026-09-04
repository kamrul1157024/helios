import 'package:flutter/material.dart';
import '../models/notification.dart';
import '../services/daemon_api_service.dart';
import 'cards.dart';
import 'notification_ext.dart';

/// Signature for a notification card builder.
typedef CardBuilder =
    Widget Function({
      required HeliosNotification notification,
      required DaemonAPIService sse,
      required Set<String> selected,
      required VoidCallback onSelectionChanged,
    });

/// Maps a notification to the card that renders it.
///
/// Keyed on [HeliosNotification.kind] — the part of the type after the
/// provider prefix — so `codex.permission` and `claude.permission` get the
/// same card. A permission request is the same request whoever raised it.
///
/// Returns null when no card is registered, which callers render as a plain
/// status card rather than nothing.
Widget? buildCardForType({
  required HeliosNotification notification,
  required DaemonAPIService sse,
  required Set<String> selected,
  required VoidCallback onSelectionChanged,
}) {
  switch (notification.kind) {
    case 'permission':
      return PermissionCard(
        notification: notification,
        sse: sse,
        selected: selected,
        onSelectionChanged: onSelectionChanged,
      );
    case 'question':
      return QuestionCard(notification: notification, sse: sse);
    case 'elicitation.form':
      return ElicitationFormCard(notification: notification, sse: sse);
    case 'elicitation.url':
      return ElicitationUrlCard(notification: notification, sse: sse);
    case 'trust':
      return TrustCard(notification: notification, sse: sse);
    case 'error':
      return ErrorCard(notification: notification, sse: sse);
    default:
      return null;
  }
}

/// Whether this notification needs user action, for any provider.
bool needsAction(HeliosNotification n) => n.needsAction;

/// Kinds of request that need an answer.
///
/// The daemon also serves this, on /api/notification-types. Until the app
/// reads that, this list is the fallback — and it is keyed on kind, so an
/// unrecognised provider's permission request is still treated as actionable
/// rather than quietly filed as news.
const _actionableKinds = {'permission', 'question', 'trust', 'error'};

/// Whether this notification type blocks the agent until someone answers.
///
/// Type-level counterpart to [needsAction], for when only the type string is
/// in hand (an SSE payload) rather than a parsed notification. Kept in sync
/// with `needsAction` minus its `isPending` term.
bool isActionableType(String type) {
  final i = type.indexOf('.');
  if (i < 0) return false;
  final kind = type.substring(i + 1);
  return _actionableKinds.contains(kind) || kind.startsWith('elicitation.');
}

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

/// The kind of request a type names, or "" when it names none.
///
/// The type-string counterpart to [HeliosNotification.kind], for the SSE path
/// where only the string has arrived.
String kindOfType(String type) {
  final i = type.indexOf('.');
  return i < 0 ? '' : type.substring(i + 1);
}

/// A heading for a kind of request, used when the server sent no title.
///
/// Deliberately says nothing about which agent asked: the same words have to
/// read correctly for every provider.
String labelForKind(String kind) {
  switch (kind) {
    case 'permission':
      return 'Permission needed';
    case 'question':
      return 'A question is waiting';
    case 'elicitation.form':
      return 'Input requested';
    case 'elicitation.url':
      return 'Authentication required';
    case 'trust':
      return 'Workspace trust required';
    case 'done':
      return 'Task completed';
    case 'error':
      return 'Session error';
    default:
      return 'Helios';
  }
}

/// The body shown when the server sent no detail.
String bodyForKind(String kind) {
  switch (kind) {
    case 'question':
      return 'Answer required';
    case 'elicitation.form':
    case 'elicitation.url':
      return 'Input required';
    case 'trust':
      return 'The agent is asking to trust this workspace.';
    case 'done':
      return 'The agent finished a task.';
    case 'error':
      return 'The agent stopped due to an error.';
    default:
      return 'The agent needs your attention.';
  }
}
