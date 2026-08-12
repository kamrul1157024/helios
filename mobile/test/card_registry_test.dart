import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/notification.dart';
import 'package:helios/providers/card_registry.dart';
import 'package:helios/providers/claude/notification_ext.dart';

HeliosNotification _notif(String type, String status) => HeliosNotification(
      id: 'n1',
      source: 'claude',
      sourceSession: 's1',
      cwd: '/tmp',
      type: type,
      status: status,
      createdAt: '2026-01-01T00:00:00Z',
    );

const _allTypes = [
  'claude.permission',
  'claude.question',
  'claude.elicitation.form',
  'claude.elicitation.url',
  'claude.trust',
  'claude.done',
  'claude.error',
];

void main() {
  group('isActionableType', () {
    // The two predicates answer the same question from different inputs, and
    // they drift apart silently: one gates the OS notification, the other gates
    // the in-app card.
    test('agrees with needsClaudeAction for every type when pending', () {
      for (final type in _allTypes) {
        expect(
          isActionableType(type),
          _notif(type, 'pending').needsClaudeAction,
          reason: 'disagreement on $type',
        );
      }
    });

    test('is status-independent, unlike needsClaudeAction', () {
      expect(isActionableType('claude.permission'), isTrue);
      expect(_notif('claude.permission', 'approved').needsClaudeAction, isFalse);
    });

    test('does not treat terminal types as actionable', () {
      expect(isActionableType('claude.done'), isFalse);
      expect(isActionableType('something.else'), isFalse);
    });

    // claude.error gained a Retry action, so it is answerable now and a
    // resolved one must stop re-raising like any other approval.
    test('treats claude.error as actionable', () {
      expect(isActionableType('claude.error'), isTrue);
    });
  });

  group('shouldRaiseNotification', () {
    test('raises a pending approval', () {
      expect(
        shouldRaiseNotification(
          type: 'claude.permission',
          status: 'pending',
          alreadyPosted: false,
        ),
        isTrue,
      );
    });

    test('suppresses an approval that is already resolved', () {
      for (final status in ['approved', 'denied', 'resolved', 'timeout']) {
        expect(
          shouldRaiseNotification(
            type: 'claude.permission',
            status: status,
            alreadyPosted: false,
          ),
          isFalse,
          reason: 'status $status should not raise',
        );
      }
    });

    // Regression guard for the non-blanket resolved check. claude.done is
    // created with status "dismissed" rather than "pending", so gating every
    // type on status would silently kill "Task completed".
    test('raises claude.done even though its status is dismissed', () {
      expect(
        shouldRaiseNotification(
          type: 'claude.done',
          status: 'dismissed',
          alreadyPosted: false,
        ),
        isTrue,
      );
    });

    test('raises a pending error but not a retried one', () {
      expect(
        shouldRaiseNotification(
          type: 'claude.error',
          status: 'pending',
          alreadyPosted: false,
        ),
        isTrue,
      );
      // Retried from the desktop or the terminal: re-raising it on the phone
      // would offer a Retry button for a turn that already resumed.
      expect(
        shouldRaiseNotification(
          type: 'claude.error',
          status: 'approved',
          alreadyPosted: false,
        ),
        isFalse,
      );
    });

    test('does not raise the same notification twice', () {
      expect(
        shouldRaiseNotification(
          type: 'claude.permission',
          status: 'pending',
          alreadyPosted: true,
        ),
        isFalse,
      );
      expect(
        shouldRaiseNotification(
          type: 'claude.done',
          status: 'dismissed',
          alreadyPosted: true,
        ),
        isFalse,
      );
    });

    test('raises when the event carries no status at all', () {
      expect(
        shouldRaiseNotification(
          type: 'claude.permission',
          status: null,
          alreadyPosted: false,
        ),
        isTrue,
      );
    });
  });
}
