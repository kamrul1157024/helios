/// Writing a schedule, on a phone.
///
/// A full screen rather than a sheet: a cron field, a check and a six-line
/// prompt do not fit in something you can swipe away by accident.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/schedule.dart';
import '../providers/daemon_providers.dart';

class ScheduleEditorScreen extends rp.ConsumerStatefulWidget {
  final String hostId;

  /// Empty for a new one.
  final String scheduleId;

  const ScheduleEditorScreen({
    super.key,
    required this.hostId,
    this.scheduleId = '',
  });

  @override
  rp.ConsumerState<ScheduleEditorScreen> createState() =>
      _ScheduleEditorScreenState();
}

class _ScheduleEditorScreenState
    extends rp.ConsumerState<ScheduleEditorScreen> {
  final _name = TextEditingController();
  final _cron = TextEditingController(text: '0 9 * * 1-5');
  final _runAt = TextEditingController();
  final _cwd = TextEditingController();
  final _prompt = TextEditingController();
  final _check = TextEditingController();
  final _match = TextEditingController();

  String _kind = 'timer';
  String _afterId = '';
  String _afterWhen = 'success';
  String _checkSource = 'command';
  bool _loaded = false;
  bool _saving = false;
  String _error = '';

  @override
  void dispose() {
    for (final c in [_name, _cron, _runAt, _cwd, _prompt, _check, _match]) {
      c.dispose();
    }
    super.dispose();
  }

  /// Fills the form from the schedule being edited, once.
  void _load(List<Schedule> schedules) {
    if (_loaded || widget.scheduleId.isEmpty) return;
    final sc = schedules.where((s) => s.id == widget.scheduleId).firstOrNull;
    if (sc == null) return;
    _loaded = true;
    _name.text = sc.name;
    _cron.text = sc.cron;
    _runAt.text = sc.runAt;
    _cwd.text = sc.cwd;
    _prompt.text = sc.prompt;
    _match.text = sc.checkMatch;
    _kind = sc.kind;
    _afterId = sc.afterId;
    _afterWhen = sc.afterWhen.isEmpty ? 'success' : sc.afterWhen;
    _checkSource = sc.checkFile.isNotEmpty ? 'file' : 'command';
    _check.text = sc.checkFile.isNotEmpty ? sc.checkFile : sc.checkCmd;
  }

  Future<void> _save() async {
    setState(() {
      _saving = true;
      _error = '';
    });
    final service = ref.read(serviceProvider(widget.hostId));
    final fields = <String, dynamic>{
      'name': _name.text.trim(),
      'kind': _kind,
      'prompt': _prompt.text,
      'cwd': _cwd.text.trim(),
      'cron': _kind == 'timer' || _kind == 'monitor' ? _cron.text.trim() : '',
      'run_at': _kind == 'once' ? _runAt.text.trim() : '',
      'after_id': _kind == 'after' ? _afterId : '',
      'after_when': _kind == 'after' ? _afterWhen : '',
      'check_cmd': _kind == 'monitor' && _checkSource == 'command'
          ? _check.text
          : '',
      'check_file': _kind == 'monitor' && _checkSource == 'file'
          ? _check.text.trim()
          : '',
      'check_match': _kind == 'monitor' ? _match.text : '',
    };

    try {
      await service?.saveSchedule(widget.scheduleId, fields);
      ref.invalidate(schedulesProvider(widget.hostId));
      if (mounted) Navigator.of(context).pop();
    } catch (error) {
      // The daemon refuses at save what would be found at 3am otherwise, and
      // says which — so its words go on screen rather than a generic failure.
      if (mounted)
        setState(() => _error = '$error'.replaceFirst('Exception: ', ''));
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final schedules =
        ref.watch(schedulesProvider(widget.hostId)).valueOrNull ??
        const <Schedule>[];
    _load(schedules);

    final monitor = _kind == 'monitor';

    return Scaffold(
      appBar: AppBar(
        title: Text(
          widget.scheduleId.isEmpty ? 'New schedule' : 'Edit schedule',
        ),
        actions: [
          TextButton(
            onPressed: _saving ? null : _save,
            child: const Text('Save'),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          TextField(
            controller: _name,
            decoration: const InputDecoration(
              labelText: 'Name',
              hintText: 'build-watch',
            ),
          ),
          const SizedBox(height: 16),

          Text('Fires', style: theme.textTheme.labelMedium),
          RadioGroup<String>(
            groupValue: _kind,
            onChanged: (v) => setState(() => _kind = v ?? 'timer'),
            child: Column(
              children: [
                for (final option in const [
                  ('timer', 'on a clock'),
                  ('once', 'once, at one moment'),
                  ('monitor', 'when a check matches'),
                  ('after', 'after another job'),
                ])
                  RadioListTile<String>(
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    value: option.$1,
                    title: Text(option.$2),
                  ),
              ],
            ),
          ),

          if (_kind == 'timer' || monitor) ...[
            TextField(
              controller: _cron,
              onChanged: (_) => setState(() {}),
              decoration: InputDecoration(
                labelText: monitor ? 'Check every' : 'When',
                hintText: '0 9 * * 1-5',
                helperText: Schedule(
                  id: '',
                  name: '',
                  kind: 'timer',
                  enabled: true,
                  cron: _cron.text,
                ).cronWords,
              ),
            ),
            const SizedBox(height: 16),
          ],

          if (_kind == 'once') ...[
            TextField(
              controller: _runAt,
              decoration: const InputDecoration(
                labelText: 'At',
                hintText: '2026-03-02T22:00:00Z',
              ),
            ),
            const SizedBox(height: 16),
          ],

          if (_kind == 'after') ...[
            DropdownButtonFormField<String>(
              initialValue: _afterId.isEmpty ? null : _afterId,
              decoration: const InputDecoration(labelText: 'After'),
              items: [
                for (final sc in schedules.where(
                  (s) => s.id != widget.scheduleId,
                ))
                  DropdownMenuItem(value: sc.id, child: Text(sc.name)),
              ],
              onChanged: (v) => setState(() => _afterId = v ?? ''),
            ),
            const SizedBox(height: 8),
            DropdownButtonFormField<String>(
              initialValue: _afterWhen,
              decoration: const InputDecoration(labelText: 'Runs'),
              items: const [
                DropdownMenuItem(
                  value: 'success',
                  child: Text('only if it succeeds'),
                ),
                DropdownMenuItem(value: 'any', child: Text('either way')),
              ],
              onChanged: (v) => setState(() => _afterWhen = v ?? 'success'),
            ),
            const SizedBox(height: 16),
          ],

          if (monitor) ...[
            Text('Check', style: theme.textTheme.labelMedium),
            RadioGroup<String>(
              groupValue: _checkSource,
              onChanged: (v) => setState(() => _checkSource = v ?? 'command'),
              child: Column(
                children: [
                  for (final option in const [
                    ('command', 'a command'),
                    ('file', 'a script on that machine'),
                  ])
                    RadioListTile<String>(
                      dense: true,
                      contentPadding: EdgeInsets.zero,
                      value: option.$1,
                      title: Text(option.$2),
                    ),
                ],
              ),
            ),
            TextField(
              controller: _check,
              decoration: InputDecoration(
                hintText: _checkSource == 'command'
                    ? 'make test 2>&1'
                    : '~/checks/queue.py',
              ),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _match,
              onChanged: (_) => setState(() {}),
              decoration: InputDecoration(
                labelText: 'Match',
                hintText: 'optional — a pattern in the output',
                helperText: _match.text.isEmpty
                    ? 'Fires when the check exits non-zero.'
                    : 'Fires when the output matches, whatever the exit code.',
              ),
            ),
            const SizedBox(height: 16),
          ],

          TextField(
            controller: _cwd,
            decoration: const InputDecoration(
              labelText: 'Where',
              hintText:
                  'optional — empty for work that is not about a directory',
            ),
          ),
          const SizedBox(height: 16),

          TextField(
            controller: _prompt,
            maxLines: 8,
            decoration: InputDecoration(
              labelText: 'Prompt',
              alignLabelWithHint: true,
              helperText: monitor
                  ? '{{output}} is replaced with what the check printed.'
                  : null,
              hintText: monitor
                  ? 'The check found something:\n\n{{output}}\n\nLook into it.'
                  : 'What to do',
            ),
          ),

          if (_error.isNotEmpty) ...[
            const SizedBox(height: 12),
            Text(_error, style: TextStyle(color: theme.colorScheme.error)),
          ],
        ],
      ),
    );
  }
}
