import 'package:flutter_test/flutter_test.dart';
import 'package:helios/models/notification.dart';
import 'package:helios/providers/card_registry.dart' as registry;
import 'package:helios/providers/notification_ext.dart';
import 'package:helios/services/notification_service.dart';

/// A notification of [type], with everything else plausible.
HeliosNotification notif(String type, {String status = 'pending'}) =>
    HeliosNotification(
      id: 'n1',
      source: type.split('.').first,
      sourceSession: 'sess-1',
      cwd: '/tmp/proj',
      type: type,
      status: status,
      createdAt: '2026-08-29T00:00:00Z',
    );

void main() {
  _migrationTests();
  group('a type splits into its provider and its kind', () {
    test('the split is where the first dot is', () {
      expect(notif('codex.permission').provider, 'codex');
      expect(notif('codex.permission').kind, 'permission');
      expect(notif('claude.elicitation.form').kind, 'elicitation.form');
    });

    test('a type with no dot has no kind and must not be mistaken for one', () {
      expect(notif('malformed').kind, '');
      expect(registry.isActionableType('malformed'), isFalse);
      expect(registry.kindOfType('malformed'), '');
    });
  });

  group('a request is handled the same way whoever raised it', () {
    // The regression this suite exists for. Before it, every one of these
    // switches was an allowlist of literal claude.* strings, so a codex
    // permission request was not actionable, raised no OS notification and
    // rendered no card — the agent waited and the phone never buzzed.
    for (final provider in ['claude', 'codex', 'someone-new']) {
      test('$provider requests are actionable', () {
        expect(registry.isActionableType('$provider.permission'), isTrue);
        expect(registry.isActionableType('$provider.question'), isTrue);
        expect(registry.isActionableType('$provider.trust'), isTrue);
        expect(registry.isActionableType('$provider.elicitation.form'), isTrue);
        expect(notif('$provider.permission').needsAction, isTrue);
      });

      test('$provider news is still news', () {
        expect(registry.isActionableType('$provider.done'), isFalse);
        expect(notif('$provider.done').needsAction, isFalse);
      });
    }
  });

  group('every kind of request has something to show', () {
    // The bug was a dispatch chain with no final else: an unrecognised type
    // fell out of it and raised nothing at all. Every kind, known or not, must
    // yield a heading and a body.
    for (final kind in [
      'permission',
      'question',
      'trust',
      'elicitation.form',
      'elicitation.url',
      'done',
      'error',
      'a-kind-nobody-has-written-yet',
      '',
    ]) {
      test(
        'kind ${kind.isEmpty ? '(empty)' : kind} has a label and a body',
        () {
          expect(registry.labelForKind(kind), isNotEmpty);
          expect(registry.bodyForKind(kind), isNotEmpty);
        },
      );
    }

    test('the fallback names no particular agent', () {
      for (final kind in ['permission', 'question', 'done', 'error']) {
        expect(
          registry.labelForKind(kind).toLowerCase(),
          isNot(contains('claude')),
          reason: 'the label for $kind names one provider',
        );
        expect(
          registry.bodyForKind(kind).toLowerCase(),
          isNot(contains('claude')),
        );
      }
    });
  });

  group('alert settings', () {
    // The safe direction. Silence is the dangerous failure: a blocked agent
    // nobody hears. This must survive any later refactor of the settings
    // screen.
    test('an unknown provider is noisy rather than silent', () {
      expect(
        NotificationService.instance.isAlertEnabled('codex.permission'),
        isTrue,
      );
      expect(
        NotificationService.instance.isAlertEnabled('nobody.knows'),
        isTrue,
      );
    });
  });

  group('display titles', () {
    test('the server title wins', () {
      final n = HeliosNotification(
        id: 'n1',
        source: 'codex',
        sourceSession: 's',
        cwd: '/tmp',
        type: 'codex.permission',
        status: 'pending',
        title: 'apply_patch',
        createdAt: '2026-08-29T00:00:00Z',
      );
      expect(n.displayTitle, 'apply_patch');
    });

    test('the fallback describes the request, not the agent', () {
      expect(notif('codex.permission').displayTitle, 'Permission request');
      expect(notif('claude.permission').displayTitle, 'Permission request');
    });
  });
}

// Alert settings moved from per-type keys to per-kind ones. A user who
// silenced a type before that would otherwise keep the old key for ever: the
// settings switch reads the kind and shows ON, flipping it writes the kind,
// and the stale per-type key still wins. Silent, with no way back but a reset.
void _migrationTests() {
  group('legacy alert keys', () {
    test('a silenced type carries over to its kind', () {
      final migrated = NotificationService.migrateLegacyAlertKeys({
        'claude.permission': false,
        'claude.done': true,
      });
      expect(
        migrated['permission'],
        isFalse,
        reason: 'the silence the user chose must survive',
      );
      expect(
        migrated.containsKey('claude.permission'),
        isFalse,
        reason: 'the stale key must not linger and override the switch',
      );
      expect(migrated['done'], isTrue);
    });

    test('off wins when two providers disagree', () {
      final migrated = NotificationService.migrateLegacyAlertKeys({
        'claude.permission': true,
        'codex.permission': false,
      });
      expect(migrated['permission'], isFalse);
    });

    test('keys already stored by kind pass through', () {
      final migrated = NotificationService.migrateLegacyAlertKeys({
        'permission': false,
      });
      expect(migrated['permission'], isFalse);
    });

    test(
      'a key naming no kind we know is kept verbatim rather than guessed',
      () {
        final migrated = NotificationService.migrateLegacyAlertKeys({
          'weird.thing': false,
        });
        expect(migrated['weird.thing'], isFalse);
      },
    );
  });
}
