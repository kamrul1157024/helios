import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import '../models/message.dart';
import '../services/voice_service.dart';
import '../utils/markdown_stripper.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:url_launcher/url_launcher.dart';
import '../screens/file_browser_screen.dart';

class MessageCard extends StatelessWidget {
  final Message message;
  final String hostId;
  final String sessionCwd;

  const MessageCard({
    super.key,
    required this.message,
    this.hostId = '',
    this.sessionCwd = '',
  });

  @override
  Widget build(BuildContext context) {
    switch (message.role) {
      case 'user':
        return _UserMessageCard(message: message);
      case 'assistant':
        return _AssistantMessageCard(
          message: message,
          hostId: hostId,
          sessionCwd: sessionCwd,
        );
      case 'tool_use':
        return _ToolUseCard(message: message);
      case 'tool_result':
        return _ToolResultCard(message: message);
      default:
        return const SizedBox.shrink();
    }
  }
}

class _UserMessageCard extends StatelessWidget {
  final Message message;
  const _UserMessageCard({required this.message});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final content = message.content ?? '';
    return Align(
      alignment: Alignment.centerRight,
      child: GestureDetector(
        onLongPress: () => _copyToClipboard(context, content),
        child: Container(
          constraints: BoxConstraints(maxWidth: MediaQuery.of(context).size.width * 0.8),
          margin: const EdgeInsets.only(bottom: 8),
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
          decoration: BoxDecoration(
            color: theme.colorScheme.primaryContainer,
            borderRadius: const BorderRadius.only(
              topLeft: Radius.circular(16),
              topRight: Radius.circular(16),
              bottomLeft: Radius.circular(16),
              bottomRight: Radius.circular(4),
            ),
          ),
          child: SelectableText(
            content,
            style: TextStyle(
              fontSize: 14,
              color: theme.colorScheme.onPrimaryContainer,
            ),
          ),
        ),
      ),
    );
  }
}

/// Extracts unique file paths from message content for display as tappable chips.
final _filePathPattern = RegExp(
  r'(^|[\s:,;(`])(~/[a-zA-Z0-9_.-]+(?:/[a-zA-Z0-9_.-]+)+|/[a-zA-Z0-9_.-]+(?:/[a-zA-Z0-9_.-]+)+|[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_.-]+){2,})',
  multiLine: true,
);

List<String> _extractFilePaths(String content) {
  final seen = <String>{};
  final paths = <String>[];
  for (final m in _filePathPattern.allMatches(content)) {
    final path = m.group(2)!;
    if (seen.add(path)) paths.add(path);
  }
  return paths;
}

class _AssistantMessageCard extends StatefulWidget {
  final Message message;
  final String hostId;
  final String sessionCwd;
  const _AssistantMessageCard({
    required this.message,
    required this.hostId,
    required this.sessionCwd,
  });

  @override
  State<_AssistantMessageCard> createState() => _AssistantMessageCardState();
}

class _AssistantMessageCardState extends State<_AssistantMessageCard> {
  bool _isSpeaking = false;

  void _speakMessage() async {
    final content = widget.message.content;
    final spoken = (content != null && content.isNotEmpty) ? stripMarkdown(content) : null;
    debugPrint('[MessageCard] _speakMessage: spoken=${spoken != null ? '"${spoken.length > 80 ? '${spoken.substring(0, 80)}...' : spoken}"' : 'null'}');
    if (spoken != null) {
      setState(() => _isSpeaking = true);
      final ok = await VoiceService.instance.speak(spoken);
      if (!ok && mounted) {
        setState(() => _isSpeaking = false);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('TTS unavailable. Check that Google Text-to-Speech is installed and enabled in Settings.'),
            duration: Duration(seconds: 4),
          ),
        );
        return;
      }
      // Listen for completion
      final prevCallback = VoiceService.instance.onStateChanged;
      VoiceService.instance.onStateChanged = () {
        prevCallback?.call();
        if (!VoiceService.instance.isSpeaking && mounted) {
          setState(() => _isSpeaking = false);
          VoiceService.instance.onStateChanged = prevCallback;
        }
      };
    }
  }

  void _stopSpeaking() {
    VoiceService.instance.stopSpeaking();
    if (mounted) setState(() => _isSpeaking = false);
  }

  void _handleLinkTap(String text, String? href, String title) {
    if (href == null) return;
    final uri = Uri.tryParse(href);
    if (uri != null && (uri.scheme == 'http' || uri.scheme == 'https')) {
      launchUrl(uri, mode: LaunchMode.externalApplication);
    }
  }

  void _openFilePath(String path) {
    String resolvedPath;
    if (path.startsWith('/') || path.startsWith('~')) {
      resolvedPath = path;
    } else {
      // Relative path — try to resolve against sessionCwd.
      // If the first segment of the relative path matches the last segment of
      // sessionCwd (e.g. cwd=".../helios/mobile" and path="mobile/lib/..."),
      // strip that duplicate segment.
      final cwdLastSegment = widget.sessionCwd.split('/').where((s) => s.isNotEmpty).lastOrNull ?? '';
      final firstSegment = path.split('/').first;
      if (cwdLastSegment.isNotEmpty && firstSegment == cwdLastSegment) {
        final parent = widget.sessionCwd.substring(0, widget.sessionCwd.lastIndexOf('/'));
        resolvedPath = '$parent/$path';
      } else {
        resolvedPath = '${widget.sessionCwd}/$path';
      }
    }
    Navigator.of(context).push(
      MaterialPageRoute(
        settings: const RouteSettings(name: '/file-browser'),
        builder: (_) => FileBrowserScreen(
          hostId: widget.hostId,
          rootPath: widget.sessionCwd,
          openFilePath: resolvedPath,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final content = widget.message.content ?? '';
    if (content.isEmpty) return const SizedBox.shrink();

    final filePaths = widget.hostId.isNotEmpty ? _extractFilePaths(content) : <String>[];

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        constraints: BoxConstraints(maxWidth: MediaQuery.of(context).size.width * 0.85),
        margin: const EdgeInsets.only(bottom: 8),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerHighest,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(4),
            bottomRight: Radius.circular(16),
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            MarkdownBody(
              data: content,
              selectable: true,
              onTapLink: _handleLinkTap,
              builders: {
                'code': _SyntaxHighlightBuilder(
                  isDark: Theme.of(context).brightness == Brightness.dark,
                ),
              },
              styleSheet: MarkdownStyleSheet(
                  p: TextStyle(fontSize: 14, color: theme.colorScheme.onSurface),
                  code: TextStyle(
                    fontSize: 12,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.onSurface,
                    backgroundColor: theme.colorScheme.surfaceContainerHigh,
                  ),
                  codeblockDecoration: BoxDecoration(
                    color: theme.colorScheme.surfaceContainerHigh,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  codeblockPadding: const EdgeInsets.all(10),
                  blockquoteDecoration: BoxDecoration(
                    border: Border(
                      left: BorderSide(color: theme.colorScheme.primary, width: 3),
                    ),
                  ),
                  blockquotePadding: const EdgeInsets.only(left: 12, top: 4, bottom: 4),
                  h1: TextStyle(fontSize: 20, fontWeight: FontWeight.bold, color: theme.colorScheme.onSurface),
                  h2: TextStyle(fontSize: 18, fontWeight: FontWeight.bold, color: theme.colorScheme.onSurface),
                  h3: TextStyle(fontSize: 16, fontWeight: FontWeight.w600, color: theme.colorScheme.onSurface),
                  listBullet: TextStyle(fontSize: 14, color: theme.colorScheme.onSurface),
                ),
              ),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  GestureDetector(
                    onTap: () => _copyToClipboard(context, content),
                    child: Padding(
                      padding: const EdgeInsets.only(top: 4, right: 8),
                      child: Icon(
                        Icons.copy,
                        size: 14,
                        color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.6),
                      ),
                    ),
                  ),
                  GestureDetector(
                    onTap: _isSpeaking ? _stopSpeaking : _speakMessage,
                    child: Padding(
                      padding: const EdgeInsets.only(top: 4),
                      child: Icon(
                        _isSpeaking ? Icons.stop : Icons.volume_up,
                        size: 16,
                        color: _isSpeaking
                            ? theme.colorScheme.error
                            : theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.6),
                      ),
                    ),
                  ),
                ],
              ),
              // File path chips
              if (filePaths.isNotEmpty) ...[
                const SizedBox(height: 8),
                Wrap(
                  spacing: 6,
                  runSpacing: 4,
                  children: filePaths.map((path) {
                    final name = path.split('/').last;
                    return GestureDetector(
                      onTap: () => _openFilePath(path),
                      child: Container(
                        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                        decoration: BoxDecoration(
                          color: theme.colorScheme.secondaryContainer.withValues(alpha: 0.6),
                          borderRadius: BorderRadius.circular(6),
                          border: Border.all(
                            color: theme.colorScheme.secondary.withValues(alpha: 0.3),
                          ),
                        ),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(Icons.insert_drive_file, size: 12, color: theme.colorScheme.secondary),
                            const SizedBox(width: 4),
                            Flexible(
                              child: Text(
                                name,
                                style: TextStyle(
                                  fontSize: 11,
                                  fontFamily: 'monospace',
                                  color: theme.colorScheme.secondary,
                                ),
                                overflow: TextOverflow.ellipsis,
                              ),
                            ),
                          ],
                        ),
                      ),
                    );
                  }).toList(),
                ),
              ],
            ],
          ),
        ),
    );
  }
}

class _ToolUseCard extends StatefulWidget {
  final Message message;
  const _ToolUseCard({required this.message});

  @override
  State<_ToolUseCard> createState() => _ToolUseCardState();
}

class _ToolUseCardState extends State<_ToolUseCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final meta = widget.message.metadata;
    final hasDetails = meta != null && meta.isNotEmpty;

    return GestureDetector(
      onTap: hasDetails ? () => setState(() => _expanded = !_expanded) : null,
      child: Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: theme.colorScheme.tertiaryContainer.withValues(alpha: 0.3),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: theme.colorScheme.tertiaryContainer,
            width: 1,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  _toolIcon(widget.message.tool ?? ''),
                  size: 14,
                  color: theme.colorScheme.tertiary,
                ),
                const SizedBox(width: 8),
                Text(
                  widget.message.tool ?? 'Tool',
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: theme.colorScheme.tertiary,
                  ),
                ),
                if (widget.message.summary != null && widget.message.summary!.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      widget.message.summary!,
                      style: TextStyle(
                        fontSize: 12,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
                if (hasDetails)
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 16,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
              ],
            ),
            if (_expanded && hasDetails) ...[
              const SizedBox(height: 6),
              _buildExpandedContent(context, theme, meta),
            ],
          ],
        ),
      ),
    );
  }

  Widget _buildExpandedContent(BuildContext context, ThemeData theme, Map<String, dynamic> meta) {
    final tool = widget.message.tool ?? '';
    final isDark = theme.brightness == Brightness.dark;

    // For file-content tools, render code with syntax highlighting
    if (tool == 'Read' || tool == 'Write' || tool == 'Edit') {
      final filePath = (meta['file_path'] ?? meta['path'] ?? '') as String;
      final ext = filePath.contains('.')
          ? filePath.split('.').last.toLowerCase()
          : '';
      final language = _langForExt(ext);
      final widgets = <Widget>[];

      // Show non-content fields as plain text first (e.g. old_string label)
      final contentKeys = {'content', 'new_string', 'old_string', 'new_content'};
      final otherFields = meta.entries
          .where((e) => !contentKeys.contains(e.key) && e.key != 'file_path' && e.key != 'path')
          .toList();
      if (otherFields.isNotEmpty) {
        final plain = otherFields.map((e) => '${e.key}: ${e.value}').join('\n');
        widgets.add(
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 4, 8, 4),
            child: SelectableText(
              plain,
              style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: theme.colorScheme.onSurfaceVariant),
            ),
          ),
        );
      }

      // Render each content field with syntax highlighting
      for (final key in ['content', 'new_content', 'new_string', 'old_string']) {
        final value = meta[key];
        if (value is! String || value.isEmpty) continue;
        final label = key == 'new_string' ? 'new' : key == 'old_string' ? 'old' : null;
        if (label != null) {
          widgets.add(
            Padding(
              padding: const EdgeInsets.fromLTRB(8, 4, 8, 0),
              child: Text(
                label,
                style: TextStyle(fontSize: 10, color: theme.colorScheme.onSurfaceVariant, fontWeight: FontWeight.w600),
              ),
            ),
          );
        }
        widgets.add(
          ClipRRect(
            borderRadius: BorderRadius.circular(6),
            child: HighlightView(
              value,
              language: language ?? 'plaintext',
              theme: isDark ? atomOneDarkTheme : atomOneLightTheme,
              padding: const EdgeInsets.all(8),
              textStyle: const TextStyle(fontSize: 11, fontFamily: 'monospace', height: 1.4),
            ),
          ),
        );
      }

      if (widgets.isNotEmpty) {
        return Container(
          width: double.infinity,
          decoration: BoxDecoration(
            color: theme.colorScheme.surface,
            borderRadius: BorderRadius.circular(6),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: widgets,
          ),
        );
      }
    }

    // Bash tool — render as shell
    if (tool == 'Bash') {
      final cmd = (meta['command'] ?? meta['cmd'] ?? '') as String;
      if (cmd.isNotEmpty) {
        return ClipRRect(
          borderRadius: BorderRadius.circular(6),
          child: HighlightView(
            cmd,
            language: 'bash',
            theme: isDark ? atomOneDarkTheme : atomOneLightTheme,
            padding: const EdgeInsets.all(8),
            textStyle: const TextStyle(fontSize: 11, fontFamily: 'monospace', height: 1.4),
          ),
        );
      }
    }

    // Fallback: plain key:value text
    final lines = <String>[];
    for (final MapEntry(:key, :value) in meta.entries) {
      if (value is String) {
        lines.add(value.length > 500 ? '$key: ${value.substring(0, 500)}...' : '$key: $value');
      } else {
        lines.add('$key: $value');
      }
    }
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        borderRadius: BorderRadius.circular(6),
      ),
      child: SelectableText(
        lines.join('\n'),
        style: TextStyle(fontSize: 11, fontFamily: 'monospace', color: theme.colorScheme.onSurface),
      ),
    );
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

  IconData _toolIcon(String tool) {
    switch (tool) {
      case 'Read':
        return Icons.description;
      case 'Write':
        return Icons.edit_document;
      case 'Edit':
        return Icons.edit;
      case 'Bash':
        return Icons.terminal;
      case 'Glob':
        return Icons.search;
      case 'Grep':
        return Icons.search;
      case 'Agent':
        return Icons.smart_toy;
      default:
        return Icons.build;
    }
  }
}

void _copyToClipboard(BuildContext context, String text) {
  Clipboard.setData(ClipboardData(text: text));
  HapticFeedback.lightImpact();
  ScaffoldMessenger.of(context).showSnackBar(
    const SnackBar(
      content: Text('Copied to clipboard'),
      duration: Duration(seconds: 1),
      behavior: SnackBarBehavior.floating,
    ),
  );
}

class _ToolResultCard extends StatelessWidget {
  final Message message;
  const _ToolResultCard({required this.message});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final success = message.success ?? true;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Row(
        children: [
          Icon(
            success ? Icons.check_circle : Icons.error,
            size: 12,
            color: success ? Colors.green : theme.colorScheme.error,
          ),
          const SizedBox(width: 6),
          Text(
            message.tool != null ? '${message.tool} ${success ? "done" : "failed"}' : (success ? 'done' : 'failed'),
            style: TextStyle(
              fontSize: 11,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}

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

    // Inline code has no class attribute and no newlines — skip, let markdown render it normally.
    if (rawLang.isEmpty && !code.contains('\n')) return null;

    final lang = rawLang.replaceFirst('language-', '');
    return HighlightView(
      code.trimRight(),
      language: lang.isNotEmpty ? lang : 'plaintext',
      theme: isDark ? atomOneDarkTheme : atomOneLightTheme,
      padding: const EdgeInsets.all(10),
      textStyle: const TextStyle(fontSize: 12, fontFamily: 'monospace', height: 1.5),
    );
  }
}
