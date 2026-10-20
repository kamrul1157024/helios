import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../providers/daemon_providers.dart';
import '../services/daemon_api_service.dart';
import '../widgets/git_diff_view.dart';
import '../widgets/git_scope.dart';
import 'file_viewer_screen.dart';
import 'routes.dart';

/// What one commit changed, or everything between two.
class CommitDetailScreen extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String root;
  final String to;
  final String? from;
  final String title;
  final String? sessionId;

  /// A review of the branch: [from] is the base, read from where it forked.
  final bool mergeBase;

  const CommitDetailScreen({
    super.key,
    required this.hostId,
    required this.root,
    required this.to,
    this.from,
    required this.title,
    this.sessionId,
    this.mergeBase = false,
  });

  @override
  rp.ConsumerState<CommitDetailScreen> createState() =>
      _CommitDetailScreenState();
}

class _CommitDetailScreenState extends rp.ConsumerState<CommitDetailScreen> {
  GitChanges? _changes;
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    final changes = await ref.read(
      gitChangesProvider(
        GitChangesKey(
          widget.hostId,
          widget.root,
          widget.to,
          from: widget.from,
          mergeBase: widget.mergeBase,
        ),
      ).future,
    );
    if (!mounted) return;
    setState(() {
      _changes = changes;
      _loading = false;
    });
  }

  /// A file of the commit opens in the viewer on its diff, as a tap in the
  /// Changes view does.
  void _openFile(CommitFile file) {
    final target = DiffTarget(
      root: widget.root,
      change: GitChange(path: file.path, status: file.status),
      from: widget.from,
      to: widget.to,
      mergeBase: widget.mergeBase,
    );
    Navigator.of(context).push(
      MaterialPageRoute(
        settings: const RouteSettings(name: fileViewerRoute),
        builder: (_) => FileViewerScreen(
          hostId: widget.hostId,
          path: target.path,
          rootPath: widget.root,
          sessionId: widget.sessionId,
          diff: target,
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
          changes?.single == true && changes!.subject.isNotEmpty
              ? changes.subject
              : widget.title,
          style: const TextStyle(fontSize: 15),
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.chat_bubble_outline),
            tooltip: 'Back to chat',
            onPressed: () => popToSession(Navigator.of(context)),
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : changes == null
          ? Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    Icons.error_outline,
                    size: 40,
                    color: theme.colorScheme.error,
                  ),
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
                    changes.single
                        ? changes.shortTo
                        : '${changes.shortFrom}...${changes.shortTo}',
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
                        style: TextStyle(
                          fontSize: 12,
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                  if (changes.date.isNotEmpty) ...[
                    const SizedBox(width: 8),
                    Text(
                      changes.timeAgo,
                      style: TextStyle(
                        fontSize: 12,
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                  const Spacer(),
                  ...statChips(changes.insertions, changes.deletions),
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
                style: TextStyle(
                  fontSize: 13,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ),
        ...changes.files.map(
          (file) => _CommitFileTile(file: file, onTap: () => _openFile(file)),
        ),
        if (changes.truncated)
          Padding(
            padding: const EdgeInsets.only(top: 12),
            child: Text(
              'Showing the first ${changes.files.length} files.',
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onSurfaceVariant,
              ),
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
                  color: gitStatusColor(file.status),
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
                    style: TextStyle(
                      fontSize: 13,
                      fontFamily: 'monospace',
                      color: theme.colorScheme.onSurface,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (file.from.isNotEmpty)
                    Text(
                      'was ${file.from}',
                      style: TextStyle(
                        fontSize: 11,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            ...statChips(file.insertions, file.deletions),
            Icon(
              Icons.chevron_right,
              size: 16,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ],
        ),
      ),
    );
  }
}
