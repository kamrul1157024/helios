import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../providers/theme_provider.dart';
import '../services/host_manager.dart';
import '../services/notification_service.dart';
import 'host_detail_screen.dart';
import 'setup_screen.dart';

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
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        return Scaffold(
          appBar: AppBar(title: const Text('Settings')),
          body: ListView(
            children: [
              const _SectionHeader('Hosts'),
              ...hm.hosts.map((host) => _buildHostTile(host, hm)),
              ListTile(
                leading: Icon(Icons.add, color: Theme.of(context).colorScheme.primary),
                title: const Text('Add new host'),
                onTap: () {
                  Navigator.of(context).push(
                    MaterialPageRoute(builder: (_) => const SetupScreen()),
                  );
                },
              ),
              const _SectionHeader('Appearance'),
              _buildThemeTile(context),
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
      },
    );
  }

  Widget _buildThemeTile(BuildContext context) {
    final tp = context.watch<ThemeProvider>();
    return ListTile(
      leading: Icon(
        tp.mode == ThemeMode.dark
            ? Icons.dark_mode
            : tp.mode == ThemeMode.light
                ? Icons.light_mode
                : Icons.brightness_auto,
      ),
      title: const Text('Theme'),
      trailing: SegmentedButton<ThemeMode>(
        segments: const [
          ButtonSegment(value: ThemeMode.system, icon: Icon(Icons.brightness_auto, size: 18)),
          ButtonSegment(value: ThemeMode.light, icon: Icon(Icons.light_mode, size: 18)),
          ButtonSegment(value: ThemeMode.dark, icon: Icon(Icons.dark_mode, size: 18)),
        ],
        selected: {tp.mode},
        onSelectionChanged: (modes) => tp.setMode(modes.first),
        showSelectedIcon: false,
        style: ButtonStyle(
          visualDensity: VisualDensity.compact,
          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        ),
      ),
    );
  }

  Widget _buildHostTile(HostConnection host, HostManager hm) {
    final isConnected = hm.serviceFor(host.id)?.connected == true;

    return ListTile(
      leading: Container(
        width: 12,
        height: 12,
        decoration: BoxDecoration(
          shape: BoxShape.circle,
          color: host.color.withValues(alpha: isConnected ? 1.0 : 0.3),
        ),
      ),
      title: Text(host.label),
      subtitle: Text(
        host.serverUrl,
        style: TextStyle(fontSize: 11, color: Theme.of(context).colorScheme.onSurfaceVariant),
        overflow: TextOverflow.ellipsis,
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            isConnected ? 'Connected' : 'Offline',
            style: TextStyle(
              fontSize: 12,
              color: isConnected ? Colors.green : Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
          const SizedBox(width: 4),
          const Icon(Icons.chevron_right, size: 20),
        ],
      ),
      onTap: () {
        Navigator.of(context).push(
          MaterialPageRoute(builder: (_) => HostDetailScreen(hostId: host.id)),
        );
      },
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
