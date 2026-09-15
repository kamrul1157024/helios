import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:helios/models/session.dart';
import 'package:helios/models/session_group.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/providers/grouping_providers.dart';
import 'package:helios/services/api_client.dart';
import 'package:helios/services/daemon_api_service.dart';

/// What the phone says to the daemon about groups, and what it makes of the
/// answer. The 404 is the interesting one: a daemon from before grouping is not
/// a broken daemon, and the list has to keep working against it.

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

MockClient clientWhere(http.Response Function(http.Request) onCall) {
  return MockClient((req) async {
    if (req.url.path.contains('/auth/')) {
      return http.Response(
        jsonEncode({
          'token': 't',
          'expires_at': DateTime.now()
              .toUtc()
              .add(const Duration(hours: 1))
              .toIso8601String(),
        }),
        200,
      );
    }
    return onCall(req);
  });
}

/// A container whose one host answers with [onCall].
ProviderContainer containerFor(http.Response Function(http.Request) onCall) {
  final container = ProviderContainer(
    overrides: [
      serviceProvider(
        'h1',
      ).overrideWithValue(serviceReturning(clientWhere(onCall))),
    ],
  );
  addTearDown(container.dispose);
  return container;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  group('listGroups', () {
    test('reads the catalogue', () async {
      final svc = serviceReturning(
        clientWhere(
          (_) => http.Response(
            jsonEncode({
              'groups': [
                {'key': 'g1', 'name': 'Work', 'parent': '', 'position': 0},
                {'key': 'g2', 'name': 'helios', 'parent': 'g1', 'position': 1},
              ],
            }),
            200,
          ),
        ),
      );

      final groups = await svc.listGroups();
      expect(groups.map((g) => g.key), ['g1', 'g2']);
      expect(groups.last.parent, 'g1');
      expect(groups.last.position, 1);
    });

    // The caller has to tell a daemon that cannot hold groups from one that
    // failed, so this throws with the status rather than answering empty.
    test('a 404 is reported as a 404, not as an empty catalogue', () async {
      final svc = serviceReturning(
        clientWhere((_) => http.Response('no such route', 404)),
      );

      await expectLater(
        svc.listGroups(),
        throwsA(
          isA<HeliosApiException>().having((e) => e.statusCode, 'status', 404),
        ),
      );
    });

    test('a 500 is reported too', () async {
      final svc = serviceReturning(
        clientWhere((_) => http.Response('boom', 500)),
      );

      await expectLater(
        svc.listGroups(),
        throwsA(
          isA<HeliosApiException>().having((e) => e.statusCode, 'status', 500),
        ),
      );
    });
  });

  group('writes', () {
    test('createGroup names its parent and reads the group back', () async {
      Map<String, dynamic>? sent;
      final svc = serviceReturning(
        clientWhere((req) {
          sent = jsonDecode(req.body) as Map<String, dynamic>;
          return http.Response(
            jsonEncode({
              'key': 'g9',
              'name': 'Spikes',
              'parent': 'g1',
              'position': 2,
            }),
            200,
          );
        }),
      );

      final made = await svc.createGroup('Spikes', parent: 'g1');
      expect(sent, {'name': 'Spikes', 'parent': 'g1'});
      expect(made?.key, 'g9');
      expect(made?.position, 2);
    });

    test('patchGroup sends only the field being changed', () async {
      final bodies = <Map<String, dynamic>>[];
      final svc = serviceReturning(
        clientWhere((req) {
          bodies.add(jsonDecode(req.body) as Map<String, dynamic>);
          return http.Response('{"success":true}', 200);
        }),
      );

      await svc.patchGroup('g1', name: 'Renamed');
      await svc.patchGroup('g1', parent: '');
      expect(bodies, [
        {'name': 'Renamed'},
        {'parent': ''},
      ]);
    });

    test('setGroupOrder names the parent whose children moved', () async {
      Map<String, dynamic>? sent;
      final svc = serviceReturning(
        clientWhere((req) {
          sent = jsonDecode(req.body) as Map<String, dynamic>;
          return http.Response('{"success":true}', 200);
        }),
      );

      expect(await svc.setGroupOrder('g1', ['b', 'a']), isTrue);
      expect(sent, {
        'parent': 'g1',
        'order': ['b', 'a'],
      });
    });

    test('a refused write answers false rather than throwing', () async {
      final svc = serviceReturning(
        clientWhere((_) => http.Response('nope', 500)),
      );

      expect(await svc.deleteGroup('g1'), isFalse);
      expect(await svc.patchGroup('g1', name: 'x'), isFalse);
      expect(await svc.createGroup('x'), isNull);
    });
  });

  group('filing a session', () {
    // An unfile is a value, not an omission: the daemon reads an empty group as
    // "take it out of whatever it is in".
    test('an empty group is still sent', () async {
      Map<String, dynamic>? sent;
      final svc = serviceReturning(
        clientWhere((req) {
          sent = jsonDecode(req.body) as Map<String, dynamic>;
          return http.Response('{"success":true}', 200);
        }),
      );

      await svc.patchSession('s1', group: '');
      expect(sent, {'group': ''});
    });

    test('a pin still says nothing about the group', () async {
      Map<String, dynamic>? sent;
      final svc = serviceReturning(
        clientWhere((req) {
          sent = jsonDecode(req.body) as Map<String, dynamic>;
          return http.Response('{"success":true}', 200);
        }),
      );

      await svc.patchSession('s1', pinned: true);
      expect(sent, {'pinned': true});
    });
  });

  group('listSessions', () {
    // The tree needs the resolved ancestry, and the query is the cache key, so
    // it is asked for every time rather than only when the tree is on screen.
    test('always asks for the group path', () async {
      Uri? asked;
      final svc = serviceReturning(
        clientWhere((req) {
          asked = req.url;
          return http.Response(jsonEncode({'sessions': []}), 200);
        }),
      );

      await svc.listSessions(const SessionQuery());
      expect(asked?.queryParameters['grouped'], '1');
    });
  });

  group('groupsProvider', () {
    test('an old daemon reads as unsupported, not as an error', () async {
      final container = containerFor((_) => http.Response('nope', 404));

      final catalog = await container.read(groupsProvider('h1').future);
      expect(catalog.unsupported, isTrue);
      expect(catalog.groups, isEmpty);
    });

    test('a real failure is still a failure', () async {
      final container = containerFor((_) => http.Response('boom', 500));

      await expectLater(
        container.read(groupsProvider('h1').future),
        throwsA(isA<HeliosApiException>()),
      );
    });
  });

  group('filing a session through the cache', () {
    Future<ProviderContainer> holding(
      List<Map<String, dynamic>> sessions,
      http.Response Function(http.Request) onWrite,
    ) async {
      final container = containerFor((req) {
        if (req.method == 'GET' && req.url.path == '/api/sessions') {
          return http.Response(jsonEncode({'sessions': sessions}), 200);
        }
        return onWrite(req);
      });
      await container.read(sessionsProvider(allSessionsKey('h1')).future);
      return container;
    }

    Map<String, dynamic> row(String id) => {
      'session_id': id,
      'source': 'claude',
      'cwd': '/tmp/p',
      'project': 'p',
      'status': 'idle',
      'created_at': '2026-01-01T00:00:00Z',
    };

    Session only(ProviderContainer c) =>
        c.read(sessionsProvider(allSessionsKey('h1'))).valueOrNull!.single;

    test('paints the group and its path before the daemon answers', () async {
      final c = await holding([row('a')], (_) => http.Response('{}', 200));

      final write = c
          .read(sessionsProvider(allSessionsKey('h1')).notifier)
          .patch(
            'a',
            group: 'g1',
            groupPath: const [SessionGroup(key: 'g1', name: 'Work')],
          );
      await Future.microtask(() {});

      expect(only(c).groupKey, 'g1');
      expect(only(c).groupPath.single.name, 'Work');
      expect(await write, isTrue);
    });

    test('puts the old group back when the daemon refuses', () async {
      final c = await holding([row('a')], (_) => http.Response('nope', 500));

      expect(
        await c
            .read(sessionsProvider(allSessionsKey('h1')).notifier)
            .patch('a', group: 'g1'),
        isFalse,
      );
      expect(only(c).groupKey, '');
    });
  });
}
