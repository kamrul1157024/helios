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

  const GitStatusScreen({super.key, required this.hostId, required this.cwd});

  @override
  State<GitStatusScreen> createState() => _GitStatusScreenState();
}

class _GitStatusScreenState extends State<GitStatusScreen> {
  GitStatus? _status;
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
    final status = await _svc?.gitStatus(widget.cwd);
    if (!mounted) return;
    setState(() {
      _status = status;
      _loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Git Status'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _load,
          ),
          IconButton(
            icon: const Icon(Icons.chat_bubble_outline),
            tooltip: 'Back to chat',
            onPressed: () => Navigator.of(context).pop(),
          ),
        ],
      ),
      body: _loading
          ? const _GitStatusSkeleton()
          : _status == null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
                      const SizedBox(height: 12),
                      const Text('Not a git repository'),
                    ],
                  ),
                )
              : RefreshIndicator(
                  onRefresh: _load,
                  child: _buildContent(theme),
                ),
    );
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

// ==================== Git Diff Screen ====================

enum DiffViewMode { diff, unified, full }

class GitDiffScreen extends StatefulWidget {
  final String hostId;
  final String cwd;
  final GitChange change;
  final bool staged;

  const GitDiffScreen({
    super.key,
    required this.hostId,
    required this.cwd,
    required this.change,
    required this.staged,
  });

  @override
  State<GitDiffScreen> createState() => _GitDiffScreenState();
}

class _GitDiffScreenState extends State<GitDiffScreen> {
  GitDiff? _diff;
  FileReadResult? _fullFile;
  bool _loading = true;
  DiffViewMode _mode = DiffViewMode.unified;

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

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
    final diff = await svc.gitDiff(widget.cwd, widget.change.path, staged: widget.staged);
    if (!mounted) return;
    setState(() {
      _diff = diff;
      _loading = false;
    });
    // Preload full file for full-file mode.
    final fullPath = '${widget.cwd}/${widget.change.path}';
    final file = await svc.readFile(fullPath);
    if (mounted && file != null) {
      setState(() => _fullFile = file);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(widget.change.fileName, style: const TextStyle(fontSize: 15), overflow: TextOverflow.ellipsis),
            if (_diff?.stat.isNotEmpty == true)
              Text(_diff!.stat, style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant)),
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
              segments: const [
                ButtonSegment(value: DiffViewMode.diff, label: Text('Diff', style: TextStyle(fontSize: 12))),
                ButtonSegment(value: DiffViewMode.unified, label: Text('Unified', style: TextStyle(fontSize: 12))),
                ButtonSegment(value: DiffViewMode.full, label: Text('Full', style: TextStyle(fontSize: 12))),
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

        return Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
          color: bgColor,
          child: textWidget,
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

        return Container(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          decoration: BoxDecoration(
            color: bgColor,
            border: isChanged
                ? Border(left: BorderSide(color: Colors.green.shade400, width: 3))
                : null,
          ),
          child: Row(
            children: [
              SizedBox(
                width: 36,
                child: Text(
                  '$lineNum',
                  style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.5)),
                  textAlign: TextAlign.right,
                ),
              ),
              const SizedBox(width: 8),
              Expanded(child: textWidget),
            ],
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
