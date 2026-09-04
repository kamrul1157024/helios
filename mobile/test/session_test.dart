import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/session.dart';

Session sessionWithStatus(String status, {bool queue = false}) => Session(
  sessionId: 's1',
  source: 'claude',
  cwd: '/tmp/proj',
  project: 'proj',
  status: status,
  supportsPromptQueue: queue,
  createdAt: '2026-01-01T00:00:00Z',
);

void main() {
  group('canSendPrompt', () {
    test('an idle session accepts a prompt', () {
      expect(sessionWithStatus('idle').canSendPrompt, isTrue);
    });

    // A turn that died on an API error leaves a live, idle agent, and the
    // daemon accepts a prompt in that state. Blocking it here is what stranded
    // the session on mobile while "continue" kept working in the terminal.
    test('an errored session accepts a prompt', () {
      expect(sessionWithStatus('error').canSendPrompt, isTrue);
    });

    test('a terminated session still does not', () {
      expect(sessionWithStatus('terminated').canSendPrompt, isFalse);
    });

    test('a busy session only accepts a prompt when it can queue', () {
      for (final status in [
        'active',
        'waiting_permission',
        'compacting',
        'starting',
      ]) {
        expect(
          sessionWithStatus(status).canSendPrompt,
          isFalse,
          reason: '$status without queueing',
        );
        expect(
          sessionWithStatus(status, queue: true).canSendPrompt,
          isTrue,
          reason: '$status with queueing',
        );
      }
    });

    // error is not "active": it must not pull in the queueing UI or the stop
    // button, both of which key off isActive.
    test('error is not treated as active', () {
      final s = sessionWithStatus('error');
      expect(s.isActive, isFalse);
      expect(s.isQueueing, isFalse);
      expect(s.canStop, isFalse);
    });
  });

  // The right-swipe terminates, and a confirm on every swipe is a confirm
  // people learn to tap through. It has to appear only where work is lost.
  group('needsTerminateConfirm', () {
    test('an idle session goes without asking', () {
      expect(needsTerminateConfirm([sessionWithStatus('idle')]), isFalse);
    });

    test('a terminated or errored session goes without asking', () {
      expect(needsTerminateConfirm([sessionWithStatus('terminated')]), isFalse);
      expect(needsTerminateConfirm([sessionWithStatus('error')]), isFalse);
    });

    test('every mid-turn state asks first', () {
      for (final status in [
        'active',
        'waiting_permission',
        'compacting',
        'starting',
      ]) {
        expect(
          needsTerminateConfirm([sessionWithStatus(status)]),
          isTrue,
          reason: status,
        );
      }
    });

    // A batch hides what it holds: the user picked a dozen rows and cannot see
    // which of them is still working.
    test('one busy session in a batch is enough', () {
      expect(
        needsTerminateConfirm([
          sessionWithStatus('idle'),
          sessionWithStatus('terminated'),
          sessionWithStatus('active'),
        ]),
        isTrue,
      );
    });

    test('an empty selection asks nothing', () {
      expect(needsTerminateConfirm([]), isFalse);
    });
  });

  // The daemon reclaims no memory on its own, so this label is what tells the
  // user which session is worth closing.
  group('memoryLabel', () {
    Session withMemory(int? bytes) => Session.fromJson({
      'session_id': 's1',
      'source': 'claude',
      'cwd': '/tmp/proj',
      'status': 'idle',
      'created_at': '2026-01-01T00:00:00Z',
      'memory_bytes': ?bytes,
    });

    test('megabytes below a gigabyte', () {
      expect(withMemory(412 * 1024 * 1024).memoryLabel, '412 MB');
    });

    test('gigabytes above one', () {
      expect(withMemory(1610612736).memoryLabel, '1.5 GB');
    });

    // A cold session runs nothing: an empty label is what keeps the chip off
    // the card entirely, rather than showing a misleading 0 MB.
    test('a cold session has no label', () {
      expect(withMemory(null).memoryLabel, isEmpty);
      expect(withMemory(0).memoryLabel, isEmpty);
    });
  });
}
