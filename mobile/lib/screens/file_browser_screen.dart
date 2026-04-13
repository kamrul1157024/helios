import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:provider/provider.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';

class FileBrowserScreen extends StatefulWidget {
  final String hostId;
  final String rootPath;
  /// If set, the file viewer opens immediately for this path.
  final String? openFilePath;

  const FileBrowserScreen({
    super.key,
    required this.hostId,
    required this.rootPath,
    this.openFilePath,
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
        builder: (_) => _FileViewerScreen(path: path, hostId: widget.hostId),
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
              onPressed: () => Navigator.of(context).pop(),
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
      return const Center(child: CircularProgressIndicator());
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

class _FileViewerScreen extends StatefulWidget {
  final String path;
  final String hostId;

  const _FileViewerScreen({required this.path, required this.hostId});

  @override
  State<_FileViewerScreen> createState() => _FileViewerScreenState();
}

class _FileViewerScreenState extends State<_FileViewerScreen> {
  FileReadResult? _result;
  bool _loading = true;
  bool _userConfirmedLarge = false;
  static const _softLimit = 1024 * 1024; // 1 MB

  DaemonAPIService? get _svc =>
      context.read<HostManager>().serviceFor(widget.hostId);

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
              // Pop both file viewer and file browser to return to session detail.
              final nav = Navigator.of(context);
              nav.pop(); // file viewer screen
              if (nav.canPop()) nav.pop(); // file browser screen
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
    );
  }

  Widget _buildContent(ThemeData theme) {
    if (_loading) {
      return const Center(child: CircularProgressIndicator());
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

    // Code files — syntax highlighted
    final language = _languageForExt(ext);
    if (language != null) {
      return SingleChildScrollView(
        controller: null,
        child: HighlightView(
          content,
          language: language,
          theme: isDark ? atomOneDarkTheme : atomOneLightTheme,
          padding: const EdgeInsets.all(16),
          textStyle: const TextStyle(fontSize: 12, fontFamily: 'monospace', height: 1.5),
        ),
      );
    }

    // Plain text fallback
    return SingleChildScrollView(
      controller: null,
      padding: const EdgeInsets.all(16),
      child: SelectableText(
        content,
        style: TextStyle(
          fontSize: 12,
          fontFamily: 'monospace',
          color: theme.colorScheme.onSurface,
          height: 1.5,
        ),
      ),
    );
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
