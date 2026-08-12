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
      for (final status in ['active', 'waiting_permission', 'compacting', 'starting']) {
        expect(sessionWithStatus(status).canSendPrompt, isFalse,
            reason: '$status without queueing');
        expect(sessionWithStatus(status, queue: true).canSendPrompt, isTrue,
            reason: '$status with queueing');
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
}
