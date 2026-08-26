import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../providers/theme_provider.dart';
import '../services/host_manager.dart';
import '../services/notification_service.dart';
import '../services/update_service.dart';
import 'host_detail_screen.dart';
import 'notification_settings_screen.dart';
import 'setup_screen.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late bool _soundEnabled;
  late bool _vibrationEnabled;
  bool _settingsLoaded = false;

  // Auto title settings
  bool _autoTitleEnabled = false;
  bool _autoTitleEmoji = true;

  /// Share of the host's memory its warm sessions may hold. The budget belongs
  /// to the daemon rather than to this device, so it is worth being able to
  /// change it from here — otherwise you can watch a session go cold from the
  /// phone and have to walk to the machine to adjust it.
  double _memoryBudgetFraction = 0.25;

  /// Off unless asked for. Eviction kills a running agent, so upgrading must
  /// not start doing it to somebody's machine.
  bool _evictEnabled = false;

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
    _loadSettings();
    _loadVersionAndCheckUpdate();
  }

  Future<void> _loadSettings() async {
    final hm = context.read<HostManager>();
    // Use the first connected host for settings
    for (final host in hm.hosts) {
      final service = hm.serviceFor(host.id);
      if (service == null || !service.connected) continue;
      final data = await service.getSettings();
      if (data != null && mounted) {
        final settings = (data['settings'] as Map<String, dynamic>?) ?? {};
        setState(() {
          _autoTitleEnabled = (settings['autotitle.enabled'] as String?) == 'true';
          // Off unless turned on: Flutter ships no Nerd Font, so the glyphs
          // render as empty boxes on the phone.
          _autoTitleEmoji = (settings['autotitle.emoji'] as String?) == 'true';
          final budget = double.tryParse(
            (settings['memory.budget_fraction'] as String?) ?? '',
          );
          _memoryBudgetFraction =
              (budget != null && budget >= 0 && budget <= 1) ? budget : 0.25;
          _evictEnabled = (settings['memory.evict'] as String?) == 'true';
          _settingsLoaded = true;
        });
      }
      break;
    }
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

  /// The budget choices. A free-text field invites a typo in a memory limit,
  /// and the useful range is narrow.
  static const _budgetChoices = <(double, String, String?)>[
    (0.25, 'Quarter of RAM', 'Recommended'),
    (0.5, 'Half of RAM', null),
    (0.75, 'Three quarters', null),
    (0, 'No limit', 'Nothing is ever evicted'),
  ];

  List<Widget> _buildMemoryBudgetTiles() {
    return [
      SwitchListTile(
        secondary: const Icon(Icons.memory),
        title: const Text('Let idle sessions go cold'),
        subtitle: const Text(
          'Off by default. Frees memory by stopping agents you have not '
          'opened lately; opening one starts it again.',
        ),
        value: _evictEnabled,
        onChanged: (value) {
          setState(() => _evictEnabled = value);
          _updateSetting('memory.evict', value ? 'true' : 'false');
        },
      ),
      if (_evictEnabled) ...[
        const Padding(
          padding: EdgeInsets.fromLTRB(16, 0, 16, 8),
          child: Text(
            'Past this share of the host, sessions nobody has opened for a '
            'while go cold. The conversation is kept and opening one brings '
            'it back.',
            style: TextStyle(fontSize: 12),
          ),
        ),
        RadioGroup<double>(
          groupValue: _memoryBudgetFraction,
          onChanged: (value) {
            if (value == null) return;
            setState(() => _memoryBudgetFraction = value);
            _updateSetting('memory.budget_fraction', value.toString());
          },
          child: Column(
            children: [
              for (final (fraction, label, note) in _budgetChoices)
                RadioListTile<double>(
                  value: fraction,
                  title: Text(label),
                  subtitle: note == null ? null : Text(note),
                ),
            ],
          ),
        ),
      ],
    ];
  }

  Future<void> _updateSetting(String key, String value) async {
    final hm = context.read<HostManager>();
    for (final host in hm.hosts) {
      final service = hm.serviceFor(host.id);
      if (service == null || !service.connected) continue;
      await service.updateSettings({key: value});
      break;
    }
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
              const _SectionHeader('Memory'),
              ..._buildMemoryBudgetTiles(),
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
              const _SectionHeader('Session Titles'),
              if (!_settingsLoaded)
                const ListTile(
                  leading: SizedBox(
                    width: 20, height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                  title: Text('Loading settings...'),
                ),
              if (_settingsLoaded) ...[
                SwitchListTile(
                  secondary: const Icon(Icons.title),
                  title: const Text('Auto title'),
                  subtitle: const Text('Generate session titles automatically'),
                  value: _autoTitleEnabled,
                  onChanged: (value) {
                    setState(() => _autoTitleEnabled = value);
                    _updateSetting('autotitle.enabled', value ? 'true' : 'false');
                  },
                ),
                if (_autoTitleEnabled)
                  SwitchListTile(
                    secondary: const Icon(Icons.emoji_emotions_outlined),
                    title: const Text('Title icon'),
                    subtitle: const Text('Needs a Nerd Font — boxes without one'),
                    value: _autoTitleEmoji,
                    onChanged: (value) {
                      setState(() => _autoTitleEmoji = value);
                      _updateSetting('autotitle.emoji', value ? 'true' : 'false');
                    },
                  ),
              ],
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
