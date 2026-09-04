import 'package:flutter/material.dart';
import '../services/notification_service.dart';

class NotificationSettingsScreen extends StatefulWidget {
  const NotificationSettingsScreen({super.key});

  @override
  State<NotificationSettingsScreen> createState() =>
      _NotificationSettingsScreenState();
}

class _NotificationSettingsScreenState
    extends State<NotificationSettingsScreen> {
  late Map<String, bool> _alertTypes;

  static const _blockingTypes = [
    _NotifType(
      kind: 'permission',
      label: 'Permission requests',
      description:
          'The agent is asking to use a tool that requires your approval.',
      blocking: true,
    ),
    _NotifType(
      kind: 'question',
      label: 'Questions',
      description: 'The agent needs your input to continue.',
      blocking: true,
    ),
    _NotifType(
      kind: 'elicitation.form',
      label: 'Elicitation — form input',
      description: 'An MCP server is requesting structured input from you.',
      blocking: true,
    ),
    _NotifType(
      kind: 'elicitation.url',
      label: 'Elicitation — authentication',
      description: 'An MCP server requires you to authenticate via a URL.',
      blocking: true,
    ),
    _NotifType(
      kind: 'trust',
      label: 'Workspace trust',
      description: 'The agent is asking to trust the files in this workspace.',
      blocking: true,
    ),
  ];

  static const _informationalTypes = [
    _NotifType(
      kind: 'done',
      label: 'Session completed',
      description: 'The agent finished a task.',
      blocking: false,
    ),
    _NotifType(
      kind: 'error',
      label: 'Session error',
      description: 'The agent stopped due to an error.',
      blocking: false,
    ),
  ];

  @override
  void initState() {
    super.initState();
    _alertTypes = Map.of(NotificationService.instance.alertTypes);
  }

  Future<void> _setAlert(String kind, bool value) async {
    setState(() => _alertTypes[kind] = value);
    await NotificationService.instance.setAlertEnabled(kind, value);
  }

  Future<void> _resetToDefaults() async {
    await NotificationService.instance.resetAlertTypes();
    setState(() {
      _alertTypes = Map.of(NotificationService.instance.alertTypes);
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Alert Settings')),
      body: ListView(
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
            child: Text(
              'Notifications always appear in your notification shade. '
              'These toggles control whether they also buzz and play sound.',
              style: TextStyle(
                fontSize: 13,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          _SectionHeader('Action required'),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Text(
              'These notifications block the agent until you respond.',
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          for (final t in _blockingTypes) _buildTile(t),
          _SectionHeader('Informational'),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Text(
              'These notifications do not block Claude.',
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          for (final t in _informationalTypes) _buildTile(t),
          const SizedBox(height: 16),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: OutlinedButton.icon(
              onPressed: _resetToDefaults,
              icon: const Icon(Icons.restore, size: 18),
              label: const Text('Reset to defaults'),
            ),
          ),
          const SizedBox(height: 32),
        ],
      ),
    );
  }

  Widget _buildTile(_NotifType t) {
    final alertOn = _alertTypes[t.kind] ?? true;
    final showWarning = t.blocking && !alertOn;

    return SwitchListTile(
      title: Text(t.label),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            t.description,
            style: TextStyle(
              fontSize: 12,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
          if (showWarning) ...[
            const SizedBox(height: 4),
            Row(
              children: [
                Icon(
                  Icons.warning_amber_rounded,
                  size: 13,
                  color: Theme.of(context).colorScheme.error,
                ),
                const SizedBox(width: 4),
                Expanded(
                  child: Text(
                    'Alert off — Claude may wait indefinitely for your response.',
                    style: TextStyle(
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ),
              ],
            ),
          ],
        ],
      ),
      isThreeLine: showWarning,
      value: alertOn,
      onChanged: (v) => _setAlert(t.kind, v),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 4),
      child: Text(
        title,
        style: Theme.of(context).textTheme.labelLarge?.copyWith(
          color: Theme.of(context).colorScheme.primary,
        ),
      ),
    );
  }
}

/// One row of the settings list.
///
/// Keyed by kind rather than by notification type, so a single "Permission
/// requests" toggle covers every provider instead of one row per agent.
class _NotifType {
  final String kind;
  final String label;
  final String description;
  final bool blocking;

  const _NotifType({
    required this.kind,
    required this.label,
    required this.description,
    required this.blocking,
  });
}
