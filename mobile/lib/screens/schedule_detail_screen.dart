/// One schedule: what it does, what it has run, and what its checks printed.
///
/// Three tabs rather than three screens, because they answer one question
/// between them — is this thing working — and the phone is where that question
/// is usually asked.
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/schedule.dart';
import '../providers/daemon_providers.dart';
import 'schedules_screen.dart';
import 'session_detail_screen.dart';

class ScheduleDetailScreen extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String scheduleId;

  const ScheduleDetailScreen({super.key, required this.hostId, required this.scheduleId});

  @override
  rp.ConsumerState<ScheduleDetailScreen> createState() => _ScheduleDetailScreenState();
}

class _ScheduleDetailScreenState extends rp.ConsumerState<ScheduleDetailScreen>
    with SingleTickerProviderStateMixin {
  late final TabController _tabs = TabController(length: 3, vsync: this);
  CheckResult? _check;
  bool _busy = false;

  @override
  void dispose() {
    _tabs.dispose();
    super.dispose();
  }

  Future<void> _act(Future<void> Function() work) async {
    setState(() => _busy = true);
    try {
      await work();
    } catch (error) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('$error')));
      }
    } finally {
      ref.invalidate(schedulesProvider(widget.hostId));
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final schedules = ref.watch(schedulesProvider(widget.hostId)).valueOrNull ?? const <Schedule>[];
    final schedule = schedules.where((sc) => sc.id == widget.scheduleId).firstOrNull;
    final service = ref.read(serviceProvider(widget.hostId));

    if (schedule == null) {
      return const Scaffold(body: Center(child: Text('That schedule is gone.')));
    }

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(schedule.name),
            Text(
              schedule.subtitle,
              style: Theme.of(context).textTheme.labelSmall,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: _busy
                ? null
                : () => _act(() async => service?.saveSchedule(
                      schedule.id,
                      {'enabled': !schedule.enabled},
                    )),
            child: Text(schedule.enabled ? '● on' : '○ paused'),
          ),
        ],
        bottom: TabBar(
          controller: _tabs,
          tabs: const [Tab(text: 'overview'), Tab(text: 'runs'), Tab(text: 'log')],
        ),
      ),
      body: TabBarView(
        controller: _tabs,
        children: [
          _Overview(schedule: schedule, check: _check),
          _Runs(hostId: widget.hostId, scheduleId: schedule.id),
          _Log(hostId: widget.hostId, scheduleId: schedule.id),
        ],
      ),
      bottomNavigationBar: BottomAppBar(
        child: Row(
          children: [
            TextButton(
              onPressed: _busy ? null : () => _act(() async => service?.runSchedule(schedule.id)),
              child: const Text('Run now'),
            ),
            if (schedule.isMonitor)
              TextButton(
                onPressed: _busy
                    ? null
                    : () => _act(() async {
                        final result = await service?.checkSchedule(schedule.id);
                        if (!mounted) return;
                        setState(() => _check = result);
                        _tabs.animateTo(0);
                      }),
                child: const Text('Test'),
              ),
            const Spacer(),
            TextButton(
              onPressed: () => openScheduleEditor(context, widget.hostId, scheduleId: schedule.id),
              child: const Text('Edit'),
            ),
            IconButton(
              tooltip: 'Delete',
              icon: const Icon(Icons.delete_outline),
              onPressed: _busy
                  ? null
                  : () async {
                      // Taken before the dialog: after the await this callback
                      // is across an async gap and its context may be gone.
                      final navigator = Navigator.of(context);
                      final ok = await showDialog<bool>(
                        context: context,
                        builder: (dialogContext) => AlertDialog(
                          title: const Text('Delete this schedule?'),
                          content: const Text('Anything that followed it will be paused.'),
                          actions: [
                            TextButton(
                              onPressed: () => Navigator.pop(dialogContext, false),
                              child: const Text('Cancel'),
                            ),
                            TextButton(
                              onPressed: () => Navigator.pop(dialogContext, true),
                              child: const Text('Delete'),
                            ),
                          ],
                        ),
                      );
                      if (ok != true) return;
                      await _act(() async => service?.deleteSchedule(schedule.id));
                      navigator.pop();
                    },
            ),
          ],
        ),
      ),
    );
  }
}

class _Overview extends StatelessWidget {
  final Schedule schedule;
  final CheckResult? check;

  const _Overview({required this.schedule, this.check});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final target = schedule.mode == 'resume'
        ? 'into session ${schedule.targetSession}'
        : 'a new session in ${schedule.cwd.isEmpty ? 'the home directory' : schedule.cwd}';

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (schedule.isMonitor) ...[
          _fact(theme, 'Check', schedule.check),
          _dim(
            theme,
            schedule.checkMatch.isEmpty
                ? 'fires when it exits non-zero'
                : 'fires when the output matches ${schedule.checkMatch}',
          ),
          if (schedule.lastCheckAt.isNotEmpty)
            _dim(
              theme,
              'last check ${agoWords(schedule.lastCheckAt)} · exit ${schedule.lastCheckExit ?? '—'}',
            ),
          const SizedBox(height: 16),
        ],
        _fact(theme, 'Runs', target),
        _dim(theme, [
          schedule.provider.isEmpty ? 'the default agent' : schedule.provider,
          if (schedule.model.isNotEmpty) schedule.model,
          if (schedule.permissionMode.isNotEmpty) schedule.permissionMode,
        ].join(' · ')),
        const SizedBox(height: 16),
        _fact(theme, 'Prompt', ''),
        Container(
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHigh,
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text(schedule.prompt, style: const TextStyle(fontSize: 12)),
        ),
        const SizedBox(height: 16),
        _dim(theme, _footNote(schedule)),
        if (check != null) ...[
          const SizedBox(height: 16),
          _fact(theme, 'Test', ''),
          Container(
            padding: const EdgeInsets.all(10),
            decoration: BoxDecoration(
              color: theme.colorScheme.surfaceContainerHigh,
              borderRadius: BorderRadius.circular(8),
            ),
            child: Text(
              check!.failed
                  ? 'the check failed — ${check!.error}'
                  : 'exit ${check!.exit} — ${check!.matched ? 'MATCH, this would fire' : 'quiet, this would not fire'}'
                      '${check!.output.isEmpty ? '' : '\n---\n${check!.output}'}',
              style: const TextStyle(fontFamily: 'monospace', fontSize: 11),
            ),
          ),
        ],
      ],
    );
  }

  Widget _fact(ThemeData theme, String label, String value) => Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(label, style: theme.textTheme.labelSmall),
            if (value.isNotEmpty) Text(value, style: theme.textTheme.bodyMedium),
          ],
        ),
      );

  Widget _dim(ThemeData theme, String text) => text.isEmpty
      ? const SizedBox.shrink()
      : Text(
          text,
          style: theme.textTheme.labelSmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
        );
}

String _footNote(Schedule sc) {
  final parts = <String>[];
  if (sc.nextRunAt.isNotEmpty && sc.enabled && sc.doneAt.isEmpty) {
    parts.add('${sc.isMonitor ? 'next check' : 'next run'} ${untilWords(sc.nextRunAt)}');
  }
  if (sc.lastFiredAt.isNotEmpty) parts.add('last ran ${agoWords(sc.lastFiredAt)}');
  if (sc.firesToday > 0) parts.add('fired ${sc.firesToday}× today');
  if (sc.failStreak > 1) parts.add('failing since ${agoWords(sc.failingSince)}');
  return parts.join(' · ');
}

/// The runs are sessions, so this is the session list with a filter on it.
class _Runs extends rp.ConsumerWidget {
  final String hostId;
  final String scheduleId;

  const _Runs({required this.hostId, required this.scheduleId});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final runs = ref.watch(scheduleRunsProvider((hostId, scheduleId)));

    return runs.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (err, _) => Center(child: Text('$err')),
      data: (list) {
        if (list.isEmpty) return const Center(child: Text('No runs yet.'));
        return ListView.builder(
          itemCount: list.length,
          itemBuilder: (context, i) {
            final run = list[i];
            return ListTile(
              dense: true,
              leading: Icon(Icons.circle, size: 8, color: _runColour(context, run.status)),
              title: Text(run.displayTitle, maxLines: 1, overflow: TextOverflow.ellipsis),
              subtitle: Text('${run.status} · ${agoWords(run.createdAt)}'),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => SessionDetailScreen(session: run),
                ),
              ),
            );
          },
        );
      },
    );
  }

  Color _runColour(BuildContext context, String status) {
    final scheme = Theme.of(context).colorScheme;
    switch (status) {
      case 'active':
      case 'starting':
        return scheme.primary;
      case 'error':
        return scheme.error;
      default:
        return scheme.outline;
    }
  }
}

/// The schedule's own log, polled while the tab is open.
///
/// Polling rather than streaming: there is no streaming log in the daemon, and
/// a check that runs every five minutes does not need sub-second delivery.
class _Log extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String scheduleId;

  const _Log({required this.hostId, required this.scheduleId});

  @override
  rp.ConsumerState<_Log> createState() => _LogState();
}

class _LogState extends rp.ConsumerState<_Log> {
  Timer? _poll;
  List<String> _lines = const [];

  @override
  void initState() {
    super.initState();
    unawaited(_read());
    _poll = Timer.periodic(const Duration(seconds: 2), (_) => unawaited(_read()));
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _read() async {
    final service = ref.read(serviceProvider(widget.hostId));
    if (service == null) return;
    try {
      final lines = await service.scheduleLog(widget.scheduleId);
      if (mounted) setState(() => _lines = lines);
    } catch (_) {
      // A log that cannot be read is not worth interrupting the screen for.
    }
  }

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.all(12),
      child: SelectableText(
        _lines.isEmpty ? '(nothing yet)' : _lines.join('\n'),
        style: const TextStyle(fontFamily: 'monospace', fontSize: 11, height: 1.5),
      ),
    );
  }
}
