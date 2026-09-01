/// Schedules: a saved prompt with something that decides when it runs.
///
/// The phone is where a chain is watched rather than built, so this is a list
/// and a detail rather than a tree you can drag. Nesting is shown by indent,
/// and a link is made in the editor's "runs after" picker.
///
/// See docs/specs/55-scheduled-runs.md.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/schedule.dart';
import '../providers/daemon_providers.dart';
import 'schedule_detail_screen.dart';
import 'schedule_editor_screen.dart';

class SchedulesScreen extends rp.ConsumerWidget {
  const SchedulesScreen({super.key});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final hosts = ref.watch(hostManagerProvider).hosts;

    if (hosts.isEmpty) {
      return const Center(child: Text('Pair a machine first.'));
    }

    return ListView(
      padding: const EdgeInsets.only(bottom: 88),
      children: [
        for (final host in hosts) ...[
          if (hosts.length > 1)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
              child: Text(
                host.label,
                style: Theme.of(context).textTheme.labelMedium?.copyWith(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
              ),
            ),
          _HostSchedules(hostId: host.id),
        ],
      ],
    );
  }
}

class _HostSchedules extends rp.ConsumerWidget {
  final String hostId;

  const _HostSchedules({required this.hostId});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final theme = Theme.of(context);
    final schedules = ref.watch(schedulesProvider(hostId));

    return schedules.when(
      loading: () => const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: CircularProgressIndicator()),
      ),
      // A daemon too old to know about schedules answers 404, and silence reads
      // as a hang.
      error: (err, _) => Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          'Schedules need a newer daemon on this machine.',
          style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
        ),
      ),
      data: (list) {
        if (list.isEmpty) {
          return Padding(
            padding: const EdgeInsets.all(16),
            child: Text(
              'Nothing scheduled here yet — a saved prompt with a clock, or a check that '
              'decides when there is something to do.',
              style: TextStyle(color: theme.colorScheme.onSurfaceVariant),
            ),
          );
        }

        final depth = _depthOf(list);
        return Column(
          children: [
            for (final sc in list)
              ScheduleTile(
                hostId: hostId,
                schedule: sc,
                depth: depth[sc.id] ?? 0,
              ),
          ],
        );
      },
    );
  }
}

/// How deep in the after-chain each schedule sits, so a grandchild indents twice.
Map<String, int> _depthOf(List<Schedule> list) {
  final parent = {for (final sc in list) sc.id: sc.afterId};
  final depth = <String, int>{};
  for (final sc in list) {
    var n = 0;
    var at = sc.afterId;
    while (at.isNotEmpty && n < 16) {
      n++;
      at = parent[at] ?? '';
    }
    depth[sc.id] = n;
  }
  return depth;
}

class ScheduleTile extends rp.ConsumerWidget {
  final String hostId;
  final Schedule schedule;
  final int depth;

  const ScheduleTile({
    super.key,
    required this.hostId,
    required this.schedule,
    this.depth = 0,
  });

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final theme = Theme.of(context);
    final missed = schedule.lastStatus == 'missed';

    return Opacity(
      opacity: schedule.enabled ? 1 : 0.6,
      child: InkWell(
        onTap: () => Navigator.of(context).push(
          MaterialPageRoute(
            builder: (_) => ScheduleDetailScreen(hostId: hostId, scheduleId: schedule.id),
          ),
        ),
        child: Container(
          padding: EdgeInsets.fromLTRB(16.0 + depth * 16, 10, 16, 10),
          decoration: BoxDecoration(
            border: Border(
              bottom: BorderSide(color: theme.colorScheme.outlineVariant.withValues(alpha: 0.4)),
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Text(kindGlyph(schedule), style: const TextStyle(fontSize: 12)),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      schedule.name,
                      style: theme.textTheme.bodyMedium?.copyWith(fontWeight: FontWeight.w600),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  Text(
                    schedule.stateWord,
                    style: theme.textTheme.labelSmall?.copyWith(color: stateColour(theme, schedule)),
                  ),
                ],
              ),
              const SizedBox(height: 2),
              Text(
                schedule.subtitle,
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              if (schedule.lastError.isNotEmpty)
                Text(
                  schedule.lastError,
                  style: theme.textTheme.labelSmall?.copyWith(color: theme.colorScheme.error),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              // A missed run is a question, and the answer belongs where the
              // question is being read.
              if (missed)
                Row(
                  children: [
                    TextButton(
                      onPressed: () async {
                        final service = ref.read(serviceProvider(hostId));
                        await service?.runSchedule(schedule.id);
                        ref.invalidate(schedulesProvider(hostId));
                      },
                      child: const Text('Run now'),
                    ),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
  }
}

String kindGlyph(Schedule sc) {
  switch (sc.kind) {
    case 'monitor':
      return '◉';
    case 'once':
      return '⧗';
    case 'after':
      return '⇢';
    default:
      return '⏰';
  }
}

Color? stateColour(ThemeData theme, Schedule sc) {
  switch (sc.lastStatus) {
    case 'failed':
    case 'missed':
      return theme.colorScheme.error;
    case 'running':
      return theme.colorScheme.primary;
    default:
      return theme.colorScheme.onSurfaceVariant;
  }
}

/// The button the schedules tab floats: a new one starts in the editor.
void openScheduleEditor(BuildContext context, String hostId, {String scheduleId = ''}) {
  Navigator.of(context).push(
    MaterialPageRoute(
      builder: (_) => ScheduleEditorScreen(hostId: hostId, scheduleId: scheduleId),
    ),
  );
}
