/// An HTML file, shown as the page it is.
///
/// The desktop counterpart renders into an iframe with `sandbox=""`, inside a
/// window whose CSP forbids the network outright. A webview has neither of
/// those, so the same guarantee is reached from the other end: the document is
/// rewritten before it is loaded, and anything that could reach off the device
/// is removed rather than merely blocked.
///
/// What that leaves:
///
///   * No scripts. `JavaScriptMode.disabled`, and the tags are dropped too.
///   * No network. Every `src`/`href` with a scheme is stripped, so there is
///     nothing left to fetch; local files are read over the daemon and inlined.
///   * No navigation. Every request but the initial load is refused.
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:html/dom.dart' as dom;
import 'package:html/parser.dart' as html_parser;
import 'package:webview_flutter/webview_flutter.dart';

import '../providers/daemon_providers.dart';
import '../services/daemon_api_service.dart';
import '../utils/file_types.dart';
import '../utils/preview_assets.dart';

class HtmlPreview extends rp.ConsumerStatefulWidget {
  final String hostId;

  /// The page's own text, as read from disk.
  final String html;

  /// Where the page lives, which is what relative references resolve against.
  final String path;

  /// The checkout. Nothing outside it is ever read.
  final String root;

  /// Whether the reader has asked for the page's scripts to run.
  final bool scripts;

  const HtmlPreview({
    super.key,
    required this.hostId,
    required this.html,
    required this.path,
    required this.root,
    this.scripts = false,
  });

  @override
  rp.ConsumerState<HtmlPreview> createState() => _HtmlPreviewState();
}

class _HtmlPreviewState extends rp.ConsumerState<HtmlPreview> {
  WebViewController? _controller;
  String? _failure;

  @override
  void initState() {
    super.initState();
    unawaited(_prepare());
  }

  @override
  void didUpdateWidget(HtmlPreview old) {
    super.didUpdateWidget(old);
    if (old.html != widget.html || old.path != widget.path || old.scripts != widget.scripts) {
      unawaited(_prepare());
    }
  }

  Future<void> _prepare() async {
    try {
      final document = html_parser.parse(widget.html);
      await _inlineAssets(document);
      _stripUnreachable(document);
      _installPolicy(document);

      final controller = WebViewController()
        // Off unless asked for. On, the page can compute and draw and has
        // nowhere to send anything — see the policy injected above.
        ..setJavaScriptMode(
          widget.scripts ? JavaScriptMode.unrestricted : JavaScriptMode.disabled,
        )
        ..setBackgroundColor(Colors.white)
        ..setNavigationDelegate(
          NavigationDelegate(
            // Nothing may navigate. There is no address bar to come back from,
            // and after the rewrite above there is nowhere legitimate to go.
            onNavigationRequest: (_) => NavigationDecision.prevent,
          ),
        )
        // No base URL: relative references resolve against nothing, which is
        // correct once everything local has been inlined.
        ..loadHtmlString(document.outerHtml);

      if (mounted) setState(() => _controller = controller);
    } catch (error) {
      if (mounted) setState(() => _failure = '$error');
    }
  }

  /// Reads the local files the page points at and puts them in the document.
  Future<void> _inlineAssets(dom.Document document) async {
    final images = document.querySelectorAll('img[src]');
    final sheets = document
        .querySelectorAll('link[href]')
        .where((el) => (el.attributes['rel'] ?? '').split(RegExp(r'\s+')).contains('stylesheet'))
        .toList();

    // A local script is only worth fetching if it will be allowed to run.
    final code = widget.scripts ? document.querySelectorAll('script[src]') : <dom.Element>[];

    final refs = <AssetRef>[
      for (final el in images) AssetRef('img', el.attributes['src'] ?? ''),
      for (final el in sheets) AssetRef('style', el.attributes['href'] ?? ''),
      for (final el in code) AssetRef('script', el.attributes['src'] ?? ''),
    ];

    final planned = planAssets(refs, widget.path, widget.root);
    if (planned.isEmpty) return;

    final loaded = <String, FileReadResult>{};
    var spent = 0;
    for (final asset in planned) {
      final file = await ref.read(readFileProvider((widget.hostId, asset.path)).future);
      // A page that names a file the agent has since deleted still renders;
      // the reference simply goes unresolved.
      if (file?.content == null) continue;
      spent += file!.content!.length;
      if (spent > maxTotalBytes) break;
      loaded['${asset.kind}:${asset.href}'] = file;
    }

    for (final el in images) {
      final href = el.attributes['src'] ?? '';
      final file = loaded['img:$href'];
      if (file == null) continue;
      final matches = planned.where((a) => a.kind == 'img' && a.href == href);
      if (matches.isEmpty) continue;
      final asset = matches.first;

      if (file.encoding == 'base64') {
        el.attributes['src'] = 'data:${mimeForPath(asset.path)};base64,${file.content}';
      } else if (mimeForPath(asset.path) == 'image/svg+xml') {
        // An SVG is text and arrives as text; anything else that is not base64
        // came from a daemon too old to send bytes, and is already lost.
        final encoded = base64Encode(utf8.encode(file.content!));
        el.attributes['src'] = 'data:image/svg+xml;base64,$encoded';
      }
    }

    // A stylesheet becomes a <style>. A data: URL would be a second thing that
    // has to be allowed to load, and this needs nothing allowed.
    for (final el in sheets) {
      final file = loaded['style:${el.attributes['href'] ?? ''}'];
      if (file?.content == null || file!.encoding == 'base64') continue;
      final style = dom.Element.tag('style')..text = file.content!;
      el.replaceWith(style);
    }

    // And a local script becomes an inline one, for the same reason: the
    // policy below allows 'unsafe-inline' and no source at all.
    for (final el in code) {
      final file = loaded['script:${el.attributes['src'] ?? ''}'];
      if (file?.content == null || file!.encoding == 'base64') continue;
      final inline = dom.Element.tag('script')..text = file.content!;
      el.replaceWith(inline);
    }
  }

  /// Removes everything the page could still use to reach the network, run
  /// code, or navigate away.
  ///
  /// The desktop preview leaves the markup alone and lets the frame and the
  /// CSP contain it. A webview contains far less, so the containment is done
  /// here instead — by deletion, which does not depend on a policy being right.
  void _stripUnreachable(dom.Document document) {
    // The page's own <meta http-equiv> goes whatever happens: it could carry a
    // policy of its own, and the one this installs below has to be the only one.
    const always = 'iframe, object, embed, form, base, meta[http-equiv]';
    for (final el in document.querySelectorAll(widget.scripts ? always : 'script, $always')) {
      el.remove();
    }

    // Anything still pointing at a scheme after the inlining above is remote:
    // an image on a CDN, a font, a tracking pixel. It is dropped rather than
    // left to be fetched the moment the page loads.
    for (final el in document.querySelectorAll('[src], [href], [srcset], [poster], [data]')) {
      for (final name in const ['src', 'href', 'srcset', 'poster', 'data']) {
        final value = el.attributes[name];
        if (value == null) continue;
        if (_isRemote(value)) el.attributes.remove(name);
      }
    }
  }

  /// The policy the page runs under, as a meta the document carries with it.
  ///
  /// A webview has no equivalent of the desktop's per-response header, so the
  /// policy is written into the document instead. `default-src 'none'` with no
  /// `connect-src` of its own is the load-bearing part: with scripts on, fetch,
  /// XHR, websockets and beacons are all refused, so a script can compute and
  /// draw and has nowhere to send anything.
  void _installPolicy(dom.Document document) {
    final head = document.head;
    if (head == null) return;
    final meta = dom.Element.tag('meta')
      ..attributes['http-equiv'] = 'Content-Security-Policy'
      ..attributes['content'] = [
        "default-src 'none'",
        if (widget.scripts) "script-src 'unsafe-inline'",
        "style-src 'unsafe-inline'",
        'img-src data:',
        'font-src data:',
        "form-action 'none'",
        "base-uri 'none'",
      ].join('; ');
    head.nodes.insert(0, meta);
  }

  static bool _isRemote(String value) {
    final raw = value.trim();
    if (raw.startsWith('//')) return true;
    if (raw.startsWith('#')) return false;
    if (raw.startsWith('data:image/')) return false;
    return RegExp(r'^[a-z][a-z0-9+.\-]*:', caseSensitive: false).hasMatch(raw);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    if (_failure != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Text(
            'Could not render this page.\n$_failure',
            textAlign: TextAlign.center,
            style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
          ),
        ),
      );
    }
    final controller = _controller;
    if (controller == null) return const Center(child: CircularProgressIndicator());

    // White behind it, because a document assumes paper. A dark app around a
    // page written for a browser would misrepresent it.
    return Container(color: Colors.white, child: WebViewWidget(controller: controller));
  }
}
