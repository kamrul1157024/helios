// How a flat list of sessions becomes a tree.
//
// A port of the desktop's `renderer/components/grouping.ts`, function for
// function. The ordering model is the reason: a session's rank is the vector of
// its groups' positions with its own order last, and comparing those element by
// element is what makes moving a group carry its sessions with it. Two
// implementations of that rule would drift, and then two clients reading one
// daemon would disagree about what the same numbers mean.
//
// No Flutter import, so this can be tested without a widget binding. The colour
// a group wears lives in the widget that draws it.

import '../models/session.dart';
import '../models/session_group.dart';

/// A level a session does not reach. Sorts after every real position, which
/// puts subgroups above loose sessions and ungrouped sessions at the end.
const int kUnranked = 9007199254740991;

/// How the list is grouped. A device preference, not a daemon setting.
enum GroupMode { off, manual, auto }

/// What orders the directory groups. Offered in [GroupMode.auto] only: a made
/// group sits where its header was moved to, and a choice here would fight it.
enum GroupOrder { activity, name, manual }

/// Where each directory has been moved to, by path. A directory has no id to
/// hang a position on, so the path is the key.
typedef PathOrder = Map<String, int>;

const String kUngroupedName = 'Ungrouped';

/// A session's place, as a vector: the position of each group it holds, its own
/// order last, padded to the depth being rendered.
List<int> rankOf(Session session, int depth) {
  final path = session.groupPath.map((g) => g.position).toList();
  while (path.length < depth) {
    path.add(kUnranked);
  }
  return [...path.take(depth), session.sortOrder];
}

int byRank(List<int> a, List<int> b) {
  for (var i = 0; i < a.length; i++) {
    final left = a[i];
    final right = i < b.length ? b[i] : 0;
    if (left != right) return left.compareTo(right);
  }
  return 0;
}

/// How deep the tree goes for these sessions: the most groups any one holds.
int depthOf(List<Session> sessions) {
  var deepest = 0;
  for (final session in sessions) {
    if (session.groupPath.length > deepest) deepest = session.groupPath.length;
  }
  return deepest;
}

/// One node of the tree: a group, the groups inside it, and the sessions that
/// stop here rather than going deeper.
class GroupNode {
  /// Empty for the synthetic Ungrouped node, which is not a stored group.
  final String key;
  final String name;
  final int position;

  /// Every key from the root down to here — what a drop target compares.
  final List<String> path;
  final List<GroupNode> children;
  final List<Session> sessions;

  /// Sessions here and everywhere below, which is what the header counts.
  int total;

  GroupNode({
    required this.key,
    required this.name,
    required this.position,
    required this.path,
    List<GroupNode>? children,
    List<Session>? sessions,
    this.total = 0,
  }) : children = children ?? [],
       sessions = sessions ?? [];

  /// The synthetic node is not a group: nothing can be renamed, deleted or
  /// filed into a bucket the daemon does not hold.
  bool get isUngrouped => key.isEmpty;
}

/// Builds the tree from the group catalogue, then hangs the sessions on it.
///
/// The catalogue is the source of which nodes exist — not the sessions.
/// Deriving nodes from session paths means a group with nothing in it does not
/// render, so the first thing anyone does after making one, filing a session
/// into it, has nowhere to happen.
///
/// Sessions arrive already sorted by the caller. This only decides where each
/// one hangs.
List<GroupNode> buildTree(
  List<Session> sessions, [
  List<SessionGroup> groups = const [],
]) {
  final nodes = <String, GroupNode>{};
  final byKey = <String, SessionGroup>{};
  for (final group in groups) {
    byKey[group.key] = group;
  }

  for (final group in groups) {
    nodes[group.key] = GroupNode(
      key: group.key,
      name: group.name,
      position: group.position,
      path: [],
    );
  }

  final roots = <GroupNode>[];
  for (final group in groups) {
    final node = nodes[group.key];
    if (node == null) continue;
    final parent = group.parent.isEmpty ? null : nodes[group.parent];
    node.path
      ..clear()
      ..addAll(parent == null ? [group.key] : [...parent.path, group.key]);
    if (parent != null) {
      parent.children.add(node);
    } else {
      roots.add(node);
    }
  }

  List<GroupNode> chainOf(String key) {
    final chain = <GroupNode>[];
    final seen = <String>{};
    var at = key.isEmpty ? null : byKey[key];
    while (at != null && seen.add(at.key)) {
      final node = nodes[at.key];
      if (node != null) chain.insert(0, node);
      at = at.parent.isEmpty ? null : byKey[at.parent];
    }
    return chain;
  }

  GroupNode? ungrouped;

  for (final session in sessions) {
    var chain = chainOf(session.groupKey);
    if (chain.isEmpty) {
      ungrouped ??= GroupNode(
        key: '',
        name: kUngroupedName,
        position: kUnranked,
        path: [],
      );
      if (!roots.contains(ungrouped)) roots.add(ungrouped);
      chain = [ungrouped];
    }

    chain.last.sessions.add(session);
    for (final node in chain) {
      node.total += 1;
    }
  }

  _sortNodes(roots);
  return roots;
}

/// When a group last did anything: the newest moment across the sessions in it.
///
/// `lastEventAt` is missing until a session has produced something, so a fresh
/// one falls back to when it was made. Otherwise a brand new directory would
/// sort as the oldest thing on the list.
String lastActivityOf(GroupNode node) {
  var newest = '';
  for (final session in node.sessions) {
    final at = session.lastEventAt ?? session.createdAt;
    if (at.compareTo(newest) > 0) newest = at;
  }
  return newest;
}

/// Orders the directory groups.
///
/// Only the derived tree comes through here. A made group carries a position
/// the user moved it to, and an ordering choice over the top of that would undo
/// the move the moment it landed.
List<GroupNode> orderGroups(
  List<GroupNode> nodes,
  GroupOrder order, [
  PathOrder placed = const {},
]) {
  final sorted = [...nodes];
  switch (order) {
    case GroupOrder.name:
      sorted.sort((a, b) => a.name.compareTo(b.name));
    case GroupOrder.manual:
      // A directory nobody has moved sorts after every one that has, by
      // activity — so turning manual on does not scatter the untouched ones,
      // and a directory seen for the first time lands at the end rather than
      // somewhere arbitrary in the middle.
      int at(GroupNode node) => placed[node.key] ?? kUnranked;
      sorted.sort((a, b) {
        final byPlace = at(a).compareTo(at(b));
        if (byPlace != 0) return byPlace;
        return lastActivityOf(b).compareTo(lastActivityOf(a));
      });
    case GroupOrder.activity:
      sorted.sort((a, b) => lastActivityOf(b).compareTo(lastActivityOf(a)));
  }
  return sorted;
}

/// The label a directory node wears: the last segment of its path.
///
/// The whole path is the fallback rather than the first choice because it is
/// the thing that does not fit — at a phone's width every row would read
/// `/Users/…/workspace/` and differ only past the truncation.
String cwdLabel(String cwd) {
  final trimmed = cwd.replaceAll(RegExp(r'/+$'), '');
  final last = trimmed.split('/').last;
  if (last.isNotEmpty) return last;
  return cwd.isNotEmpty ? cwd : 'sessions';
}

/// Groups sessions by the directory they run in.
///
/// Kept apart from [buildTree] rather than folded into it behind a flag: that
/// one reads a catalogue, nests to any depth, and invents a node for the
/// sessions no group claims. This one has no catalogue, one level, and no
/// leftovers — every session has a cwd. The two share the node shape and
/// nothing else.
List<GroupNode> buildCwdTree(
  List<Session> sessions, [
  GroupOrder order = GroupOrder.activity,
  PathOrder placed = const {},
]) {
  final nodes = <String, GroupNode>{};
  for (final session in sessions) {
    final cwd = session.cwd;
    final node = nodes.putIfAbsent(
      cwd,
      () => GroupNode(
        key: cwd,
        name: cwdLabel(cwd),
        position: nodes.length,
        path: [cwd],
      ),
    );
    node.sessions.add(session);
    node.total += 1;
  }
  return orderGroups(nodes.values.toList(), order, placed);
}

/// Every session in the tree, in the order it is drawn.
///
/// What a reorder posts back. The daemon holds one flat order per host, so a
/// drag inside one node still has to name every other session: sending the
/// node's own ids would renumber it from zero and scatter the rest.
List<Session> flattenSessions(List<GroupNode> nodes) {
  final out = <Session>[];
  void walk(GroupNode node) {
    for (final child in node.children) {
      walk(child);
    }
    out.addAll(node.sessions);
  }

  for (final node in nodes) {
    walk(node);
  }
  return out;
}

void _sortNodes(List<GroupNode> nodes) {
  nodes.sort((a, b) {
    final byPosition = a.position.compareTo(b.position);
    if (byPosition != 0) return byPosition;
    return a.name.compareTo(b.name);
  });
  for (final node in nodes) {
    _sortNodes(node.children);
  }
}
