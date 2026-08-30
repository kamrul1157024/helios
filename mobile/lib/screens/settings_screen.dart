import 'package:flutter/material.dart';
// Both packages export Provider, ChangeNotifierProvider and Consumer.
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:provider/provider.dart';
import '../providers/daemon_providers.dart';
import '../models/host_connection.dart';
import '../providers/theme_provider.dart';
import '../services/host_manager.dart';
import '../services/notification_service.dart';
import '../services/update_service.dart';
import 'host_detail_screen.dart';
import 'notification_settings_screen.dart';
import 'setup_screen.dart';

class SettingsScreen extends rp.ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  rp.ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends rp.ConsumerState<SettingsScreen> {
  late bool _soundEnabled;
  late bool _vibrationEnabled;

  // Update check
  String _currentVersion = '';
  UpdateInfo? _updateInfo;
  bool _updateChecking = false;
  bool _updateDownloading = false;
  double _updateProgress = 0;

  @override
  void initState() {
    super.initState();
    _soundEnabled = NotificationService.instance.soundEnabled;
    _vibrationEnabled = NotificationService.instance.vibrationEnabled;
    _loadVersionAndCheckUpdate();
  }

  Future<void> _loadVersionAndCheckUpdate() async {
    final version = await UpdateService.instance.currentVersion;
    if (!mounted) return;
    setState(() {
      _currentVersion = version;
      _updateChecking = true;
    });
    final info = await UpdateService.instance.checkForUpdate();
    if (!mounted) return;
    setState(() {
      _updateInfo = info;
      _updateChecking = false;
    });
  }

  Future<void> _doInstall() async {
    final info = _updateInfo;
    if (info == null) return;
    setState(() {
      _updateDownloading = true;
      _updateProgress = 0;
    });
    await UpdateService.instance.install(info, onProgress: (p) {
      if (mounted) setState(() => _updateProgress = p);
    });
    if (mounted) setState(() => _updateDownloading = false);
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        return Scaffold(
          appBar: AppBar(title: const Text('Settings')),
          body: ListView(
            children: [
              const _SectionHeader('App'),
              _buildUpdateTile(),
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
              ListTile(
                leading: const Icon(Icons.notifications_outlined),
                title: const Text('Alert settings'),
                subtitle: const Text('Choose which notifications buzz and play sound'),
                trailing: const Icon(Icons.chevron_right, size: 20),
                onTap: () {
                  Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => const NotificationSettingsScreen(),
                    ),
                  );
                },
              ),
            ],
          ),
        );
      },
    );
  }

  Widget _buildUpdateTile() {
    final hasUpdate = _updateInfo != null;
    final checking = _updateChecking;
    final downloading = _updateDownloading;

    if (downloading) {
      return ListTile(
        leading: const Icon(Icons.system_update),
        title: const Text('Downloading update...'),
        subtitle: LinearProgressIndicator(value: _updateProgress > 0 ? _updateProgress : null),
      );
    }

    if (checking) {
      return const ListTile(
        leading: Icon(Icons.system_update),
        title: Text('Checking for updates...'),
        subtitle: LinearProgressIndicator(),
      );
    }

    if (hasUpdate) {
      return ListTile(
        leading: Icon(Icons.system_update, color: Theme.of(context).colorScheme.primary),
        title: Text('Update available — v${_updateInfo!.latestVersion}'),
        subtitle: Text(
          'Current: v$_currentVersion  ·  Tap to ${_updateInfo!.canDirectInstall ? 'download & install' : 'open release page'}',
          style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
        ),
        trailing: FilledButton.tonal(
          onPressed: _doInstall,
          child: const Text('Update'),
        ),
      );
    }

    return ListTile(
      leading: const Icon(Icons.check_circle_outline),
      title: Text('helios v$_currentVersion'),
      subtitle: Text(
        'Up to date',
        style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
      ),
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

  /// What a host is set to, in one line, so the list says it without a tap.
  /// Null until the host has answered: a default shown here would be a claim
  /// about a machine nobody has asked yet.
  String? _hostSettingsSummary(String hostId) {
    final settings = ref.watch(hostSettingsProvider(hostId)).valueOrNull;
    if (settings == null) return null;
    final title = settings.autoTitleEnabled ? 'Auto title on' : 'Auto title off';
    final memory = settings.evictEnabled
        ? 'Save memory ${(settings.budgetFraction * 100).round()}%'
        : 'Save memory off';
    return '$title · $memory';
  }

  Widget _buildHostTile(HostConnection host, HostManager hm) {
    final service = hm.serviceFor(host.id);
    final isConnected = service?.connected == true;
    final summary = _hostSettingsSummary(host.id);

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
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            host.serverUrl,
            style: TextStyle(fontSize: 11, color: Theme.of(context).colorScheme.onSurfaceVariant),
            overflow: TextOverflow.ellipsis,
          ),
          if (summary != null)
            Text(
              summary,
              style: TextStyle(
                fontSize: 11,
                color: Theme.of(context)
                    .colorScheme
                    .onSurfaceVariant
                    .withValues(alpha: isConnected ? 1.0 : 0.5),
              ),
              overflow: TextOverflow.ellipsis,
            ),
        ],
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
