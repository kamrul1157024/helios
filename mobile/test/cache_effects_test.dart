import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/session.dart';
import 'package:helios/providers/cache_effects.dart';

/// What each server event takes out of the cache.
///
/// This is the pure half of the data layer, and the half worth asserting
/// directly: every screen's freshness depends on this mapping being right, and
/// none of it needs a container to check.

const host = 'h1';
const other = 'h2';

void main() {
  group('session_status', () {
    test('patches the row and then refetches the list', () {
      expect(
        effectsFor(host, 'session_status', {'session_id': 's1', 'status': 'active'}),
        const [
          PatchSession(hostId: host, sessionId: 's1', status: 'active'),
          InvalidateTarget(CacheTarget.sessions, host),
        ],
      );
    });

    // A resume carries the new host handle. Taking it matters: the session is
    // cold in this client's copy until something says otherwise.
    test('carries the terminal handle when the event has one', () {
      final effects = effectsFor(
        host,
        'session_status',
        {'session_id': 's1', 'status': 'active', 'terminal': 't-9'},
      );
      expect((effects.first as PatchSession).terminal, 't-9');
    });

    // Most session_status events say nothing about the terminal, and an absent
    // handle is no evidence the host went away.
    test('leaves the handle alone when the event omits it', () {
      final effects = effectsFor(host, 'session_status', {'session_id': 's1', 'status': 'idle'});
      expect((effects.first as PatchSession).terminal, isNull);
    });

    test('a payload missing its id or status touches nothing', () {
      expect(effectsFor(host, 'session_status', {'status': 'active'}), isEmpty);
      expect(effectsFor(host, 'session_status', {'session_id': 's1'}), isEmpty);
    });
  });

  test('session_updated and session_deleted refetch the list', () {
    for (final type in ['session_updated', 'session_deleted']) {
      expect(
        effectsFor(host, type, {'session_id': 's1'}),
        const [InvalidateTarget(CacheTarget.sessions, host)],
        reason: type,
      );
    }
  });

  // A permission request writes waiting_permission to the session and announces
  // only the notification, so the list is the one way the UI hears of it.
  test('a notification takes out the sessions as well as the notifications', () {
    for (final type in ['notification', 'notification_resolved']) {
      expect(
        effectsFor(host, type, const {}),
        const [
          InvalidateTarget(CacheTarget.notifications, host),
          InvalidateTarget(CacheTarget.sessions, host),
        ],
        reason: type,
      );
    }
  });

  group('subagent_status', () {
    test('narrows to the session it names', () {
      expect(
        effectsFor(host, 'subagent_status', {'session_id': 's1'}),
        const [InvalidateTarget(CacheTarget.subagents, host, sessionId: 's1')],
      );
    });

    test('without a session id it touches nothing', () {
      expect(effectsFor(host, 'subagent_status', const {}), isEmpty);
    });
  });

  test('session_evicted takes out the host-wide lists', () {
    expect(
      effectsFor(host, 'session_evicted', const {}),
      const [
        InvalidateTarget(CacheTarget.sessions, host),
        InvalidateTarget(CacheTarget.notifications, host),
      ],
    );
  });

  // 'show' instructs the window; the terminal events move connections and the
  // shell strip acts on them directly.
  test('events that are not about data touch no keys', () {
    expect(effectsFor(host, 'show', const {}), isEmpty);
    expect(effectsFor(host, 'terminal_opened', {'session_id': 's1'}), isEmpty);
    expect(effectsFor(host, 'terminal_closed', {'terminal_id': 't1'}), isEmpty);
    expect(effectsFor(host, 'something_new', const {}), isEmpty);
  });

  group('host namespacing', () {
    // Two daemons hold the same session ids and must not answer for each other.
    test('the same event on two hosts names different keys', () {
      final a = effectsFor(host, 'session_updated', const {});
      final b = effectsFor(other, 'session_updated', const {});
      expect(a, isNot(equals(b)));
    });

    test('a per-session target separates by session as well as by host', () {
      const one = InvalidateTarget(CacheTarget.subagents, host, sessionId: 's1');
      const two = InvalidateTarget(CacheTarget.subagents, host, sessionId: 's2');
      expect(one, isNot(equals(two)));
      expect(one, equals(const InvalidateTarget(CacheTarget.subagents, host, sessionId: 's1')));
    });
  });

  group('patching one row', () {
    Session row(String id, {String status = 'idle', String? terminal}) => Session(
          sessionId: id,
          source: 'claude',
          cwd: '/tmp/p',
          project: 'p',
          status: status,
          terminal: terminal,
          createdAt: '2026-01-01T00:00:00Z',
        );

    test('changes the named row and leaves the others alone', () {
      final held = [row('a'), row('b')];
      final next = patchSessionRow(held, 'b', 'active', null);
      expect(next[0].status, 'idle');
      expect(next[1].status, 'active');
    });

    test('keeps the order', () {
      final next = patchSessionRow([row('a'), row('b'), row('c')], 'b', 'active', null);
      expect(next.map((s) => s.sessionId), ['a', 'b', 'c']);
    });

    // A resume carries a new handle, and taking it is how the client learns the
    // session is warm again.
    test('takes a new terminal handle when the event carries one', () {
      final next = patchSessionRow([row('a', terminal: 'old')], 'a', 'active', 'new');
      expect(next.single.terminal, 'new');
    });

    // Most events carry none, and an absent handle is no evidence it went away.
    test('keeps the existing handle when the event carries none', () {
      final next = patchSessionRow([row('a', terminal: 'old')], 'a', 'active', null);
      expect(next.single.terminal, 'old');
    });

    // A filtered list legitimately does not hold every session.
    test('an absent row leaves the list untouched', () {
      final held = [row('a')];
      expect(identical(patchSessionRow(held, 'zzz', 'active', null), held), isTrue);
    });

    test('an empty list is survivable', () {
      expect(patchSessionRow(const [], 'a', 'active', null), isEmpty);
    });
  });

  group('malformed payloads', () {
    // The stream is JSON off the wire, and a daemon at a different version can
    // send anything. None of it should throw.
    test('a non-map payload is survivable', () {
      expect(effectsFor(host, 'session_status', null), isEmpty);
      expect(effectsFor(host, 'session_status', 'nonsense'), isEmpty);
      expect(effectsFor(host, 'notification', 42), hasLength(2));
    });

    test('non-string fields are ignored rather than coerced', () {
      expect(effectsFor(host, 'session_status', {'session_id': 7, 'status': 'active'}), isEmpty);
    });
  });
}
