import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/notification.dart';
import 'package:helios/providers/notification_ext.dart';

HeliosNotification errorNotif(Map<String, dynamic> payload,
        {String status = 'pending'}) =>
    HeliosNotification.fromJson({
      'id': 'n1',
      'source': 'claude',
      'source_session': 'sess-1',
      'cwd': '/tmp/proj',
      'type': 'claude.error',
      'status': status,
      'title': 'Session error',
      'detail': 'API Error: Response stalled mid-stream.',
      // The daemon stores the payload as a JSON string.
      'payload': jsonEncode(payload),
      'created_at': '2026-01-01T00:00:00Z',
    });

void main() {
  group('error payload accessors', () {
    test('reads the fields handleStopFailure writes', () {
      final n = errorNotif({
        'session_id': 'sess-1',
        'error': 'API Error: Stream idle timeout - no chunks received',
        'is_rate_limit': false,
        'retryable': true,
      });

      expect(n.errorSessionId, 'sess-1');
      expect(n.errorText, 'API Error: Stream idle timeout - no chunks received');
      expect(n.isRateLimit, isFalse);
      expect(n.isRetryable, isTrue);
      expect(n.rateLimitResetAt, isNull);
    });

    test('parses reset_at as UTC', () {
      final n = errorNotif({
        'session_id': 'sess-1',
        'error': 'Claude AI usage limit reached|1754899200',
        'is_rate_limit': true,
        'retryable': true,
        'reset_at': '2025-08-11T08:00:00Z',
      });

      expect(n.isRateLimit, isTrue);
      expect(n.rateLimitResetAt, DateTime.utc(2025, 8, 11, 8));
      expect(n.rateLimitResetAt!.isUtc, isTrue);
    });

    // An unknown window is not a reason to lock the user out of retrying, so
    // the card must see null rather than a fabricated time.
    test('a rate limit with no reset_at yields a null reset', () {
      final n = errorNotif({
        'session_id': 'sess-1',
        'error': 'Claude AI usage limit reached',
        'is_rate_limit': true,
        'retryable': true,
      });

      expect(n.isRateLimit, isTrue);
      expect(n.rateLimitResetAt, isNull);
    });

    test('an unparseable reset_at yields null rather than throwing', () {
      final n = errorNotif({
        'session_id': 'sess-1',
        'reset_at': 'tomorrow-ish',
      });

      expect(n.rateLimitResetAt, isNull);
    });

    // Rows written before this change carry no payload at all.
    test('a payload-less notification degrades cleanly', () {
      final n = HeliosNotification.fromJson({
        'id': 'n1',
        'source': 'claude',
        'source_session': 'sess-1',
        'cwd': '/tmp/proj',
        'type': 'claude.error',
        'status': 'pending',
        'detail': 'helios: commit',
        'created_at': '2026-01-01T00:00:00Z',
      });

      expect(n.errorText, isNull);
      expect(n.isRateLimit, isFalse);
      expect(n.rateLimitResetAt, isNull);
      // The card falls back to the detail so the row is never blank.
      expect(n.displayDetail, 'helios: commit');
    });
  });

  group('needsAction', () {
    // It has a Retry button now, so it belongs in the dashboard's pending
    // bucket rather than the passive "active" one.
    test('a pending error needs action', () {
      expect(errorNotif({'retryable': true}).needsAction, isTrue);
    });

    test('a resolved error does not', () {
      expect(
        errorNotif({'retryable': true}, status: 'approved').needsAction,
        isFalse,
      );
      expect(
        errorNotif({'retryable': true}, status: 'dismissed').needsAction,
        isFalse,
      );
    });

    test('claude.done is still not actionable', () {
      final done = HeliosNotification.fromJson({
        'id': 'n2',
        'source': 'claude',
        'source_session': 'sess-1',
        'cwd': '/tmp/proj',
        'type': 'claude.done',
        'status': 'pending',
        'created_at': '2026-01-01T00:00:00Z',
      });
      expect(done.needsAction, isFalse);
    });
  });
}
