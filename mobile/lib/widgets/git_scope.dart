import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../providers/daemon_providers.dart';
import '../services/daemon_api_service.dart';
import 'git_diff_view.dart';

/// Which changes the Changes view lists (spec 65, B8).
///
/// The Git screen's commit history used to be a tab of its own. It is the
/// scope bar's menu now: the working tree, the branch's review against its
/// base, one commit, or a range of them. History has no mode of its own; it
/// is a way of picking what to look at.
enum GitScopeKind { working, review, commit, range }

class GitScope {
  final GitScopeKind kind;

  /// What the scope reads to, and from. Null for the working tree.
  final String? to;
  final String? from;

  /// What the bar says.
  final String label;

  const GitScope._(this.kind, this.label, {this.to, this.from});

  const GitScope.working() : this._(GitScopeKind.working, 'Uncommitted');

  /// The branch against where it forked from [base]: everything the branch
  /// did, and nothing the base did since.
  GitScope.review(String base)
    : this._(GitScopeKind.review, 'Review vs $base', to: 'HEAD', from: base);

  GitScope.commit(String sha, String subject)
    : this._(
        GitScopeKind.commit,
        subject.isEmpty ? shortSha(sha) : '${shortSha(sha)} $subject',
        to: sha,
      );

  GitScope.range(String from, String to, int count)
    : this._(GitScopeKind.range, '$count commits', to: to, from: from);

  bool get isWorking => kind == GitScopeKind.working;

  /// The files this scope changed, as the daemon is asked for them.
  GitChangesKey changesKey(String hostId, String root) => GitChangesKey(
    hostId,
    root,
    to ?? 'HEAD',
    from: from,
    mergeBase: kind == GitScopeKind.review,
  );

  /// The diff of one of those files in this scope.
  DiffTarget target(String root, CommitFile file) => DiffTarget(
    root: root,
    change: GitChange(path: file.path, status: file.status),
    from: from,
    to: to,
    mergeBase: kind == GitScopeKind.review,
  );

  @override
  bool operator ==(Object other) =>
      other is GitScope &&
      other.kind == kind &&
      other.to == to &&
      other.from == from;

  @override
  int get hashCode => Object.hash(kind, to, from);
}

/// What the Changes view opens on before anybody picks: the review of the
/// branch when it has commits on top of its base, else the working tree.
///
/// The desktop's rule (`components/git.tsx`). The question asked of a session's
/// changes is "what has the agent done to this branch"; the working tree alone
/// answers a narrower one. No history, no base, or not a repository — the
/// working tree is the answer that needs nothing to be true.
GitScope defaultScope(GitLog? log) {
  if (log == null ||
      log.scope != 'branch' ||
      log.base.isEmpty ||
      log.commits.isEmpty) {
    return const GitScope.working();
  }
  return GitScope.review(log.base);
}

/// How git's status letters are coloured, here and in the diff.
Color gitStatusColor(String status) => switch (status) {
  'M' => Colors.orange,
  'A' => Colors.green,
  'D' => Colors.red,
  'R' || 'C' => Colors.blue,
  _ => Colors.grey,
};

/// Insertion and deletion counts, the way git writes them.
List<Widget> statChips(int insertions, int deletions) {
  return [
    if (insertions > 0)
      Text(
        '+$insertions',
        style: const TextStyle(
          fontSize: 11,
          fontFamily: 'monospace',
          color: Colors.green,
        ),
      ),
    if (insertions > 0 && deletions > 0) const SizedBox(width: 6),
    if (deletions > 0)
      Text(
        '-$deletions',
        style: const TextStyle(
          fontSize: 11,
          fontFamily: 'monospace',
          color: Colors.red,
        ),
      ),
  ];
}

/// The bar at the top of the Changes view: the branch, and which changes are
/// listed. Tapping the scope opens the history to pick another; a commit or a
/// range gets a button to its detail — message, author, the whole list.
class GitScopeBar extends StatelessWidget {
  final String branch;
  final GitScope scope;
  final VoidCallback onPick;

  /// Opens the commit's detail. Null in the scopes that have none.
  final VoidCallback? onDetail;

  const GitScopeBar({
    super.key,
    required this.branch,
    required this.scope,
    required this.onPick,
    this.onDetail,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      height: 48,
      padding: const EdgeInsets.only(left: 16, right: 4),
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: theme.colorScheme.outlineVariant),
        ),
      ),
      child: Row(
        children: [
          Icon(Icons.fork_right, size: 16, color: theme.colorScheme.primary),
          const SizedBox(width: 6),
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 120),
            child: Text(
              branch,
              style: TextStyle(
                fontSize: 13,
                fontFamily: 'monospace',
                fontWeight: FontWeight.w600,
                color: theme.colorScheme.onSurface,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: InkWell(
              key: const ValueKey('git-scope'),
              borderRadius: BorderRadius.circular(8),
              onTap: onPick,
              child: SizedBox(
                height: 48,
                child: Row(
                  children: [
                    Flexible(
                      child: Text(
                        scope.label,
                        style: TextStyle(
                          fontSize: 13,
                          color: theme.colorScheme.primary,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    Icon(
                      Icons.arrow_drop_down,
                      color: theme.colorScheme.primary,
                    ),
                  ],
                ),
              ),
            ),
          ),
          if (onDetail != null)
            IconButton(
              icon: const Icon(Icons.info_outline, size: 20),
              tooltip: 'Commit detail',
              onPressed: onDetail,
            ),
        ],
      ),
    );
  }
}

/// Opens the scope menu: the working tree, the review, and the history.
///
/// A tap on a commit picks it. A long press marks one end of a range, and the
/// next tap picks everything between — the Git screen's gesture, kept.
Future<GitScope?> showScopePicker(
  BuildContext context, {
  required String hostId,
  required String root,
  required GitScope current,
}) {
  return showModalBottomSheet<GitScope>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => DraggableScrollableSheet(
      expand: false,
      initialChildSize: 0.7,
      maxChildSize: 0.95,
      builder: (context, controller) => _ScopePicker(
        hostId: hostId,
        root: root,
        current: current,
        controller: controller,
      ),
    ),
  );
}

class _ScopePicker extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String root;
  final GitScope current;
  final ScrollController controller;

  const _ScopePicker({
    required this.hostId,
    required this.root,
    required this.current,
    required this.controller,
  });

  @override
  rp.ConsumerState<_ScopePicker> createState() => _ScopePickerState();
}

class _ScopePickerState extends rp.ConsumerState<_ScopePicker> {
  GitLog? _log;
  final List<Commit> _commits = [];
  bool _all = false;
  bool _loading = true;
  bool _loadingMore = false;
  String? _compareFrom;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _compareFrom = null;
    });
    final log = await ref.read(
      gitLogProvider(GitLogKey(widget.hostId, widget.root, all: _all)).future,
    );
    if (!mounted) return;
    setState(() {
      _log = log;
      _commits
        ..clear()
        ..addAll(log?.commits ?? []);
      _loading = false;
    });
  }

  Future<void> _loadMore() async {
    if (_loadingMore) return;
    setState(() => _loadingMore = true);
    final next = await ref.read(
      gitLogProvider(
        GitLogKey(widget.hostId, widget.root, all: _all, skip: _commits.length),
      ).future,
    );
    if (!mounted) return;
    setState(() {
      if (next != null) {
        _commits.addAll(next.commits);
        _log = next;
      }
      _loadingMore = false;
    });
  }

  void _tap(Commit commit) {
    final anchor = _compareFrom;
    if (anchor == null || anchor == commit.sha) {
      Navigator.of(context).pop(GitScope.commit(commit.sha, commit.subject));
      return;
    }
    final a = _commits.indexWhere((c) => c.sha == anchor);
    final b = _commits.indexWhere((c) => c.sha == commit.sha);
    if (a < 0 || b < 0) {
      Navigator.of(context).pop(GitScope.commit(commit.sha, commit.subject));
      return;
    }
    // Lower in the list is older, and the older end is what we diff from.
    final newer = a < b ? a : b;
    final older = a < b ? b : a;
    Navigator.of(context).pop(
      GitScope.range(
        _commits[older].sha,
        _commits[newer].sha,
        older - newer + 1,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final log = _log;
    final base = log != null && log.scope == 'branch' ? log.base : '';
    final options = <GitScope>[
      const GitScope.working(),
      if (base.isNotEmpty) GitScope.review(base),
    ];
    return ListView(
      controller: widget.controller,
      children: [
        for (final option in options)
          ListTile(
            leading: Icon(
              option == widget.current
                  ? Icons.radio_button_checked
                  : Icons.radio_button_unchecked,
              color: option == widget.current
                  ? theme.colorScheme.primary
                  : theme.colorScheme.onSurfaceVariant,
            ),
            title: Text(option.label),
            onTap: () => Navigator.of(context).pop(option),
          ),
        const Divider(height: 1),
        _buildHistoryBar(theme, log),
        if (_compareFrom != null) _buildCompareBanner(theme),
        if (_loading)
          const Padding(
            padding: EdgeInsets.all(24),
            child: Center(child: CircularProgressIndicator()),
          )
        else if (log == null)
          _note(theme, 'Failed to load history')
        else if (_commits.isEmpty)
          _note(theme, 'No commits')
        else ...[
          for (final commit in _commits)
            _CommitTile(
              commit: commit,
              picked:
                  widget.current.kind == GitScopeKind.commit &&
                  widget.current.to == commit.sha,
              marked: commit.sha == _compareFrom,
              onTap: () => _tap(commit),
              onLongPress: () {
                HapticFeedback.lightImpact();
                setState(() => _compareFrom = commit.sha);
              },
            ),
          if (log.hasMore)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: 12),
              child: Center(
                child: _loadingMore
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : TextButton(
                        onPressed: _loadMore,
                        child: const Text(
                          'Load more',
                          style: TextStyle(fontSize: 13),
                        ),
                      ),
              ),
            ),
        ],
      ],
    );
  }

  Widget _note(ThemeData theme, String text) => Padding(
    padding: const EdgeInsets.all(24),
    child: Center(
      child: Text(
        text,
        style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
      ),
    ),
  );

  Widget _buildHistoryBar(ThemeData theme, GitLog? log) {
    final label = log == null
        ? ''
        : log.scope == 'branch' && log.base.isNotEmpty
        ? 'vs ${log.base}'
        : 'full history';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      child: Row(
        children: [
          Text(
            'HISTORY',
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              letterSpacing: 1.2,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              label,
              style: TextStyle(
                fontSize: 11,
                color: theme.colorScheme.onSurfaceVariant,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          SegmentedButton<bool>(
            segments: const [
              ButtonSegment(
                value: false,
                label: Text('Branch', style: TextStyle(fontSize: 11)),
              ),
              ButtonSegment(
                value: true,
                label: Text('All', style: TextStyle(fontSize: 11)),
              ),
            ],
            selected: {_all},
            onSelectionChanged: (s) {
              setState(() => _all = s.first);
              _load();
            },
            showSelectedIcon: false,
            style: const ButtonStyle(visualDensity: VisualDensity.compact),
          ),
        ],
      ),
    );
  }

  Widget _buildCompareBanner(ThemeData theme) {
    final short = shortSha(_compareFrom!);
    return Container(
      padding: const EdgeInsets.only(left: 16),
      color: theme.colorScheme.primaryContainer,
      child: Row(
        children: [
          Icon(
            Icons.compare_arrows,
            size: 16,
            color: theme.colorScheme.onPrimaryContainer,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Comparing from $short — tap another commit',
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onPrimaryContainer,
              ),
            ),
          ),
          IconButton(
            icon: Icon(
              Icons.close,
              size: 18,
              color: theme.colorScheme.onPrimaryContainer,
            ),
            tooltip: 'Stop comparing',
            onPressed: () => setState(() => _compareFrom = null),
          ),
        ],
      ),
    );
  }
}

class _CommitTile extends StatelessWidget {
  final Commit commit;
  final bool picked;
  final bool marked;
  final VoidCallback onTap;
  final VoidCallback onLongPress;

  const _CommitTile({
    required this.commit,
    required this.picked,
    required this.marked,
    required this.onTap,
    required this.onLongPress,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      onLongPress: onLongPress,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        decoration: BoxDecoration(
          color: marked || picked
              ? theme.colorScheme.primaryContainer.withValues(alpha: 0.4)
              : null,
          border: Border(
            bottom: BorderSide(
              color: theme.colorScheme.outlineVariant.withValues(alpha: 0.5),
            ),
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              commit.subject,
              style: TextStyle(
                fontSize: 14,
                color: theme.colorScheme.onSurface,
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
            const SizedBox(height: 4),
            Row(
              children: [
                Text(
                  commit.short,
                  style: TextStyle(
                    fontSize: 11,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.primary,
                  ),
                ),
                const SizedBox(width: 8),
                Flexible(
                  child: Text(
                    commit.author,
                    style: TextStyle(
                      fontSize: 11,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  commit.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
                const Spacer(),
                ...statChips(commit.insertions, commit.deletions),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
