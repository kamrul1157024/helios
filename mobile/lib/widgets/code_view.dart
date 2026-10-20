import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_highlight/themes/atom-one-dark.dart';
import 'package:flutter_highlight/themes/atom-one-light.dart';

import '../utils/code_highlight.dart';
import 'scroll_to_index.dart';

/// A run of lines the reader picked, 1-based and inclusive.
class LineSelection {
  final int start;
  final int end;

  const LineSelection(this.start, this.end);

  int get count => end - start + 1;

  bool contains(int line) => line >= start && line <= end;

  /// `L12`, or `L12-20`.
  String get label => start == end ? 'L$start' : 'L$start-$end';

  /// `12`, or `12-20` — what follows the colon in `path:12-20`.
  String get range => start == end ? '$start' : '$start-$end';

  @override
  bool operator ==(Object other) =>
      other is LineSelection && other.start == start && other.end == end;

  @override
  int get hashCode => Object.hash(start, end);
}

/// What a tap on [line] does to [current].
///
/// The first tap picks a line, a second tap on another line stretches the
/// pick to it, a tap on a lone picked line drops it, and a tap after a range
/// starts again.
LineSelection? nextSelection(LineSelection? current, int line) {
  if (current == null) return LineSelection(line, line);
  if (current.start == current.end) {
    if (line == current.start) return null;
    return LineSelection(
      math.min(line, current.start),
      math.max(line, current.start),
    );
  }
  return LineSelection(line, line);
}

/// Lets the screen around a [CodeView] move it to a line.
class CodeViewController {
  _CodeViewState? _state;

  Future<void> goToLine(int line) async => _state?._goToLine(line);
}

/// A text file, line by line, with line numbers.
///
/// Built for the two things a phone makes hard: long lines and small text.
/// With [wrap] off the code scrolls sideways as one block and the line numbers
/// stay put; with it on, rows grow to hold their line. A pinch moves the font
/// size, and the line at the top of the screen stays at the top while it does.
///
/// Highlighting never happens in `build`. The text shows plain at once, an
/// isolate parses it (see `utils/code_highlight.dart`), and the colours are
/// swapped in when it answers — or straight away when the same version of the
/// file was highlighted before.
class CodeView extends StatefulWidget {
  final String text;
  final String path;

  /// The daemon's validator for this version, which keys the highlight cache.
  final String? etag;

  final bool wrap;
  final double fontSize;

  /// Every step of a pinch, with the size it asks for.
  final ValueChanged<double> onFontSizeChanged;

  /// The pinch let go, so the size can be saved.
  final VoidCallback onFontSizeCommitted;

  final LineSelection? selection;
  final ValueChanged<LineSelection?> onSelectionChanged;

  /// Whether a plain tap picks a line. On when the file was opened from a
  /// session, where a pick is what "Ask AI" asks about. A long press starts a
  /// pick either way, and once there is one, taps change it.
  final bool tapToSelect;

  final CodeViewController? controller;

  /// Pull to refresh.
  final Future<void> Function()? onRefresh;

  /// The line to show first — an edit the agent made, say.
  final int? initialLine;

  const CodeView({
    super.key,
    required this.text,
    required this.path,
    this.etag,
    required this.wrap,
    required this.fontSize,
    required this.onFontSizeChanged,
    required this.onFontSizeCommitted,
    required this.selection,
    required this.onSelectionChanged,
    this.tapToSelect = false,
    this.controller,
    this.onRefresh,
    this.initialLine,
  });

  @override
  State<CodeView> createState() => _CodeViewState();
}

/// The longest line drawn whole with wrap off. A minified bundle is one line
/// of a megabyte, and laying that out as a single row would stall the frame;
/// past this the row is cut, and wrap shows the rest.
const _maxUnwrappedChars = 2000;

class _CodeViewState extends State<CodeView> {
  final _vertical = ScrollController();
  final _gutter = ScrollController();
  final _horizontal = ScrollController();

  List<String> _lines = const [];
  int _longest = 0;
  HighlightedCode? _highlight;
  String? _highlightKey;

  /// The row being scrolled to with wrap on, where rows have no fixed height.
  ScrollTarget? _target;

  /// Where the pinch started: the size, and the line at the top.
  double _pinchBase = 0;
  double _pinchTopLine = 0;

  @override
  void initState() {
    super.initState();
    widget.controller?._state = this;
    _vertical.addListener(_followWithGutter);
    _prepare();
    final line = widget.initialLine;
    if (line != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _goToLine(line));
    }
  }

  @override
  void didUpdateWidget(CodeView old) {
    super.didUpdateWidget(old);
    if (old.controller != widget.controller) {
      old.controller?._state = null;
      widget.controller?._state = this;
    }
    // Identity, not equality: a reload the daemon answered with a 304 hands
    // back the very same string, and comparing a megabyte of text to itself
    // character by character is the cost this exists to avoid.
    if (!identical(old.text, widget.text) || old.path != widget.path) {
      _prepare();
    }
    if (old.fontSize != widget.fontSize || old.wrap != widget.wrap) {
      _keepTopLine(old);
    }
  }

  @override
  void dispose() {
    if (widget.controller?._state == this) widget.controller?._state = null;
    _vertical.dispose();
    _gutter.dispose();
    _horizontal.dispose();
    super.dispose();
  }

  void _followWithGutter() {
    if (_gutter.hasClients && _gutter.offset != _vertical.offset) {
      _gutter.jumpTo(_vertical.offset);
    }
  }

  /// Splits the text and starts the highlight. Runs when the content changes,
  /// never on a rebuild.
  void _prepare() {
    final lines = widget.text.split('\n');
    var longest = 0;
    for (var i = 0; i < lines.length; i++) {
      final line = lines[i];
      // A file written on Windows: the carriage return is not something to
      // draw, and it sits past every span the highlighter reports.
      if (line.endsWith('\r')) lines[i] = line.substring(0, line.length - 1);
      final width = lines[i].length + 3 * '\t'.allMatches(lines[i]).length;
      if (width > longest) longest = width;
    }
    _lines = lines;
    _longest = math.min(longest, _maxUnwrappedChars);
    _highlight = null;
    _highlightKey = null;

    final language = languageForPath(widget.path);
    if (language == null) return;
    final key = highlightKey(widget.path, widget.etag, language, widget.text);
    _highlightKey = key;
    final cached = highlightCache.get(key);
    if (cached != null) {
      _highlight = cached;
      return;
    }
    highlightRunner(HighlightRequest(widget.text, language)).then((result) {
      if (result == null) return;
      highlightCache.put(key, result);
      if (!mounted || _highlightKey != key) return;
      setState(() => _highlight = result);
    });
  }

  double get _rowExtent => (widget.fontSize * 5 / 3).roundToDouble();

  TextStyle get _codeStyle => TextStyle(
    fontSize: widget.fontSize,
    fontFamily: 'monospace',
    height: 1.25,
    color: Theme.of(context).colorScheme.onSurface,
    // A reader of code must see the characters that are there: no `≠` for
    // `!=`, in a face that would draw one.
    fontFeatures: const [
      FontFeature.disable('calt'),
      FontFeature.disable('liga'),
    ],
  );

  double _charWidth(TextStyle style) {
    final painter = TextPainter(
      text: TextSpan(text: 'M', style: style),
      textDirection: TextDirection.ltr,
    )..layout();
    final width = painter.width;
    painter.dispose();
    return width;
  }

  /// Puts back the line that was at the top before the size or the wrap
  /// changed. Exact with wrap off, where every row is the same height; with
  /// wrap on the rows have no fixed height, so a size change scales the
  /// offset, which is close enough to keep the reader's place.
  void _keepTopLine(CodeView old) {
    if (!_vertical.hasClients) return;
    if (old.wrap && widget.wrap) {
      _jumpAfterFrame(_vertical.offset * widget.fontSize / old.fontSize);
      return;
    }
    final oldExtent = (old.fontSize * 5 / 3).roundToDouble();
    final pinching = _pinchBase > 0 && old.fontSize != widget.fontSize;
    final topLine = pinching && !old.wrap
        ? _pinchTopLine
        : _vertical.offset / oldExtent;
    if (!widget.wrap) {
      _jumpAfterFrame(topLine * _rowExtent);
      return;
    }
    WidgetsBinding.instance.addPostFrameCallback(
      (_) => _showIndex(topLine.floor(), alignment: 0),
    );
  }

  void _jumpAfterFrame(double offset) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_vertical.hasClients) return;
      final position = _vertical.position;
      _vertical.jumpTo(
        offset.clamp(position.minScrollExtent, position.maxScrollExtent),
      );
    });
  }

  Future<void> _goToLine(int line) async {
    if (_lines.isEmpty) return;
    await _showIndex((line - 1).clamp(0, _lines.length - 1), alignment: 0.3);
  }

  Future<void> _showIndex(int index, {required double alignment}) async {
    if (!_vertical.hasClients || !mounted) return;
    if (!widget.wrap) {
      final position = _vertical.position;
      final offset =
          index * _rowExtent - position.viewportDimension * alignment;
      _vertical.jumpTo(
        offset.clamp(position.minScrollExtent, position.maxScrollExtent),
      );
      return;
    }
    final target = ScrollTarget(index);
    setState(() => _target = target);
    await WidgetsBinding.instance.endOfFrame;
    if (!mounted) return;
    await scrollToTarget(
      controller: _vertical,
      target: target,
      count: _lines.length,
      alignment: alignment,
    );
  }

  void _onTap(int line) {
    if (!widget.tapToSelect && widget.selection == null) return;
    widget.onSelectionChanged(nextSelection(widget.selection, line));
  }

  void _onLongPress(int line) {
    HapticFeedback.selectionClick();
    final current = widget.selection;
    // A long press inside a range already picked leaves it alone: it is the
    // gesture that starts a pick, not one that throws one away.
    if (current != null && current.contains(line) && current.count > 1) return;
    widget.onSelectionChanged(
      current == null || current.contains(line)
          ? LineSelection(line, line)
          : LineSelection(
              math.min(line, current.start),
              math.max(line, current.end),
            ),
    );
  }

  void _onScaleStart(ScaleStartDetails details) {
    _pinchBase = widget.fontSize;
    _pinchTopLine = _vertical.hasClients ? _vertical.offset / _rowExtent : 0;
  }

  void _onScaleUpdate(ScaleUpdateDetails details) {
    if (details.pointerCount < 2 || _pinchBase == 0) return;
    widget.onFontSizeChanged(_pinchBase * details.scale);
  }

  void _onScaleEnd(ScaleEndDetails details) {
    if (_pinchBase == 0) return;
    _pinchBase = 0;
    widget.onFontSizeCommitted();
  }

  /// The colours the highlight's class names map to, for this brightness.
  Map<String, TextStyle> get _themeMap =>
      Theme.of(context).brightness == Brightness.dark
      ? atomOneDarkTheme
      : atomOneLightTheme;

  /// A tab as four spaces. Text layout draws a tab as almost nothing, which
  /// flattens Go and Makefiles; expanding after the spans are cut keeps the
  /// highlighter's offsets, which count the tab as one character, valid.
  static String _tabs(String text) =>
      text.contains('\t') ? text.replaceAll('\t', '    ') : text;

  /// One line as spans. Built per visible row, which is a screenful at most.
  TextSpan _lineSpan(int index, TextStyle base, {required bool cut}) {
    var text = _lines[index];
    if (cut && text.length > _maxUnwrappedChars) {
      text = '${text.substring(0, _maxUnwrappedChars)}…';
    }
    final highlight = _highlight;
    if (highlight == null || index >= highlight.lines.length) {
      return TextSpan(text: _tabs(text), style: base);
    }
    final spans = highlight.lines[index];
    final theme = _themeMap;
    final children = <TextSpan>[];
    var at = 0;
    for (var i = 0; i + 2 < spans.length; i += 3) {
      final start = math.min(spans[i], text.length);
      final end = math.min(spans[i + 1], text.length);
      if (start > at) {
        children.add(TextSpan(text: _tabs(text.substring(at, start))));
      }
      if (end > start) {
        final style = theme[highlight.classes[spans[i + 2]]];
        children.add(
          TextSpan(
            text: _tabs(text.substring(start, end)),
            // Colour and weight from the theme; size and face stay the
            // viewer's, so a pinch moves every span together.
            style: style == null
                ? null
                : TextStyle(
                    color: style.color,
                    fontWeight: style.fontWeight,
                    fontStyle: style.fontStyle,
                  ),
          ),
        );
      }
      at = math.max(at, end);
    }
    if (at < text.length) {
      children.add(TextSpan(text: _tabs(text.substring(at))));
    }
    return TextSpan(style: base, children: children);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final codeStyle = _codeStyle.copyWith(
      color: _themeMap['root']?.color ?? theme.colorScheme.onSurface,
    );
    final gutterStyle = TextStyle(
      fontSize: math.max(widget.fontSize - 1, 8),
      fontFamily: 'monospace',
      height: 1.25,
      color: theme.colorScheme.onSurfaceVariant.withAlpha(140),
    );
    final digits = math.max(2, '${_lines.length}'.length);
    final gutterWidth = digits * _charWidth(gutterStyle) + 16;
    final selectedColor = theme.colorScheme.primaryContainer.withAlpha(150);

    final body = widget.wrap
        ? _buildWrapped(codeStyle, gutterStyle, gutterWidth, selectedColor)
        : _buildUnwrapped(codeStyle, gutterStyle, gutterWidth, selectedColor);

    final pinchable = GestureDetector(
      onScaleStart: _onScaleStart,
      onScaleUpdate: _onScaleUpdate,
      onScaleEnd: _onScaleEnd,
      child: body,
    );
    final refresh = widget.onRefresh;
    if (refresh == null) return pinchable;
    return RefreshIndicator(
      onRefresh: refresh,
      // The vertical list sits inside the sideways scroller with wrap off, so
      // its notifications arrive one level deep. Any vertical one counts.
      notificationPredicate: (n) => n.metrics.axis == Axis.vertical,
      child: pinchable,
    );
  }

  Widget _gutterCell(
    int index,
    TextStyle style,
    double width,
    Color selectedColor,
  ) {
    final line = index + 1;
    final selected = widget.selection?.contains(line) ?? false;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: () => _onTap(line),
      onLongPress: () => _onLongPress(line),
      child: Container(
        width: width,
        color: selected ? selectedColor : null,
        alignment: widget.wrap ? Alignment.topRight : Alignment.centerRight,
        padding: const EdgeInsets.only(right: 8),
        child: Text(
          '$line',
          style: selected
              ? style.copyWith(color: Theme.of(context).colorScheme.primary)
              : style,
        ),
      ),
    );
  }

  /// Wrap off: the numbers in a column of their own that follows the code up
  /// and down but never sideways, and the code as one block that scrolls both
  /// ways. Every row has the same height, so the two columns stay in step by
  /// sharing an offset.
  Widget _buildUnwrapped(
    TextStyle codeStyle,
    TextStyle gutterStyle,
    double gutterWidth,
    Color selectedColor,
  ) {
    final extent = _rowExtent;
    final contentWidth = _longest * _charWidth(codeStyle) + 32;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SizedBox(
          width: gutterWidth,
          child: ListView.builder(
            controller: _gutter,
            physics: const NeverScrollableScrollPhysics(),
            itemExtent: extent,
            itemCount: _lines.length,
            itemBuilder: (_, i) =>
                _gutterCell(i, gutterStyle, gutterWidth, selectedColor),
          ),
        ),
        Expanded(
          child: LayoutBuilder(
            builder: (context, constraints) => SingleChildScrollView(
              key: const ValueKey('code-horizontal'),
              controller: _horizontal,
              scrollDirection: Axis.horizontal,
              child: SizedBox(
                width: math.max(contentWidth, constraints.maxWidth),
                child: ListView.builder(
                  controller: _vertical,
                  physics: const AlwaysScrollableScrollPhysics(),
                  itemExtent: extent,
                  itemCount: _lines.length,
                  itemBuilder: (_, i) {
                    final line = i + 1;
                    final selected = widget.selection?.contains(line) ?? false;
                    return GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: () => _onTap(line),
                      onLongPress: () => _onLongPress(line),
                      child: Container(
                        color: selected ? selectedColor : null,
                        alignment: Alignment.centerLeft,
                        padding: const EdgeInsets.only(left: 4, right: 16),
                        child: RichText(
                          text: _lineSpan(i, codeStyle, cut: true),
                          maxLines: 1,
                          softWrap: false,
                          overflow: TextOverflow.clip,
                        ),
                      ),
                    );
                  },
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }

  /// Wrap on: one list, each row as tall as its line needs.
  Widget _buildWrapped(
    TextStyle codeStyle,
    TextStyle gutterStyle,
    double gutterWidth,
    Color selectedColor,
  ) {
    final target = _target;
    return ListView.builder(
      controller: _vertical,
      physics: const AlwaysScrollableScrollPhysics(),
      itemCount: _lines.length,
      itemBuilder: (_, i) {
        target?.built.add(i);
        final line = i + 1;
        final selected = widget.selection?.contains(line) ?? false;
        Widget row = GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: () => _onTap(line),
          onLongPress: () => _onLongPress(line),
          child: Container(
            color: selected ? selectedColor : null,
            constraints: BoxConstraints(minHeight: _rowExtent),
            padding: EdgeInsets.symmetric(vertical: widget.fontSize / 6),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                SizedBox(
                  width: gutterWidth,
                  child: Padding(
                    padding: const EdgeInsets.only(right: 8),
                    child: Text(
                      '$line',
                      textAlign: TextAlign.right,
                      style: selected
                          ? gutterStyle.copyWith(
                              color: Theme.of(context).colorScheme.primary,
                            )
                          : gutterStyle,
                    ),
                  ),
                ),
                Expanded(
                  child: Padding(
                    padding: const EdgeInsets.only(left: 4, right: 8),
                    child: RichText(text: _lineSpan(i, codeStyle, cut: false)),
                  ),
                ),
              ],
            ),
          ),
        );
        if (target != null && target.index == i) {
          row = KeyedSubtree(key: target.key, child: row);
        }
        return row;
      },
    );
  }
}
