import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:highlight/highlight.dart' show highlight;
import 'package:provider/provider.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';
import '../widgets/skeleton.dart';

class FileBrowserScreen extends StatefulWidget {
  final String hostId;
  final String rootPath;
  /// If set, the file viewer opens immediately for this path.
  final String? openFilePath;
  final String? sessionId;

  const FileBrowserScreen({
    super.key,
    required this.hostId,
    required this.rootPath,
    this.openFilePath,
    this.sessionId,
  });

  @override
  State<FileBrowserScreen> createState() => _FileBrowserScreenState();
}

class _FileBrowserScreenState extends State<FileBrowserScreen> {
  late String _currentPath;
  final List<String> _history = [];
  FileListing? _listing;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _currentPath = widget.rootPath;
    _load(_currentPath);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // Open a specific file immediately if requested (e.g. tapped from transcript).
    if (widget.openFilePath != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _openFile(widget.openFilePath!);
      });
    }
  }

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

  Future<void> _load(String path) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final svc = _svc;
    if (svc == null) {
      setState(() {
        _error = 'Host not connected';
        _loading = false;
      });
      return;
    }
    final listing = await svc.listFiles(path);
    if (!mounted) return;
    if (listing == null) {
      setState(() {
        _error = 'Failed to load directory';
        _loading = false;
      });
    } else {
      setState(() {
        _listing = listing;
        _currentPath = listing.path;
        _loading = false;
      });
    }
  }

  void _navigateTo(String path) {
    _history.add(_currentPath);
    _load(path);
  }

  bool _onBack() {
    if (_history.isNotEmpty) {
      final prev = _history.removeLast();
      _load(prev);
      return false; // handled — don't pop screen
    }
    return true; // let Navigator pop
  }

  Future<void> _openFile(String path) async {
    if (!mounted) return;
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => FileViewerScreen(path: path, hostId: widget.hostId, sessionId: widget.sessionId),
      ),
    );
  }

  List<_BreadcrumbSegment> _buildBreadcrumbs() {
    final parts = _currentPath.split('/').where((p) => p.isNotEmpty).toList();
    final segments = <_BreadcrumbSegment>[];
    segments.add(_BreadcrumbSegment(label: '/', path: '/'));
    String accumulated = '';
    for (final part in parts) {
      accumulated += '/$part';
      segments.add(_BreadcrumbSegment(label: part, path: accumulated));
    }
    return segments;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final crumbs = _buildBreadcrumbs();

    return PopScope(
      canPop: _history.isEmpty,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _onBack();
      },
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Files'),
          actions: [
            IconButton(
              icon: const Icon(Icons.chat_bubble_outline),
              tooltip: 'Back to chat',
              onPressed: () => Navigator.of(context).popUntil(
                (route) => route.settings.name != '/file-browser',
              ),
            ),
          ],
          bottom: PreferredSize(
            preferredSize: const Size.fromHeight(36),
            child: SizedBox(
              height: 36,
              child: ListView.separated(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
                itemCount: crumbs.length,
                separatorBuilder: (_, i) => Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 2),
                  child: Icon(
                    Icons.chevron_right,
                    size: 16,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
                itemBuilder: (ctx, i) {
                  final crumb = crumbs[i];
                  final isLast = i == crumbs.length - 1;
                  return GestureDetector(
                    onTap: isLast ? null : () => _navigateTo(crumb.path),
                    child: Text(
                      crumb.label,
                      style: TextStyle(
                        fontSize: 12,
                        fontFamily: 'monospace',
                        color: isLast
                            ? theme.colorScheme.onSurface
                            : theme.colorScheme.primary,
                        fontWeight: isLast ? FontWeight.w600 : FontWeight.normal,
                      ),
                    ),
                  );
                },
              ),
            ),
          ),
        ),
        body: _buildBody(theme),
      ),
    );
  }

  Widget _buildBody(ThemeData theme) {
    if (_loading) {
      return const _FileListSkeleton();
    }
    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
            const SizedBox(height: 12),
            Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
            const SizedBox(height: 16),
            FilledButton(onPressed: () => _load(_currentPath), child: const Text('Retry')),
          ],
        ),
      );
    }
    final entries = _listing?.entries ?? [];
    if (entries.isEmpty) {
      return Center(
        child: Text(
          'Empty directory',
          style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
        ),
      );
    }
    return ListView.builder(
      itemCount: entries.length,
      itemBuilder: (ctx, i) => _EntryTile(
        entry: entries[i],
        onTap: entries[i].isDir
            ? () => _navigateTo(entries[i].path)
            : () => _openFile(entries[i].path),
      ),
    );
  }
}

class _BreadcrumbSegment {
  final String label;
  final String path;
  _BreadcrumbSegment({required this.label, required this.path});
}

class _EntryTile extends StatelessWidget {
  final FileEntry entry;
  final VoidCallback onTap;

  const _EntryTile({required this.entry, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return ListTile(
      leading: Icon(
        entry.isDir ? Icons.folder : _iconForFile(entry.name),
        color: entry.isDir ? Colors.amber.shade700 : theme.colorScheme.onSurfaceVariant,
      ),
      title: Text(
        entry.name,
        style: const TextStyle(fontSize: 14, fontFamily: 'monospace'),
      ),
      trailing: entry.isDir
          ? Icon(Icons.chevron_right, color: theme.colorScheme.onSurfaceVariant)
          : Text(
              entry.formattedSize,
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
            ),
      onTap: onTap,
      dense: true,
    );
  }

  IconData _iconForFile(String name) {
    final ext = name.contains('.') ? name.split('.').last.toLowerCase() : '';
    switch (ext) {
      case 'dart':
      case 'go':
      case 'py':
      case 'js':
      case 'ts':
      case 'tsx':
      case 'jsx':
      case 'java':
      case 'kt':
      case 'swift':
      case 'rs':
      case 'c':
      case 'cpp':
      case 'h':
        return Icons.code;
      case 'md':
      case 'txt':
      case 'rst':
        return Icons.description;
      case 'json':
      case 'yaml':
      case 'yml':
      case 'toml':
      case 'xml':
        return Icons.data_object;
      case 'png':
      case 'jpg':
      case 'jpeg':
      case 'gif':
      case 'svg':
      case 'webp':
        return Icons.image;
      case 'pdf':
        return Icons.picture_as_pdf;
      default:
        return Icons.insert_drive_file;
    }
  }
}

// ==================== File Viewer Screen ====================

class FileViewerScreen extends StatefulWidget {
  final String path;
  final String hostId;
  final String? sessionId;

  const FileViewerScreen({super.key, required this.path, required this.hostId, this.sessionId});

  @override
  State<FileViewerScreen> createState() => _FileViewerScreenState();
}

class _FileViewerScreenState extends State<FileViewerScreen> {
  FileReadResult? _result;
  bool _loading = true;
  bool _userConfirmedLarge = false;
  static const _softLimit = 1024 * 1024; // 1 MB
  int? _selStart; // 1-based line number
  int? _selEnd;   // 1-based line number

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

  void _onLineTap(int lineNum) {
    setState(() {
      if (_selStart == null) {
        _selStart = lineNum;
        _selEnd = null;
      } else if (_selEnd == null && lineNum == _selStart) {
        _selStart = null; // clear
      } else if (_selEnd == null) {
        // Extend to range
        final a = _selStart!;
        _selStart = a < lineNum ? a : lineNum;
        _selEnd = a < lineNum ? lineNum : a;
      } else {
        // New selection
        _selStart = lineNum;
        _selEnd = null;
      }
    });
  }

  bool _isLineSelected(int lineNum) {
    if (_selStart == null) return false;
    if (_selEnd == null) return lineNum == _selStart;
    return lineNum >= _selStart! && lineNum <= _selEnd!;
  }

  @override
  void initState() {
    super.initState();
    _loadFile();
  }

  Future<void> _loadFile() async {
    setState(() => _loading = true);
    final svc = _svc;
    if (svc == null) {
      setState(() => _loading = false);
      return;
    }
    final result = await svc.readFile(widget.path);
    if (!mounted) return;
    if (result != null && result.isDirectory) {
      // Replace this screen with a directory browser at that path.
      Navigator.of(context).pushReplacement(
        MaterialPageRoute(
          settings: const RouteSettings(name: '/file-browser'),
          builder: (_) => FileBrowserScreen(
            hostId: widget.hostId,
            rootPath: widget.path,
          ),
        ),
      );
      return;
    }
    setState(() {
      _result = result;
      _loading = false;
    });
  }

  String get _fileName => widget.path.split('/').last;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(_fileName, style: const TextStyle(fontSize: 15), overflow: TextOverflow.ellipsis),
            if (_result != null)
              Text(
                _result!.formattedSize,
                style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
              ),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.chat_bubble_outline),
            tooltip: 'Back to chat',
            onPressed: () {
              final nav = Navigator.of(context);
              nav.pop();
              if (nav.canPop()) nav.pop();
            },
          ),
          if (_result?.content != null && !_result!.isBinary)
            IconButton(
              icon: const Icon(Icons.copy),
              tooltip: 'Copy content',
              onPressed: () {
                Clipboard.setData(ClipboardData(text: _result!.content!));
                HapticFeedback.lightImpact();
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text('Copied to clipboard'),
                    duration: Duration(seconds: 1),
                    behavior: SnackBarBehavior.floating,
                  ),
                );
              },
            ),
        ],
      ),
      body: _buildContent(theme),
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
    final label = _hasSelection ? '$_fileName:$_selLabel' : widget.path;
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
                      hintText: _hasSelection ? 'Ask about this code...' : 'Ask about this file...',
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

    String prompt;
    if (_hasSelection) {
      final content = _result?.content ?? '';
      final lines = content.split('\n');
      final start = (_selStart ?? 1) - 1;
      final end = (_selEnd ?? _selStart ?? 1);
      final selected = lines.sublist(start.clamp(0, lines.length), end.clamp(0, lines.length));
      final ext = _fileName.contains('.') ? _fileName.split('.').last : '';
      prompt = 'Regarding `${widget.path}` $_selLabel:\n```$ext\n${selected.join('\n')}\n```\n${question.trim()}';
    } else {
      prompt = 'Regarding file `${widget.path}`:\n${question.trim()}';
    }

    // Send first, then navigate
    final nav = Navigator.of(context);
    Navigator.pop(ctx); // close sheet
    await svc.sendSessionPrompt(widget.sessionId!, prompt);
    if (!mounted) return;
    nav.popUntil(
      (route) => route.settings.name != '/file-browser' && route.settings.name != '/git-status',
    );
  }

  Widget _buildContent(ThemeData theme) {
    if (_loading) {
      return const _FileContentSkeleton();
    }
    if (_result == null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline, size: 40, color: theme.colorScheme.error),
            const SizedBox(height: 12),
            const Text('Failed to load file'),
          ],
        ),
      );
    }

    // Server hard limit exceeded
    if (_result!.isTooLarge) {
      return _buildTooLargeView(theme, '${_result!.formattedSize} — exceeds 10 MB server limit');
    }

    // Client soft limit: warn before showing large files
    if (!_userConfirmedLarge && _result!.size > _softLimit) {
      return _buildLargeFileWarning(theme, _result!.formattedSize);
    }

    // Binary detection
    if (_result!.isBinary) {
      return _buildBinaryView(theme);
    }

    final content = _result!.content ?? '';
    final ext = _fileName.contains('.')
        ? _fileName.split('.').last.toLowerCase()
        : '';
    final isDark = theme.brightness == Brightness.dark;

    // Markdown files rendered with MarkdownBody
    if (ext == 'md' || ext == 'markdown') {
      return SingleChildScrollView(
        controller: null,
        padding: const EdgeInsets.all(16),
        child: MarkdownBody(
          data: content,
          selectable: true,
          builders: {'code': _SyntaxHighlightBuilder(isDark: isDark)},
          styleSheet: MarkdownStyleSheet(
            p: TextStyle(fontSize: 14, color: theme.colorScheme.onSurface),
            code: TextStyle(
              fontSize: 12,
              fontFamily: 'monospace',
              color: theme.colorScheme.onSurface,
              backgroundColor: theme.colorScheme.surfaceContainerHigh,
            ),
            codeblockDecoration: BoxDecoration(
              color: isDark ? const Color(0xFF282C34) : const Color(0xFFFAFAFA),
              borderRadius: BorderRadius.circular(8),
            ),
            codeblockPadding: const EdgeInsets.all(0),
            h1: TextStyle(fontSize: 20, fontWeight: FontWeight.bold, color: theme.colorScheme.onSurface),
            h2: TextStyle(fontSize: 18, fontWeight: FontWeight.bold, color: theme.colorScheme.onSurface),
            h3: TextStyle(fontSize: 16, fontWeight: FontWeight.w600, color: theme.colorScheme.onSurface),
            listBullet: TextStyle(fontSize: 14, color: theme.colorScheme.onSurface),
          ),
        ),
      );
    }

    // Code files — syntax highlighted, line-by-line for selection
    final language = _languageForExt(ext);
    final lines = content.split('\n');

    return _buildLineListView(theme, lines, language, isDark);
  }

  Widget _buildLineListView(ThemeData theme, List<String> lines, String? language, bool isDark) {
    // Syntax highlight all lines together for context
    List<List<TextSpan>>? highlightedLines;
    if (language != null) {
      highlightedLines = _highlightCodeLines(lines.join('\n'), language, isDark);
    }

    final selectedBg = isDark ? const Color(0xFF1A3A5C) : const Color(0xFFD4E8FC);
    final gutterColor = theme.colorScheme.onSurfaceVariant.withAlpha(120);

    return ListView.builder(
      itemCount: lines.length,
      itemExtent: 20,
      itemBuilder: (ctx, i) {
        final lineNum = i + 1;
        final selected = _isLineSelected(lineNum);

        Widget textWidget;
        if (highlightedLines != null && i < highlightedLines.length) {
          textWidget = RichText(
            text: TextSpan(children: highlightedLines[i]),
            maxLines: 1,
            overflow: TextOverflow.clip,
          );
        } else {
          textWidget = Text(
            lines[i],
            style: TextStyle(fontSize: 12, fontFamily: 'monospace', color: theme.colorScheme.onSurface),
            maxLines: 1,
            overflow: TextOverflow.clip,
          );
        }

        return GestureDetector(
          onTap: widget.sessionId != null ? () => _onLineTap(lineNum) : null,
          child: Container(
            color: selected ? selectedBg : null,
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Row(
              children: [
                SizedBox(
                  width: 36,
                  child: Text(
                    '$lineNum',
                    textAlign: TextAlign.right,
                    style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: selected ? theme.colorScheme.primary : gutterColor),
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

  List<List<TextSpan>> _highlightCodeLines(String code, String language, bool isDark) {
    final themeMap = isDark ? atomOneDarkTheme : atomOneLightTheme;
    final defaultStyle = TextStyle(
      fontSize: 12, fontFamily: 'monospace',
      color: isDark ? Colors.white70 : Colors.black87,
    );
    try {
      final result = highlight.parse(code, language: language);
      final allSpans = <TextSpan>[];
      _buildHighlightSpans(result.nodes!, themeMap, defaultStyle, allSpans);
      final lines = <List<TextSpan>>[[]];
      for (final span in allSpans) {
        final text = span.text ?? '';
        if (!text.contains('\n')) {
          lines.last.add(span);
          continue;
        }
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
      return code.split('\n').map((l) => [TextSpan(text: l, style: defaultStyle)]).toList();
    }
  }

  void _buildHighlightSpans(List<dynamic> nodes, Map<String, TextStyle> themeMap, TextStyle defaultStyle, List<TextSpan> out) {
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
        _buildHighlightSpans(node.children as List<dynamic>, themeMap, defaultStyle, out);
      }
    }
  }

  String? _languageForExt(String ext) {
    switch (ext) {
      case 'dart': return 'dart';
      case 'go': return 'go';
      case 'py': return 'python';
      case 'js': return 'javascript';
      case 'ts': return 'typescript';
      case 'tsx': return 'typescript';
      case 'jsx': return 'javascript';
      case 'java': return 'java';
      case 'kt': return 'kotlin';
      case 'swift': return 'swift';
      case 'rs': return 'rust';
      case 'c': return 'c';
      case 'cpp': return 'cpp';
      case 'h': return 'cpp';
      case 'cs': return 'cs';
      case 'rb': return 'ruby';
      case 'sh': return 'bash';
      case 'bash': return 'bash';
      case 'zsh': return 'bash';
      case 'json': return 'json';
      case 'yaml': return 'yaml';
      case 'yml': return 'yaml';
      case 'toml': return 'ini';
      case 'xml': return 'xml';
      case 'html': return 'html';
      case 'css': return 'css';
      case 'scss': return 'scss';
      case 'sql': return 'sql';
      case 'dockerfile': return 'dockerfile';
      case 'tf': return 'hcl';
      case 'proto': return 'protobuf';
      default: return null;
    }
  }

  Widget _buildLargeFileWarning(ThemeData theme, String size) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.warning_amber, size: 48, color: Colors.amber.shade700),
            const SizedBox(height: 16),
            Text(
              'File is large ($size)',
              style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              'Loading may be slow or cause performance issues.',
              style: TextStyle(fontSize: 13, color: theme.colorScheme.onSurfaceVariant),
              textAlign: TextAlign.center,
            ),
            const SizedBox(height: 24),
            FilledButton(
              onPressed: () => setState(() => _userConfirmedLarge = true),
              child: const Text('Load anyway'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildTooLargeView(ThemeData theme, String detail) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.block, size: 48, color: theme.colorScheme.error),
            const SizedBox(height: 16),
            const Text(
              'File too large',
              style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              detail,
              style: TextStyle(fontSize: 13, color: theme.colorScheme.onSurfaceVariant),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildBinaryView(ThemeData theme) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.do_not_disturb, size: 48, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(height: 16),
            const Text(
              'Binary file',
              style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              '${_result!.formattedSize} — cannot display',
              style: TextStyle(fontSize: 13, color: theme.colorScheme.onSurfaceVariant),
            ),
          ],
        ),
      ),
    );
  }
}

// ==================== Skeletons ====================

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
                const Skeleton(width: 24, height: 24, borderRadius: BorderRadius.all(Radius.circular(4))),
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

class _FileContentSkeleton extends StatelessWidget {
  const _FileContentSkeleton();

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (int i = 0; i < 20; i++) ...[
            Skeleton(
              width: 40.0 + (i * 47 % 250),
              height: 14,
              borderRadius: const BorderRadius.all(Radius.circular(2)),
            ),
            const SizedBox(height: 4),
          ],
        ],
      ),
    );
  }
}

// ==================== Markdown code block syntax highlighter ====================

class _SyntaxHighlightBuilder extends MarkdownElementBuilder {
  final bool isDark;
  _SyntaxHighlightBuilder({required this.isDark});

  @override
  Widget? visitElementAfterWithContext(
    BuildContext context,
    dynamic element,
    TextStyle? preferredStyle,
    TextStyle? parentStyle,
  ) {
    final code = element.textContent as String? ?? '';
    final rawLang = (element.attributes['class'] as String? ?? '');

    // Inline code — no class, no newlines — let markdown render it normally.
    if (rawLang.isEmpty && !code.contains('\n')) return null;

    final lang = rawLang.replaceFirst('language-', '');
    return HighlightView(
      code.trimRight(),
      language: lang.isNotEmpty ? lang : 'plaintext',
      theme: isDark ? atomOneDarkTheme : atomOneLightTheme,
      padding: const EdgeInsets.all(12),
      textStyle: const TextStyle(fontSize: 12, fontFamily: 'monospace', height: 1.5),
    );
  }
}
