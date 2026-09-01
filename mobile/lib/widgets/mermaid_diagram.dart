/// A mermaid fence, drawn as the diagram it describes.
///
/// Mermaid is JavaScript, so this is a webview — but a far smaller one than
/// [HtmlPreview]. The document is written here rather than read from disk, and
/// it is the same document every time: a copy of mermaid carried in the app's
/// assets, the fence's source, and a script that draws one and posts back how
/// tall the result came out.
///
/// What that leaves, on the same terms as the HTML preview:
///
///   * No network. The policy below is `default-src 'none'`, and mermaid asks
///     for nothing — its fonts are the local stack.
///   * No navigation. Every request but the initial load is refused.
///   * No touch. The view sits inside an [IgnorePointer], because a platform
///     view in a scrolling list swallows the drags meant for the list.
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show rootBundle;
import 'package:webview_flutter/webview_flutter.dart';

/// What a diagram is given to draw in before it says how much it needs.
const double _provisional = 180;

/// Read once per process: three and a half megabytes is not a per-diagram cost.
Future<String>? _library;

Future<String> _mermaidLibrary() {
  return _library ??= rootBundle.loadString('assets/mermaid/mermaid.min.js');
}

class MermaidDiagram extends StatefulWidget {
  /// The fence's contents, as the agent wrote them.
  final String source;

  final bool isDark;

  /// What to show if the source is not a diagram after all — the code block
  /// this fence would have rendered as. Agents write diagrams a line at a time,
  /// so half of one is a normal thing to be looking at.
  final Widget fallback;

  const MermaidDiagram({
    super.key,
    required this.source,
    required this.isDark,
    required this.fallback,
  });

  @override
  State<MermaidDiagram> createState() => _MermaidDiagramState();
}

class _MermaidDiagramState extends State<MermaidDiagram> {
  WebViewController? _controller;

  /// The drawn height, in logical pixels. A webview has no size of its own.
  double? _height;

  bool _failed = false;

  @override
  void initState() {
    super.initState();
    unawaited(_prepare());
  }

  @override
  void didUpdateWidget(MermaidDiagram old) {
    super.didUpdateWidget(old);
    if (old.source != widget.source || old.isDark != widget.isDark) {
      _height = null;
      _failed = false;
      unawaited(_prepare());
    }
  }

  Future<void> _prepare() async {
    final library = await _mermaidLibrary();
    if (!mounted) return;

    final controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(Colors.transparent)
      ..addJavaScriptChannel('HeliosMermaid', onMessageReceived: _onDrawn)
      ..setNavigationDelegate(
        NavigationDelegate(onNavigationRequest: (_) => NavigationDecision.prevent),
      )
      ..loadHtmlString(_document(library));

    setState(() => _controller = controller);
  }

  void _onDrawn(JavaScriptMessage message) {
    final report = jsonDecode(message.message) as Map<String, dynamic>;
    if (!mounted) return;
    if (report['ok'] != true) {
      setState(() => _failed = true);
      return;
    }
    setState(() => _height = (report['height'] as num).toDouble());
  }

  String _document(String library) {
    final theme = Theme.of(context);
    // The diagram is drawn onto the card it sits on, so it takes the card's
    // colours rather than mermaid's — the same substitution the desktop makes
    // from its CSS variables.
    final variables = <String, String>{
      'background': _hex(theme.colorScheme.surfaceContainerHigh),
      'primaryColor': _hex(theme.colorScheme.surfaceContainerHighest),
      'primaryTextColor': _hex(theme.colorScheme.onSurface),
      'primaryBorderColor': _hex(theme.colorScheme.outlineVariant),
      'secondaryColor': _hex(theme.colorScheme.surfaceContainer),
      'tertiaryColor': _hex(theme.colorScheme.surfaceContainerHigh),
      'lineColor': _hex(theme.colorScheme.primary),
      'textColor': _hex(theme.colorScheme.onSurface),
      'fontSize': '14px',
    };

    return '''
<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action 'none'; base-uri 'none'">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  html, body { margin: 0; padding: 0; background: transparent; overflow: hidden; }
  #d svg { max-width: 100%; height: auto; display: block; margin: 0 auto; }
</style>
</head>
<body>
<div id="d"></div>
<script>$library</script>
<script>
(async () => {
  const report = (ok, height) =>
    HeliosMermaid.postMessage(JSON.stringify({ ok: ok, height: height || 0 }));
  try {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      suppressErrorRendering: true,
      theme: 'base',
      themeVariables: ${jsonEncode(variables)},
    });
    const { svg } = await mermaid.render('d0', ${jsonEncode(widget.source)});
    document.getElementById('d').innerHTML = svg;
    // After a frame, so the SVG has been laid out and has a height to report.
    requestAnimationFrame(() =>
      report(true, Math.ceil(document.getElementById('d').getBoundingClientRect().height)));
  } catch (e) {
    report(false);
  }
})();
</script>
</body>
</html>
''';
  }

  static String _hex(Color color) {
    final value = color.toARGB32() & 0xFFFFFF;
    return '#${value.toRadixString(16).padLeft(6, '0')}';
  }

  @override
  Widget build(BuildContext context) {
    if (_failed) return widget.fallback;

    final controller = _controller;
    if (controller == null) return const SizedBox(height: 24);

    // The view is mounted before its height is known, and has to be: a webview
    // with no place in the layout has no surface to draw on, so it never lays
    // the diagram out and never reports how tall it came out. Measured that
    // way — mounting on the report first left every diagram blank forever.
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: SizedBox(
        height: _height ?? _provisional,
        child: IgnorePointer(child: WebViewWidget(controller: controller)),
      ),
    );
  }
}
