import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:highlight/highlight.dart' show highlight;
import 'package:provider/provider.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';
import '../widgets/skeleton.dart';
import 'file_browser_screen.dart';

class GitStatusScreen extends StatefulWidget {
  final String hostId;
  final String cwd;
  final String? sessionId;

  const GitStatusScreen({super.key, required this.hostId, required this.cwd, this.sessionId});

  @override
  State<GitStatusScreen> createState() => _GitStatusScreenState();
}

enum _GitView { changes, commits, worktrees }

class _GitStatusScreenState extends State<GitStatusScreen> {
  GitStatus? _status;
  bool _loading = true;
  _GitView _view = _GitView.changes;

  /// The worktree the screen is scoped to — the session's own until another is
  /// picked from the Worktrees tab.
  late String _root = widget.cwd;

  /// Bumped by the refresh button so the commit and worktree tabs reload.
  int _reload = 0;

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final status = await _svc?.gitStatus(_root);
    if (!mounted) return;
    setState(() {
      _status = status;
      _loading = false;
    });
  }

  void _refresh() {
    setState(() => _reload++);
    _load();
  }

  void _scopeTo(String path) {
    setState(() {
      _root = path;
      _view = _GitView.changes;
    });
    _load();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final rescoped = _root != widget.cwd;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('Git'),
            if (rescoped)
              Text(
                _shortPath(_root),
                style: TextStyle(
                  fontSize: 11,
                  fontFamily: 'monospace',
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                overflow: TextOverflow.ellipsis,
              ),
          ],
        ),
        actions: [
          if (rescoped)
            IconButton(
              icon: const Icon(Icons.undo),
              tooltip: "Back to this session's worktree",
              onPressed: () => _scopeTo(widget.cwd),
            ),
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _refresh,
          ),
          IconButton(
            icon: const Icon(Icons.chat_bubble_outline),
            tooltip: 'Back to chat',
            onPressed: () => Navigator.of(context).pop(),
          ),
        ],
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(40),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: SegmentedButton<_GitView>(
              segments: const [
                ButtonSegment(value: _GitView.changes, label: Text('Changes', style: TextStyle(fontSize: 12))),
                ButtonSegment(value: _GitView.commits, label: Text('Commits', style: TextStyle(fontSize: 12))),
                ButtonSegment(value: _GitView.worktrees, label: Text('Worktrees', style: TextStyle(fontSize: 12))),
              ],
              selected: {_view},
              onSelectionChanged: (s) => setState(() => _view = s.first),
              style: ButtonStyle(
                visualDensity: VisualDensity.compact,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
          ),
        ),
      ),
      body: switch (_view) {
        _GitView.changes => _buildChanges(theme),
        _GitView.commits => _CommitsTab(
            key: ValueKey('commits:$_root:$_reload'),
            hostId: widget.hostId,
            root: _root,
            sessionId: widget.sessionId,
          ),
        _GitView.worktrees => _WorktreesTab(
            key: ValueKey('worktrees:$_root:$_reload'),
            hostId: widget.hostId,
            root: _root,
            active: _root,
            onPick: _scopeTo,
          ),
      },
    );
  }

  Widget _buildChanges(ThemeData theme) {
    if (_loading) return const _GitStatusSkeleton();
    if (_status == null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
            const SizedBox(height: 12),
            const Text('Not a git repository'),
          ],
        ),
      );
    }
    return RefreshIndicator(onRefresh: _load, child: _buildContent(theme));
  }

  Widget _buildContent(ThemeData theme) {
    final s = _status!;
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        // Branch info header
        Container(
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(12),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(Icons.fork_right, size: 20, color: theme.colorScheme.primary),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      s.branch,
                      style: TextStyle(
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurface,
                      ),
                    ),
                  ),
                  if (!s.dirty)
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                      decoration: BoxDecoration(
                        color: Colors.green.withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: const Text('clean', style: TextStyle(fontSize: 11, color: Colors.green, fontWeight: FontWeight.w600)),
                    ),
                ],
              ),
              if (s.ahead > 0 || s.behind > 0) ...[
                const SizedBox(height: 8),
                Row(
                  children: [
                    if (s.ahead > 0) ...[
                      Icon(Icons.arrow_upward, size: 14, color: Colors.green.shade400),
                      const SizedBox(width: 2),
                      Text('${ s.ahead} ahead', style: TextStyle(fontSize: 12, color: Colors.green.shade400)),
                      const SizedBox(width: 12),
                    ],
                    if (s.behind > 0) ...[
                      Icon(Icons.arrow_downward, size: 14, color: Colors.orange.shade400),
                      const SizedBox(width: 2),
                      Text('${s.behind} behind', style: TextStyle(fontSize: 12, color: Colors.orange.shade400)),
                    ],
                  ],
                ),
              ],
            ],
          ),
        ),
        const SizedBox(height: 16),
        // Staged
        if (s.staged.isNotEmpty)
          _buildSection(theme, 'STAGED', s.staged, Colors.green, true),
        // Unstaged
        if (s.unstaged.isNotEmpty)
          _buildSection(theme, 'UNSTAGED', s.unstaged, Colors.orange, false),
        // Untracked
        if (s.untracked.isNotEmpty)
          _buildSection(theme, 'UNTRACKED', s.untracked, Colors.grey, false),
        if (!s.dirty)
          Padding(
            padding: const EdgeInsets.only(top: 32),
            child: Center(
              child: Text(
                'Working tree clean',
                style: TextStyle(fontSize: 14, color: theme.colorScheme.onSurfaceVariant),
              ),
            ),
          ),
      ],
    );
  }

  Widget _buildSection(ThemeData theme, String title, List<GitChange> changes, Color color, bool staged) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 8),
          child: Row(
            children: [
              Text(
                title,
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w700,
                  color: color,
                  letterSpacing: 1.2,
                ),
              ),
              const SizedBox(width: 6),
              Text(
                '(${changes.length})',
                style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
              ),
            ],
          ),
        ),
        ...changes.map((c) => _ChangeTile(
          change: c,
          color: color,
          onTap: c.status == '?'
              ? () => _openFile(c.path)
              : () => _openDiff(c, staged),
        )),
        const SizedBox(height: 16),
      ],
    );
  }

  void _openDiff(GitChange change, bool staged) {
    final root = _status!.root;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => GitDiffScreen(
          hostId: widget.hostId,
          cwd: root,
          change: change,
          staged: staged,
          sessionId: widget.sessionId,
        ),
      ),
    );
  }

  void _openFile(String relativePath) {
    final root = _status!.root;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => FileViewerScreen(
          hostId: widget.hostId,
          path: '$root/$relativePath',
          sessionId: widget.sessionId,
        ),
      ),
    );
  }
}

class _ChangeTile extends StatelessWidget {
  final GitChange change;
  final Color color;
  final VoidCallback onTap;

  const _ChangeTile({required this.change, required this.color, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
        child: Row(
          children: [
            Container(
              width: 22,
              alignment: Alignment.center,
              child: Text(
                change.status,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  fontFamily: 'monospace',
                  color: _statusColor(change.status),
                ),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Text(
                change.path,
                style: TextStyle(
                  fontSize: 13,
                  fontFamily: 'monospace',
                  color: theme.colorScheme.onSurface,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            Icon(Icons.chevron_right, size: 16, color: theme.colorScheme.onSurfaceVariant),
          ],
        ),
      ),
    );
  }

  Color _statusColor(String status) {
    switch (status) {
      case 'M':
        return Colors.orange;
      case 'A':
        return Colors.green;
      case 'D':
        return Colors.red;
      case 'R':
        return Colors.blue;
      case '?':
        return Colors.grey;
      default:
        return Colors.grey;
    }
  }
}

/// The last two segments: the parent directory is what tells worktrees apart.
String _shortPath(String path) {
  final parts = path.split('/').where((p) => p.isNotEmpty).toList();
  if (parts.length <= 2) return path;
  return '.../${parts.sublist(parts.length - 2).join('/')}';
}

String _shortSha(String sha) => sha.length > 7 ? sha.substring(0, 7) : sha;

Color _statusTint(String status) {
  switch (status) {
    case 'M':
      return Colors.orange;
    case 'A':
      return Colors.green;
    case 'D':
      return Colors.red;
    case 'R':
    case 'C':
      return Colors.blue;
    default:
      return Colors.grey;
  }
}

/// Insertion and deletion counts, the way git writes them.
List<Widget> _statChips(int insertions, int deletions) {
  return [
    if (insertions > 0)
      Text(
        '+$insertions',
        style: const TextStyle(fontSize: 11, fontFamily: 'monospace', color: Colors.green),
      ),
    if (insertions > 0 && deletions > 0) const SizedBox(width: 6),
    if (deletions > 0)
      Text(
        '-$deletions',
        style: const TextStyle(fontSize: 11, fontFamily: 'monospace', color: Colors.red),
      ),
  ];
}

// ==================== Commits ====================

/// The commit history of the current branch.
///
/// Tapping a commit shows what it changed; long-pressing one marks it, and the
/// next tap shows everything between the two.
class _CommitsTab extends StatefulWidget {
  final String hostId;
  final String root;
  final String? sessionId;

  const _CommitsTab({super.key, required this.hostId, required this.root, this.sessionId});

  @override
  State<_CommitsTab> createState() => _CommitsTabState();
}

class _CommitsTabState extends State<_CommitsTab> {
  GitLog? _log;
  final List<Commit> _commits = [];
  bool _all = false;
  bool _loading = true;
  bool _loadingMore = false;
  String? _compareFrom;

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

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
    final log = await _svc?.gitLog(widget.root, all: _all);
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
    final next = await _svc?.gitLog(widget.root, all: _all, skip: _commits.length);
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
      _open(to: commit.sha, subject: commit.subject);
      return;
    }
    final a = _commits.indexWhere((c) => c.sha == anchor);
    final b = _commits.indexWhere((c) => c.sha == commit.sha);
    if (a < 0 || b < 0) {
      _open(to: commit.sha, subject: commit.subject);
      return;
    }
    // Lower in the list is older, and the older end is what we diff from.
    final newer = a < b ? a : b;
    final older = a < b ? b : a;
    setState(() => _compareFrom = null);
    _open(
      to: _commits[newer].sha,
      from: _commits[older].sha,
      subject: '${older - newer + 1} commits',
    );
  }

  void _open({required String to, String? from, required String subject}) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CommitDetailScreen(
          hostId: widget.hostId,
          root: widget.root,
          to: to,
          from: from,
          title: subject,
          sessionId: widget.sessionId,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    if (_loading) return const _GitStatusSkeleton();
    final log = _log;
    if (log == null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
            const SizedBox(height: 12),
            const Text('Failed to load history'),
          ],
        ),
      );
    }

    return Column(
      children: [
        _buildScopeBar(theme, log),
        if (_compareFrom != null) _buildCompareBanner(theme),
        Expanded(
          child: _commits.isEmpty
              ? Center(
                  child: Text(
                    'No commits',
                    style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
                  ),
                )
              : RefreshIndicator(
                  onRefresh: _load,
                  child: ListView.builder(
                    itemCount: _commits.length + (log.hasMore ? 1 : 0),
                    itemBuilder: (ctx, i) {
                      if (i == _commits.length) {
                        return Padding(
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
                                    child: const Text('Load more', style: TextStyle(fontSize: 13)),
                                  ),
                          ),
                        );
                      }
                      final commit = _commits[i];
                      return _CommitTile(
                        commit: commit,
                        marked: commit.sha == _compareFrom,
                        onTap: () => _tap(commit),
                        onLongPress: () {
                          HapticFeedback.lightImpact();
                          setState(() => _compareFrom = commit.sha);
                        },
                      );
                    },
                  ),
                ),
        ),
      ],
    );
  }

  Widget _buildScopeBar(ThemeData theme, GitLog log) {
    final label = log.scope == 'branch' && log.base.isNotEmpty
        ? 'vs ${log.base}'
        : 'full history';
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: theme.colorScheme.outlineVariant)),
      ),
      child: Row(
        children: [
          Icon(Icons.fork_right, size: 16, color: theme.colorScheme.primary),
          const SizedBox(width: 6),
          Flexible(
            child: Text(
              log.branch,
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
          Flexible(
            child: Text(
              label,
              style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const Spacer(),
          SegmentedButton<bool>(
            segments: const [
              ButtonSegment(value: false, label: Text('Branch', style: TextStyle(fontSize: 11))),
              ButtonSegment(value: true, label: Text('All', style: TextStyle(fontSize: 11))),
            ],
            selected: {_all},
            onSelectionChanged: (s) {
              setState(() => _all = s.first);
              _load();
            },
            showSelectedIcon: false,
            style: ButtonStyle(
              visualDensity: VisualDensity.compact,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildCompareBanner(ThemeData theme) {
    final short = _compareFrom!.substring(0, 7);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      color: theme.colorScheme.primaryContainer,
      child: Row(
        children: [
          Icon(Icons.compare_arrows, size: 16, color: theme.colorScheme.onPrimaryContainer),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              'Comparing from $short — tap another commit',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onPrimaryContainer),
            ),
          ),
          GestureDetector(
            onTap: () => setState(() => _compareFrom = null),
            child: Icon(Icons.close, size: 16, color: theme.colorScheme.onPrimaryContainer),
          ),
        ],
      ),
    );
  }
}

class _CommitTile extends StatelessWidget {
  final Commit commit;
  final bool marked;
  final VoidCallback onTap;
  final VoidCallback onLongPress;

  const _CommitTile({
    required this.commit,
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
          color: marked ? theme.colorScheme.primaryContainer.withValues(alpha: 0.4) : null,
          border: Border(bottom: BorderSide(color: theme.colorScheme.outlineVariant.withValues(alpha: 0.5))),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              commit.subject,
              style: TextStyle(fontSize: 14, color: theme.colorScheme.onSurface),
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
                    style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 8),
                Text(
                  commit.timeAgo,
                  style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
                ),
                const Spacer(),
                ..._statChips(commit.insertions, commit.deletions),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

/// What one commit changed, or everything between two.
class CommitDetailScreen extends StatefulWidget {
  final String hostId;
  final String root;
  final String to;
  final String? from;
  final String title;
  final String? sessionId;

  const CommitDetailScreen({
    super.key,
    required this.hostId,
    required this.root,
    required this.to,
    this.from,
    required this.title,
    this.sessionId,
  });

  @override
  State<CommitDetailScreen> createState() => _CommitDetailScreenState();
}

class _CommitDetailScreenState extends State<CommitDetailScreen> {
  GitChanges? _changes;
  bool _loading = true;

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final changes = await _svc?.gitChanges(widget.root, widget.to, from: widget.from);
    if (!mounted) return;
    setState(() {
      _changes = changes;
      _loading = false;
    });
  }

  void _openFile(CommitFile file) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => GitDiffScreen(
          hostId: widget.hostId,
          cwd: widget.root,
          change: GitChange(path: file.path, status: file.status),
          staged: false,
          from: widget.from,
          to: widget.to,
          sessionId: widget.sessionId,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final changes = _changes;
    return Scaffold(
      appBar: AppBar(
        title: Text(
          changes?.single == true && changes!.subject.isNotEmpty ? changes.subject : widget.title,
          style: const TextStyle(fontSize: 15),
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.chat_bubble_outline),
            tooltip: 'Back to chat',
            onPressed: () => Navigator.of(context).popUntil(
              (route) => route.settings.name != '/file-browser' && route.settings.name != '/git-status',
            ),
          ),
        ],
      ),
      body: _loading
          ? const _GitStatusSkeleton()
          : changes == null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
                      const SizedBox(height: 12),
                      const Text('Failed to load commit'),
                    ],
                  ),
                )
              : _buildBody(theme, changes),
    );
  }

  Widget _buildBody(ThemeData theme, GitChanges changes) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(12),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Text(
                    changes.single ? changes.shortTo : '${changes.shortFrom}...${changes.shortTo}',
                    style: TextStyle(
                      fontSize: 12,
                      fontFamily: 'monospace',
                      color: theme.colorScheme.primary,
                    ),
                  ),
                  if (changes.author.isNotEmpty) ...[
                    const SizedBox(width: 8),
                    Flexible(
                      child: Text(
                        changes.author,
                        style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                  if (changes.date.isNotEmpty) ...[
                    const SizedBox(width: 8),
                    Text(
                      changes.timeAgo,
                      style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
                    ),
                  ],
                  const Spacer(),
                  ..._statChips(changes.insertions, changes.deletions),
                ],
              ),
              if (changes.body.isNotEmpty) ...[
                const SizedBox(height: 10),
                SelectableText(
                  changes.body,
                  style: TextStyle(
                    fontSize: 12,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.onSurface,
                  ),
                ),
              ],
            ],
          ),
        ),
        const SizedBox(height: 16),
        Padding(
          padding: const EdgeInsets.only(bottom: 8),
          child: Text(
            '${changes.files.length} ${changes.files.length == 1 ? 'FILE' : 'FILES'}',
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w700,
              letterSpacing: 1.2,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ),
        if (changes.files.isEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 16),
            child: Center(
              child: Text(
                'No files — a merge commit',
                style: TextStyle(fontSize: 13, color: theme.colorScheme.onSurfaceVariant),
              ),
            ),
          ),
        ...changes.files.map((file) => _CommitFileTile(file: file, onTap: () => _openFile(file))),
        if (changes.truncated)
          Padding(
            padding: const EdgeInsets.only(top: 12),
            child: Text(
              'Showing the first ${changes.files.length} files.',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
            ),
          ),
      ],
    );
  }
}

class _CommitFileTile extends StatelessWidget {
  final CommitFile file;
  final VoidCallback onTap;

  const _CommitFileTile({required this.file, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(6),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
        child: Row(
          children: [
            SizedBox(
              width: 22,
              child: Text(
                file.status,
                textAlign: TextAlign.center,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                  fontFamily: 'monospace',
                  color: _statusTint(file.status),
                ),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    file.path,
                    style: TextStyle(fontSize: 13, fontFamily: 'monospace', color: theme.colorScheme.onSurface),
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (file.from.isNotEmpty)
                    Text(
                      'was ${file.from}',
                      style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: theme.colorScheme.onSurfaceVariant),
                      overflow: TextOverflow.ellipsis,
                    ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            ..._statChips(file.insertions, file.deletions),
            Icon(Icons.chevron_right, size: 16, color: theme.colorScheme.onSurfaceVariant),
          ],
        ),
      ),
    );
  }
}

// ==================== Worktrees ====================

/// Every worktree of this repository. Read-only: Helios shows worktrees, it
/// does not make them. Tapping one points the whole screen at it.
class _WorktreesTab extends StatefulWidget {
  final String hostId;
  final String root;
  final String active;
  final ValueChanged<String> onPick;

  const _WorktreesTab({
    super.key,
    required this.hostId,
    required this.root,
    required this.active,
    required this.onPick,
  });

  @override
  State<_WorktreesTab> createState() => _WorktreesTabState();
}

class _WorktreesTabState extends State<_WorktreesTab> {
  List<Worktree>? _worktrees;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final svc = context.read<HostManager>().serviceFor(widget.hostId);
    final worktrees = await svc?.gitWorktrees(widget.root);
    if (!mounted) return;
    setState(() => _worktrees = worktrees ?? []);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final worktrees = _worktrees;
    if (worktrees == null) return const _GitStatusSkeleton();
    if (worktrees.isEmpty) {
      return Center(
        child: Text('No worktrees', style: TextStyle(color: theme.colorScheme.onSurfaceVariant)),
      );
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.builder(
        itemCount: worktrees.length,
        itemBuilder: (ctx, i) => _WorktreeTile(
          worktree: worktrees[i],
          selected: worktrees[i].path == widget.active,
          onTap: () => widget.onPick(worktrees[i].path),
        ),
      ),
    );
  }
}

class _WorktreeTile extends StatelessWidget {
  final Worktree worktree;
  final bool selected;
  final VoidCallback onTap;

  const _WorktreeTile({required this.worktree, required this.selected, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        decoration: BoxDecoration(
          color: selected ? theme.colorScheme.primaryContainer.withValues(alpha: 0.35) : null,
          border: Border(bottom: BorderSide(color: theme.colorScheme.outlineVariant.withValues(alpha: 0.5))),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  selected ? Icons.radio_button_checked : Icons.radio_button_unchecked,
                  size: 16,
                  color: selected ? theme.colorScheme.primary : theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 8),
                Flexible(
                  child: Text(
                    worktree.branch.isEmpty ? '(detached)' : worktree.branch,
                    style: TextStyle(
                      fontSize: 13,
                      fontFamily: 'monospace',
                      fontWeight: selected ? FontWeight.w600 : FontWeight.normal,
                      color: theme.colorScheme.onSurface,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const SizedBox(width: 6),
                if (worktree.isMain) const _Pill(text: 'main'),
                if (worktree.locked) const _Pill(text: 'locked'),
                if (worktree.ahead > 0) _Pill(text: '↑${worktree.ahead}', color: Colors.green),
                if (worktree.behind > 0) _Pill(text: '↓${worktree.behind}', color: Colors.orange),
                if (worktree.dirty > 0)
                  _Pill(text: '●${worktree.dirty}', color: Colors.orange)
                else
                  const _Pill(text: 'clean', color: Colors.green),
              ],
            ),
            if (worktree.subject.isNotEmpty) ...[
              const SizedBox(height: 4),
              Text(
                worktree.subject,
                style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ],
            const SizedBox(height: 2),
            Text(
              '${worktree.head} ${_shortPath(worktree.path)}',
              style: TextStyle(
                fontSize: 11,
                fontFamily: 'monospace',
                color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.8),
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
      ),
    );
  }
}

class _Pill extends StatelessWidget {
  final String text;
  final Color? color;

  const _Pill({required this.text, this.color});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tint = color ?? theme.colorScheme.onSurfaceVariant;
    return Container(
      margin: const EdgeInsets.only(right: 4),
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: tint.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        text,
        style: TextStyle(fontSize: 10, fontWeight: FontWeight.w600, color: tint),
      ),
    );
  }
}

// ==================== Git Diff Screen ====================

enum DiffViewMode { diff, unified, full }

class GitDiffScreen extends StatefulWidget {
  final String hostId;
  final String cwd;
  final GitChange change;
  final bool staged;

  /// The revisions to diff, when this is a committed change rather than a
  /// working-tree one: [to] alone is that commit against its parent.
  final String? from;
  final String? to;
  final String? sessionId;

  const GitDiffScreen({
    super.key,
    required this.hostId,
    required this.cwd,
    required this.change,
    required this.staged,
    this.from,
    this.to,
    this.sessionId,
  });

  @override
  State<GitDiffScreen> createState() => _GitDiffScreenState();
}

class _GitDiffScreenState extends State<GitDiffScreen> {
  GitDiff? _diff;
  FileReadResult? _fullFile;
  bool _loading = true;
  DiffViewMode _mode = DiffViewMode.unified;
  int? _selStart;
  int? _selEnd;

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

  bool get _hasSelection => _selStart != null;
  String get _selLabel {
    if (_selStart == null) return '';
    if (_selEnd == null || _selStart == _selEnd) return 'L$_selStart';
    return 'L$_selStart-$_selEnd';
  }
  int get _selCount {
    if (_selStart == null) return 0;
    if (_selEnd == null) return 1;
    return (_selEnd! - _selStart!).abs() + 1;
  }

  void _onLineTap(int lineIdx) {
    setState(() {
      if (_selStart == null) {
        _selStart = lineIdx;
        _selEnd = null;
      } else if (_selEnd == null && lineIdx == _selStart) {
        _selStart = null;
      } else if (_selEnd == null) {
        final a = _selStart!;
        _selStart = a < lineIdx ? a : lineIdx;
        _selEnd = a < lineIdx ? lineIdx : a;
      } else {
        _selStart = lineIdx;
        _selEnd = null;
      }
    });
  }

  bool _isLineSelected(int lineIdx) {
    if (_selStart == null) return false;
    if (_selEnd == null) return lineIdx == _selStart;
    return lineIdx >= _selStart! && lineIdx <= _selEnd!;
  }

  @override
  void initState() {
    super.initState();
    _loadDiff();
  }

  Future<void> _loadDiff() async {
    setState(() => _loading = true);
    final svc = _svc;
    if (svc == null) {
      setState(() => _loading = false);
      return;
    }
    final diff = await svc.gitDiff(
      widget.cwd,
      widget.change.path,
      staged: widget.staged,
      from: widget.from,
      to: widget.to,
    );
    if (!mounted) return;
    setState(() {
      _diff = diff;
      _loading = false;
    });
    // Preload full file for full-file mode. Only for the working tree: on disk
    // is the current file, which is not what an old commit changed.
    if (_atRevision) return;
    final fullPath = '${widget.cwd}/${widget.change.path}';
    final file = await svc.readFile(fullPath);
    if (mounted && file != null) {
      setState(() => _fullFile = file);
    }
  }

  bool get _atRevision => widget.to != null && widget.to!.isNotEmpty;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.change.fileName, style: const TextStyle(fontSize: 15), overflow: TextOverflow.ellipsis),
            if (_diff?.stat.isNotEmpty == true || _atRevision)
              Text(
                [
                  if (_diff?.stat.isNotEmpty == true) _diff!.stat,
                  if (_atRevision) 'at ${_shortSha(widget.to!)}',
                ].join(' · '),
                style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
              ),
          ],
        ),
        actions: [
          if (_diff != null)
            IconButton(
              icon: const Icon(Icons.copy),
              tooltip: 'Copy diff',
              onPressed: () {
                Clipboard.setData(ClipboardData(text: _diff!.diff));
                HapticFeedback.lightImpact();
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('Diff copied'), duration: Duration(seconds: 1), behavior: SnackBarBehavior.floating),
                );
              },
            ),
          IconButton(
            icon: const Icon(Icons.chat_bubble_outline),
            tooltip: 'Back to chat',
            onPressed: () => Navigator.of(context).popUntil(
              (route) => route.settings.name != '/file-browser' && route.settings.name != '/git-status',
            ),
          ),
        ],
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(40),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
            child: SegmentedButton<DiffViewMode>(
              segments: [
                const ButtonSegment(value: DiffViewMode.diff, label: Text('Diff', style: TextStyle(fontSize: 12))),
                const ButtonSegment(value: DiffViewMode.unified, label: Text('Unified', style: TextStyle(fontSize: 12))),
                // The file on disk is the current one, so "full" only makes
                // sense for a working-tree diff.
                if (!_atRevision)
                  const ButtonSegment(value: DiffViewMode.full, label: Text('Full', style: TextStyle(fontSize: 12))),
              ],
              selected: {_mode},
              onSelectionChanged: (s) => setState(() => _mode = s.first),
              style: ButtonStyle(
                visualDensity: VisualDensity.compact,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
          ),
        ),
      ),
      body: _loading
          ? const _DiffSkeleton()
          : _diff == null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
                      const SizedBox(height: 12),
                      const Text('Failed to load diff'),
                    ],
                  ),
                )
              : _buildDiffView(theme),
      bottomNavigationBar: widget.sessionId != null
          ? _buildAskAIBar(theme)
          : null,
    );
  }

  Widget _buildAskAIBar(ThemeData theme) {
    final isDark = theme.brightness == Brightness.dark;
    final accentColor = isDark ? const Color(0xFF58A6FF) : const Color(0xFF0969DA);
    return Container(
      padding: EdgeInsets.only(
        left: 12, right: 8, top: 8,
        bottom: MediaQuery.of(context).padding.bottom + 8,
      ),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        border: Border(top: BorderSide(color: theme.colorScheme.outlineVariant)),
      ),
      child: Row(
        children: [
          Icon(Icons.code, size: 16, color: accentColor),
          const SizedBox(width: 6),
          if (_hasSelection) ...[
            Text(
              '$_selLabel · $_selCount ${_selCount == 1 ? 'line' : 'lines'}',
              style: TextStyle(fontSize: 12, fontFamily: 'monospace', color: theme.colorScheme.onSurface),
            ),
            const SizedBox(width: 8),
            GestureDetector(
              onTap: () => setState(() { _selStart = null; _selEnd = null; }),
              child: Icon(Icons.close, size: 14, color: theme.colorScheme.onSurfaceVariant),
            ),
          ] else
            Text(
              'Tap lines to select',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
            ),
          const Spacer(),
          FilledButton.tonalIcon(
            onPressed: () => _showAskAISheet(theme),
            icon: const Icon(Icons.auto_awesome, size: 16),
            label: const Text('Ask AI', style: TextStyle(fontSize: 12)),
            style: FilledButton.styleFrom(
              visualDensity: VisualDensity.compact,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            ),
          ),
        ],
      ),
    );
  }

  void _showAskAISheet(ThemeData theme) {
    final controller = TextEditingController();
    final isDark = theme.brightness == Brightness.dark;
    final accentColor = isDark ? const Color(0xFF58A6FF) : const Color(0xFF0969DA);
    final label = _hasSelection ? '${widget.change.fileName}:$_selLabel' : widget.change.path;
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (ctx) {
        return Padding(
          padding: EdgeInsets.only(bottom: MediaQuery.of(ctx).viewInsets.bottom),
          child: SafeArea(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Icon(Icons.insert_drive_file, size: 16, color: accentColor),
                      const SizedBox(width: 6),
                      Flexible(
                        child: Text(
                          label,
                          style: TextStyle(
                            fontSize: 13, fontFamily: 'monospace',
                            fontWeight: FontWeight.w600,
                            color: accentColor,
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ],
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: controller,
                    autofocus: true,
                    minLines: 1,
                    maxLines: 4,
                    style: const TextStyle(fontSize: 14),
                    decoration: InputDecoration(
                      hintText: _hasSelection ? 'Ask about this code...' : 'Ask about this diff...',
                      hintStyle: TextStyle(fontSize: 14, color: theme.colorScheme.onSurfaceVariant),
                      border: OutlineInputBorder(borderRadius: BorderRadius.circular(10)),
                      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                      suffixIcon: IconButton(
                        icon: const Icon(Icons.send, size: 20),
                        onPressed: () => _sendAskAI(ctx, controller.text),
                      ),
                    ),
                    onSubmitted: (v) => _sendAskAI(ctx, v),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }

  Future<void> _sendAskAI(BuildContext ctx, String question) async {
    if (question.trim().isEmpty) return;
    final svc = context.read<HostManager>().serviceFor(widget.hostId);
    if (svc == null || widget.sessionId == null) return;

    // Say which commit, so the agent looks at the same thing you are.
    final at = !_atRevision
        ? ''
        : widget.from == null || widget.from!.isEmpty
            ? ' at ${_shortSha(widget.to!)}'
            : ' between ${_shortSha(widget.from!)} and ${_shortSha(widget.to!)}';

    String prompt;
    if (_hasSelection) {
      final allLines = _parseDiff(_diff!.diff);
      final start = (_selStart ?? 0).clamp(0, allLines.length);
      final end = ((_selEnd ?? _selStart ?? 0) + 1).clamp(0, allLines.length);
      final selectedTexts = allLines.sublist(start, end).map((l) {
        final prefix = l.type == _DiffLineType.added ? '+' : l.type == _DiffLineType.removed ? '-' : ' ';
        return '$prefix${l.text}';
      }).join('\n');
      final ext = _diff?.language ?? '';
      prompt = 'Regarding diff of `${widget.change.path}`$at $_selLabel:\n```$ext\n$selectedTexts\n```\n${question.trim()}';
    } else {
      prompt = 'Regarding diff of `${widget.change.path}`$at:\n${question.trim()}';
    }

    final nav = Navigator.of(context);
    Navigator.pop(ctx); // close sheet
    await svc.sendSessionPrompt(widget.sessionId!, prompt);
    if (!mounted) return;
    nav.popUntil(
      (route) => route.settings.name != '/file-browser' && route.settings.name != '/git-status',
    );
  }

  Widget _buildDiffView(ThemeData theme) {
    switch (_mode) {
      case DiffViewMode.diff:
        return _buildChangesOnly(theme);
      case DiffViewMode.unified:
        return _buildUnified(theme);
      case DiffViewMode.full:
        return _buildFullFile(theme);
    }
  }

  // Parse diff into lines with metadata.
  List<_DiffLine> _parseDiff(String rawDiff) {
    final lines = rawDiff.split('\n');
    final result = <_DiffLine>[];
    for (final line in lines) {
      if (line.startsWith('@@')) {
        result.add(_DiffLine(line, _DiffLineType.header));
      } else if (line.startsWith('+') && !line.startsWith('+++')) {
        result.add(_DiffLine(line.substring(1), _DiffLineType.added));
      } else if (line.startsWith('-') && !line.startsWith('---')) {
        result.add(_DiffLine(line.substring(1), _DiffLineType.removed));
      } else if (line.startsWith(' ')) {
        result.add(_DiffLine(line.substring(1), _DiffLineType.context));
      }
      // Skip file headers (---, +++)
    }
    return result;
  }

  // Diff mode: only show changed lines + hunk headers
  Widget _buildChangesOnly(ThemeData theme) {
    final lines = _parseDiff(_diff!.diff);
    final filtered = lines.where((l) => l.type != _DiffLineType.context).toList();
    return _buildDiffListView(theme, filtered);
  }

  // Unified mode: show all lines with context
  Widget _buildUnified(ThemeData theme) {
    final lines = _parseDiff(_diff!.diff);
    return _buildDiffListView(theme, lines);
  }

  Widget _buildDiffListView(ThemeData theme, List<_DiffLine> lines) {
    if (lines.isEmpty) {
      return Center(
        child: Text('No changes', style: TextStyle(color: theme.colorScheme.onSurfaceVariant)),
      );
    }

    final isDark = theme.brightness == Brightness.dark;
    final language = _langForExt(_diff!.language);

    // Collect all non-header code for syntax highlighting.
    final codeLines = <int>[];
    final codeTexts = <String>[];
    for (int i = 0; i < lines.length; i++) {
      if (lines[i].type != _DiffLineType.header) {
        codeLines.add(i);
        codeTexts.add(lines[i].text);
      }
    }

    // Syntax highlight the combined code.
    List<List<TextSpan>>? highlightedLines;
    if (language != null && codeTexts.isNotEmpty) {
      highlightedLines = _highlightLines(codeTexts.join('\n'), language, isDark);
    }

    return ListView.builder(
      itemCount: lines.length,
      itemBuilder: (ctx, i) {
        final line = lines[i];
        if (line.type == _DiffLineType.header) {
          return Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            color: isDark ? const Color(0xFF1E2D3D) : const Color(0xFFE8F0FE),
            child: Text(
              line.text,
              style: TextStyle(
                fontSize: 11,
                fontFamily: 'monospace',
                color: isDark ? Colors.blue.shade200 : Colors.blue.shade700,
              ),
            ),
          );
        }

        final bgColor = _bgColorForType(line.type, isDark);
        final prefix = line.type == _DiffLineType.added
            ? '+'
            : line.type == _DiffLineType.removed
                ? '-'
                : ' ';
        final prefixColor = line.type == _DiffLineType.added
            ? Colors.green
            : line.type == _DiffLineType.removed
                ? Colors.red
                : theme.colorScheme.onSurfaceVariant;

        // Find highlighted spans for this line.
        final codeIdx = codeLines.indexOf(i);
        Widget textWidget;
        if (highlightedLines != null && codeIdx >= 0 && codeIdx < highlightedLines.length) {
          textWidget = RichText(
            text: TextSpan(
              children: [
                TextSpan(
                  text: '$prefix ',
                  style: TextStyle(fontSize: 12, fontFamily: 'monospace', color: prefixColor, fontWeight: FontWeight.w700),
                ),
                ...highlightedLines[codeIdx],
              ],
            ),
          );
        } else {
          textWidget = RichText(
            text: TextSpan(
              children: [
                TextSpan(
                  text: '$prefix ',
                  style: TextStyle(fontSize: 12, fontFamily: 'monospace', color: prefixColor, fontWeight: FontWeight.w700),
                ),
                TextSpan(
                  text: line.text,
                  style: TextStyle(fontSize: 12, fontFamily: 'monospace', color: theme.colorScheme.onSurface),
                ),
              ],
            ),
          );
        }

        final selected = _isLineSelected(i);
        final selectedBg = isDark ? const Color(0xFF1A3A5C) : const Color(0xFFD4E8FC);

        return GestureDetector(
          onTap: widget.sessionId != null ? () => _onLineTap(i) : null,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
            decoration: BoxDecoration(
              color: selected ? selectedBg : bgColor,
              border: selected ? Border(left: BorderSide(color: theme.colorScheme.primary, width: 3)) : null,
            ),
            child: textWidget,
          ),
        );
      },
    );
  }

  // Full file mode: entire file with changed regions highlighted
  Widget _buildFullFile(ThemeData theme) {
    if (_fullFile == null || _fullFile!.content == null) {
      return const _DiffSkeleton();
    }

    final isDark = theme.brightness == Brightness.dark;
    final content = _fullFile!.content!;
    final fileLines = content.split('\n');
    final language = _langForExt(_diff!.language);

    // Parse diff to find added line numbers in the new file.
    final addedLines = <int>{};
    final diffLines = _diff!.diff.split('\n');
    int newLineNum = 0;
    for (final line in diffLines) {
      if (line.startsWith('@@')) {
        // Parse @@ -a,b +c,d @@
        final match = RegExp(r'\+(\d+)').firstMatch(line);
        if (match != null) {
          newLineNum = int.parse(match.group(1)!) - 1;
        }
        continue;
      }
      if (line.startsWith('+++') || line.startsWith('---')) continue;
      if (line.startsWith('+')) {
        newLineNum++;
        addedLines.add(newLineNum);
      } else if (line.startsWith('-')) {
        // Removed line — doesn't increment new line number.
      } else if (line.startsWith(' ')) {
        newLineNum++;
      }
    }

    // Syntax highlight entire file.
    List<List<TextSpan>>? highlightedLines;
    if (language != null) {
      highlightedLines = _highlightLines(content, language, isDark);
    }

    // Find first changed line for auto-scroll.
    final scrollController = ScrollController();
    if (addedLines.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final firstChange = addedLines.reduce((a, b) => a < b ? a : b);
        final offset = (firstChange - 3).clamp(0, fileLines.length) * 18.0;
        scrollController.animateTo(offset, duration: const Duration(milliseconds: 300), curve: Curves.easeOut);
      });
    }

    return ListView.builder(
      controller: scrollController,
      itemCount: fileLines.length,
      itemExtent: 18.0,
      itemBuilder: (ctx, i) {
        final lineNum = i + 1;
        final isChanged = addedLines.contains(lineNum);
        final bgColor = isChanged
            ? (isDark ? Colors.green.withValues(alpha: 0.12) : Colors.green.withValues(alpha: 0.08))
            : null;

        Widget textWidget;
        if (highlightedLines != null && i < highlightedLines.length) {
          textWidget = RichText(
            text: TextSpan(children: highlightedLines[i]),
            overflow: TextOverflow.clip,
            maxLines: 1,
          );
        } else {
          textWidget = Text(
            fileLines[i],
            style: TextStyle(fontSize: 12, fontFamily: 'monospace', color: theme.colorScheme.onSurface),
            overflow: TextOverflow.clip,
            maxLines: 1,
          );
        }

        final selected = _isLineSelected(i);
        final selectedBg = isDark ? const Color(0xFF1A3A5C) : const Color(0xFFD4E8FC);

        return GestureDetector(
          onTap: widget.sessionId != null ? () => _onLineTap(i) : null,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            decoration: BoxDecoration(
              color: selected ? selectedBg : bgColor,
              border: selected
                  ? Border(left: BorderSide(color: theme.colorScheme.primary, width: 3))
                  : isChanged
                      ? Border(left: BorderSide(color: Colors.green.shade400, width: 3))
                      : null,
            ),
            child: Row(
              children: [
                SizedBox(
                  width: 36,
                  child: Text(
                    '$lineNum',
                    style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: selected ? theme.colorScheme.primary : theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.5)),
                    textAlign: TextAlign.right,
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(child: textWidget),
              ],
            ),
          ),
        );
      },
    );
  }

  Color? _bgColorForType(_DiffLineType type, bool isDark) {
    switch (type) {
      case _DiffLineType.added:
        return isDark ? Colors.green.withValues(alpha: 0.12) : Colors.green.withValues(alpha: 0.08);
      case _DiffLineType.removed:
        return isDark ? Colors.red.withValues(alpha: 0.12) : Colors.red.withValues(alpha: 0.08);
      default:
        return null;
    }
  }

  /// Syntax-highlights code and splits into per-line TextSpan lists.
  List<List<TextSpan>> _highlightLines(String code, String language, bool isDark) {
    final themeMap = isDark ? atomOneDarkTheme : atomOneLightTheme;
    final defaultStyle = TextStyle(
      fontSize: 12,
      fontFamily: 'monospace',
      color: isDark ? Colors.white70 : Colors.black87,
    );

    try {
      final result = highlight.parse(code, language: language);
      // Build flat list of TextSpans from highlight nodes.
      final allSpans = <TextSpan>[];
      _buildSpans(result.nodes!, themeMap, defaultStyle, allSpans);

      // Now split into lines.
      final lines = <List<TextSpan>>[[]];
      for (final span in allSpans) {
        final text = span.text ?? '';
        if (!text.contains('\n')) {
          lines.last.add(span);
          continue;
        }
        // Split span across newlines.
        final parts = text.split('\n');
        for (int i = 0; i < parts.length; i++) {
          if (i > 0) lines.add([]);
          if (parts[i].isNotEmpty) {
            lines.last.add(TextSpan(text: parts[i], style: span.style));
          }
        }
      }
      return lines;
    } catch (_) {
      // Fallback: plain text per line.
      return code.split('\n').map((l) => [TextSpan(text: l, style: defaultStyle)]).toList();
    }
  }

  void _buildSpans(List<dynamic> nodes, Map<String, TextStyle> themeMap, TextStyle defaultStyle, List<TextSpan> out) {
    for (final node in nodes) {
      if (node.value != null) {
        TextStyle style = defaultStyle;
        if (node.className != null) {
          final className = node.className as String;
          style = themeMap[className] ?? themeMap['root'] ?? defaultStyle;
          style = style.copyWith(fontSize: 12, fontFamily: 'monospace');
        }
        out.add(TextSpan(text: node.value as String, style: style));
      } else if (node.children != null) {
        _buildSpans(node.children as List<dynamic>, themeMap, defaultStyle, out);
      }
    }
  }

  String? _langForExt(String ext) {
    switch (ext) {
      case 'dart': return 'dart';
      case 'go': return 'go';
      case 'py': return 'python';
      case 'js': return 'javascript';
      case 'ts': case 'tsx': return 'typescript';
      case 'jsx': return 'javascript';
      case 'java': return 'java';
      case 'kt': return 'kotlin';
      case 'swift': return 'swift';
      case 'rs': return 'rust';
      case 'c': case 'h': return 'c';
      case 'cpp': return 'cpp';
      case 'cs': return 'cs';
      case 'rb': return 'ruby';
      case 'sh': case 'bash': case 'zsh': return 'bash';
      case 'json': return 'json';
      case 'yaml': case 'yml': return 'yaml';
      case 'toml': return 'ini';
      case 'xml': return 'xml';
      case 'html': return 'html';
      case 'css': return 'css';
      case 'scss': return 'scss';
      case 'sql': return 'sql';
      case 'md': return 'markdown';
      default: return null;
    }
  }
}

class _GitStatusSkeleton extends StatelessWidget {
  const _GitStatusSkeleton();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Branch header
          Skeleton(height: 60, borderRadius: BorderRadius.circular(12)),
          const SizedBox(height: 24),
          // Section header
          const Skeleton(width: 80, height: 12),
          const SizedBox(height: 12),
          // File rows
          for (int i = 0; i < 5; i++) ...[
            const Skeleton(height: 28),
            const SizedBox(height: 6),
          ],
          const SizedBox(height: 16),
          const Skeleton(width: 100, height: 12),
          const SizedBox(height: 12),
          for (int i = 0; i < 3; i++) ...[
            const Skeleton(height: 28),
            const SizedBox(height: 6),
          ],
        ],
      ),
    );
  }
}

class _DiffSkeleton extends StatelessWidget {
  const _DiffSkeleton();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Hunk header
          Skeleton(height: 20, borderRadius: BorderRadius.circular(4)),
          const SizedBox(height: 8),
          // Code lines
          for (int i = 0; i < 15; i++) ...[
            Skeleton(
              width: 60.0 + (i * 37 % 200),
              height: 16,
              borderRadius: BorderRadius.circular(2),
            ),
            const SizedBox(height: 3),
          ],
        ],
      ),
    );
  }
}

class _DiffLine {
  final String text;
  final _DiffLineType type;
  _DiffLine(this.text, this.type);
}

enum _DiffLineType { header, added, removed, context }
