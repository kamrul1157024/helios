import 'dart:convert';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_highlight/flutter_highlight.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_svg/flutter_svg.dart';
import 'package:markdown/markdown.dart' as md;
import 'package:url_launcher/url_launcher.dart';

import '../providers/daemon_providers.dart';
import '../utils/preview_assets.dart';
import 'mermaid_diagram.dart';
import 'scroll_to_index.dart';

/// A markdown file, as a lazy list of its blocks.
///
/// It used to be one `MarkdownBody` inside a `SingleChildScrollView`, which
/// lays out a whole README — every paragraph, every highlighted fence, every
/// diagram — before the first one is on screen. Now the document is parsed
/// once, and each top-level block is built when it scrolls into view
/// (spec 65, 3.4).
///
/// Links do what a reader expects of a file on disk: a relative link opens that
/// file in the viewer, an external one opens in the browser, and `#heading`
/// scrolls to the heading. A relative image is read through the daemon, from
/// inside the checkout only, as the HTML preview does (spec 51).
class MarkdownView extends rp.ConsumerStatefulWidget {
  final String data;
  final String hostId;

  /// The file itself, which relative links and images resolve against.
  final String path;

  /// The checkout. An image outside it is not read.
  final String root;

  /// Opens another file in the viewer, at a heading when the link named one.
  final void Function(String path, String? anchor) onOpenFile;

  /// The heading to show first, from a link in another file.
  final String? initialAnchor;

  final Future<void> Function()? onRefresh;

  const MarkdownView({
    super.key,
    required this.data,
    required this.hostId,
    required this.path,
    required this.root,
    required this.onOpenFile,
    this.initialAnchor,
    this.onRefresh,
  });

  @override
  rp.ConsumerState<MarkdownView> createState() => _MarkdownViewState();
}

class _MarkdownViewState extends rp.ConsumerState<MarkdownView>
    implements MarkdownBuilderDelegate {
  final _scroll = ScrollController();
  List<md.Node> _blocks = const [];

  /// Heading slug to the index of its block, GitHub's way: lowercased,
  /// punctuation dropped, spaces as hyphens, and `-1`, `-2` on repeats.
  Map<String, int> _anchors = const {};

  /// The link recognizers each built block made, disposed when the block is
  /// built again or the view goes.
  final Map<int, List<GestureRecognizer>> _recognizers = {};
  List<GestureRecognizer>? _collecting;

  ScrollTarget? _target;

  @override
  void initState() {
    super.initState();
    _parse();
    final anchor = widget.initialAnchor;
    if (anchor != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _scrollTo(anchor));
    }
  }

  @override
  void didUpdateWidget(MarkdownView old) {
    super.didUpdateWidget(old);
    if (!identical(old.data, widget.data)) _parse();
  }

  @override
  void dispose() {
    _disposeRecognizers();
    _scroll.dispose();
    super.dispose();
  }

  void _disposeRecognizers() {
    for (final list in _recognizers.values) {
      for (final recognizer in list) {
        recognizer.dispose();
      }
    }
    _recognizers.clear();
  }

  void _parse() {
    _disposeRecognizers();
    final document = md.Document(
      extensionSet: md.ExtensionSet.gitHubFlavored,
      encodeHtml: false,
    );
    _blocks = document.parseLines(const LineSplitter().convert(widget.data));
    final anchors = <String, int>{};
    final seen = <String, int>{};
    for (var i = 0; i < _blocks.length; i++) {
      final node = _blocks[i];
      if (node is! md.Element || !_headingTags.contains(node.tag)) continue;
      final base = headingSlug(node.textContent);
      final repeat = seen[base] ?? 0;
      seen[base] = repeat + 1;
      anchors[repeat == 0 ? base : '$base-$repeat'] = i;
    }
    _anchors = anchors;
  }

  static const _headingTags = {'h1', 'h2', 'h3', 'h4', 'h5', 'h6'};

  Future<void> _scrollTo(String anchor) async {
    final index = _anchors[anchor.toLowerCase()];
    if (index == null || !mounted) return;
    final target = ScrollTarget(index);
    setState(() => _target = target);
    await WidgetsBinding.instance.endOfFrame;
    if (!mounted) return;
    await scrollToTarget(
      controller: _scroll,
      target: target,
      count: _blocks.length,
      alignment: 0,
    );
  }

  void _onTapLink(String text, String? href, String title) {
    if (href == null || href.isEmpty) return;
    if (href.startsWith('#')) {
      _scrollTo(Uri.decodeComponent(href.substring(1)));
      return;
    }
    final uri = Uri.tryParse(href);
    if (uri != null && uri.hasScheme) {
      launchUrl(uri, mode: LaunchMode.externalApplication);
      return;
    }
    final hash = href.indexOf('#');
    final anchor = hash < 0 ? null : href.substring(hash + 1);
    final target = resolveLink(
      widget.path,
      hash < 0 ? href : href.substring(0, hash),
    );
    if (target == null) return;
    widget.onOpenFile(target, anchor);
  }

  // ─── MarkdownBuilderDelegate ─────────────────────────────────────────────

  @override
  GestureRecognizer createLink(String text, String? href, String title) {
    final recognizer = TapGestureRecognizer()
      ..onTap = () => _onTapLink(text, href, title);
    _collecting?.add(recognizer);
    return recognizer;
  }

  @override
  TextSpan formatText(MarkdownStyleSheet styleSheet, String code) => TextSpan(
    style: styleSheet.code,
    text: code.replaceAll(RegExp(r'\n$'), ''),
  );

  // ─────────────────────────────────────────────────────────────────────────

  MarkdownStyleSheet _styleSheet(ThemeData theme) {
    // The chat's styles (message_card.dart), so a file and a message about it
    // look like the same thing.
    final colors = theme.colorScheme;
    return MarkdownStyleSheet.fromTheme(theme).merge(
      MarkdownStyleSheet(
        p: TextStyle(fontSize: 14, color: colors.onSurface),
        a: TextStyle(
          fontSize: 14,
          color: colors.primary,
          decoration: TextDecoration.underline,
          decorationColor: colors.primary,
        ),
        code: TextStyle(
          fontSize: 12,
          fontFamily: 'monospace',
          color: colors.onSurface,
          backgroundColor: colors.surfaceContainerHigh,
        ),
        codeblockDecoration: BoxDecoration(
          color: colors.surfaceContainerHigh,
          borderRadius: BorderRadius.circular(8),
        ),
        codeblockPadding: EdgeInsets.zero,
        blockquoteDecoration: BoxDecoration(
          border: Border(left: BorderSide(color: colors.primary, width: 3)),
        ),
        blockquotePadding: const EdgeInsets.only(left: 12, top: 4, bottom: 4),
        h1: TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.bold,
          color: colors.onSurface,
        ),
        h2: TextStyle(
          fontSize: 18,
          fontWeight: FontWeight.bold,
          color: colors.onSurface,
        ),
        h3: TextStyle(
          fontSize: 16,
          fontWeight: FontWeight.w600,
          color: colors.onSurface,
        ),
        listBullet: TextStyle(fontSize: 14, color: colors.onSurface),
      ),
    );
  }

  Widget _image(MarkdownImageConfig config) {
    final uri = config.uri;
    final alt = config.alt ?? '';
    if (uri.scheme == 'http' || uri.scheme == 'https') {
      return Image.network(
        uri.toString(),
        width: config.width,
        height: config.height,
        errorBuilder: (_, _, _) => _AltText(alt),
      );
    }
    final path = resolveAsset(widget.path, uri.toString(), widget.root);
    if (path == null) return _AltText(alt);
    return _DaemonImage(
      hostId: widget.hostId,
      path: path,
      alt: alt,
      width: config.width,
      height: config.height,
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final builder = MarkdownBuilder(
      delegate: this,
      selectable: true,
      styleSheet: _styleSheet(theme),
      imageDirectory: null,
      sizedImageBuilder: _image,
      checkboxBuilder: null,
      bulletBuilder: null,
      builders: {
        'code': MarkdownCodeBuilder(
          isDark: theme.brightness == Brightness.dark,
        ),
      },
      paddingBuilders: const {},
      listItemCrossAxisAlignment: MarkdownListItemCrossAxisAlignment.baseline,
    );
    final target = _target;
    final list = ListView.builder(
      controller: _scroll,
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      itemCount: _blocks.length,
      itemBuilder: (context, i) {
        target?.built.add(i);
        for (final old in _recognizers.remove(i) ?? const []) {
          old.dispose();
        }
        final made = _collecting = <GestureRecognizer>[];
        final children = builder.build([_blocks[i]]);
        _collecting = null;
        if (made.isNotEmpty) _recognizers[i] = made;
        Widget block = Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: children,
          ),
        );
        if (target != null && target.index == i) {
          block = KeyedSubtree(key: target.key, child: block);
        }
        return block;
      },
    );
    final refresh = widget.onRefresh;
    if (refresh == null) return list;
    return RefreshIndicator(onRefresh: refresh, child: list);
  }
}

/// GitHub's anchor for a heading.
String headingSlug(String text) {
  final lower = text.trim().toLowerCase();
  final kept = lower.replaceAll(
    RegExp(r'[^\p{L}\p{N}\s_-]', unicode: true),
    '',
  );
  return kept.replaceAll(RegExp(r'\s'), '-');
}

/// Where a relative link in the markdown file at [from] points, or null for
/// one that is not a path at all.
String? resolveLink(String from, String href) {
  var decoded = href.trim();
  if (decoded.isEmpty) return null;
  try {
    decoded = Uri.decodeComponent(decoded);
  } catch (_) {
    // A stray % is not worth refusing the link over; use it as written.
  }
  decoded = decoded.split('?').first;
  final base = from.substring(0, from.lastIndexOf('/'));
  final joined = decoded.startsWith('/') ? decoded : '$base/$decoded';
  final out = <String>[];
  for (final part in joined.split('/')) {
    if (part.isEmpty || part == '.') continue;
    if (part == '..') {
      if (out.isNotEmpty) out.removeLast();
      continue;
    }
    out.add(part);
  }
  return '/${out.join('/')}';
}

class _AltText extends StatelessWidget {
  final String alt;

  const _AltText(this.alt);

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          Icons.broken_image_outlined,
          size: 16,
          color: theme.colorScheme.onSurfaceVariant,
        ),
        if (alt.isNotEmpty) ...[
          const SizedBox(width: 4),
          Flexible(
            child: Text(
              alt,
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
        ],
      ],
    );
  }
}

/// A picture the markdown names by a path in the checkout, read through the
/// daemon like any other file.
class _DaemonImage extends rp.ConsumerWidget {
  final String hostId;
  final String path;
  final String alt;
  final double? width;
  final double? height;

  const _DaemonImage({
    required this.hostId,
    required this.path,
    required this.alt,
    this.width,
    this.height,
  });

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final file = ref.watch(readFileProvider((hostId, path))).valueOrNull;
    if (file == null) {
      return SizedBox(width: width, height: height ?? 24);
    }
    if (file.isSvg) {
      return SvgPicture.string(
        file.content!,
        width: width,
        height: height,
        errorBuilder: (_, _, _) => _AltText(alt),
      );
    }
    final bytes = file.bytes;
    if (bytes == null) return _AltText(alt);
    return Image.memory(
      bytes,
      width: width,
      height: height,
      errorBuilder: (_, _, _) => _AltText(alt),
    );
  }
}

/// A highlight theme with its root background taken out, so the code sits on
/// whatever box it is drawn in.
Map<String, TextStyle> _clearRoot(Map<String, TextStyle> theme) => {
  ...theme,
  'root': (theme['root'] ?? const TextStyle()).copyWith(
    backgroundColor: Colors.transparent,
  ),
};

final _darkCode = _clearRoot(atomOneDarkTheme);
final _lightCode = _clearRoot(atomOneLightTheme);

/// Fenced code in markdown: highlighted, and a Mermaid fence drawn as the
/// diagram it describes.
class MarkdownCodeBuilder extends MarkdownElementBuilder {
  final bool isDark;

  MarkdownCodeBuilder({required this.isDark});

  @override
  Widget? visitElementAfterWithContext(
    BuildContext context,
    md.Element element,
    TextStyle? preferredStyle,
    TextStyle? parentStyle,
  ) {
    final code = element.textContent;
    final rawLang = element.attributes['class'] ?? '';

    // Inline code — no class, no newlines — let markdown render it normally.
    if (rawLang.isEmpty && !code.contains('\n')) return null;

    final lang = rawLang.replaceFirst('language-', '');
    // Full width, and without the highlight theme's own background: the
    // style sheet's code-block box is the one background, in the app's
    // colours. The theme's box hugged the code's width inside it, which drew
    // two backgrounds in one row.
    final block = SizedBox(
      width: double.infinity,
      child: HighlightView(
        code.trimRight(),
        language: lang.isNotEmpty ? lang : 'plaintext',
        theme: isDark ? _darkCode : _lightCode,
        padding: const EdgeInsets.all(12),
        textStyle: const TextStyle(
          fontSize: 12,
          fontFamily: 'monospace',
          height: 1.5,
        ),
      ),
    );
    if (lang == 'mermaid') {
      return MermaidDiagram(
        source: code.trimRight(),
        isDark: isDark,
        fallback: block,
      );
    }
    return block;
  }
}
