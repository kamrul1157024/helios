/// Two ways to make a schedule, and the first is the one people want.
///
/// Describing it opens an ordinary session with a prompt: the agent has the
/// `helios` skill, installed during agent setup, so it knows the CLI it is
/// about to call. The form is there for the times you want to be exact, and for
/// when there is no agent to ask.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/provider.dart';
import '../providers/daemon_providers.dart';
import 'schedule_editor_screen.dart';

/// The prompt that asks an agent to write a schedule.
///
/// Short on purpose: the skill carries the flags, the rules and the examples,
/// and repeating them here would give the agent two manuals that can disagree.
String schedulePrompt(String description, String cwd) {
  final lines = [
    'Create a Helios schedule from this description:',
    '',
    description.trim(),
    '',
    'Use the helios skill and the `helios schedule` CLI. Work out which kind it is —',
    'a timer, a one-shot, a monitor with a check, or a job that runs after another —',
    'and create it with a name that reads well in a list.',
    if (cwd.isNotEmpty) 'Unless the description says otherwise, it should run in $cwd.',
    '',
    'Then run `helios schedule list` and tell me in one or two lines what you made,',
    'when it next fires, and anything you had to guess.',
  ];
  return lines.join('\n');
}

Future<void> showNewScheduleSheet(BuildContext context, String hostId) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (_) => Padding(
      padding: EdgeInsets.only(bottom: MediaQuery.of(context).viewInsets.bottom),
      child: _NewScheduleSheet(hostId: hostId),
    ),
  );
}

class _NewScheduleSheet extends rp.ConsumerStatefulWidget {
  final String hostId;

  const _NewScheduleSheet({required this.hostId});

  @override
  rp.ConsumerState<_NewScheduleSheet> createState() => _NewScheduleSheetState();
}

class _NewScheduleSheetState extends rp.ConsumerState<_NewScheduleSheet> {
  final _description = TextEditingController();
  final _cwd = TextEditingController();
  late String _hostId = widget.hostId;
  String _provider = '';
  bool _starting = false;

  @override
  void dispose() {
    _description.dispose();
    _cwd.dispose();
    super.dispose();
  }

  Future<void> _askAnAgent() async {
    setState(() => _starting = true);
    final service = ref.read(serviceProvider(_hostId));
    final navigator = Navigator.of(context);
    final messenger = ScaffoldMessenger.of(context);

    final ok = await service?.createSession(
          provider: _provider.isEmpty ? 'claude' : _provider,
          cwd: _cwd.text.trim(),
          prompt: schedulePrompt(_description.text, _cwd.text.trim()),
        ) ??
        false;

    if (!mounted) return;
    setState(() => _starting = false);
    navigator.pop();
    messenger.showSnackBar(
      SnackBar(
        content: Text(ok
            ? 'An agent is writing it. Watch it in Sessions.'
            : 'Could not start an agent on that machine.'),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final hosts = ref.watch(hostManagerProvider).hosts;
    final providers = ref.watch(readyProvidersProvider(_hostId)).valueOrNull ?? const <ProviderInfo>[];
    final agent = _provider.isEmpty ? (providers.isEmpty ? '' : providers.first.id) : _provider;

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('New schedule', style: theme.textTheme.titleMedium),
          const SizedBox(height: 12),
          // Which machine runs it is part of the schedule, not a detail to
          // infer, and which agent writes it is the same choice the new-session
          // sheet offers.
          if (hosts.length > 1)
            DropdownButtonFormField<String>(
              initialValue: _hostId,
              decoration: const InputDecoration(labelText: 'On'),
              items: [
                for (final host in hosts)
                  DropdownMenuItem(value: host.id, child: Text(host.label)),
              ],
              onChanged: (v) => setState(() {
                _hostId = v ?? _hostId;
                _provider = '';
              }),
            ),
          if (hosts.length > 1) const SizedBox(height: 8),
          TextField(
            controller: _description,
            maxLines: 4,
            autofocus: true,
            decoration: const InputDecoration(
              labelText: 'Describe it',
              alignLabelWithHint: true,
              hintText: 'every 15 minutes, run the tests and if they fail, fix them',
            ),
          ),
          const SizedBox(height: 8),
          TextField(
            controller: _cwd,
            decoration: const InputDecoration(
              labelText: 'In',
              hintText: 'optional — a directory for the agent to work in',
            ),
          ),
          const SizedBox(height: 8),
          if (providers.isNotEmpty)
            DropdownButtonFormField<String>(
              initialValue: agent.isEmpty ? null : agent,
              decoration: const InputDecoration(labelText: 'Agent'),
              items: [
                for (final p in providers) DropdownMenuItem(value: p.id, child: Text(p.name)),
              ],
              onChanged: (v) => setState(() => _provider = v ?? ''),
            ),
          const SizedBox(height: 8),
          Text(
            'An agent reads this, works out the schedule and creates it with the CLI. '
            'You see what it made before it ever fires.',
            style: theme.textTheme.labelSmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 16),
          Row(
            children: [
              TextButton(
                onPressed: () {
                  Navigator.of(context).pop();
                  Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => ScheduleEditorScreen(hostId: _hostId),
                    ),
                  );
                },
                child: const Text('Set it up manually'),
              ),
              const Spacer(),
              FilledButton(
                onPressed: _starting || _description.text.trim().isEmpty ? null : _askAnAgent,
                child: Text(_starting ? 'Starting…' : 'Ask an agent'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
