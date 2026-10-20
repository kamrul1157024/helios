import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
// Both packages export Provider, ChangeNotifierProvider and Consumer.
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../providers/daemon_providers.dart';
import '../providers/desktop_providers.dart';
import '../services/daemon_api_service.dart';
import '../services/desktop_bridge.dart';
import '../utils/keysyms.dart';
import '../widgets/desktop_display_sheet.dart';
import '../widgets/desktop_key_bar.dart';

/// Makes the page's bridge. Tests replace it, because a widget test has no
/// WebView to put the page in.
final desktopBridgeFactoryProvider = rp.Provider<DesktopBridge Function()>(
  (ref) => WebViewDesktopBridge.new,
);

/// The host's desktop (spec 66, D8): the install and start progress until it
/// is ready, then the desktop itself.
///
/// It belongs to the host rather than to one session, so it is opened from the
/// host's page and from a session's menu, and both land here.
class DesktopScreen extends rp.ConsumerWidget {
  final String hostId;

  const DesktopScreen({super.key, required this.hostId});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final label = ref.watch(hostManagerProvider).hostById(hostId)?.label;
    final title = Text(label == null ? 'Desktop' : 'Desktop · $label');
    final async = ref.watch(desktopProvider(hostId));
    final status = async.valueOrNull;

    if (status != null && !status.enabled) {
      return Scaffold(
        appBar: AppBar(title: title),
        body: DesktopTurnedOffView(hostId: hostId),
      );
    }
    if (status != null && status.phase == DesktopPhase.ready) {
      return DesktopViewer(hostId: hostId, status: status, title: title);
    }

    final Widget body;
    if (async.hasError && status == null) {
      body = _Notice(
        icon: Icons.cloud_off_outlined,
        title: 'Could not reach the host.',
        action: FilledButton.tonal(
          onPressed: () => ref.invalidate(desktopProvider(hostId)),
          child: const Text('Try again'),
        ),
      );
    } else if (async.isLoading && status == null) {
      body = const Center(child: CircularProgressIndicator());
    } else if (status == null) {
      body = const _Notice(
        icon: Icons.update,
        title: 'This host runs an older Helios.',
        detail: 'Update Helios on the host to use its desktop from here.',
      );
    } else {
      body = DesktopSetupView(hostId: hostId, status: status);
    }
    return Scaffold(
      appBar: AppBar(title: title),
      body: body,
    );
  }
}

/// Everything before the desktop is ready: install, progress, the command to
/// run by hand, and what went wrong.
class DesktopSetupView extends rp.ConsumerStatefulWidget {
  final String hostId;
  final DesktopStatus status;

  const DesktopSetupView({
    super.key,
    required this.hostId,
    required this.status,
  });

  @override
  rp.ConsumerState<DesktopSetupView> createState() => _DesktopSetupViewState();
}

class _DesktopSetupViewState extends rp.ConsumerState<DesktopSetupView> {
  bool _busy = false;

  /// Set when the host refused to install because it cannot get root, which
  /// a failed install's Retry can find out before the state says so.
  bool _needsRoot = false;

  Future<void> _install() async {
    final service = ref.read(serviceProvider(widget.hostId));
    if (service == null) return;
    setState(() => _busy = true);
    try {
      await service.installDesktop();
    } on DesktopActionException catch (e) {
      if (!mounted) return;
      if (e.code == 'needs_root') {
        setState(() => _needsRoot = true);
      } else if (e.disabled) {
        ref.read(desktopProvider(widget.hostId).notifier).markTurnedOff();
        return;
      } else if (e.code != 'busy') {
        _say(e.words);
      }
    } catch (_) {
      if (mounted) _say('Could not reach the host.');
    }
    if (!mounted) return;
    // The events carry the progress, but a refetch costs one request and
    // covers a stream that is down.
    ref.invalidate(desktopProvider(widget.hostId));
    setState(() => _busy = false);
  }

  void _say(String text) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));

  @override
  Widget build(BuildContext context) {
    final s = widget.status;
    final theme = Theme.of(context);
    final children = <Widget>[];

    switch (s.phase) {
      case DesktopPhase.unsupported:
        return const _Notice(
          icon: Icons.desktop_access_disabled_outlined,
          title: 'Remote desktop needs a Linux host.',
          detail: 'This host runs macOS or Windows.',
        );
      case DesktopPhase.missing when !s.canInstall || _needsRoot:
        children.addAll([
          _heading(theme, 'Helios needs root once to install the desktop.'),
          const SizedBox(height: 8),
          Text(
            'Run this in a terminal on the host. It asks for your password, '
            'then installs xrdp, XFCE and guacd.',
            style: theme.textTheme.bodyMedium,
          ),
          const SizedBox(height: 16),
          _CommandBox(command: s.installCommand),
          const SizedBox(height: 16),
          OutlinedButton(
            onPressed: () {
              setState(() => _needsRoot = false);
              ref.invalidate(desktopProvider(widget.hostId));
            },
            child: const Text('Check again'),
          ),
        ]);
      case DesktopPhase.missing:
        children.addAll([
          _heading(theme, 'This host has no desktop yet.'),
          const SizedBox(height: 8),
          Text(
            'Helios can install one. Agents then draw their windows on it, '
            'and you can watch them here.',
            style: theme.textTheme.bodyMedium,
          ),
          if (s.missing.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(
              'Missing: ${s.missing.map(_component).join(', ')}',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ],
          const SizedBox(height: 20),
          FilledButton.icon(
            onPressed: _busy ? null : _install,
            icon: const Icon(Icons.download),
            label: const Text('Install'),
          ),
        ]);
      case DesktopPhase.installing:
        final done = s.steps.where((x) => x.finished).length;
        children.addAll([
          _heading(theme, 'Installing the desktop'),
          const SizedBox(height: 12),
          LinearProgressIndicator(
            value: s.steps.isEmpty ? null : done / s.steps.length,
          ),
        ]);
      case DesktopPhase.starting:
        children.addAll([
          Row(
            children: [
              const SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
              const SizedBox(width: 12),
              Expanded(child: _heading(theme, 'Starting the desktop…')),
            ],
          ),
        ]);
      case DesktopPhase.failed:
        children.addAll([
          Row(
            children: [
              Icon(Icons.error_outline, color: theme.colorScheme.error),
              const SizedBox(width: 12),
              Expanded(child: _heading(theme, 'The desktop could not start.')),
            ],
          ),
          // The state's error is written for people (the contract says so),
          // unlike an HTTP refusal's text. The failed step and its last line
          // sit beneath it in the list, as the detail.
          if (s.error.isNotEmpty) ...[
            const SizedBox(height: 12),
            SelectableText(s.error, style: theme.textTheme.bodyMedium),
          ],
          const SizedBox(height: 20),
          FilledButton.icon(
            onPressed: _busy ? null : _install,
            icon: const Icon(Icons.refresh),
            label: const Text('Retry'),
          ),
          if (_needsRoot) ...[
            const SizedBox(height: 20),
            Text(
              'Helios needs root to try again. Run this in a terminal on '
              'the host:',
              style: theme.textTheme.bodyMedium,
            ),
            const SizedBox(height: 12),
            _CommandBox(command: s.installCommand),
          ],
        ]);
      case DesktopPhase.ready:
        break;
    }

    final showSteps =
        s.steps.isNotEmpty &&
        (s.phase != DesktopPhase.missing ||
            s.steps.any((x) => x.state != 'pending'));
    return ListView(
      padding: const EdgeInsets.all(24),
      children: [
        ...children,
        if (showSteps) ...[
          const SizedBox(height: 24),
          DesktopStepList(steps: s.steps),
        ],
      ],
    );
  }

  Widget _heading(ThemeData theme, String text) =>
      Text(text, style: theme.textTheme.titleMedium);

  static String _component(String id) => switch (id) {
    'xfce' => 'XFCE',
    'xrdp' => 'xrdp',
    'guacd' => 'guacd',
    _ => id,
  };
}

/// The host's owner turned remote desktop off. One button turns it back on
/// and the screen carries on from whatever state the host answers with.
class DesktopTurnedOffView extends rp.ConsumerStatefulWidget {
  final String hostId;

  const DesktopTurnedOffView({super.key, required this.hostId});

  @override
  rp.ConsumerState<DesktopTurnedOffView> createState() =>
      _DesktopTurnedOffViewState();
}

class _DesktopTurnedOffViewState
    extends rp.ConsumerState<DesktopTurnedOffView> {
  bool _busy = false;

  Future<void> _turnOn() async {
    setState(() => _busy = true);
    String? failure;
    try {
      await ref.read(desktopProvider(widget.hostId).notifier).enable();
    } on DesktopActionException catch (e) {
      failure = e.words;
    } catch (_) {
      failure = 'Could not reach the host.';
    }
    if (!mounted) return;
    setState(() => _busy = false);
    if (failure != null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(failure)));
    }
  }

  @override
  Widget build(BuildContext context) => _Notice(
    icon: Icons.desktop_access_disabled_outlined,
    title: 'Remote desktop is turned off on this host.',
    detail: 'Turn it on to install the desktop or to watch it from here.',
    action: FilledButton(
      onPressed: _busy ? null : _turnOn,
      child: const Text('Turn on'),
    ),
  );
}

/// The install's steps, each with how far it got.
class DesktopStepList extends StatelessWidget {
  final List<DesktopStep> steps;

  const DesktopStepList({super.key, required this.steps});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (final step in steps)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 6),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                SizedBox(width: 24, height: 24, child: _icon(theme, step)),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        step.state == 'skipped'
                            ? '${step.label} — already done'
                            : step.label,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: step.state == 'pending'
                              ? theme.colorScheme.onSurfaceVariant
                              : null,
                        ),
                      ),
                      if (step.line.isNotEmpty &&
                          (step.state == 'running' || step.state == 'failed'))
                        Padding(
                          padding: const EdgeInsets.only(top: 2),
                          child: Text(
                            step.line,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              fontFamily: 'monospace',
                              fontSize: 11,
                              color: step.state == 'failed'
                                  ? theme.colorScheme.error
                                  : theme.colorScheme.onSurfaceVariant,
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ],
            ),
          ),
      ],
    );
  }

  Widget _icon(ThemeData theme, DesktopStep step) {
    final muted = theme.colorScheme.onSurfaceVariant;
    return switch (step.state) {
      'running' => const Padding(
        padding: EdgeInsets.all(3),
        child: CircularProgressIndicator(strokeWidth: 2),
      ),
      'done' => const Icon(Icons.check_circle, color: Colors.green),
      'skipped' => Icon(Icons.check_circle_outline, color: muted),
      'failed' => Icon(Icons.error, color: theme.colorScheme.error),
      _ => Icon(Icons.radio_button_unchecked, color: muted),
    };
  }
}

class _CommandBox extends StatelessWidget {
  final String command;

  const _CommandBox({required this.command});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.only(left: 12),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Expanded(
            child: SelectableText(
              command,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 14),
            ),
          ),
          IconButton(
            icon: const Icon(Icons.copy),
            tooltip: 'Copy',
            onPressed: () async {
              await Clipboard.setData(ClipboardData(text: command));
              if (!context.mounted) return;
              ScaffoldMessenger.of(
                context,
              ).showSnackBar(const SnackBar(content: Text('Copied')));
            },
          ),
        ],
      ),
    );
  }
}

class _Notice extends StatelessWidget {
  final IconData icon;
  final String title;
  final String? detail;
  final Widget? action;

  const _Notice({
    required this.icon,
    required this.title,
    this.detail,
    this.action,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 48, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(height: 16),
            Text(
              title,
              textAlign: TextAlign.center,
              style: theme.textTheme.titleMedium,
            ),
            if (detail != null) ...[
              const SizedBox(height: 8),
              Text(
                detail!,
                textAlign: TextAlign.center,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ],
            if (action != null) ...[const SizedBox(height: 20), action!],
          ],
        ),
      ),
    );
  }
}

/// The desktop itself: the daemon's page in a WebView, the key bar under it,
/// and the screen-size sheet behind a button.
class DesktopViewer extends rp.ConsumerStatefulWidget {
  final String hostId;
  final DesktopStatus status;
  final Widget title;

  const DesktopViewer({
    super.key,
    required this.hostId,
    required this.status,
    required this.title,
  });

  @override
  rp.ConsumerState<DesktopViewer> createState() => _DesktopViewerState();
}

class _DesktopViewerState extends rp.ConsumerState<DesktopViewer> {
  late final DesktopBridge _bridge;
  late final DesktopLink _link;
  final _modifiers = DesktopModifiers();
  final _keyboardFocus = FocusNode();
  bool _keyboardOpen = false;

  /// Whether the system has shown the keyboard since the field took focus.
  /// Until it has, a zero inset only means the keyboard is still on its way.
  bool _keyboardShown = false;
  bool _immersive = false;

  DaemonAPIService? get _service => ref.read(serviceProvider(widget.hostId));

  @override
  void initState() {
    super.initState();
    _bridge = ref.read(desktopBridgeFactoryProvider)();
    _link = DesktopLink(
      bridge: _bridge,
      ticket: () {
        final service = _service;
        if (service == null) throw StateError('host is not paired');
        return service.desktopTicket();
      },
      pageFor: (ticket, p) => _service!.desktopPageUri(
        ticket,
        width: p.width,
        height: p.height,
        dpi: p.dpi,
      ),
      onNotReady: () => ref.invalidate(desktopProvider(widget.hostId)),
      onDisabled: () =>
          ref.read(desktopProvider(widget.hostId).notifier).markTurnedOff(),
    );
    _keyboardFocus.addListener(_onKeyboardFocus);
    WidgetsBinding.instance.addPostFrameCallback((_) => _start());
  }

  @override
  void dispose() {
    if (_immersive) {
      unawaited(SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge));
    }
    _keyboardFocus.dispose();
    _link.dispose();
    _bridge.dispose();
    _modifiers.dispose();
    super.dispose();
  }

  math.Point<double> get _screen {
    final size = MediaQuery.sizeOf(context);
    return math.Point(size.width, size.height);
  }

  Future<void> _start() async {
    final notifier = ref.read(desktopPrefsProvider(widget.hostId).notifier);
    await notifier.loaded;
    if (!mounted) return;
    final prefs = ref.read(desktopPrefsProvider(widget.hostId));
    final (w, h) = prefs.resolve(_screen);
    await _link.start(width: w, height: h, dpi: prefs.dpi);
    await _defaultDpi();
  }

  /// The phone's text size, applied once per host when the host is still at
  /// the stock 96. A phone shows a desktop small, and 96 makes XFCE's text too
  /// small to read there. After the first time it is left alone, so a choice
  /// made later, here or on another device, is never undone.
  Future<void> _defaultDpi() async {
    final notifier = ref.read(desktopPrefsProvider(widget.hostId).notifier);
    final prefs = ref.read(desktopPrefsProvider(widget.hostId));
    final hostDpi = widget.status.dpi;
    if (prefs.dpiDefaulted || hostDpi <= 0) return;
    if (hostDpi != 96) {
      await notifier.save(prefs.copyWith(dpiDefaulted: true, dpi: hostDpi));
      return;
    }
    try {
      final applied = await _service?.setDesktopDpi(
        DesktopDisplayPrefs.phoneDpi,
      );
      if (applied == null) return;
      await notifier.save(prefs.copyWith(dpiDefaulted: true, dpi: applied));
    } catch (_) {
      // Tried again on the next open.
    }
  }

  /// Follows the keyboard the system shows, not only the one the bar asked
  /// for. Back closes the keyboard without touching focus, and a field that
  /// keeps focus is one a closing sheet hands focus back to — which opens the
  /// keyboard again by itself. So a keyboard the system put away takes the
  /// focus with it.
  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final up = MediaQuery.viewInsetsOf(context).bottom > 0;
    if (up) {
      if (_keyboardFocus.hasFocus) _keyboardShown = true;
    } else if (_keyboardShown) {
      _keyboardShown = false;
      if (_keyboardFocus.hasFocus) _keyboardFocus.unfocus();
    }
  }

  void _onKeyboardFocus() {
    final open = _keyboardFocus.hasFocus;
    if (open == _keyboardOpen) return;
    setState(() => _keyboardOpen = open);
    unawaited(_bridge.setKeyboard(open));
  }

  void _toggleKeyboard() {
    if (_keyboardFocus.hasFocus) {
      _keyboardFocus.unfocus();
    } else {
      _keyboardFocus.requestFocus();
    }
  }

  void _toggleImmersive() {
    setState(() => _immersive = !_immersive);
    unawaited(
      SystemChrome.setEnabledSystemUIMode(
        _immersive ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
      ),
    );
  }

  Future<void> _reconnect() async {
    final prefs = ref.read(desktopPrefsProvider(widget.hostId));
    final (w, h) = prefs.resolve(_screen);
    await _link.start(width: w, height: h, dpi: prefs.dpi);
  }

  Future<bool> _setDpi(int dpi) async {
    final service = _service;
    if (service == null) return false;
    try {
      final applied = await service.setDesktopDpi(dpi);
      final notifier = ref.read(desktopPrefsProvider(widget.hostId).notifier);
      await notifier.save(
        ref
            .read(desktopPrefsProvider(widget.hostId))
            .copyWith(dpi: applied, dpiDefaulted: true),
      );
      return true;
    } on DesktopActionException catch (e) {
      _say(switch (e.code) {
        'not_ready' => 'The desktop is not running, so its DPI cannot change.',
        'bad_dpi' => 'The host takes a DPI from 72 to 288.',
        _ => e.words,
      });
    } catch (_) {
      _say('Could not reach the host.');
    }
    return false;
  }

  void _say(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }

  void _openDisplaySheet() {
    final notifier = ref.read(desktopPrefsProvider(widget.hostId).notifier);
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (_) => DesktopDisplaySheet(
        prefs: ref.read(desktopPrefsProvider(widget.hostId)),
        hostDpi: widget.status.dpi,
        screen: _screen,
        onPrefs: (next) {
          unawaited(notifier.save(next));
          final (w, h) = next.resolve(_screen);
          unawaited(_link.resize(w, h));
        },
        onDpi: _setDpi,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final page = Stack(
      fit: StackFit.expand,
      children: [
        ColoredBox(color: const Color(0xFF1E1E1E), child: _bridge.view()),
        // Holds the phone keyboard open. It has to be in the tree to take
        // focus, and in a corner where it covers nothing.
        Positioned(
          left: 0,
          top: 0,
          child: DesktopKeyboardSink(
            focusNode: _keyboardFocus,
            onText: (text) =>
                unawaited(_bridge.sendChords(_modifiers.takeText(text))),
            onBackspace: () =>
                unawaited(_bridge.sendKeys(_modifiers.take(keyBackspace))),
          ),
        ),
        ListenableBuilder(
          listenable: _link,
          builder: (context, _) => _LinkOverlay(
            phase: _link.phase,
            message: _link.message,
            onReconnect: _reconnect,
          ),
        ),
        if (_immersive)
          Positioned(
            top: 8,
            right: 8,
            child: SafeArea(
              child: IconButton.filledTonal(
                icon: const Icon(Icons.fullscreen_exit),
                tooltip: 'Leave full screen',
                onPressed: _toggleImmersive,
              ),
            ),
          ),
      ],
    );

    return Scaffold(
      appBar: _immersive
          ? null
          : AppBar(
              title: widget.title,
              actions: [
                IconButton(
                  icon: const Icon(Icons.aspect_ratio),
                  tooltip: 'Screen size and DPI',
                  onPressed: _openDisplaySheet,
                ),
                IconButton(
                  icon: const Icon(Icons.fullscreen),
                  tooltip: 'Full screen',
                  onPressed: _toggleImmersive,
                ),
              ],
            ),
      body: Column(
        children: [
          Expanded(child: page),
          DesktopKeyBar(
            modifiers: _modifiers,
            keyboardOpen: _keyboardOpen,
            onToggleKeyboard: _toggleKeyboard,
            onChord: (chord) => unawaited(_bridge.sendKeys(chord)),
          ),
        ],
      ),
    );
  }
}

/// What covers the page until the desktop is drawn, or after it is lost.
class _LinkOverlay extends StatelessWidget {
  final DesktopLinkPhase phase;
  final String message;
  final VoidCallback onReconnect;

  const _LinkOverlay({
    required this.phase,
    required this.message,
    required this.onReconnect,
  });

  @override
  Widget build(BuildContext context) {
    if (phase == DesktopLinkPhase.connected) return const SizedBox.shrink();
    final theme = Theme.of(context);
    final failed = phase == DesktopLinkPhase.failed;
    return ColoredBox(
      color: const Color(0xCC1E1E1E),
      child: Center(
        child: Card(
          margin: const EdgeInsets.all(32),
          child: Padding(
            padding: const EdgeInsets.all(20),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (failed)
                  Icon(Icons.link_off, color: theme.colorScheme.error)
                else
                  const SizedBox(
                    width: 24,
                    height: 24,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                const SizedBox(height: 12),
                Text(
                  failed ? message : 'Connecting to the desktop…',
                  textAlign: TextAlign.center,
                ),
                if (failed) ...[
                  const SizedBox(height: 16),
                  FilledButton.tonal(
                    onPressed: onReconnect,
                    child: const Text('Reconnect'),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
