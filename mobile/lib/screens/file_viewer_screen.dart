import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_svg/flutter_svg.dart';

import '../providers/daemon_providers.dart';
import '../providers/file_view_prefs.dart';
import '../services/daemon_api_service.dart';
import '../utils/file_types.dart';
import '../widgets/ask_ai_sheet.dart';
import '../widgets/code_view.dart';
import '../widgets/git_diff_view.dart';
import '../widgets/html_preview.dart';
import '../widgets/markdown_view.dart';
import '../widgets/skeleton.dart';
import 'file_browser_screen.dart';
import 'routes.dart';

class FileViewerScreen extends rp.ConsumerStatefulWidget {
  final String path;
  final String hostId;
  final String? sessionId;

  /// The checkout this file belongs to. A preview reads nothing outside it, and
  /// "show in folder" browses from it. Absent means the file's own directory.
  final String? rootPath;

  /// The line to open at — the one the agent last edited, from the Changes
  /// view. It is picked as well as shown, so it stands out.
  final int? line;

  /// For a markdown file: the heading a link in another file pointed at.
  final String? anchor;

  /// The change this file has, when it was opened from the Changes view or a
  /// commit (spec 65, B8). The viewer then opens on the diff, with a Source /
  /// Diff toggle — or on the diff alone, for a file that was deleted.
  final DiffTarget? diff;

  const FileViewerScreen({
    super.key,
    required this.path,
    required this.hostId,
    this.sessionId,
    this.rootPath,
    this.line,
    this.anchor,
    this.diff,
  });

  @override
  rp.ConsumerState<FileViewerScreen> createState() => _FileViewerScreenState();
}

class _FileViewerScreenState extends rp.ConsumerState<FileViewerScreen>
    with WidgetsBindingObserver {
  /// The last value `build` saw.
  ///
  /// Held in a field so the callbacks below — copy, ask-AI — read the same
  /// answer the frame was drawn from. Watching from inside a callback would
  /// subscribe outside a build, which Riverpod rejects.
  rp.AsyncValue<FileReadResult?> _file = const rp.AsyncLoading();

  /// An HTML file opens rendered, and can be turned back into source.
  bool _htmlPreview = true;

  /// An SVG opens drawn, and can be turned into its source.
  bool _svgSource = false;

  /// Whether this page's scripts may run.
  ///
  /// Off every time a file is opened, and never remembered. Turning it on is a
  /// decision about one page an agent wrote, not a setting to carry into the
  /// next one without being asked again.
  bool _runScripts = false;

  bool _userConfirmedLarge = false;
  static const _softLimit = 1024 * 1024; // 1 MB

  /// The lines picked — by a tap when Ask AI is on offer, or by a long press.
  LineSelection? _selection;

  final _code = CodeViewController();

  FileReadResult? get _result => _file.valueOrNull;

  /// Whether there is nothing to show yet.
  ///
  /// Not a bare `isLoading`: an agent editing this file invalidates the entry,
  /// which reports loading while still holding the previous value. Drawing the
  /// skeleton then would replace the text the reader is part-way through with
  /// grey bars, and put it back a moment later. Same guard as the transcript's
  /// (spec 52, `session_detail_screen.dart:460`).
  bool get _loading => _file.isLoading && !_file.hasValue;

  (String, String) get _fileKey => (widget.hostId, widget.path);

  /// Whether the read that [didChangeDependencies] owes the first build is done.
  bool _readOnOpen = false;

  /// Whether the diff is on screen rather than the source.
  late bool _showDiff = widget.diff != null;

  /// A deleted file has no source to read, so there is nothing to toggle to.
  bool get _diffOnly => widget.diff?.deleted ?? false;

  @override
  void initState() {
    super.initState();
    final line = widget.line;
    if (line != null && line > 0) _selection = LineSelection(line, line);
    WidgetsBinding.instance.addObserver(this);
  }

  /// Why the read waits for dependencies, and why it happens at all: see
  /// `_FileBrowserScreenState.didChangeDependencies`. The entry may still be
  /// held from a moment ago; the read then carries its ETag, and an unchanged
  /// file costs a 304.
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_readOnOpen || _diffOnly) return;
    _readOnOpen = true;
    ref.invalidate(readFileProvider(_fileKey));
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && !_diffOnly) {
      ref.invalidate(readFileProvider(_fileKey));
    }
  }

  /// Pull to refresh: read again, and keep the spinner up until the answer.
  Future<void> _refresh() async {
    ref.invalidate(readFileProvider(_fileKey));
    try {
      await ref.read(readFileProvider(_fileKey).future);
    } on FileError {
      // The screen shows the error itself; the spinner only has to stop.
    }
  }

  /// What a preview may read from, and what "show in folder" browses.
  String get _root => widget.rootPath ?? _directory;

  String get _directory =>
      widget.path.substring(0, widget.path.lastIndexOf('/'));

  String get _fileName => widget.path.split('/').last;

  bool get _isMarkdown => isMarkdownPath(widget.path);

  /// The path as a reader would write it: from the checkout when the file is
  /// in it, so `lib/main.dart:12-20` rather than a home directory first.
  String get _displayPath {
    final root = widget.rootPath;
    if (root != null && widget.path.startsWith('$root/')) {
      return widget.path.substring(root.length + 1);
    }
    return widget.path;
  }

  /// Whether the file is on screen as lines of text, rather than a picture, a
  /// rendered page or rendered markdown.
  bool get _showsLines {
    if (_showDiff) return false;
    final file = _result;
    if (file == null || file.content == null) return false;
    if (file.isTooLarge || file.isBinary) return false;
    if (!_userConfirmedLarge && file.size > _softLimit) return false;
    if (isHtmlPath(widget.path) && _htmlPreview) return false;
    if (file.isSvg && !_svgSource) return false;
    return !_isMarkdown;
  }

  /// Hands the directory back to the listing underneath, if there is one, so
  /// the stack does not grow a second browser every time. The listing records
  /// it as a step, so Back from the folder comes back to this file.
  void _showInFolder() {
    final nav = Navigator.of(context);
    if (nav.canPop() &&
        ModalRoute.of(context)?.settings.name == fileViewerRoute) {
      nav.pop(_directory);
      return;
    }
    nav.push(
      MaterialPageRoute(
        settings: const RouteSettings(name: fileBrowserRoute),
        builder: (_) => FileBrowserScreen(
          hostId: widget.hostId,
          rootPath: _root,
          startPath: _directory,
          sessionId: widget.sessionId,
        ),
      ),
    );
  }

  /// A path that turns out to be a directory replaces this screen with a
  /// browser rooted there.
  ///
  /// Driven from a listener rather than from the read itself: navigation is a
  /// side effect, and a provider that navigated while producing its value
  /// would do it again on every refresh.
  void _redirectIfDirectory(rp.AsyncValue<FileReadResult?> file) {
    if (file.valueOrNull?.isDirectory != true) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      Navigator.of(context).pushReplacement(
        MaterialPageRoute(
          settings: const RouteSettings(name: fileBrowserRoute),
          builder: (_) => FileBrowserScreen(
            hostId: widget.hostId,
            rootPath: widget.rootPath ?? widget.path,
            startPath: widget.path,
            sessionId: widget.sessionId,
          ),
        ),
      );
    });
  }

  void _openLinked(String path, String? anchor) {
    Navigator.of(context).push(
      MaterialPageRoute(
        settings: const RouteSettings(name: fileViewerRoute),
        builder: (_) => FileViewerScreen(
          hostId: widget.hostId,
          path: path,
          sessionId: widget.sessionId,
          rootPath: widget.rootPath,
          anchor: anchor,
        ),
      ),
    );
  }

  void _toast(String message) {
    HapticFeedback.lightImpact();
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        duration: const Duration(seconds: 1),
        behavior: SnackBarBehavior.floating,
      ),
    );
  }

  List<String> get _selectedLines {
    final selection = _selection;
    final content = _result?.content;
    if (selection == null || content == null) return const [];
    final lines = content.split('\n');
    final start = (selection.start - 1).clamp(0, lines.length);
    final end = selection.end.clamp(0, lines.length);
    return lines.sublist(start, end);
  }

  void _copySelection({required bool withPath}) {
    final selection = _selection;
    if (selection == null) return;
    final text = _selectedLines.join('\n');
    Clipboard.setData(
      ClipboardData(
        text: withPath ? '$_displayPath:${selection.range}\n$text' : text,
      ),
    );
    _toast(
      withPath
          ? 'Copied $_displayPath:${selection.range}'
          : 'Copied ${selection.count} ${selection.count == 1 ? 'line' : 'lines'}',
    );
  }

  Future<void> _goToLine() async {
    final controller = TextEditingController();
    final count = '\n'.allMatches(_result?.content ?? '').length + 1;
    final line = await showDialog<int>(
      context: context,
      builder: (ctx) {
        void submit() =>
            Navigator.of(ctx).pop(int.tryParse(controller.text.trim()));
        return AlertDialog(
          title: const Text('Go to line'),
          content: TextField(
            controller: controller,
            autofocus: true,
            keyboardType: TextInputType.number,
            inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            decoration: InputDecoration(hintText: '1 – $count'),
            onSubmitted: (_) => submit(),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(ctx).pop(),
              child: const Text('Cancel'),
            ),
            FilledButton(onPressed: submit, child: const Text('Go')),
          ],
        );
      },
    );
    controller.dispose();
    if (line == null || line < 1 || !mounted) return;
    final target = line.clamp(1, count);
    setState(() => _selection = LineSelection(target, target));
    await _code.goToLine(target);
  }

  @override
  Widget build(BuildContext context) {
    if (!_diffOnly) {
      _file = ref.watch(readFileProvider(_fileKey));
      ref.listen(
        readFileProvider(_fileKey),
        (_, next) => _redirectIfDirectory(next),
      );
    }
    final source = !_showDiff;
    final theme = Theme.of(context);
    final prefs = ref.watch(fileViewPrefsProvider);
    final result = _result;
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              _fileName,
              style: const TextStyle(fontSize: 15),
              overflow: TextOverflow.ellipsis,
            ),
            if (result != null)
              Text(
                result.formattedSize,
                style: TextStyle(
                  fontSize: 11,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
          ],
        ),
        actions: [
          // Back belongs to wherever the file was opened from — the transcript,
          // or the listing. Getting to the folder is a thing you ask for, so it
          // is a button rather than something the back gesture does for you.
          IconButton(
            icon: const Icon(Icons.folder_open_outlined),
            tooltip: 'Show in folder',
            onPressed: _showInFolder,
          ),
          // Labelled and switched, not an icon: whether an agent's page is
          // executing is not something to leave the reader to infer.
          if (source && isHtmlPath(widget.path) && _htmlPreview)
            _ScriptsSwitch(
              value: _runScripts,
              onChanged: (on) => setState(() => _runScripts = on),
            ),
          if (source && isHtmlPath(widget.path))
            IconButton(
              icon: Icon(_htmlPreview ? Icons.code : Icons.article_outlined),
              tooltip: _htmlPreview ? 'Show source' : 'Render the page',
              onPressed: () => setState(() => _htmlPreview = !_htmlPreview),
            ),
          if (source && result?.isSvg == true)
            IconButton(
              icon: Icon(_svgSource ? Icons.image_outlined : Icons.code),
              tooltip: _svgSource ? 'Draw the picture' : 'Show source',
              onPressed: () => setState(() => _svgSource = !_svgSource),
            ),
          if (_showsLines)
            IconButton(
              icon: const Icon(Icons.format_list_numbered),
              tooltip: 'Go to line',
              onPressed: _goToLine,
            ),
          _buildMenu(prefs),
        ],
        bottom: widget.diff != null && !_diffOnly
            ? _buildSourceDiffToggle()
            : null,
      ),
      body: _showDiff
          ? GitDiffView(
              key: ValueKey(widget.diff),
              hostId: widget.hostId,
              target: widget.diff!,
              sessionId: widget.sessionId,
            )
          : _buildContent(theme, prefs),
      // The diff carries its own bar, with its own selection, inside it.
      bottomNavigationBar: _showDiff ? null : _buildBottomBar(theme),
    );
  }

  /// Source or Diff: two views of one file, so one screen with a switch
  /// rather than a screen for each (spec 65, B8).
  PreferredSizeWidget _buildSourceDiffToggle() {
    return PreferredSize(
      preferredSize: const Size.fromHeight(52),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
        child: SizedBox(
          width: double.infinity,
          child: SegmentedButton<bool>(
            segments: const [
              ButtonSegment(
                value: false,
                icon: Icon(Icons.description_outlined, size: 18),
                label: Text('Source'),
              ),
              ButtonSegment(
                value: true,
                icon: Icon(Icons.difference_outlined, size: 18),
                label: Text('Diff'),
              ),
            ],
            selected: {_showDiff},
            showSelectedIcon: false,
            onSelectionChanged: (s) => setState(() => _showDiff = s.first),
          ),
        ),
      ),
    );
  }

  Widget _buildMenu(FileViewPrefs prefs) {
    final content = _result?.content;
    final canCopy = content != null && _result?.isBinary == false;
    return PopupMenuButton<String>(
      tooltip: 'More',
      onSelected: (action) {
        switch (action) {
          case 'wrap':
            ref.read(fileViewPrefsProvider.notifier).setWrap(!prefs.wrap);
          case 'copy':
            Clipboard.setData(ClipboardData(text: content!));
            _toast('Copied to clipboard');
          case 'path':
            Clipboard.setData(ClipboardData(text: widget.path));
            _toast('Copied ${widget.path}');
        }
      },
      itemBuilder: (_) => [
        if (_showsLines)
          CheckedPopupMenuItem(
            value: 'wrap',
            checked: prefs.wrap,
            child: const Text('Wrap lines'),
          ),
        if (canCopy)
          const PopupMenuItem(value: 'copy', child: Text('Copy content')),
        const PopupMenuItem(value: 'path', child: Text('Copy path')),
      ],
    );
  }

  /// The bar under the text: what is picked and what to do with it.
  ///
  /// Over lines only, or over markdown when there is a session to ask. A
  /// picture and a rendered page have no lines, so the bar would be offering
  /// a gesture that does nothing; markdown has no line taps, so it gets Ask AI
  /// without the hint.
  Widget? _buildBottomBar(ThemeData theme) {
    final hasSession = widget.sessionId != null;
    final selection = _selection;
    if (_showsLines && (hasSession || selection != null)) {
      return _ActionBar(
        children: [
          if (selection != null) ...[
            Expanded(
              child: Text(
                '${selection.label} · ${selection.count} '
                '${selection.count == 1 ? 'line' : 'lines'}',
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
              onPressed: () => setState(() => _selection = null),
            ),
            IconButton(
              icon: const Icon(Icons.copy, size: 20),
              tooltip: 'Copy lines',
              onPressed: () => _copySelection(withPath: false),
            ),
            IconButton(
              icon: const Icon(Icons.link, size: 20),
              tooltip: 'Copy with path and lines',
              onPressed: () => _copySelection(withPath: true),
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
          if (hasSession) _askButton(),
        ],
      );
    }
    final file = _result;
    if (_isMarkdown && hasSession && file?.content != null) {
      return _ActionBar(children: [const Spacer(), _askButton()]);
    }
    return null;
  }

  Widget _askButton() => FilledButton.tonalIcon(
    onPressed: _askAI,
    icon: const Icon(Icons.auto_awesome, size: 18),
    label: const Text('Ask AI', style: TextStyle(fontSize: 12)),
  );

  void _askAI() {
    final navigator = Navigator.of(context);
    final selection = _selection;
    showAskAISheet(
      context,
      label: selection != null ? '$_fileName:${selection.label}' : widget.path,
      hint: selection != null
          ? 'Ask about this code...'
          : 'Ask about this file...',
      send: _sendAskAI,
      onSent: () => popToSession(navigator),
    );
  }

  /// Null when the agent took the question, or the reason it did not.
  Future<String?> _sendAskAI(String question) async {
    final svc = ref.read(serviceProvider(widget.hostId));
    final sessionId = widget.sessionId;
    if (svc == null || sessionId == null) return 'Host not connected';
    final selection = _selection;
    final String prompt;
    if (selection != null) {
      final ext = extensionOf(widget.path);
      prompt =
          'Regarding `${widget.path}` ${selection.label}:\n'
          '```$ext\n${_selectedLines.join('\n')}\n```\n$question';
    } else {
      prompt = 'Regarding file `${widget.path}`:\n$question';
    }
    return svc.sendSessionPrompt(sessionId, prompt);
  }

  Widget _buildContent(ThemeData theme, FileViewPrefs prefs) {
    if (_loading) return const _FileContentSkeleton();
    final error = _file.error;
    if (error != null && !_file.hasValue) {
      return FileErrorView(
        error: error,
        path: widget.path,
        onRetry: () => ref.invalidate(readFileProvider(_fileKey)),
      );
    }
    final file = _result;
    if (file == null) {
      return FileErrorView(
        error: FileError(FileErrorKind.notFound, widget.path),
        path: widget.path,
        onRetry: () => ref.invalidate(readFileProvider(_fileKey)),
      );
    }

    // Server hard limit exceeded
    if (file.isTooLarge) {
      return _buildTooLargeView(
        theme,
        FileError(
          FileErrorKind.tooLarge,
          widget.path,
          size: file.size,
          limit: 10 * 1024 * 1024,
        ).message,
      );
    }

    // Client soft limit: warn before showing large files
    if (!_userConfirmedLarge && file.size > _softLimit) {
      return _buildLargeFileWarning(theme, file.formattedSize);
    }

    // A picture is worth showing, and is binary, so it goes first.
    final bytes = file.bytes;
    if (file.isImage && bytes != null) {
      return ZoomableImage(
        child: Image.memory(
          bytes,
          fit: BoxFit.contain,
          errorBuilder: (context, error, stack) =>
              _buildBinaryView(theme, file),
        ),
      );
    }

    if (file.isSvg && !_svgSource) {
      return ZoomableImage(
        child: SvgPicture.string(
          file.content!,
          fit: BoxFit.contain,
          errorBuilder: (context, error, stack) => Center(
            child: Text(
              'This SVG could not be drawn. Show the source instead.',
              style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
            ),
          ),
        ),
      );
    }

    if (file.isBinary) return _buildBinaryView(theme, file);

    final content = file.content ?? '';
    if (isHtmlPath(widget.path) && _htmlPreview) {
      return HtmlPreview(
        hostId: widget.hostId,
        html: content,
        path: widget.path,
        root: _root,
        scripts: _runScripts,
      );
    }

    if (_isMarkdown) {
      return MarkdownView(
        data: content,
        hostId: widget.hostId,
        path: widget.path,
        root: _root,
        onOpenFile: _openLinked,
        initialAnchor: widget.anchor,
        onRefresh: _refresh,
      );
    }

    final notifier = ref.read(fileViewPrefsProvider.notifier);
    return CodeView(
      text: content,
      path: widget.path,
      etag: file.etag,
      wrap: prefs.wrap,
      fontSize: prefs.fontSize,
      onFontSizeChanged: notifier.previewFontSize,
      onFontSizeCommitted: notifier.commitFontSize,
      selection: _selection,
      onSelectionChanged: (next) => setState(() => _selection = next),
      tapToSelect: widget.sessionId != null,
      controller: _code,
      onRefresh: _refresh,
      initialLine: widget.line,
    );
  }

  Widget _buildLargeFileWarning(ThemeData theme, String size) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.warning_amber,
              size: 48,
              color: theme.colorScheme.tertiary,
            ),
            const SizedBox(height: 16),
            Text(
              'File is large ($size)',
              style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              'Loading may be slow or cause performance issues.',
              style: TextStyle(
                fontSize: 13,
                color: theme.colorScheme.onSurfaceVariant,
              ),
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
            Text(
              detail,
              style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
              textAlign: TextAlign.center,
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildBinaryView(ThemeData theme, FileReadResult file) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.do_not_disturb,
              size: 48,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(height: 16),
            const Text(
              'Binary file',
              style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              '${file.formattedSize} — cannot display',
              style: TextStyle(
                fontSize: 13,
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The strip under the viewer that holds the selection and its actions.
class _ActionBar extends StatelessWidget {
  final List<Widget> children;

  const _ActionBar({required this.children});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
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
      child: Row(children: children),
    );
  }
}

/// Says what went wrong reading a file or a folder, and offers to try again.
///
/// The words come from [FileError.message] — "Not found", "Permission
/// denied", "Host unreachable" — and the path goes under them, so a chip that
/// named a file which does not exist says which one.
class FileErrorView extends StatelessWidget {
  final Object error;
  final String path;
  final VoidCallback onRetry;

  const FileErrorView({
    super.key,
    required this.error,
    required this.path,
    required this.onRetry,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final message = error is FileError
        ? (error as FileError).message
        : 'Could not read this';
    return LayoutBuilder(
      // Scrollable, so pull to refresh works on an error as well.
      builder: (context, constraints) => SingleChildScrollView(
        physics: const AlwaysScrollableScrollPhysics(),
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: constraints.maxHeight),
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(32),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(
                    Icons.error_outline,
                    size: 40,
                    color: theme.colorScheme.error,
                  ),
                  const SizedBox(height: 12),
                  Text(
                    message,
                    style: TextStyle(
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                      color: theme.colorScheme.error,
                    ),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 6),
                  SelectableText(
                    path,
                    style: TextStyle(
                      fontSize: 12,
                      fontFamily: 'monospace',
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    textAlign: TextAlign.center,
                  ),
                  const SizedBox(height: 16),
                  FilledButton(onPressed: onRetry, child: const Text('Retry')),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// A picture, pinch-zoomable, on a checkerboard so transparency reads as
/// transparency. Double-tap zooms to 2× where you tapped; a second double-tap
/// puts it back.
class ZoomableImage extends StatefulWidget {
  final Widget child;

  const ZoomableImage({super.key, required this.child});

  @override
  State<ZoomableImage> createState() => _ZoomableImageState();
}

class _ZoomableImageState extends State<ZoomableImage> {
  final _transform = TransformationController();
  Offset _tapAt = Offset.zero;

  @override
  void dispose() {
    _transform.dispose();
    super.dispose();
  }

  void _toggleZoom() {
    if (_transform.value.getMaxScaleOnAxis() > 1.01) {
      _transform.value = Matrix4.identity();
      return;
    }
    const scale = 2.0;
    final x = -_tapAt.dx * (scale - 1);
    final y = -_tapAt.dy * (scale - 1);
    _transform.value = Matrix4.identity()
      ..translateByDouble(x, y, 0, 1)
      ..scaleByDouble(scale, scale, 1, 1);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return GestureDetector(
      onDoubleTapDown: (details) => _tapAt = details.localPosition,
      onDoubleTap: _toggleZoom,
      child: CustomPaint(
        painter: _Checkerboard(
          light: theme.colorScheme.surfaceContainerLow,
          dark: theme.colorScheme.surfaceContainerHighest,
        ),
        child: InteractiveViewer(
          transformationController: _transform,
          maxScale: 8,
          child: Center(child: widget.child),
        ),
      ),
    );
  }
}

class _Checkerboard extends CustomPainter {
  final Color light;
  final Color dark;

  const _Checkerboard({required this.light, required this.dark});

  @override
  void paint(Canvas canvas, Size size) {
    const cell = 12.0;
    canvas.drawRect(Offset.zero & size, Paint()..color = light);
    final paint = Paint()..color = dark;
    for (var y = 0.0; y < size.height; y += cell) {
      final odd = (y ~/ cell).isOdd;
      for (var x = odd ? cell : 0.0; x < size.width; x += cell * 2) {
        canvas.drawRect(Rect.fromLTWH(x, y, cell, cell), paint);
      }
    }
  }

  @override
  bool shouldRepaint(_Checkerboard old) =>
      old.light != light || old.dark != dark;
}

/// The switch that lets an agent's page run its own scripts.
///
/// A labelled switch rather than an icon, because "is this page executing"
/// should be readable at a glance rather than decoded from a glyph. Full size,
/// so the whole control is a 48 dp target.
class _ScriptsSwitch extends StatelessWidget {
  final bool value;
  final ValueChanged<bool> onChanged;

  const _ScriptsSwitch({required this.value, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Tooltip(
      message: "Run this page's scripts. It still cannot reach the network.",
      child: InkWell(
        borderRadius: BorderRadius.circular(999),
        onTap: () => onChanged(!value),
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 48),
          child: Padding(
            padding: const EdgeInsets.only(left: 8),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  'JS',
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: value ? FontWeight.w700 : FontWeight.w500,
                    color: value
                        ? theme.colorScheme.primary
                        : theme.colorScheme.onSurfaceVariant,
                  ),
                ),
                Switch(value: value, onChanged: onChanged),
              ],
            ),
          ),
        ),
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
