import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/models/session.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';

/// The writes that paint before the daemon answers, and put it back when the
/// daemon refuses.
///
/// These are pinned because they are the behaviour a move to a keyed cache has
/// to carry across unchanged. Nothing else in the suite covers them, so without
/// this file a migration could drop the rollback and every test would still be
/// green.

DaemonAPIService serviceReturning(MockClient client) => DaemonAPIService(
      hostId: 'h1',
      serverUrl: 'http://localhost:1',
      api: ApiClient(
        serverUrl: 'http://localhost:1',
        deviceId: 'd1',
        privateKeySeed: Uint8List(32),
        client: client,
      ),
    );

/// Answers the token handshake, then defers to [onWrite] for everything else.
MockClient clientWhere(http.Response Function(http.Request) onWrite) {
  return MockClient((req) async {
    if (req.url.path.contains('/auth/')) {
      return http.Response(
        jsonEncode({
          'token': 't',
          'expires_at':
              DateTime.now().toUtc().add(const Duration(hours: 1)).toIso8601String(),
        }),
        200,
      );
    }
    return onWrite(req);
  });
}

Map<String, dynamic> sessionJson(String id, {int order = 0, bool pinned = false, String? title}) => {
      'session_id': id,
      'source': 'claude',
      'cwd': '/tmp/p',
      'project': 'p',
      'status': 'idle',
      'created_at': '2026-01-01T00:00:00Z',
      'sort_order': order,
      'pinned': pinned,
      'title': ?title,
    };

/// A container whose session list has loaded with [sessions], and whose writes
/// are answered by [onWrite].
Future<ProviderContainer> containerHolding(
  List<Map<String, dynamic>> sessions,
  http.Response Function(http.Request) onWrite,
) async {
  final svc = serviceReturning(clientWhere((req) {
    if (req.method == 'GET' && req.url.path == '/api/sessions') {
      return http.Response(jsonEncode({'sessions': sessions}), 200);
    }
    return onWrite(req);
  }));
  final container = ProviderContainer(
    overrides: [serviceProvider('h1').overrideWithValue(svc)],
  );
  addTearDown(container.dispose);
  await container.read(sessionsProvider(allSessionsKey('h1')).future);
  return container;
}

List<Session> rowsOf(ProviderContainer c) =>
    c.read(sessionsProvider(allSessionsKey('h1'))).valueOrNull!;

SessionsNotifier writerOf(ProviderContainer c) =>
    c.read(sessionsProvider(allSessionsKey('h1')).notifier);

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  // The unfiltered list opens from disk before it fetches, so the store has to
  // exist even when it is empty.
  setUp(() => SharedPreferences.setMockInitialValues({}));

  group('reorder', () {
    test('paints the new order before the daemon answers', () async {
      final c = await containerHolding(
        [sessionJson('a', order: 0), sessionJson('b', order: 1)],
        (_) => http.Response('{}', 200),
      );

      final write = writerOf(c).reorder(['b', 'a']);

      expect(rowsOf(c).firstWhere((s) => s.sessionId == 'b').sortOrder, 0);
      expect(rowsOf(c).firstWhere((s) => s.sessionId == 'a').sortOrder, 1);
      expect(await write, isTrue);
    });

    test('puts the old order back when the daemon refuses', () async {
      final c = await containerHolding(
        [sessionJson('a', order: 0), sessionJson('b', order: 1)],
        (_) => http.Response('nope', 500),
      );

      expect(await writerOf(c).reorder(['b', 'a']), isFalse);
      expect(rowsOf(c).firstWhere((s) => s.sessionId == 'a').sortOrder, 0);
      expect(rowsOf(c).firstWhere((s) => s.sessionId == 'b').sortOrder, 1);
    });
  });

  group('patch', () {
    test('puts the original row back when the daemon refuses', () async {
      final c = await containerHolding(
        [sessionJson('a', title: 'before')],
        (_) => http.Response('nope', 500),
      );

      expect(await writerOf(c).patch('a', title: 'after', pinned: true), isFalse);
      final row = rowsOf(c).single;
      expect(row.title, 'before');
      expect(row.pinned, isFalse);
    });

    // The paint is deferred by a microtask so a sheet that triggered the write
    // finishes popping first — rebuilding its dependents inside that transition
    // trips the framework's _dependents.isEmpty assertion.
    test('defers the paint past the current synchronous turn', () async {
      final c = await containerHolding(
        [sessionJson('a', title: 'before')],
        (_) => http.Response(jsonEncode({'ok': true}), 200),
      );

      final write = writerOf(c).patch('a', title: 'after');
      expect(rowsOf(c).single.title, 'before',
          reason: 'must not repaint synchronously');

      await write;
      expect(rowsOf(c).single.title, 'after', reason: 'the paint still arrives');
    });
  });

  group('delete', () {
    test('drops the row, and puts it back when the daemon refuses', () async {
      final c = await containerHolding(
        [sessionJson('a'), sessionJson('b')],
        (_) => http.Response('nope', 500),
      );

      expect(await writerOf(c).delete('a'), isFalse);
      expect(rowsOf(c).map((s) => s.sessionId), ['a', 'b']);
    });
  });

  group('the settings document', () {
    /// A container wired to one host, whose settings read succeeds and whose
    /// writes are refused.
    ProviderContainer containerRefusingWrites() {
      final svc = serviceReturning(clientWhere((req) {
        if (req.method == 'GET') {
          return http.Response(
            jsonEncode({
              'settings': {
                DaemonAPIService.settingAutoTitle: 'true',
                DaemonAPIService.settingEvict: 'true',
              },
            }),
            200,
          );
        }
        return http.Response('nope', 500);
      }));
      final container = ProviderContainer(
        overrides: [serviceProvider('h1').overrideWithValue(svc)],
      );
      addTearDown(container.dispose);
      return container;
    }

    test('a refused write reverts only the field that was written', () async {
      final container = containerRefusingWrites();
      final settings = await container.read(hostSettingsProvider('h1').future);
      expect(settings.autoTitleEnabled, isTrue);
      expect(settings.evictEnabled, isTrue);

      final ok = await container
          .read(hostSettingsProvider('h1').notifier)
          .setEvictEnabled(false);
      expect(ok, isFalse);

      final after = container.read(hostSettingsProvider('h1')).valueOrNull!;
      expect(after.evictEnabled, isTrue, reason: 'the refused write reverts');
      expect(after.autoTitleEnabled, isTrue, reason: 'its neighbour is untouched');
    });

    // The daemon merges by key, so the cache has to merge by field. Replacing
    // the document would blank whatever the other panes own.
    test('a write leaves the fields it did not name alone', () async {
      final container = containerRefusingWrites();
      await container.read(hostSettingsProvider('h1').future);

      // Paints before the refusal lands, so the neighbour is observable mid-write.
      final pending = container
          .read(hostSettingsProvider('h1').notifier)
          .setEvictEnabled(false);
      final during = container.read(hostSettingsProvider('h1')).valueOrNull!;
      expect(during.evictEnabled, isFalse, reason: 'painted before the answer');
      expect(during.autoTitleEnabled, isTrue, reason: 'not named, not touched');
      await pending;
    });

    test('the budget is clamped to the slider travel', () async {
      final container = containerRefusingWrites();
      await container.read(hostSettingsProvider('h1').future);

      final pending = container
          .read(hostSettingsProvider('h1').notifier)
          .setBudgetFraction(5);
      final during = container.read(hostSettingsProvider('h1')).valueOrNull!;
      expect(during.budgetFraction, DaemonAPIService.budgetMax);
      await pending;
    });
  });
}
