import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../providers/daemon_providers.dart';
import '../services/daemon_api_service.dart';
import '../utils/file_types.dart';
import '../widgets/git_diff_view.dart';
import '../widgets/git_scope.dart';
import '../widgets/skeleton.dart';
import 'commit_detail_screen.dart';
import 'file_viewer_screen.dart';
import 'routes.dart';

export 'file_viewer_screen.dart';

class FileBrowserScreen extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String rootPath;

  /// Where to start browsing, if not at the root — "show in folder" from the
  /// viewer lands here, with the root left alone.
  final String? startPath;
  final String? sessionId;

  /// Open on the Changes view rather than the folder — the session screen's
  /// git bar lands here, now that there is no Git screen (spec 65, B8).
  final bool startInChanges;

  const FileBrowserScreen({
    super.key,
    required this.hostId,
    required this.rootPath,
    this.startPath,
    this.sessionId,
    this.startInChanges = false,
  });

  @override
  rp.ConsumerState<FileBrowserScreen> createState() =>
      _FileBrowserScreenState();
}

/// One step back: the folder to return to, and the file to open again on top
/// of it when the step was "show in folder" from that file.
typedef _Step = ({String path, String? reopen});

class _FileBrowserScreenState extends rp.ConsumerState<FileBrowserScreen>
    with WidgetsBindingObserver {
  late String _currentPath;
  late String _rootPath;
  final List<_Step> _history = [];

  /// Whether the list shows the folder or the Changes view (spec 65, B6).
  late bool _changes = widget.startInChanges;

  /// The scope each session's Changes view was left on, for the life of the
  /// app. Two sessions on two branches are two reviews, and one's scope means
  /// nothing in the other. Absent means the default (`defaultScope`).
  static final _scopes = <String, GitScope>{};

  String get _scopeKey => '${widget.hostId}:${widget.sessionId ?? _rootPath}';

  final _crumbs = ScrollController();

  /// The folder the breadcrumb was last scrolled to the end for.
  String? _crumbsShown;

  FileMarks? _marks;
  GitStatus? _marksGit;
  List<TouchedFile>? _marksTouched;

  /// Whether the read that [didChangeDependencies] owes the first build is done.
  bool _readOnOpen = false;

  @override
  void initState() {
    super.initState();
    _currentPath = widget.startPath ?? widget.rootPath;
    _rootPath = widget.rootPath;
    WidgetsBinding.instance.addObserver(this);
  }

  /// The first read, from the earliest place `ref` is allowed to work.
  ///
  /// Not `initState`. Riverpod resolves the enclosing scope lazily, on the
  /// first `ref` call, through `dependOnInheritedWidgetOfExactType` — and
  /// Flutter forbids that until `initState` has returned. The check is an
  /// `assert`, so this throws a red screen in debug and passes silently in
  /// release, which is how it survived being shipped.
  ///
  /// `didChangeDependencies` still runs before the first build, which is what
  /// `_reread` needs.
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_readOnOpen) return;
    _readOnOpen = true;
    _reread();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _crumbs.dispose();
    super.dispose();
  }

  /// Coming back to the app is a reason to look again.
  ///
  /// The daemon announces what it is asked to watch, and it was not being
  /// watched while this was suspended. Covers the writer no hook can see, too:
  /// somebody's own editor.
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _reread();
  }

  /// Drops the held listing so the next build fetches.
  ///
  /// Before the first build on purpose. The entry outlives the screen by
  /// [fileKeepAlive], so returning to a directory would otherwise serve what
  /// it held on the way out — and the read is also what re-registers the path
  /// with the daemon after its watch expired. A cold key has no entry to drop,
  /// so this costs one fetch rather than two either way. See
  /// docs/specs/54-file-change-events.md.
  void _reread() => ref.invalidate(listFilesProvider(_listingKey));

  /// Pull to refresh: the listing, and the marks drawn on it.
  Future<void> _refresh() async {
    ref.invalidate(listFilesProvider(_listingKey));
    ref.invalidate(gitStatusProvider((widget.hostId, _rootPath)));
    // The Changes view's lists, whichever scope it is on. Only what is
    // watched refetches.
    ref.invalidate(gitChangesProvider);
    ref.invalidate(gitLogProvider);
    final session = widget.sessionId;
    if (session != null) {
      ref.invalidate(touchedProvider((widget.hostId, session)));
    }
    try {
      await ref.read(listFilesProvider(_listingKey).future);
    } on FileError {
      // The list shows the error itself; the spinner only has to stop.
    }
  }

  /// The listing for wherever the browser currently is.
  ///
  /// Keying on the path is what fixes the race that `mounted` never caught:
  /// tapping folder A then B used to let A's slower answer land last and walk
  /// the browser backwards. Now A's answer lands under A's key, and the build
  /// is watching B's.
  (String, String) get _listingKey => (widget.hostId, _currentPath);

  void _navigateTo(String path, {String? reopen}) {
    setState(() {
      _history.add((path: _currentPath, reopen: reopen));
      _currentPath = path;
      _changes = false;
    });
  }

  void _goTo(String path) => setState(() => _currentPath = path);

  /// Re-roots the browser on another worktree of the same repository. History is
  /// dropped: the paths in it belong to the worktree being left behind.
  Future<void> _pickRoot() async {
    if (ref.read(serviceProvider(widget.hostId)) == null) return;
    final worktrees = sortWorktreesByLastTouched(
      await ref.read(
        gitWorktreesProvider((widget.hostId, _currentPath)).future,
      ),
    );
    if (!mounted) return;
    if (worktrees.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('No worktrees here'),
          duration: Duration(seconds: 2),
          behavior: SnackBarBehavior.floating,
        ),
      );
      return;
    }
    final picked = await showModalBottomSheet<String>(
      context: context,
      builder: (ctx) {
        final theme = Theme.of(ctx);
        return SafeArea(
          child: ListView(
            shrinkWrap: true,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Text(
                  'Root folder',
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 1.2,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
              ...worktrees.map((wt) {
                final selected = wt.path == _rootPath;
                return ListTile(
                  leading: Icon(
                    selected
                        ? Icons.radio_button_checked
                        : Icons.radio_button_unchecked,
                    size: 22,
                    color: selected
                        ? theme.colorScheme.primary
                        : theme.colorScheme.onSurfaceVariant,
                  ),
                  title: Text(
                    wt.branch.isEmpty ? '(detached)' : wt.branch,
                    style: const TextStyle(
                      fontSize: 14,
                      fontFamily: 'monospace',
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  subtitle: Text(
                    wt.date.isEmpty
                        ? wt.shortPath
                        : '${wt.shortPath} · ${wt.timeAgo}',
                    style: const TextStyle(fontSize: 12),
                    overflow: TextOverflow.ellipsis,
                  ),
                  onTap: () => Navigator.of(ctx).pop(wt.path),
                );
              }),
            ],
          ),
        );
      },
    );
    if (picked == null || !mounted) return;
    _history.clear();
    setState(() {
      _rootPath = picked;
      _currentPath = picked;
    });
  }

  void _onBack() {
    if (_history.isEmpty) return;
    final step = _history.removeLast();
    _goTo(step.path);
    final reopen = step.reopen;
    if (reopen != null) _openFile(reopen);
  }

  Future<void> _openFile(String path, {int? line, DiffTarget? diff}) async {
    if (!mounted) return;
    final reveal = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        settings: const RouteSettings(name: fileViewerRoute),
        builder: (_) => FileViewerScreen(
          path: path,
          hostId: widget.hostId,
          sessionId: widget.sessionId,
          rootPath: _rootPath,
          line: line,
          diff: diff,
        ),
      ),
    );
    // "Show in folder" comes back as the directory to move to, rather than
    // pushing a second browser on top of this one. It is a step like any
    // other, and Back from it opens the file again.
    if (reveal != null && mounted) _navigateTo(reveal, reopen: path);
  }

  Future<void> _pickScope(String gitRoot, GitScope current) async {
    final picked = await showScopePicker(
      context,
      hostId: widget.hostId,
      root: gitRoot,
      current: current,
    );
    if (picked == null || !mounted) return;
    setState(() => _scopes[_scopeKey] = picked);
  }

  void _openCommitDetail(String gitRoot, GitScope scope) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => CommitDetailScreen(
          hostId: widget.hostId,
          root: gitRoot,
          to: scope.to ?? 'HEAD',
          from: scope.from,
          mergeBase: scope.kind == GitScopeKind.review,
          title: scope.label,
          sessionId: widget.sessionId,
        ),
      ),
    );
  }

  /// The Changes view: the scope bar, and the files the scope changed.
  Widget _buildChanges(
    GitStatus? git,
    List<TouchedFile> touched,
    FileMarks marks,
  ) {
    final gitRoot = git != null && git.root.isNotEmpty ? git.root : _rootPath;
    var scope = _scopes[_scopeKey];
    GitLog? log;
    if (scope == null) {
      // The default depends on the branch, so it waits for the history
      // rather than drawing the working tree and then flipping the scope
      // under the reader.
      final probe = ref.watch(
        gitLogProvider(GitLogKey(widget.hostId, gitRoot)),
      );
      if (probe.isLoading && !probe.hasValue) {
        return const Center(child: CircularProgressIndicator());
      }
      log = probe.valueOrNull;
      scope = defaultScope(log);
    }
    final picked = scope;
    return Column(
      children: [
        GitScopeBar(
          branch: git?.branch ?? log?.branch ?? '',
          scope: picked,
          onPick: () => _pickScope(gitRoot, picked),
          onDetail: picked.isWorking
              ? null
              : () => _openCommitDetail(gitRoot, picked),
        ),
        Expanded(
          child: picked.isWorking
              ? ChangesView(
                  root: _rootPath,
                  git: git,
                  touched: touched,
                  showTouched: widget.sessionId != null,
                  marks: marks,
                  onOpen: _openChange,
                  onRefresh: _refresh,
                )
              : ScopeChangesView(
                  hostId: widget.hostId,
                  gitRoot: gitRoot,
                  root: _rootPath,
                  scope: picked,
                  marks: marks,
                  onOpen: _openChange,
                  onRefresh: _refresh,
                ),
        ),
      ],
    );
  }

  void _openChange(String path, {int? line, DiffTarget? diff}) =>
      _openFile(path, line: line, diff: diff);

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final git = ref
        .watch(gitStatusProvider((widget.hostId, _rootPath)))
        .valueOrNull;
    final session = widget.sessionId;
    final touched = session == null
        ? const <TouchedFile>[]
        : ref.watch(touchedProvider((widget.hostId, session))).valueOrNull ??
              const <TouchedFile>[];
    // Built once per answer rather than per build: a status with thousands
    // of changes is a lot of string work to redo on every frame.
    if (!identical(git, _marksGit) || !identical(touched, _marksTouched)) {
      _marks = null;
    }
    _marksGit = git;
    _marksTouched = touched;
    final marks = _marks ??= FileMarks.of(git, touched);

    if (_crumbsShown != _currentPath) {
      _crumbsShown = _currentPath;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_crumbs.hasClients) {
          _crumbs.jumpTo(_crumbs.position.maxScrollExtent);
        }
      });
    }

    return PopScope(
      canPop: _history.isEmpty,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _onBack();
      },
      child: Scaffold(
        appBar: AppBar(
          title: _BrowserTitle(root: _rootPath, branch: git?.branch),
          actions: [
            IconButton(
              icon: const Icon(Icons.account_tree_outlined),
              tooltip: 'Change root folder',
              onPressed: _pickRoot,
            ),
            IconButton(
              icon: const Icon(Icons.chat_bubble_outline),
              tooltip: 'Back to chat',
              onPressed: () => popToSession(Navigator.of(context)),
            ),
          ],
          bottom: PreferredSize(
            preferredSize: const Size.fromHeight(48),
            child: Breadcrumbs(
              root: _rootPath,
              path: _currentPath,
              controller: _crumbs,
              onTap: _navigateTo,
            ),
          ),
        ),
        body: Column(
          children: [
            _FilterRow(
              changes: _changes,
              count: marks.changeCount,
              onChanged: (on) => setState(() => _changes = on),
            ),
            Expanded(
              child: _changes
                  ? _buildChanges(git, touched, marks)
                  : _buildListing(theme, marks),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildListing(ThemeData theme, FileMarks marks) {
    final listing = ref.watch(listFilesProvider(_listingKey));

    // Not a bare isLoading: an invalidation keeps the previous value and still
    // reports loading, so a file_changed event would blank the listing the
    // reader is looking at and redraw it. The skeleton is a first-load
    // affordance. Same guard as the transcript's (spec 52,
    // session_detail_screen.dart:460).
    if (listing.isLoading && !listing.hasValue) {
      return const _FileListSkeleton();
    }

    final error = listing.error;
    if (error != null && !listing.hasValue) {
      return RefreshIndicator(
        onRefresh: _refresh,
        child: FileErrorView(
          error: error,
          path: _currentPath,
          onRetry: () => ref.invalidate(listFilesProvider(_listingKey)),
        ),
      );
    }
    final entries = listing.valueOrNull?.entries ?? const <FileEntry>[];
    return RefreshIndicator(
      onRefresh: _refresh,
      child: entries.isEmpty
          ? ListView(
              physics: const AlwaysScrollableScrollPhysics(),
              children: [
                const SizedBox(height: 120),
                Center(
                  child: Text(
                    'Empty directory',
                    style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
                  ),
                ),
              ],
            )
          : ListView.builder(
              // One scroll position per folder: back out of a deep folder and
              // the one above is where it was left.
              key: PageStorageKey<String>('files:$_currentPath'),
              physics: const AlwaysScrollableScrollPhysics(),
              itemCount: entries.length,
              itemBuilder: (ctx, i) {
                final entry = entries[i];
                return FileEntryTile(
                  entry: entry,
                  marks: marks,
                  onTap: entry.isDir
                      ? () => _navigateTo(entry.path)
                      : () => _openFile(entry.path),
                );
              },
            ),
    );
  }
}

/// The root folder's name, and the branch checked out in it.
class _BrowserTitle extends StatelessWidget {
  final String root;
  final String? branch;

  const _BrowserTitle({required this.root, required this.branch});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final name = root == '/'
        ? '/'
        : root.split('/').where((p) => p.isNotEmpty).last;
    final branch = this.branch;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          name,
          style: const TextStyle(fontSize: 15),
          overflow: TextOverflow.ellipsis,
        ),
        if (branch != null && branch.isNotEmpty)
          Row(
            children: [
              Icon(
                Icons.fork_right,
                size: 14,
                color: theme.colorScheme.onSurfaceVariant,
              ),
              const SizedBox(width: 2),
              Flexible(
                child: Text(
                  branch,
                  style: TextStyle(
                    fontSize: 11,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
      ],
    );
  }
}

/// The path from the session's root to where the browser is.
///
/// It starts at the root rather than at `/`: everything above the checkout is
/// the same on every screen and pushes the part that changes off the right
/// edge. A long press on the root crumb shows the full path. Outside the root
/// — "show in folder" on a plan under `~/.claude` — it falls back to `/`.
class Breadcrumbs extends StatelessWidget {
  final String root;
  final String path;
  final ScrollController controller;
  final ValueChanged<String> onTap;

  const Breadcrumbs({
    super.key,
    required this.root,
    required this.path,
    required this.controller,
    required this.onTap,
  });

  List<({String label, String path})> get segments {
    final inside =
        path == root || path.startsWith(root == '/' ? '/' : '$root/');
    final base = inside ? root : '/';
    final rest = path
        .substring(base.length)
        .split('/')
        .where((p) => p.isNotEmpty);
    final rootLabel = base == '/'
        ? '/'
        : base.split('/').where((p) => p.isNotEmpty).last;
    final out = [(label: rootLabel, path: base)];
    var at = base == '/' ? '' : base;
    for (final part in rest) {
      at = '$at/$part';
      out.add((label: part, path: at));
    }
    return out;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final crumbs = segments;
    return SizedBox(
      height: 48,
      width: double.infinity,
      // A row, not a lazy list: a lazy list only estimates its length until
      // every crumb is built, so "scroll to the end" would stop short.
      child: SingleChildScrollView(
        controller: controller,
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 4),
        child: Row(
          children: [
            for (var i = 0; i < crumbs.length; i++) ...[
              if (i > 0)
                Icon(
                  Icons.chevron_right,
                  size: 18,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              _crumb(theme, crumbs, i),
            ],
          ],
        ),
      ),
    );
  }

  Widget _crumb(
    ThemeData theme,
    List<({String label, String path})> crumbs,
    int i,
  ) {
    final crumb = crumbs[i];
    final isLast = i == crumbs.length - 1;
    Widget label = InkWell(
      key: ValueKey('crumb:${crumb.path}'),
      borderRadius: BorderRadius.circular(8),
      onTap: isLast ? null : () => onTap(crumb.path),
      child: ConstrainedBox(
        constraints: const BoxConstraints(minWidth: 48, minHeight: 48),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Center(
            widthFactor: 1,
            child: Text(
              crumb.label,
              style: TextStyle(
                fontSize: 13,
                fontFamily: 'monospace',
                color: isLast
                    ? theme.colorScheme.onSurface
                    : theme.colorScheme.primary,
                fontWeight: isLast ? FontWeight.w600 : FontWeight.normal,
              ),
            ),
          ),
        ),
      ),
    );
    if (i == 0) {
      label = Tooltip(
        message: crumb.path,
        triggerMode: TooltipTriggerMode.longPress,
        child: label,
      );
    }
    return label;
  }
}

/// The chip that turns the folder into the Changes view.
class _FilterRow extends StatelessWidget {
  final bool changes;
  final int count;
  final ValueChanged<bool> onChanged;

  const _FilterRow({
    required this.changes,
    required this.count,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12),
      child: SizedBox(
        height: 48,
        child: Row(
          children: [
            FilterChip(
              label: Text(count > 0 ? 'Changes · $count' : 'Changes'),
              avatar: changes
                  ? null
                  : const Icon(Icons.difference_outlined, size: 18),
              selected: changes,
              materialTapTargetSize: MaterialTapTargetSize.padded,
              onSelected: onChanged,
            ),
          ],
        ),
      ),
    );
  }
}

/// What git and the session's agent say about each path, ready to look up
/// per row.
class FileMarks {
  /// Absolute path to its git letter: M, A, D, R or U.
  final Map<String, String> status;

  /// Folders that hold a change somewhere below them.
  final Set<String> dirtyDirs;

  /// Absolute path to the agent's last write of it.
  final Map<String, TouchedFile> touched;

  /// Folders that hold a file the agent wrote.
  final Set<String> touchedDirs;

  final int changeCount;

  const FileMarks._(
    this.status,
    this.dirtyDirs,
    this.touched,
    this.touchedDirs,
    this.changeCount,
  );

  factory FileMarks.of(GitStatus? git, List<TouchedFile> touched) {
    final status = <String, String>{};
    final dirtyDirs = <String>{};
    final root = git?.root ?? '';
    if (git != null && root.isNotEmpty) {
      for (final change in [...git.unstaged, ...git.staged, ...git.untracked]) {
        final path = '$root/${change.path}';
        status.putIfAbsent(path, () => gitLetter(change.status));
        _addParents(path, root, dirtyDirs);
      }
    }
    final byPath = <String, TouchedFile>{};
    final touchedDirs = <String>{};
    for (final file in touched) {
      byPath.putIfAbsent(file.path, () => file);
      _addParents(file.path, '/', touchedDirs);
    }
    return FileMarks._(
      status,
      dirtyDirs,
      byPath,
      touchedDirs,
      {...status.keys, ...byPath.keys}.length,
    );
  }

  static void _addParents(String path, String stop, Set<String> into) {
    var at = path;
    while (true) {
      final cut = at.lastIndexOf('/');
      if (cut <= 0) return;
      at = at.substring(0, cut);
      if (!into.add(at)) return;
      if (at == stop) return;
    }
  }
}

/// Git's status as the one letter a row has room for.
String gitLetter(String status) {
  if (status == '?' || status == '??') return 'U';
  return status.isEmpty ? '?' : status.substring(0, 1).toUpperCase();
}

String _gitWord(String letter) => switch (letter) {
  'M' => 'Modified',
  'A' => 'Added',
  'D' => 'Deleted',
  'R' => 'Renamed',
  'U' => 'Untracked',
  _ => 'Changed',
};

IconData _familyIcon(FileFamily family) => switch (family) {
  FileFamily.code => Icons.code,
  FileFamily.markdown => Icons.article_outlined,
  FileFamily.text => Icons.description_outlined,
  FileFamily.data => Icons.data_object,
  FileFamily.image => Icons.image_outlined,
  FileFamily.html => Icons.html,
  FileFamily.style => Icons.palette_outlined,
  FileFamily.shell => Icons.terminal,
  FileFamily.pdf => Icons.picture_as_pdf_outlined,
  FileFamily.archive => Icons.inventory_2_outlined,
  FileFamily.other => Icons.insert_drive_file_outlined,
};

/// When a file last changed, in the fewest characters that still say it:
/// `now`, `5m`, `3h`, `2d`, and a date past a week.
String shortAgo(DateTime? at, {DateTime? now}) {
  if (at == null) return '';
  final since = (now ?? DateTime.now()).difference(at);
  if (since.inMinutes < 1) return 'now';
  if (since.inHours < 1) return '${since.inMinutes}m';
  if (since.inDays < 1) return '${since.inHours}h';
  if (since.inDays < 7) return '${since.inDays}d';
  final local = at.toLocal();
  const months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ];
  return '${months[local.month - 1]} ${local.day}, ${local.year}';
}

/// The agent's mark: filled for a file it wrote, hollow for a folder that
/// holds one.
class _AgentMark extends StatelessWidget {
  final bool filled;
  final String tooltip;

  const _AgentMark({required this.filled, required this.tooltip});

  @override
  Widget build(BuildContext context) {
    final color = Theme.of(context).colorScheme.primary;
    return Tooltip(
      message: tooltip,
      child: Container(
        width: 8,
        height: 8,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          color: filled ? color : null,
          border: Border.all(color: color, width: 1.5),
        ),
      ),
    );
  }
}

class _GitBadge extends StatelessWidget {
  final String letter;

  const _GitBadge(this.letter);

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: _gitWord(letter),
      child: SizedBox(
        width: 16,
        child: Text(
          letter,
          textAlign: TextAlign.center,
          style: TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w700,
            fontFamily: 'monospace',
            color: gitStatusColor(letter),
          ),
        ),
      ),
    );
  }
}

/// One row of a folder.
class FileEntryTile extends StatelessWidget {
  final FileEntry entry;
  final FileMarks marks;
  final VoidCallback onTap;

  const FileEntryTile({
    super.key,
    required this.entry,
    required this.marks,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final letter = entry.isDir ? null : marks.status[entry.path];
    final dirty = entry.isDir && marks.dirtyDirs.contains(entry.path);
    final touched = marks.touched[entry.path];
    final holdsTouched = entry.isDir && marks.touchedDirs.contains(entry.path);
    final modified = shortAgo(DateTime.tryParse(entry.modTime));
    final subtitle = entry.isDir
        ? modified
        : [entry.formattedSize, if (modified.isNotEmpty) modified].join(' · ');

    final tile = ListTile(
      minTileHeight: 48,
      leading: Icon(
        entry.isDir ? Icons.folder : _familyIcon(familyOf(entry.name)),
        size: 26,
        color: entry.isDir ? colors.tertiary : colors.onSurfaceVariant,
      ),
      title: Text(
        entry.name,
        style: const TextStyle(fontSize: 14, fontFamily: 'monospace'),
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: subtitle.isEmpty
          ? null
          : Text(
              subtitle,
              style: TextStyle(fontSize: 11, color: colors.onSurfaceVariant),
            ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (touched != null || holdsTouched) ...[
            _AgentMark(
              filled: touched != null,
              tooltip: touched != null
                  ? 'Edited by this session, ${shortAgo(touched.lastAt)} ago'
                  : 'Holds a file this session edited',
            ),
            const SizedBox(width: 8),
          ],
          if (letter != null) _GitBadge(letter),
          if (dirty)
            Tooltip(
              message: 'Holds uncommitted changes',
              child: Container(
                width: 6,
                height: 6,
                margin: const EdgeInsets.symmetric(horizontal: 5),
                decoration: const BoxDecoration(
                  shape: BoxShape.circle,
                  color: Colors.orange,
                ),
              ),
            ),
          if (entry.isDir)
            Icon(Icons.chevron_right, size: 24, color: colors.onSurfaceVariant),
        ],
      ),
      onTap: onTap,
      dense: true,
    );
    if (entry.ignored != true) return tile;
    return Tooltip(
      message: 'Ignored by .gitignore. Search skips it.',
      child: Opacity(opacity: 0.5, child: tile),
    );
  }
}

/// Opens a changed file: at the line the agent last edited, and on its diff
/// when it has one.
typedef OpenChange = void Function(String path, {int? line, DiffTarget? diff});

/// The working tree, in two parts (spec 65, B6): the files this session's
/// agent wrote, newest first, and every other uncommitted change git sees —
/// `Bash` writes, other sessions in the same worktree, the reader's own edits.
class ChangesView extends StatelessWidget {
  final String root;
  final GitStatus? git;
  final List<TouchedFile> touched;

  /// Off when the browser was opened without a session: there is no agent to
  /// attribute anything to, so only git's list shows.
  final bool showTouched;
  final FileMarks marks;
  final OpenChange onOpen;
  final Future<void> Function() onRefresh;

  const ChangesView({
    super.key,
    required this.root,
    required this.git,
    required this.touched,
    required this.showTouched,
    required this.marks,
    required this.onOpen,
    required this.onRefresh,
  });

  /// Every uncommitted change, by absolute path, as the diff to open for it.
  /// Unstaged first: that is what is on disk now.
  Map<String, DiffTarget> _targets() {
    final status = git;
    final gitRoot = status?.root ?? '';
    final targets = <String, DiffTarget>{};
    if (status == null || gitRoot.isEmpty) return targets;
    void add(GitChange change, {bool staged = false, bool untracked = false}) {
      targets.putIfAbsent(
        '$gitRoot/${change.path}',
        () => DiffTarget(
          root: gitRoot,
          change: change,
          staged: staged,
          untracked: untracked,
        ),
      );
    }

    for (final change in status.unstaged) {
      add(change);
    }
    for (final change in status.staged) {
      add(change, staged: true);
    }
    for (final change in status.untracked) {
      add(change, untracked: true);
    }
    return targets;
  }

  @override
  Widget build(BuildContext context) {
    final targets = _targets();
    final mine = touched.map((t) => t.path).toSet();
    final gitRoot = git?.root ?? '';
    return RefreshIndicator(
      onRefresh: onRefresh,
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          if (showTouched) ...[
            const _SectionHeader('Edited by this session'),
            if (touched.isEmpty)
              const _EmptyLine('Nothing yet')
            else
              for (final file in touched)
                _ChangeRow(
                  path: file.path,
                  root: root,
                  letter: marks.status[file.path],
                  when: shortAgo(file.lastAt),
                  agent: true,
                  deleted: file.deleted,
                  onTap: () => onOpen(
                    file.path,
                    line: file.line,
                    // A file the agent deleted that git no longer lists —
                    // committed since — still opens on what went.
                    diff:
                        targets[file.path] ??
                        (file.deleted && file.path.startsWith('$gitRoot/')
                            ? DiffTarget(
                                root: gitRoot,
                                change: GitChange(
                                  path: file.path.substring(gitRoot.length + 1),
                                  status: 'D',
                                ),
                              )
                            : null),
                  ),
                ),
          ],
          _SectionHeader(
            showTouched ? 'Other uncommitted changes' : 'Uncommitted changes',
          ),
          if (targets.keys.every(mine.contains))
            _EmptyLine(git == null ? 'Not a git repository' : 'None')
          else
            for (final entry in targets.entries)
              if (!mine.contains(entry.key))
                _ChangeRow(
                  path: entry.key,
                  root: root,
                  letter: gitLetter(entry.value.change.status),
                  deleted: entry.value.deleted,
                  onTap: () => onOpen(entry.key, diff: entry.value),
                ),
        ],
      ),
    );
  }
}

/// What a review, a commit or a range changed, with the agent's mark on the
/// files this session wrote (spec 65, B8).
class ScopeChangesView extends rp.ConsumerWidget {
  final String hostId;

  /// The repository root the scope's paths are relative to.
  final String gitRoot;

  /// The browser's root, which the rows show their folders from.
  final String root;
  final GitScope scope;
  final FileMarks marks;
  final OpenChange onOpen;
  final Future<void> Function() onRefresh;

  const ScopeChangesView({
    super.key,
    required this.hostId,
    required this.gitRoot,
    required this.root,
    required this.scope,
    required this.marks,
    required this.onOpen,
    required this.onRefresh,
  });

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final changes = ref.watch(
      gitChangesProvider(scope.changesKey(hostId, gitRoot)),
    );
    if (changes.isLoading && !changes.hasValue) {
      return const Center(child: CircularProgressIndicator());
    }
    final value = changes.valueOrNull;
    return RefreshIndicator(
      onRefresh: onRefresh,
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        children: [
          if (value == null)
            const _EmptyLine('Could not read these changes')
          else ...[
            _SectionHeader(
              '${value.files.length} ${value.files.length == 1 ? 'file' : 'files'}',
            ),
            if (value.files.isEmpty) const _EmptyLine('No files changed'),
            for (final file in value.files)
              _ChangeRow(
                path: '$gitRoot/${file.path}',
                root: root,
                letter: gitLetter(file.status),
                agent: marks.touched.containsKey('$gitRoot/${file.path}'),
                deleted: file.status == 'D',
                onTap: () => onOpen(
                  '$gitRoot/${file.path}',
                  diff: scope.target(gitRoot, file),
                ),
              ),
            if (value.truncated)
              _EmptyLine('Showing the first ${value.files.length} files.'),
          ],
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String text;

  const _SectionHeader(this.text);

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: Text(
        text.toUpperCase(),
        style: TextStyle(
          fontSize: 11,
          fontWeight: FontWeight.w700,
          letterSpacing: 1.2,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}

class _EmptyLine extends StatelessWidget {
  final String text;

  const _EmptyLine(this.text);

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Text(
        text,
        style: TextStyle(
          fontSize: 13,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}

class _ChangeRow extends StatelessWidget {
  final String path;
  final String root;
  final String? letter;
  final String when;
  final bool agent;
  final bool deleted;
  final VoidCallback onTap;

  const _ChangeRow({
    required this.path,
    required this.root,
    required this.letter,
    this.when = '',
    this.agent = false,
    this.deleted = false,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final name = path.split('/').last;
    final relative = path.startsWith('$root/')
        ? path.substring(root.length + 1)
        : path;
    final cut = relative.lastIndexOf('/');
    final folder = cut < 0 ? '' : relative.substring(0, cut);
    return ListTile(
      minTileHeight: 48,
      dense: true,
      leading: Icon(
        _familyIcon(familyOf(name)),
        size: 22,
        color: colors.onSurfaceVariant,
      ),
      title: Text(
        name,
        style: TextStyle(
          fontSize: 14,
          fontFamily: 'monospace',
          decoration: deleted ? TextDecoration.lineThrough : null,
        ),
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: folder.isEmpty && when.isEmpty
          ? null
          : Text(
              [
                if (folder.isNotEmpty) folder,
                if (when.isNotEmpty) when,
              ].join(' · '),
              style: TextStyle(fontSize: 11, color: colors.onSurfaceVariant),
              overflow: TextOverflow.ellipsis,
            ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (agent) ...[
            const _AgentMark(filled: true, tooltip: 'Edited by this session'),
            const SizedBox(width: 8),
          ],
          if (letter != null) _GitBadge(letter!),
        ],
      ),
      onTap: onTap,
    );
  }
}

class _FileListSkeleton extends StatelessWidget {
  const _FileListSkeleton();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
      child: Column(
        children: [
          for (int i = 0; i < 10; i++) ...[
            Row(
              children: [
                const Skeleton(
                  width: 24,
                  height: 24,
                  borderRadius: BorderRadius.all(Radius.circular(4)),
                ),
                const SizedBox(width: 12),
                Skeleton(width: 80.0 + (i * 31 % 120), height: 16),
                const Spacer(),
                const Skeleton(width: 40, height: 14),
              ],
            ),
            const SizedBox(height: 14),
          ],
        ],
      ),
    );
  }
}
