import 'package:flutter/material.dart';
import '../services/notification_service.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late bool _soundEnabled;
  late bool _vibrationEnabled;

  @override
  void initState() {
    super.initState();
    _soundEnabled = NotificationService.instance.soundEnabled;
    _vibrationEnabled = NotificationService.instance.vibrationEnabled;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          const _SectionHeader('Notifications'),
          SwitchListTile(
            title: const Text('Sound'),
            subtitle: const Text('Play sound on notifications'),
            value: _soundEnabled,
            onChanged: (value) {
              setState(() => _soundEnabled = value);
              NotificationService.instance.setSoundEnabled(value);
            },
          ),
          SwitchListTile(
            title: const Text('Vibration'),
            subtitle: const Text('Vibrate on notifications'),
            value: _vibrationEnabled,
            onChanged: (value) {
              setState(() => _vibrationEnabled = value);
              NotificationService.instance.setVibrationEnabled(value);
            },
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 24, 16, 8),
      child: Text(
        title,
        style: Theme.of(context).textTheme.labelLarge?.copyWith(
              color: Theme.of(context).colorScheme.primary,
            ),
      ),
    );
  }
}
