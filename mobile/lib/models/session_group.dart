/// A group the user made: a folder over sessions, nested as deep as they like.
class SessionGroup {
  final String key;
  final String name;

  /// The group this one sits in, or empty for a root.
  final String parent;

  /// Where it sits among its siblings. Ordinal within one parent, not global.
  final int position;

  const SessionGroup({
    required this.key,
    required this.name,
    this.parent = '',
    this.position = 0,
  });

  factory SessionGroup.fromJson(Map<String, dynamic> json) {
    return SessionGroup(
      key: json['key'] as String? ?? '',
      name: json['name'] as String? ?? '',
      parent: json['parent'] as String? ?? '',
      position: (json['position'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Every group on one host, and whether the host can hold any.
///
/// A daemon from before grouping answers 404, which is not a failure the list
/// should show: the sessions are fine and only the filing is missing. The flag
/// says so, and the tree falls back to flat.
class GroupCatalog {
  final List<SessionGroup> groups;
  final bool unsupported;

  const GroupCatalog({this.groups = const [], this.unsupported = false});

  static const GroupCatalog empty = GroupCatalog();
  static const GroupCatalog missing = GroupCatalog(unsupported: true);

  bool get isEmpty => groups.isEmpty;

  SessionGroup? byKey(String key) {
    for (final group in groups) {
      if (group.key == key) return group;
    }
    return null;
  }

  /// The groups directly inside [parent], in the order they sit.
  List<SessionGroup> childrenOf(String parent) {
    final children = groups.where((g) => g.parent == parent).toList();
    children.sort((a, b) {
      final byPosition = a.position.compareTo(b.position);
      if (byPosition != 0) return byPosition;
      return a.name.compareTo(b.name);
    });
    return children;
  }

  /// Whether [key] sits somewhere under [ancestor].
  ///
  /// What stops a group being moved into its own child, which would cut the
  /// subtree off from every root. The walk is bounded by a seen set because a
  /// cycle already in the data would otherwise hang the caller.
  bool isDescendant(String key, String ancestor) {
    if (key.isEmpty || ancestor.isEmpty) return false;
    final seen = <String>{};
    var at = byKey(key);
    while (at != null && seen.add(at.key)) {
      if (at.parent == ancestor) return true;
      at = at.parent.isEmpty ? null : byKey(at.parent);
    }
    return false;
  }

  /// [key] and everything above it, outermost first.
  ///
  /// The same shape the daemon serves as `group_path`, so a row filed here can
  /// be painted with an ancestry that sorts rather than waiting for a refetch.
  List<SessionGroup> ancestryOf(String key) {
    final chain = <SessionGroup>[];
    final seen = <String>{};
    var at = key.isEmpty ? null : byKey(key);
    while (at != null && seen.add(at.key)) {
      chain.insert(0, at);
      at = at.parent.isEmpty ? null : byKey(at.parent);
    }
    return chain;
  }
}
