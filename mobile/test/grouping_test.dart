import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/session.dart';
import 'package:helios/models/session_group.dart';
import 'package:helios/utils/grouping.dart';

/// The tree the phone draws, and the rules it inherits from the desktop's
/// `grouping.ts`. Pinned here because two clients read one daemon: if this port
/// drifts, the same groups render in two different orders.

Session sessionIn(
  String id, {
  String group = '',
  List<SessionGroup> path = const [],
  int order = 0,
  String cwd = '/tmp/p',
  String? lastEventAt,
  String createdAt = '2026-01-01T00:00:00Z',
}) => Session(
  sessionId: id,
  source: 'claude',
  cwd: cwd,
  project: 'p',
  status: 'idle',
  createdAt: createdAt,
  lastEventAt: lastEventAt,
  sortOrder: order,
  groupKey: group,
  groupPath: path,
);

SessionGroup groupAt(
  String key, {
  String name = '',
  String parent = '',
  int position = 0,
}) => SessionGroup(
  key: key,
  name: name.isEmpty ? key : name,
  parent: parent,
  position: position,
);

void main() {
  group('buildTree', () {
    // The catalogue decides what exists, not the sessions. Deriving nodes from
    // session paths means a group with nothing in it does not render, so the
    // first thing anyone does after making one has nowhere to happen.
    test('a group with nothing in it still renders', () {
      final tree = buildTree([], [groupAt('g1', name: 'Work')]);
      expect(tree, hasLength(1));
      expect(tree.first.name, 'Work');
      expect(tree.first.total, 0);
    });

    test('a session with no group lands in Ungrouped', () {
      final tree = buildTree([sessionIn('s1')], [groupAt('g1')]);
      expect(tree.map((n) => n.key), ['g1', '']);
      expect(tree.last.name, kUngroupedName);
      expect(tree.last.sessions.single.sessionId, 's1');
    });

    test('Ungrouped is absent when every session is filed', () {
      final tree = buildTree([sessionIn('s1', group: 'g1')], [groupAt('g1')]);
      expect(tree.map((n) => n.key), ['g1']);
    });

    test('Ungrouped sorts after every stored group', () {
      final tree = buildTree(
        [sessionIn('loose'), sessionIn('filed', group: 'g2')],
        [groupAt('g2', position: 9)],
      );
      expect(tree.last.isUngrouped, isTrue);
    });

    test('total counts the subtree, not the level', () {
      final tree = buildTree(
        [
          sessionIn('s1', group: 'parent'),
          sessionIn('s2', group: 'child'),
          sessionIn('s3', group: 'child'),
        ],
        [groupAt('parent'), groupAt('child', parent: 'parent')],
      );
      final parent = tree.single;
      expect(parent.sessions, hasLength(1));
      expect(parent.children.single.total, 2);
      expect(parent.total, 3);
    });

    test('siblings sort by position, then by name', () {
      final tree = buildTree([], [
        groupAt('b', name: 'Beta', position: 1),
        groupAt('a', name: 'Alpha', position: 1),
        groupAt('z', name: 'Zed', position: 0),
      ]);
      expect(tree.map((n) => n.name), ['Zed', 'Alpha', 'Beta']);
    });

    // A parent that is not in the catalogue would otherwise strand its child
    // where nothing renders it.
    test('a group whose parent is missing hangs at the root', () {
      final tree = buildTree([], [groupAt('orphan', parent: 'gone')]);
      expect(tree.map((n) => n.key), ['orphan']);
    });
  });

  group('rankOf and byRank', () {
    test('a subgroup sorts above a loose session in the same parent', () {
      final loose = sessionIn(
        'loose',
        group: 'p',
        path: [groupAt('p', position: 0)],
        order: 0,
      );
      final nested = sessionIn(
        'nested',
        group: 'c',
        path: [groupAt('p', position: 0), groupAt('c', position: 3)],
        order: 100,
      );
      expect(byRank(rankOf(nested, 2), rankOf(loose, 2)), lessThan(0));
    });

    test('a session in an earlier group sorts first whatever its own order', () {
      final early = sessionIn(
        'early',
        group: 'a',
        path: [groupAt('a', position: 0)],
        order: 900,
      );
      final late = sessionIn(
        'late',
        group: 'b',
        path: [groupAt('b', position: 1)],
        order: -900,
      );
      expect(byRank(rankOf(early, 1), rankOf(late, 1)), lessThan(0));
    });

    test('depthOf is the most groups any one session holds', () {
      expect(
        depthOf([
          sessionIn('a'),
          sessionIn('b', path: [groupAt('p'), groupAt('c', parent: 'p')]),
        ]),
        2,
      );
    });
  });

  group('buildCwdTree', () {
    test('one node per directory, labelled by its last segment', () {
      final tree = buildCwdTree([
        sessionIn('s1', cwd: '/Users/me/workspace/helios'),
        sessionIn('s2', cwd: '/Users/me/workspace/helios'),
        sessionIn('s3', cwd: '/Users/me/workspace/opal'),
      ]);
      expect(tree, hasLength(2));
      expect(tree.map((n) => n.name).toSet(), {'helios', 'opal'});
      expect(tree.map((n) => n.total).reduce((a, b) => a + b), 3);
    });

    test('activity puts the newest directory first', () {
      final tree = buildCwdTree([
        sessionIn('old', cwd: '/a', lastEventAt: '2026-01-01T00:00:00Z'),
        sessionIn('new', cwd: '/b', lastEventAt: '2026-06-01T00:00:00Z'),
      ]);
      expect(tree.first.key, '/b');
    });

    test('a session that has done nothing falls back to when it was made', () {
      final tree = buildCwdTree([
        sessionIn('fresh', cwd: '/fresh', createdAt: '2026-06-01T00:00:00Z'),
        sessionIn('older', cwd: '/older', lastEventAt: '2026-01-01T00:00:00Z'),
      ]);
      expect(tree.first.key, '/fresh');
    });

    test('name orders A to Z', () {
      final tree = buildCwdTree(
        [sessionIn('s1', cwd: '/z/zed'), sessionIn('s2', cwd: '/a/alpha')],
        GroupOrder.name,
      );
      expect(tree.map((n) => n.name), ['alpha', 'zed']);
    });

    // Turning manual on must not scatter the directories nobody has touched,
    // and one seen for the first time belongs at the end rather than somewhere
    // arbitrary in the middle.
    test('manual keeps the unplaced directories after the placed ones', () {
      final tree = buildCwdTree(
        [
          sessionIn('s1', cwd: '/a', lastEventAt: '2026-06-01T00:00:00Z'),
          sessionIn('s2', cwd: '/b', lastEventAt: '2026-01-01T00:00:00Z'),
          sessionIn('s3', cwd: '/c', lastEventAt: '2026-03-01T00:00:00Z'),
        ],
        GroupOrder.manual,
        {'/b': 0},
      );
      expect(tree.map((n) => n.key), ['/b', '/a', '/c']);
    });

    test('cwdLabel falls back to the whole path when there is no segment', () {
      expect(cwdLabel('/Users/me/proj/'), 'proj');
      expect(cwdLabel('/'), '/');
      expect(cwdLabel(''), 'sessions');
    });
  });

  group('flattenSessions', () {
    // The daemon holds one flat order per host, so a drag inside one node has
    // to name every other session too.
    test('returns render order: children before the sessions above them', () {
      final tree = buildTree(
        [
          sessionIn('loose', group: 'parent'),
          sessionIn('nested', group: 'child'),
          sessionIn('unfiled'),
        ],
        [groupAt('parent'), groupAt('child', parent: 'parent')],
      );
      expect(flattenSessions(tree).map((s) => s.sessionId), [
        'nested',
        'loose',
        'unfiled',
      ]);
    });
  });

  group('GroupCatalog', () {
    final catalog = GroupCatalog(
      groups: [
        groupAt('root', position: 0),
        groupAt('mid', parent: 'root'),
        groupAt('leaf', parent: 'mid'),
        groupAt('other', position: 1),
      ],
    );

    test('a group cannot be moved into its own descendant', () {
      expect(catalog.isDescendant('leaf', 'root'), isTrue);
      expect(catalog.isDescendant('mid', 'root'), isTrue);
      expect(catalog.isDescendant('root', 'leaf'), isFalse);
      expect(catalog.isDescendant('other', 'root'), isFalse);
    });

    test('a group is not its own descendant', () {
      expect(catalog.isDescendant('root', 'root'), isFalse);
    });

    test('childrenOf reads one level, in position order', () {
      expect(catalog.childrenOf('').map((g) => g.key), ['root', 'other']);
      expect(catalog.childrenOf('root').map((g) => g.key), ['mid']);
    });

    test('ancestryOf reads outermost first', () {
      expect(catalog.ancestryOf('leaf').map((g) => g.key), [
        'root',
        'mid',
        'leaf',
      ]);
      expect(catalog.ancestryOf(''), isEmpty);
    });

    // Data that already holds a cycle must not hang the walk.
    test('a cycle in the catalogue terminates', () {
      final looped = GroupCatalog(
        groups: [groupAt('a', parent: 'b'), groupAt('b', parent: 'a')],
      );
      expect(looped.isDescendant('a', 'zzz'), isFalse);
      expect(looped.ancestryOf('a'), hasLength(2));
    });
  });

  group('Session.fromJson', () {
    test('reads group_key and group_path', () {
      final session = Session.fromJson({
        'session_id': 's1',
        'status': 'idle',
        'created_at': '2026-01-01T00:00:00Z',
        'group_key': 'g1',
        'group_path': [
          {'key': 'g0', 'name': 'Work', 'position': 2},
          {'key': 'g1', 'name': 'helios', 'position': 0},
        ],
      });
      expect(session.groupKey, 'g1');
      expect(session.groupPath.map((g) => g.name), ['Work', 'helios']);
      expect(session.groupPath.last.position, 0);
    });

    test('an old daemon leaves both empty', () {
      final session = Session.fromJson({
        'session_id': 's1',
        'status': 'idle',
        'created_at': '2026-01-01T00:00:00Z',
      });
      expect(session.groupKey, '');
      expect(session.groupPath, isEmpty);
    });
  });
}
