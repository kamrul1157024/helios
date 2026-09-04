/// What a server event does to the cache, as data rather than as calls.
///
/// Deliberately free of any Riverpod import. The interesting part is *which*
/// keys an event takes out, and a function that invalidates a container can
/// only be tested through a stand-in for one. Returning the decision lets the
/// mapping be asserted directly; `applyCacheEffects` is the only thing that
/// turns these into `ref.invalidate` calls.
library;

import '../models/session.dart';

/// Replaces one row in a held list, leaving the rest and the order alone.
///
/// Returns the list unchanged when the row is absent, which is the common case
/// for a filtered list: it legitimately does not hold every session.
List<Session> patchSessionRow(
  List<Session> held,
  String sessionId,
  String status,
  String? terminal,
) {
  var found = false;
  final next = [
    for (final s in held)
      if (s.sessionId == sessionId)
        () {
          found = true;
          // An absent handle is no evidence the host went away, so it is only
          // taken when the event actually carried one.
          return s.copyWith(status: status, terminal: terminal ?? s.terminal);
        }()
      else
        s,
  ];
  return found ? next : held;
}

/// The resources the cache holds. One per family, not one per screen.
enum CacheTarget {
  sessions,
  notifications,
  providers,
  settings,
  directories,
  subagents,
  transcript,

  /// Every git read for a host at once. A commit or a branch change moves all
  /// of them together, and none of the events name a path.
  git,
  files,

  /// A host's schedules. Host-wide: a fire changes one row and the list is
  /// short, so there is nothing to gain from narrowing it.
  schedules,
}

/// One thing to do to the cache.
sealed class CacheEffect {
  const CacheEffect();
}

/// Drop a resource for a host, optionally narrowed to one session.
class InvalidateTarget extends CacheEffect {
  final CacheTarget target;
  final String hostId;

  /// The session a per-session target belongs to. Null for host-wide targets.
  final String? sessionId;

  const InvalidateTarget(this.target, this.hostId, {this.sessionId});

  @override
  bool operator ==(Object other) =>
      other is InvalidateTarget &&
      other.target == target &&
      other.hostId == hostId &&
      other.sessionId == sessionId;

  @override
  int get hashCode => Object.hash(target, hostId, sessionId);

  @override
  String toString() =>
      'InvalidateTarget(${target.name}, $hostId${sessionId == null ? '' : ', $sessionId'})';
}

/// Paint one row from the event's own payload, before any refetch lands.
///
/// Without this the list flickers: the row is correct, then blank while the
/// refetch is in flight, then correct again.
class PatchSession extends CacheEffect {
  final String hostId;
  final String sessionId;
  final String status;

  /// The host handle. A resume carries a new one, and taking it matters — the
  /// session is cold in this client's copy until something says otherwise.
  /// Most events carry none, and an absent handle is no evidence it went away.
  final String? terminal;

  const PatchSession({
    required this.hostId,
    required this.sessionId,
    required this.status,
    this.terminal,
  });

  @override
  bool operator ==(Object other) =>
      other is PatchSession &&
      other.hostId == hostId &&
      other.sessionId == sessionId &&
      other.status == status &&
      other.terminal == terminal;

  @override
  int get hashCode => Object.hash(hostId, sessionId, status, terminal);

  @override
  String toString() => 'PatchSession($hostId, $sessionId, $status, $terminal)';
}

String _text(dynamic value) => value is String ? value : '';

/// What one SSE event means for the cache.
///
/// Keyed by host throughout: two daemons hold the same paths and session ids,
/// and must not answer for each other. A host nobody is watching has no
/// listener, so naming its keys costs nothing and refetches nothing — which is
/// why there is no active-host condition anywhere in here.
List<CacheEffect> effectsFor(String hostId, String type, dynamic data) {
  final map = data is Map ? data : const {};

  switch (type) {
    case 'session_status':
      final sessionId = _text(map['session_id']);
      final status = _text(map['status']);
      if (sessionId.isEmpty || status.isEmpty) return const [];
      final terminal = _text(map['terminal']);
      return [
        PatchSession(
          hostId: hostId,
          sessionId: sessionId,
          status: status,
          terminal: terminal.isEmpty ? null : terminal,
        ),
        // The payload carries a status and little else, but the record behind
        // it moved too — last_event_at above all, which is the only thing
        // telling the transcript there is more of it to read.
        InvalidateTarget(CacheTarget.sessions, hostId),
      ];

    case 'session_updated':
    case 'session_deleted':
      return [InvalidateTarget(CacheTarget.sessions, hostId)];

    case 'file_changed':
      // Every path named has changed *content*, not merely a changed mtime —
      // the daemon compares digests before it says anything (spec 54). Which
      // paths they are does not change the answer here: both families are
      // invalidated whole, and Riverpod refetches only the entries something is
      // watching, so naming one costs the same as naming all of them.
      //
      // Git comes too. A working-tree write moves `git status`, and a repo
      // entry is a commit or a checkout.
      final named = map['paths'];
      if (named is! List || named.isEmpty) return const [];
      return [
        InvalidateTarget(CacheTarget.files, hostId),
        InvalidateTarget(CacheTarget.git, hostId),
      ];

    case 'stream_reconnected':
      // The socket was down and the daemon keeps no replay buffer, so anything
      // that changed in the gap was announced to nobody. Files and git are here
      // for a second reason: a path whose watch expired while a screen sat open
      // is no longer being swept, and re-reading is what registers it again.
      return [
        InvalidateTarget(CacheTarget.sessions, hostId),
        InvalidateTarget(CacheTarget.notifications, hostId),
        InvalidateTarget(CacheTarget.files, hostId),
        InvalidateTarget(CacheTarget.git, hostId),
      ];

    case 'schedule_created':
    case 'schedule_updated':
    case 'schedule_deleted':
      return [InvalidateTarget(CacheTarget.schedules, hostId)];

    // A fire produces a session, so the runs list has a new row in it — and the
    // schedule's own summary moved to "running".
    case 'schedule_fired':
      return [
        InvalidateTarget(CacheTarget.schedules, hostId),
        InvalidateTarget(CacheTarget.sessions, hostId),
      ];

    case 'notification':
    case 'notification_resolved':
      // Sessions too: a permission request writes waiting_permission to the
      // session and then announces only the notification
      // (internal/provider/claude/hooks.go:110,148), so refetching the list is
      // the one way the list hears about it.
      return [
        InvalidateTarget(CacheTarget.notifications, hostId),
        InvalidateTarget(CacheTarget.sessions, hostId),
      ];

    case 'subagent_status':
      final sessionId = _text(map['session_id']);
      return sessionId.isEmpty
          ? const []
          : [
              InvalidateTarget(
                CacheTarget.subagents,
                hostId,
                sessionId: sessionId,
              ),
            ];

    case 'session_evicted':
      // A session going cold invalidates everything about it, and the payload
      // does not say which session, so the whole host goes.
      return [
        InvalidateTarget(CacheTarget.sessions, hostId),
        InvalidateTarget(CacheTarget.notifications, hostId),
      ];

    // 'show' instructs the window. The terminal events move connections, and
    // the shell strip acts on them directly — a call whose answer drives an
    // action is not a cache read. None of these is a fact about cached data.
    default:
      return const [];
  }
}
