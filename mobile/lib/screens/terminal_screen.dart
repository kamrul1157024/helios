import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:xterm/xterm.dart';

import '../models/session.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';
import '../services/terminal/frames.dart';
import '../services/terminal/terminal_connection.dart';

/// The session's live terminal, and any shells opened beside it.
///
/// The phone joins as an observer, so it never votes on the PTY size and
/// cannot reflow a terminal somebody is watching on a desktop. The screen
/// renders whatever geometry the host reports, shrinking the font to fit;
/// claiming the size for this screen is an explicit, warned-about action.
class TerminalScreen extends StatefulWidget {
  const TerminalScreen({super.key, required this.session});

  final Session session;

  @override
  State<TerminalScreen> createState() => _TerminalScreenState();
}

class _TerminalScreenState extends State<TerminalScreen> with WidgetsBindingObserver {
  final Map<String, _TerminalTab> _tabs = {};
  List<TerminalInfo> _terminals = [];
  String? _activeId;
  bool _loading = true;
  bool _opening = false;
  double _fontSize = _defaultFontSize;
  StreamSubscription<SSEEvent>? _eventSub;

  DaemonAPIService? get _sse =>
      context.read<HostManager>().serviceFor(widget.session.hostId);

  _TerminalTab? get _active => _activeId == null ? null : _tabs[_activeId];

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _loadFontSize();
    _load();
    // A shell is the session's, not this client's: one opened on the desktop
    // should appear here without a manual refresh.
    _eventSub = _sse?.events.listen((event) {
      if (event.type != 'terminal_opened' && event.type != 'terminal_closed') return;
      if (event.data is! Map) return;
      if ((event.data as Map)['session_id'] != widget.session.sessionId) return;
      _load();
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _eventSub?.cancel();
    for (final tab in _tabs.values) {
      tab.dispose();
    }
    super.dispose();
  }

  /// A live stream is the most expensive thing this app does. Backgrounded, it
  /// buys nothing: the screen is not on, and the sequence number means the
  /// reconnect resumes where it left off rather than resyncing.
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      for (final tab in _tabs.values) {
        tab.connect();
      }
    } else if (state == AppLifecycleState.paused) {
      for (final tab in _tabs.values) {
        tab.detach();
      }
    }
  }

  Future<void> _loadFontSize() async {
    final prefs = await SharedPreferences.getInstance();
    final saved = prefs.getDouble(_fontSizeKey);
    if (saved != null && mounted) setState(() => _fontSize = saved);
  }

  Future<void> _setFontSize(double size) async {
    setState(() => _fontSize = size.clamp(7.0, 20.0));
    final prefs = await SharedPreferences.getInstance();
    await prefs.setDouble(_fontSizeKey, _fontSize);
  }

  Future<void> _load() async {
    final terminals = await _sse?.fetchTerminals(widget.session.sessionId) ?? [];
    if (!mounted) return;

    // Anything gone from the daemon's list is gone for good; keep the rest
    // connected rather than tearing down a terminal that is still there.
    final live = terminals.map((t) => t.id).toSet();
    for (final id in _tabs.keys.where((id) => !live.contains(id)).toList()) {
      _tabs.remove(id)?.dispose();
    }

    setState(() {
      _terminals = terminals;
      _loading = false;
      if (_activeId == null || !live.contains(_activeId)) {
        _activeId = terminals.isNotEmpty ? terminals.first.id : null;
      }
    });
    if (_activeId != null) _tabFor(_activeId!);
  }

  _TerminalTab _tabFor(String terminalId) {
    final existing = _tabs[terminalId];
    if (existing != null) return existing;

    final service = _sse;
    final tab = _TerminalTab(
      terminalId: terminalId,
      serverUrl: service?.serverUrl ?? '',
      getToken: service?.getToken ?? () async => '',
      onChanged: () {
        if (mounted) setState(() {});
      },
    );
    _tabs[terminalId] = tab;
    tab.connect();
    return tab;
  }

  Future<void> _openShell() async {
    setState(() => _opening = true);
    final shell = await _sse?.openShell(widget.session.sessionId);
    if (!mounted) return;
    setState(() {
      _opening = false;
      if (shell != null) {
        _terminals = [..._terminals, shell];
        _activeId = shell.id;
      }
    });
    if (shell == null) {
      _toast('Could not open a shell');
      return;
    }
    _tabFor(shell.id);
  }

  Future<void> _closeShell(TerminalInfo terminal) async {
    final ok = await _sse?.closeTerminal(terminal.id) ?? false;
    if (!mounted) return;
    if (!ok) {
      _toast('Could not close that shell');
      return;
    }
    _tabs.remove(terminal.id)?.dispose();
    setState(() {
      _terminals = _terminals.where((t) => t.id != terminal.id).toList();
      if (_activeId == terminal.id) {
        _activeId = _terminals.isNotEmpty ? _terminals.first.id : null;
      }
    });
  }

  /// Takes over size negotiation for this terminal. The host adopts the
  /// smallest size any interactive viewer asks for, so this reflows the
  /// terminal for everyone else watching — hence the confirmation.
  /// Hands the size to this screen, or back. Fitting reflows the terminal for
  /// everyone watching it, which is why handing back is one tap and taking it
  /// again asks first.
  Future<void> _claimSize() async {
    final tab = _active;
    if (tab == null) return;

    if (tab.interactive) {
      tab.releaseSize();
      setState(() {});
      return;
    }

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Fit to this screen?'),
        content: const Text(
          'The terminal resizes for everyone watching it, including the desktop '
          'and anyone attached in a terminal.',
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('Fit')),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    tab.claimSize();
    setState(() {});
  }

  /// Reading a terminal on a phone is a trade between how much of the screen
  /// you can see and whether you can read it, and where that lands depends on
  /// the eyes and the phone. So it is a setting, and it persists.
  void _showFontSizeSheet() {
    showModalBottomSheet<void>(
      context: context,
      builder: (ctx) => SafeArea(
        child: StatefulBuilder(
          builder: (ctx, setSheetState) => Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
                child: Row(
                  children: [
                    Text('Text size', style: Theme.of(ctx).textTheme.titleSmall),
                    const Spacer(),
                    Text('${_fontSize.toStringAsFixed(0)} pt'),
                  ],
                ),
              ),
              Slider(
                value: _fontSize,
                min: 7,
                max: 20,
                divisions: 13,
                label: _fontSize.toStringAsFixed(0),
                onChanged: (value) {
                  setSheetState(() {});
                  _setFontSize(value);
                },
              ),
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                child: Text(
                  'Smaller text shows more of the terminal at once.',
                  style: TextStyle(
                    fontSize: 12,
                    color: Theme.of(ctx).colorScheme.onSurfaceVariant,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _toast(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message), duration: const Duration(seconds: 3)),
    );
  }

  @override
  Widget build(BuildContext context) {
    final tab = _active;
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.session.displayTitle, overflow: TextOverflow.ellipsis),
        actions: [
          if (tab != null)
            IconButton(
              icon: Icon(tab.interactive ? Icons.fit_screen : Icons.fit_screen_outlined,
                  color: tab.interactive ? Theme.of(context).colorScheme.primary : null),
              tooltip: tab.interactive ? 'Hand the size back' : 'Fit to this screen',
              onPressed: _claimSize,
            ),
          IconButton(
            icon: const Icon(Icons.text_fields),
            tooltip: 'Text size',
            onPressed: _showFontSizeSheet,
          ),
          IconButton(
            icon: _opening
                ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                : const Icon(Icons.add),
            tooltip: 'Open a shell',
            onPressed: _opening ? null : _openShell,
          ),
        ],
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _terminals.isEmpty
              ? _buildCold()
              : Column(
                  children: [
                    _buildTabs(),
                    if (tab != null) _buildStatusLine(tab),
                    Expanded(child: tab == null ? const SizedBox.shrink() : _buildTerminal(tab)),
                    if (tab != null) _KeyBar(tab: tab),
                  ],
                ),
    );
  }

  Widget _buildCold() => Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.terminal, size: 48, color: Theme.of(context).colorScheme.onSurfaceVariant),
              const SizedBox(height: 12),
              const Text('This session has no live terminal.', textAlign: TextAlign.center),
              const SizedBox(height: 4),
              Text(
                'Send it a prompt to wake it, or open a shell in its directory.',
                textAlign: TextAlign.center,
                style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
              ),
              const SizedBox(height: 16),
              FilledButton.tonalIcon(
                onPressed: _opening ? null : _openShell,
                icon: const Icon(Icons.add),
                label: const Text('Open a shell'),
              ),
            ],
          ),
        ),
      );

  Widget _buildTabs() {
    if (_terminals.length < 2) return const SizedBox.shrink();
    final theme = Theme.of(context);
    return SizedBox(
      height: 40,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
        itemCount: _terminals.length,
        separatorBuilder: (_, _) => const SizedBox(width: 6),
        itemBuilder: (context, index) {
          final terminal = _terminals[index];
          final selected = terminal.id == _activeId;
          return InputChip(
            selected: selected,
            showCheckmark: false,
            avatar: Icon(terminal.isAgent ? Icons.smart_toy_outlined : Icons.terminal, size: 16),
            label: Text(terminal.isAgent ? 'agent' : 'shell $index'),
            labelStyle: TextStyle(fontSize: 12, color: selected ? theme.colorScheme.primary : null),
            onPressed: () {
              setState(() => _activeId = terminal.id);
              _tabFor(terminal.id);
            },
            // The agent's host belongs to the session, which has stop and
            // terminate of its own; only shells are the user's to close here.
            onDeleted: terminal.isAgent ? null : () => _closeShell(terminal),
          );
        },
      ),
    );
  }

  Widget _buildStatusLine(_TerminalTab tab) {
    final theme = Theme.of(context);
    final status = tab.status;
    final parts = <String>[
      switch (tab.state) {
        LinkState.connecting => 'connecting',
        LinkState.reconnecting => 'reconnecting',
        LinkState.closed => tab.closeReason ?? 'closed',
        LinkState.live => tab.interactive ? 'fitted to this screen' : 'watching',
      },
      if (status != null) '${status.cols}×${status.rows}',
      if (status != null && status.viewers > 1) '${status.viewers} viewers',
      if (status?.writer != null && status!.writer!.isNotEmpty) 'typing: ${status.writer}',
    ];
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      color: theme.colorScheme.surfaceContainerHighest,
      child: Text(
        parts.join(' · '),
        style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
      ),
    );
  }

  Widget _buildTerminal(_TerminalTab tab) {
    return ColoredBox(
      // A terminal is a black rectangle. Letting the app's backdrop through
      // made it look like the screen had failed to load.
      color: const Color(0xFF10101A),
      child: LayoutBuilder(
        builder: (context, constraints) {
          if (tab.interactive) {
            // The host has taken this screen's geometry, so everything fits and
            // the only job left is to keep the type readable.
            tab.fitTo(constraints.biggest, _fontSize);
            return _view(tab, constraints.maxWidth);
          }

          // Observing a desktop means inheriting its columns — 191 of them is
          // normal. Shrinking type until they fit produces something no one can
          // read, so the type stays legible and the screen pans instead.
          final width = (tab.status?.cols ?? tab.cols) * _fontSize * _cellRatio + 12;
          return SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: _view(tab, width < constraints.maxWidth ? constraints.maxWidth : width),
          );
        },
      ),
    );
  }

  Widget _view(_TerminalTab tab, double width) => SizedBox(
        width: width,
        child: TerminalView(
          tab.terminal,
          controller: tab.controller,
          textStyle: TerminalStyle(fontSize: _fontSize, fontFamily: _fontFamily),
          padding: const EdgeInsets.all(6),
          // The host owns the size; letting the view resize the emulator would
          // fight whatever geometry the Status frame just reported.
          autoResize: false,
          backgroundOpacity: 0,
        ),
      );

  /// Bundled rather than the platform's "monospace": Android maps that to
  /// Droid Sans Mono, whose box-drawing characters do not join, and a TUI is
  /// mostly box-drawing characters.
  static const String _fontFamily = 'JetBrainsMono';

  /// Small enough to get a useful number of columns on screen, large enough to
  /// read without zooming.
  static const double _defaultFontSize = 11;

  static const String _fontSizeKey = 'terminal.font_size';

  /// Advance width of one cell as a fraction of the font size, for sizing the
  /// scrollable. JetBrains Mono is a 0.6 em face.
  static const double _cellRatio = 0.6;
}

/// One terminal's emulator, connection and decoder.
class _TerminalTab {
  _TerminalTab({
    required this.terminalId,
    required this.serverUrl,
    required this.getToken,
    required this.onChanged,
  }) {
    terminal = Terminal(maxLines: 4000);
    terminal.onOutput = (data) {
      // The soft keyboard has no Ctrl, so the key bar arms it and the next
      // character it produces is folded into a control code here. Doing it in
      // the key bar would miss everything typed on the keyboard itself, which
      // is where the letter of a Ctrl chord actually comes from.
      if (ctrlArmed && data.length == 1) {
        final code = data.codeUnitAt(0) | 0x20;
        if (code >= 0x61 && code <= 0x7a) {
          ctrlArmed = false;
          onChanged();
          send(String.fromCharCode(code - 0x60));
          return;
        }
      }
      _connection?.send(Uint8List.fromList(utf8.encode(data)));
    };
  }

  final String terminalId;
  final String serverUrl;
  final Future<String> Function() getToken;
  final VoidCallback onChanged;

  late final Terminal terminal;
  final controller = TerminalController();

  TerminalConnection? _connection;
  StreamSubscription<Uint8List>? _outputSub;
  StreamSubscription<void>? _snapshotSub;
  StreamSubscription<TerminalStatus>? _statusSub;
  StreamSubscription<LinkState>? _stateSub;

  /// Output arrives in frames, and a multibyte character can straddle two of
  /// them. A chunked decoder holds the tail until the rest turns up; decoding
  /// each frame alone would litter the screen with replacement characters.
  ByteConversionSink? _decoder;

  ByteConversionSink _newDecoder() =>
      utf8.decoder.startChunkedConversion(_TerminalSink(terminal));

  LinkState state = LinkState.connecting;
  TerminalStatus? status;
  String? closeReason;
  /// Whether this viewer votes on the PTY size. On by default: a phone
  /// showing a desktop's 180 columns is unreadable, and the terminal is here
  /// to be used rather than watched. Handing it back leaves the phone an
  /// observer, which never disturbs anyone else's geometry.
  bool interactive = true;

  /// Ctrl is a modifier a phone keyboard does not have: the key bar arms it,
  /// and the next character consumes it.
  bool ctrlArmed = false;
  int cols = 80;
  int rows = 24;

  void connect() {
    if (_connection != null) return;

    _decoder = _newDecoder();

    final connection = TerminalConnection(
      serverUrl: serverUrl,
      terminalId: terminalId,
      getToken: getToken,
      cols: cols,
      rows: rows,
      role: interactive ? Role.interactive : Role.observer,
    );
    _connection = connection;

    _outputSub = connection.output.listen((bytes) => _decoder?.add(bytes));
    _snapshotSub = connection.snapshots.listen((_) {
      // A snapshot is a full repaint of the host's screen; anything held back
      // mid-character belongs to the stream it replaces.
      _decoder?.close();
      _decoder = _newDecoder();
    });
    _statusSub = connection.status.listen((next) {
      status = next;
      if (!interactive && next.cols > 0 && next.rows > 0) {
        cols = next.cols;
        rows = next.rows;
        terminal.resize(next.cols, next.rows);
      }
      onChanged();
    });
    _stateSub = connection.states.listen((next) {
      state = next;
      closeReason = connection.closeReason;
      onChanged();
    });

    connection.start();
  }

  /// Drops the socket without forgetting where we were, so a reconnect resumes
  /// from the same sequence.
  void detach() {
    _cancelSubs();
    _connection?.dispose();
    _connection = null;
  }

  void claimSize() {
    interactive = true;
    // Role is fixed at Hello, so switching sides means a fresh connection.
    detach();
    connect();
  }

  void releaseSize() {
    interactive = false;
    // 0×0 withdraws the vote without disturbing the PTY, which is what the
    // desktop does for a background tab.
    _connection?.resize(0, 0);
    detach();
    connect();
  }

  /// Reports this screen's geometry while interactive, so the host shrinks to
  /// what the phone can actually show.
  void fitTo(Size size, double fontSize) {
    final nextCols = ((size.width - 12) / (fontSize * 0.6)).floor().clamp(20, 200);
    final nextRows = ((size.height - 12) / (fontSize * 1.35)).floor().clamp(6, 100);
    if (nextCols == cols && nextRows == rows) return;
    cols = nextCols;
    rows = nextRows;
    terminal.resize(nextCols, nextRows);
    _connection?.resize(nextCols, nextRows);
  }

  void send(String data) => _connection?.send(Uint8List.fromList(utf8.encode(data)));

  void key(TerminalKey key, {bool ctrl = false, bool shift = false, bool alt = false}) =>
      terminal.keyInput(key, ctrl: ctrl, shift: shift, alt: alt);

  Future<void> pasteFromClipboard() async {
    final data = await Clipboard.getData(Clipboard.kTextPlain);
    final text = data?.text;
    if (text == null || text.isEmpty) return;
    _connection?.paste(text);
  }

  void _cancelSubs() {
    _outputSub?.cancel();
    _snapshotSub?.cancel();
    _statusSub?.cancel();
    _stateSub?.cancel();
    _outputSub = null;
    _snapshotSub = null;
    _statusSub = null;
    _stateSub = null;
  }

  void dispose() {
    detach();
    _decoder?.close();
    controller.dispose();
  }
}

/// Feeds decoded text straight to the emulator.
///
/// Not StringConversionSink.withCallback: that one buffers everything and
/// calls back on close, so a live stream renders nothing at all.
class _TerminalSink implements Sink<String> {
  _TerminalSink(this.terminal);

  final Terminal terminal;

  @override
  void add(String data) => terminal.write(data);

  @override
  void close() {}
}

/// The keys a phone keyboard does not have and Claude Code cannot be driven
/// without: Escape to interrupt, Shift+Tab to cycle permission modes, arrows
/// for every menu it draws.
class _KeyBar extends StatefulWidget {
  const _KeyBar({required this.tab});

  final _TerminalTab tab;

  @override
  State<_KeyBar> createState() => _KeyBarState();
}

class _KeyBarState extends State<_KeyBar> {
  void _tap(TerminalKey key) {
    final ctrl = widget.tab.ctrlArmed;
    widget.tab.key(key, ctrl: ctrl);
    if (ctrl) setState(() => widget.tab.ctrlArmed = false);
  }

  /// A chord the bar sends outright, rather than making the user arm Ctrl and
  /// then find the letter. ^C interrupts, and the rest are what a shell needs.
  void _chord(int letter) {
    widget.tab.send(String.fromCharCode(letter - 0x60));
    if (widget.tab.ctrlArmed) setState(() => widget.tab.ctrlArmed = false);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: theme.colorScheme.surfaceContainerHigh,
      child: SafeArea(
        top: false,
        child: SizedBox(
          height: 44,
          child: ListView(
            scrollDirection: Axis.horizontal,
            padding: const EdgeInsets.symmetric(horizontal: 6),
            children: [
              _key('esc', () => _tap(TerminalKey.escape)),
              _key('tab', () => _tap(TerminalKey.tab)),
              _key('⇧tab', () => widget.tab.key(TerminalKey.tab, shift: true)),
              _key(
                'ctrl',
                () => setState(() => widget.tab.ctrlArmed = !widget.tab.ctrlArmed),
                active: widget.tab.ctrlArmed,
              ),
              _key('^C', () => _chord(0x63)),
              _key('^D', () => _chord(0x64)),
              _key('^Z', () => _chord(0x7a)),
              _key('^L', () => _chord(0x6c)),
              _key('↑', () => _tap(TerminalKey.arrowUp)),
              _key('↓', () => _tap(TerminalKey.arrowDown)),
              _key('←', () => _tap(TerminalKey.arrowLeft)),
              _key('→', () => _tap(TerminalKey.arrowRight)),
              _key('home', () => _tap(TerminalKey.home)),
              _key('end', () => _tap(TerminalKey.end)),
              _key('⏎', () => _tap(TerminalKey.enter)),
              _key('paste', widget.tab.pasteFromClipboard),
            ],
          ),
        ),
      ),
    );
  }

  Widget _key(String label, VoidCallback onTap, {bool active = false}) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 3, vertical: 6),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          alignment: Alignment.center,
          constraints: const BoxConstraints(minWidth: 44),
          padding: const EdgeInsets.symmetric(horizontal: 10),
          decoration: BoxDecoration(
            color: active ? theme.colorScheme.primaryContainer : theme.colorScheme.surface,
            borderRadius: BorderRadius.circular(6),
          ),
          child: Text(
            label,
            style: TextStyle(
              fontSize: 13,
              color: active ? theme.colorScheme.onPrimaryContainer : theme.colorScheme.onSurface,
            ),
          ),
        ),
      ),
    );
  }
}
