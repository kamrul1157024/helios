import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:highlight/highlight.dart' show highlight;

import '../providers/daemon_providers.dart';
import '../screens/routes.dart';
import '../services/daemon_api_service.dart';
import 'ask_ai_sheet.dart';
import 'skeleton.dart';

/// A commit's short name, as git prints it.
String shortSha(String sha) => sha.length > 7 ? sha.substring(0, 7) : sha;

enum DiffViewMode { diff, unified, full }

/// Which diff to show: a working-tree change, or what a commit, a range or a
/// review of the branch changed.
class DiffTarget {
  /// The repository root the change's path is relative to.
  final String root;
  final GitChange change;
  final bool staged;

  /// A file git does not track yet, diffed against nothing.
  final bool untracked;

  /// The revisions, when this is a committed change: [to] alone is that
  /// commit against its parent.
  final String? from;
  final String? to;

  /// Diff from where [from] and [to] forked — the review of a branch.
  final bool mergeBase;

  const DiffTarget({
    required this.root,
    required this.change,
    this.staged = false,
    this.untracked = false,
    this.from,
    this.to,
    this.mergeBase = false,
  });

  /// The file is gone, so there is no source to show — only what went.
  bool get deleted => change.status == 'D';

  String get path => '$root/${change.path}';

  @override
  bool operator ==(Object other) =>
      other is DiffTarget &&
      other.root == root &&
      other.change.path == change.path &&
      other.change.status == change.status &&
      other.staged == staged &&
      other.untracked == untracked &&
      other.from == from &&
      other.to == to &&
      other.mergeBase == mergeBase;

  @override
  int get hashCode => Object.hash(
    root,
    change.path,
    change.status,
    staged,
    untracked,
    from,
    to,
    mergeBase,
  );
}

/// A file's diff, as the Diff side of the file viewer.
///
/// This was the body of the Git screen's diff page (spec 65, B8): the same
/// three views — the changes alone, unified, and the full file with the changes
/// marked — the same line taps, and the same Ask AI. It lives inside the
/// viewer now, behind a Source / Diff toggle, so one file is one screen.
class GitDiffView extends rp.ConsumerStatefulWidget {
  final String hostId;
  final DiffTarget target;
  final String? sessionId;

  const GitDiffView({
    super.key,
    required this.hostId,
    required this.target,
    this.sessionId,
  });

  @override
  rp.ConsumerState<GitDiffView> createState() => _GitDiffViewState();
}

class _GitDiffViewState extends rp.ConsumerState<GitDiffView> {
  GitDiff? _diff;
  FileReadResult? _fullFile;
  bool _loading = true;
  DiffViewMode _mode = DiffViewMode.unified;
  int? _selStart;
  int? _selEnd;

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
    final diff = await ref.read(
      gitDiffProvider(
        GitDiffKey(
          widget.hostId,
          widget.target.root,
          widget.target.change.path,
          staged: widget.target.staged,
          from: widget.target.from,
          to: widget.target.to,
          untracked: widget.target.untracked,
          mergeBase: widget.target.mergeBase,
        ),
      ).future,
    );
    if (!mounted) return;
    setState(() {
      _diff = diff;
      _loading = false;
    });
    // Preload full file for full-file mode. Only for the working tree: on disk
    // is the current file, which is not what an old commit changed — and a
    // deleted file has none.
    if (_atRevision || widget.target.deleted) return;
    final fullPath = '${widget.target.root}/${widget.target.change.path}';
    final FileReadResult? file;
    try {
      file = await ref.read(readFileProvider((widget.hostId, fullPath)).future);
    } on FileError {
      // A deleted file has no full view; the diff above is still worth it.
      return;
    }
    if (mounted && file != null) {
      setState(() => _fullFile = file);
    }
  }

  bool get _atRevision =>
      widget.target.to != null && widget.target.to!.isNotEmpty;

  void _copyDiff() {
    final diff = _diff;
    if (diff == null) return;
    Clipboard.setData(ClipboardData(text: diff.diff));
    HapticFeedback.lightImpact();
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('Diff copied'),
        duration: Duration(seconds: 1),
        behavior: SnackBarBehavior.floating,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final stat = [
      if (_diff?.stat.isNotEmpty == true) _diff!.stat,
      if (_atRevision) 'at ${shortSha(widget.target.to!)}',
    ].join(' · ');
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 4, 4, 4),
          child: Row(
            children: [
              SegmentedButton<DiffViewMode>(
                segments: [
                  const ButtonSegment(
                    value: DiffViewMode.diff,
                    label: Text('Changes', style: TextStyle(fontSize: 12)),
                  ),
                  const ButtonSegment(
                    value: DiffViewMode.unified,
                    label: Text('Unified', style: TextStyle(fontSize: 12)),
                  ),
                  // The file on disk is the current one, so "full" only makes
                  // sense for a working-tree diff.
                  if (!_atRevision && !widget.target.deleted)
                    const ButtonSegment(
                      value: DiffViewMode.full,
                      label: Text('Full', style: TextStyle(fontSize: 12)),
                    ),
                ],
                selected: {_mode},
                onSelectionChanged: (s) => setState(() => _mode = s.first),
                showSelectedIcon: false,
                style: const ButtonStyle(visualDensity: VisualDensity.compact),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  stat,
                  style: TextStyle(
                    fontSize: 11,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              if (_diff != null)
                IconButton(
                  icon: const Icon(Icons.copy, size: 20),
                  tooltip: 'Copy diff',
                  onPressed: _copyDiff,
                ),
            ],
          ),
        ),
        Expanded(
          child: _loading
              ? const _DiffSkeleton()
              : _diff == null
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
                      const Text('Failed to load diff'),
                    ],
                  ),
                )
              : _buildDiffView(theme),
        ),
        if (widget.sessionId != null) _buildAskAIBar(theme),
      ],
    );
  }

  /// The same bar the file viewer has under its source, so the two sides of
  /// the toggle offer the same things in the same place.
  Widget _buildAskAIBar(ThemeData theme) {
    return Container(
      padding: EdgeInsets.only(
        left: 12,
        right: 8,
        top: 4,
        bottom: MediaQuery.of(context).padding.bottom + 4,
      ),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        border: Border(
          top: BorderSide(color: theme.colorScheme.outlineVariant),
        ),
      ),
      child: Row(
        children: [
          if (_hasSelection) ...[
            Expanded(
              child: Text(
                '$_selLabel · $_selCount ${_selCount == 1 ? 'line' : 'lines'}',
                style: TextStyle(
                  fontSize: 12,
                  fontFamily: 'monospace',
                  color: theme.colorScheme.onSurface,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
            IconButton(
              icon: const Icon(Icons.close, size: 20),
              tooltip: 'Clear selection',
              onPressed: () => setState(() {
                _selStart = null;
                _selEnd = null;
              }),
            ),
          ] else ...[
            Icon(Icons.code, size: 20, color: theme.colorScheme.primary),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                'Tap lines to select',
                style: TextStyle(
                  fontSize: 12,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ],
          FilledButton.tonalIcon(
            onPressed: () => _showAskAISheet(theme),
            icon: const Icon(Icons.auto_awesome, size: 18),
            label: const Text('Ask AI', style: TextStyle(fontSize: 12)),
          ),
        ],
      ),
    );
  }

  void _showAskAISheet(ThemeData theme) {
    final navigator = Navigator.of(context);
    showAskAISheet(
      context,
      label: _hasSelection
          ? '${widget.target.change.fileName}:$_selLabel'
          : widget.target.change.path,
      hint: _hasSelection ? 'Ask about this code...' : 'Ask about this diff...',
      send: _sendAskAI,
      onSent: () => popToSession(navigator),
    );
  }

  /// Null when the agent took the question, or the reason it did not.
  Future<String?> _sendAskAI(String question) async {
    final svc = ref.read(serviceProvider(widget.hostId));
    if (svc == null || widget.sessionId == null) return 'Host not connected';

    // Say which commit, so the agent looks at the same thing you are.
    final at = !_atRevision
        ? ''
        : widget.target.from == null || widget.target.from!.isEmpty
        ? ' at ${shortSha(widget.target.to!)}'
        : ' between ${shortSha(widget.target.from!)} and ${shortSha(widget.target.to!)}';

    String prompt;
    if (_hasSelection) {
      final allLines = _parseDiff(_diff!.diff);
      final start = (_selStart ?? 0).clamp(0, allLines.length);
      final end = ((_selEnd ?? _selStart ?? 0) + 1).clamp(0, allLines.length);
      final selectedTexts = allLines
          .sublist(start, end)
          .map((l) {
            final prefix = l.type == _DiffLineType.added
                ? '+'
                : l.type == _DiffLineType.removed
                ? '-'
                : ' ';
            return '$prefix${l.text}';
          })
          .join('\n');
      final ext = _diff?.language ?? '';
      prompt =
          'Regarding diff of `${widget.target.change.path}`$at $_selLabel:\n```$ext\n$selectedTexts\n```\n${question.trim()}';
    } else {
      prompt =
          'Regarding diff of `${widget.target.change.path}`$at:\n${question.trim()}';
    }

    return svc.sendSessionPrompt(widget.sessionId!, prompt);
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
    final filtered = lines
        .where((l) => l.type != _DiffLineType.context)
        .toList();
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
        child: Text(
          'No changes',
          style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
        ),
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
      highlightedLines = _highlightLines(
        codeTexts.join('\n'),
        language,
        isDark,
      );
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
        if (highlightedLines != null &&
            codeIdx >= 0 &&
            codeIdx < highlightedLines.length) {
          textWidget = RichText(
            text: TextSpan(
              children: [
                TextSpan(
                  text: '$prefix ',
                  style: TextStyle(
                    fontSize: 12,
                    fontFamily: 'monospace',
                    color: prefixColor,
                    fontWeight: FontWeight.w700,
                  ),
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
                  style: TextStyle(
                    fontSize: 12,
                    fontFamily: 'monospace',
                    color: prefixColor,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                TextSpan(
                  text: line.text,
                  style: TextStyle(
                    fontSize: 12,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.onSurface,
                  ),
                ),
              ],
            ),
          );
        }

        final selected = _isLineSelected(i);
        final selectedBg = isDark
            ? const Color(0xFF1A3A5C)
            : const Color(0xFFD4E8FC);

        return GestureDetector(
          onTap: widget.sessionId != null ? () => _onLineTap(i) : null,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
            decoration: BoxDecoration(
              color: selected ? selectedBg : bgColor,
              border: selected
                  ? Border(
                      left: BorderSide(
                        color: theme.colorScheme.primary,
                        width: 3,
                      ),
                    )
                  : null,
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
    // A binary change has no lines to lay a diff over. Since the read asks for
    // encoding=auto the content is now base64 rather than mojibake, which would
    // otherwise be printed as if it were the file.
    if (_fullFile!.isBinary) {
      return _buildChangesOnly(theme);
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
        scrollController.animateTo(
          offset,
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        );
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
            ? (isDark
                  ? Colors.green.withValues(alpha: 0.12)
                  : Colors.green.withValues(alpha: 0.08))
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
            style: TextStyle(
              fontSize: 12,
              fontFamily: 'monospace',
              color: theme.colorScheme.onSurface,
            ),
            overflow: TextOverflow.clip,
            maxLines: 1,
          );
        }

        final selected = _isLineSelected(i);
        final selectedBg = isDark
            ? const Color(0xFF1A3A5C)
            : const Color(0xFFD4E8FC);

        return GestureDetector(
          onTap: widget.sessionId != null ? () => _onLineTap(i) : null,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            decoration: BoxDecoration(
              color: selected ? selectedBg : bgColor,
              border: selected
                  ? Border(
                      left: BorderSide(
                        color: theme.colorScheme.primary,
                        width: 3,
                      ),
                    )
                  : isChanged
                  ? Border(
                      left: BorderSide(color: Colors.green.shade400, width: 3),
                    )
                  : null,
            ),
            child: Row(
              children: [
                SizedBox(
                  width: 36,
                  child: Text(
                    '$lineNum',
                    style: TextStyle(
                      fontSize: 11,
                      fontFamily: 'monospace',
                      color: selected
                          ? theme.colorScheme.primary
                          : theme.colorScheme.onSurfaceVariant.withValues(
                              alpha: 0.5,
                            ),
                    ),
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
        return isDark
            ? Colors.green.withValues(alpha: 0.12)
            : Colors.green.withValues(alpha: 0.08);
      case _DiffLineType.removed:
        return isDark
            ? Colors.red.withValues(alpha: 0.12)
            : Colors.red.withValues(alpha: 0.08);
      default:
        return null;
    }
  }

  /// Syntax-highlights code and splits into per-line TextSpan lists.
  List<List<TextSpan>> _highlightLines(
    String code,
    String language,
    bool isDark,
  ) {
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
      return code
          .split('\n')
          .map((l) => [TextSpan(text: l, style: defaultStyle)])
          .toList();
    }
  }

  void _buildSpans(
    List<dynamic> nodes,
    Map<String, TextStyle> themeMap,
    TextStyle defaultStyle,
    List<TextSpan> out,
  ) {
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
        _buildSpans(
          node.children as List<dynamic>,
          themeMap,
          defaultStyle,
          out,
        );
      }
    }
  }

  String? _langForExt(String ext) {
    switch (ext) {
      case 'dart':
        return 'dart';
      case 'go':
        return 'go';
      case 'py':
        return 'python';
      case 'js':
        return 'javascript';
      case 'ts':
      case 'tsx':
        return 'typescript';
      case 'jsx':
        return 'javascript';
      case 'java':
        return 'java';
      case 'kt':
        return 'kotlin';
      case 'swift':
        return 'swift';
      case 'rs':
        return 'rust';
      case 'c':
      case 'h':
        return 'c';
      case 'cpp':
        return 'cpp';
      case 'cs':
        return 'cs';
      case 'rb':
        return 'ruby';
      case 'sh':
      case 'bash':
      case 'zsh':
        return 'bash';
      case 'json':
        return 'json';
      case 'yaml':
      case 'yml':
        return 'yaml';
      case 'toml':
        return 'ini';
      case 'xml':
        return 'xml';
      case 'html':
        return 'html';
      case 'css':
        return 'css';
      case 'scss':
        return 'scss';
      case 'sql':
        return 'sql';
      case 'md':
        return 'markdown';
      default:
        return null;
    }
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
