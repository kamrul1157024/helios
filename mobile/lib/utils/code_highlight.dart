/// Syntax highlighting for the file viewer, done once and off the UI thread.
///
/// The viewer used to run `highlight.parse` over the whole file from `build()`,
/// so every `setState` — a tap on a line was enough — parsed the file again on
/// the UI isolate. Now the parse runs in an isolate when the content changes,
/// and what comes back is small: for each line, a flat list of
/// `(start, end, class)` triples. Turning those into `TextSpan`s is left to the
/// row that draws the line, which is only ever a screenful of them.
///
/// See docs/specs/65-fast-files.md, 3.1.
library;

import 'package:flutter/foundation.dart';
import 'package:highlight/highlight.dart' show highlight, Node;

import 'file_types.dart';

/// What to highlight. A plain class so it crosses the isolate boundary.
class HighlightRequest {
  final String code;
  final String language;

  const HighlightRequest(this.code, this.language);
}

/// The highlight of a whole file, line by line.
class HighlightedCode {
  /// The class names the spans refer to, by index.
  final List<String> classes;

  /// One list per line of `start, end, classIndex` triples. Offsets are in
  /// UTF-16 code units from the start of the line, ascending, and never
  /// overlap. Text no triple covers is drawn in the default colour.
  final List<Int32List> lines;

  const HighlightedCode(this.classes, this.lines);
}

/// Parses [request] and flattens the tree into per-line spans.
///
/// Top-level so `compute` can send it to an isolate. A language the
/// highlighter does not know, or a parse that throws, gives null: the viewer
/// then keeps the plain text it already shows, which is a fine answer.
HighlightedCode? highlightLines(HighlightRequest request) {
  final List<Node>? nodes;
  try {
    nodes = highlight.parse(request.code, language: request.language).nodes;
  } catch (_) {
    return null;
  }
  if (nodes == null) return null;

  final classes = <String>[];
  final classIndex = <String, int>{};
  final lines = <Int32List>[];
  var current = <int>[];
  var column = 0;

  void emit(String text, int cls) {
    var start = 0;
    while (true) {
      final newline = text.indexOf('\n', start);
      final end = newline < 0 ? text.length : newline;
      if (cls >= 0 && end > start) {
        current
          ..add(column)
          ..add(column + end - start)
          ..add(cls);
      }
      column += end - start;
      if (newline < 0) return;
      lines.add(Int32List.fromList(current));
      current = <int>[];
      column = 0;
      start = newline + 1;
    }
  }

  // A leaf takes the class of its nearest classed ancestor: the highlighter
  // puts a keyword's text in an unclassed node under a classed one.
  void walk(List<Node> nodes, int inherited) {
    for (final node in nodes) {
      var cls = inherited;
      final name = node.className;
      if (name != null) {
        cls = classIndex.putIfAbsent(name, () {
          classes.add(name);
          return classes.length - 1;
        });
      }
      final value = node.value;
      if (value != null) {
        emit(value, cls);
      } else if (node.children != null) {
        walk(node.children!, cls);
      }
    }
  }

  walk(nodes, -1);
  lines.add(Int32List.fromList(current));
  return HighlightedCode(classes, lines);
}

/// How the viewer gets a highlight. An isolate in the app; a test swaps it to
/// count the calls or to answer synchronously, which is why it is a variable.
typedef HighlightRunner =
    Future<HighlightedCode?> Function(HighlightRequest request);

HighlightRunner highlightRunner = (request) => compute(highlightLines, request);

/// The last few highlights, so reopening an unchanged file draws in colour at
/// once.
///
/// Keyed by path and ETag: the ETag is the daemon's word that the bytes did
/// not change, so a reopen that came back as a 304 is a hit without looking at
/// the text at all.
class HighlightCache {
  final int capacity;
  final _entries = <String, HighlightedCode>{};

  HighlightCache({this.capacity = 10});

  HighlightedCode? get(String key) {
    final hit = _entries.remove(key);
    if (hit != null) _entries[key] = hit;
    return hit;
  }

  void put(String key, HighlightedCode value) {
    _entries.remove(key);
    _entries[key] = value;
    while (_entries.length > capacity) {
      _entries.remove(_entries.keys.first);
    }
  }

  @visibleForTesting
  void clear() => _entries.clear();
}

final highlightCache = HighlightCache();

/// The cache key for one version of one file.
///
/// A daemon older than spec 65 sends no ETag, so the text stands in for it.
String highlightKey(String path, String? etag, String language, String text) {
  final version = etag ?? 'h${text.length}:${text.hashCode}';
  return '$path|$version|$language';
}

/// The highlighter's name for a file's language, or null for plain text.
String? languageForPath(String path) {
  final name = path.substring(path.lastIndexOf('/') + 1);
  if (name == 'Dockerfile') return 'dockerfile';
  if (name == 'Makefile') return 'makefile';
  return _languages[extensionOf(path)];
}

const _languages = <String, String>{
  'dart': 'dart',
  'go': 'go',
  'py': 'python',
  'js': 'javascript',
  'mjs': 'javascript',
  'cjs': 'javascript',
  'ts': 'typescript',
  'tsx': 'typescript',
  'jsx': 'javascript',
  'java': 'java',
  'kt': 'kotlin',
  'swift': 'swift',
  'rs': 'rust',
  'c': 'c',
  'cc': 'cpp',
  'cpp': 'cpp',
  'h': 'cpp',
  'hpp': 'cpp',
  'cs': 'cs',
  'rb': 'ruby',
  'php': 'php',
  'lua': 'lua',
  'sh': 'bash',
  'bash': 'bash',
  'zsh': 'bash',
  'json': 'json',
  'yaml': 'yaml',
  'yml': 'yaml',
  'toml': 'ini',
  'ini': 'ini',
  'xml': 'xml',
  'html': 'xml',
  'htm': 'xml',
  'svg': 'xml',
  'css': 'css',
  'scss': 'scss',
  'less': 'less',
  'sql': 'sql',
  'proto': 'protobuf',
  'diff': 'diff',
  'patch': 'diff',
  'md': 'markdown',
  'markdown': 'markdown',
};
