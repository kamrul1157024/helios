import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/session.dart';
import 'package:helios/models/session_group.dart';
import 'package:helios/utils/grouping.dart';

/// The fork tree the phone draws. A port of the desktop's `forks.test.ts`, and
/// pinned for the same reason the grouping tests are: two clients read one
/// daemon, and a family that nests one way here and another way there is two
/// answers to one question.

const helios = SessionGroup(key: 'g_helios', name: 'helios', position: 0);

Session root(String id, {String cwd = '/x/repo', SessionGroup? group}) => Session(
  sessionId: id,
  source: 'claude',
  cwd: cwd,
  project: 'repo',
  status: 'idle',
  createdAt: '2026-09-19T00:00:00Z',
  groupKey: group?.key ?? '',
  groupPath: group == null ? const [] : [group],
);

Session fork(
  String id,
  String parent,
  String at, {
  String cwd = '/x/repo',
  SessionGroup? group,
}) => Session(
  sessionId: id,
  source: 'claude',
  cwd: cwd,
  project: 'repo',
  status: 'idle',
  createdAt: '2026-09-19T00:00:00Z',
  groupKey: group?.key ?? '',
  groupPath: group == null ? const [] : [group],
  forkedFrom: parent,
  forkedAt: at,
);

bool never(String _) => false;
bool always(String _) => true;

void main() {
  test('a fork nests only when its parent is in the same list', () {
    final sessions = [root('a'), fork('b', 'a', '1'), fork('orphan', 'gone', '2')];
    final present = idsOf(sessions);

    expect(isNestedFork(sessions[1], present), isTrue);
    expect(isNestedFork(sessions[2], present), isFalse,
        reason: 'an orphan draws as a root rather than never drawing');
    expect(isNestedFork(sessions[0], present), isFalse);
  });

  test('siblings are ordered by when the branch was taken', () {
    final forks = forksByParent([
      fork('late', 'a', '2026-09-19T12:00:00Z'),
      fork('early', 'a', '2026-09-19T09:00:00Z'),
    ]);
    expect(forks['a']!.map((s) => s.sessionId), ['early', 'late']);
  });

  test('a family is drawn depth-first, parent then its own forks', () {
    final sessions = [
      root('r'),
      fork('a', 'r', '1'),
      fork('deep', 'a', '2'),
      fork('b', 'r', '3'),
    ];
    final rows = familyRows(sessions.first, forksByParent(sessions), never);

    expect(
      rows.map((row) => '${row.session.sessionId}:${row.depth}'),
      ['r:0', 'a:1', 'deep:2', 'b:1'],
    );
  });

  test('the trunk says which ancestor lines run past each row', () {
    final sessions = [
      root('r'),
      fork('a', 'r', '1'),
      fork('deep', 'a', '2'),
      fork('b', 'r', '3'),
    ];
    final rows = familyRows(sessions.first, forksByParent(sessions), never);
    FamilyRow by(String id) =>
        rows.firstWhere((row) => row.session.sessionId == id);

    expect(by('r').trunk, isEmpty, reason: 'a root has no lines to its left');
    // b is still to come, so a's own column carries on past it.
    expect(by('a').trunk, [true]);
    expect(by('a').last, isFalse);
    // deep sits under a, and a still has b below: the outer column runs
    // through. The last entry is deep's own elbow, and it is an only child.
    expect(by('deep').trunk, [true, false]);
    expect(by('deep').last, isTrue);
    expect(by('b').trunk, [false]);
    expect(by('b').last, isTrue);
  });

  test("an ancestor's column goes blank once its last child is drawn", () {
    final sessions = [
      root('r'),
      fork('a', 'r', '1'),
      fork('x', 'a', '2'),
      fork('y', 'a', '3'),
    ];
    final rows = familyRows(sessions.first, forksByParent(sessions), never);
    FamilyRow by(String id) =>
        rows.firstWhere((row) => row.session.sessionId == id);

    expect(by('a').trunk, [false]);
    expect(by('x').trunk, [false, true], reason: 'blank under r, line under a');
    expect(by('y').trunk, [false, false]);
  });

  test('folding a row hides what is under it and nothing beside it', () {
    final sessions = [
      root('r'),
      fork('a', 'r', '1'),
      fork('deep', 'a', '2'),
      fork('b', 'r', '3'),
    ];
    final rows = familyRows(
      sessions.first,
      forksByParent(sessions),
      (id) => id == 'a',
    );
    expect(rows.map((row) => row.session.sessionId), ['r', 'a', 'b']);
  });

  test('folding the root leaves only the root', () {
    final sessions = [root('r'), fork('a', 'r', '1')];
    final rows = familyRows(sessions.first, forksByParent(sessions), always);
    expect(rows.map((row) => row.session.sessionId), ['r']);
  });

  test('depth stops growing past the cap', () {
    final sessions = <Session>[root('s0')];
    for (var i = 1; i <= kMaxForkDepth + 3; i++) {
      sessions.add(fork('s$i', 's${i - 1}', '$i'));
    }
    final rows = familyRows(sessions.first, forksByParent(sessions), never);

    expect(rows.length, sessions.length, reason: 'every session still draws');
    expect(rows.map((row) => row.depth).reduce((a, b) => a > b ? a : b),
        kMaxForkDepth);
    for (final row in rows) {
      expect(row.trunk.length, lessThanOrEqualTo(kMaxForkDepth));
    }
  });

  test('a cycle draws each session once instead of hanging', () {
    final a = fork('a', 'b', '1');
    final b = fork('b', 'a', '2');
    final rows = familyRows(a, forksByParent([a, b]), never);
    expect(rows.map((row) => row.session.sessionId), ['a', 'b']);
  });

  test('a group node holds roots only, but counts the whole family', () {
    final sessions = [
      root('r', group: helios),
      fork('a', 'r', '1', group: helios),
    ];
    final nodes = buildTree(sessions, [helios]);

    expect(nodes.first.sessions.map((s) => s.sessionId), ['r'],
        reason: 'the fork is drawn by its parent');
    expect(nodes.first.total, 2,
        reason: 'a folded family must not shrink the header count');
  });

  test('grouping by directory keeps a family whole', () {
    // The whole point of the default: a fork gets a worktree of its own, which
    // is a different directory. Grouping by directory must not split the pair.
    final sessions = [
      root('r', cwd: '/repo/helios'),
      fork('a', 'r', '1', cwd: '/repo/helios-worktrees/try'),
    ];
    final nodes = buildCwdTree(sessions);

    expect(nodes.length, 1, reason: 'one directory node, not two');
    expect(nodes.first.key, '/repo/helios');
    expect(nodes.first.sessions.map((s) => s.sessionId), ['r']);
    expect(nodes.first.total, 2);
  });

  test('an orphan fork gets a node of its own rather than vanishing', () {
    final nodes = buildCwdTree([fork('lonely', 'gone', '1', cwd: '/repo/x')]);
    expect(nodes.length, 1);
    expect(nodes.first.sessions.map((s) => s.sessionId), ['lonely']);
  });

  test('the fork fields survive a round trip from the daemon', () {
    final session = Session.fromJson({
      'session_id': 'f1',
      'source': 'claude',
      'cwd': '/x',
      'project': 'x',
      'status': 'idle',
      'created_at': '2026-09-19T00:00:00Z',
      'forked_from': 'p1',
      'forked_at': '2026-09-19T10:00:00Z',
      'fork_count': 2,
      'root_session_id': 'p1',
    });

    expect(session.isFork, isTrue);
    expect(session.forkedFrom, 'p1');
    expect(session.forkCount, 2);
    expect(session.rootSessionId, 'p1');
  });
}
