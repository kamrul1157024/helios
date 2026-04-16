import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../providers/theme_provider.dart';
import '../services/host_manager.dart';
import '../services/notification_service.dart';
import '../services/update_service.dart';
import '../services/voice_service.dart';
import 'event_filter_screen.dart';
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
  late bool _voiceInputEnabled;
  late bool _autoReadEnabled;
  late double _speechRate;
  late double _pitch;
  Map<String, String>? _selectedVoice;
  Timer? _sampleDebounce;

  // Reporter settings from backend
  String _activePersonaId = 'default';
  int _debounceSeconds = 10;
  String _customPrompt = '';
  List<Map<String, dynamic>> _personas = [];
  Map<String, List<Map<String, dynamic>>> _eventTypes = {};
  String? _globalFilterJson;
  String? _sessionFilterJson;
  bool _settingsLoaded = false;

  // Auto title settings
  bool _autoTitleEnabled = false;
  bool _autoTitleEmoji = true;

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
    _voiceInputEnabled = VoiceService.instance.voiceInputEnabled;
    _autoReadEnabled = VoiceService.instance.autoReadEnabled;
    _speechRate = VoiceService.instance.speechRate;
    _pitch = VoiceService.instance.pitch;
    _selectedVoice = VoiceService.instance.selectedVoice;
    _loadReporterSettings();
    _loadVersionAndCheckUpdate();
  }

  @override
  void dispose() {
    _sampleDebounce?.cancel();
    super.dispose();
  }

  void _debounceSample() {
    _sampleDebounce?.cancel();
    _sampleDebounce = Timer(const Duration(milliseconds: 500), () {
      VoiceService.instance.speakSample();
    });
  }

  Future<void> _loadReporterSettings() async {
    final hm = context.read<HostManager>();
    // Use the first connected host for settings
    for (final host in hm.hosts) {
      final service = hm.serviceFor(host.id);
      if (service == null || !service.connected) continue;
      final data = await service.getSettings();
      if (data != null && mounted) {
        final settings = (data['settings'] as Map<String, dynamic>?) ?? {};
        final personas = (data['personas'] as List?)
                ?.map((p) => Map<String, dynamic>.from(p as Map))
                .toList() ??
            [];
        final rawEventTypes = data['event_types'] as Map<String, dynamic>? ?? {};
        final parsedEventTypes = <String, List<Map<String, dynamic>>>{};
        for (final entry in rawEventTypes.entries) {
          parsedEventTypes[entry.key] = (entry.value as List)
              .map((e) => Map<String, dynamic>.from(e as Map))
              .toList();
        }
        setState(() {
          _activePersonaId = (settings['reporter.persona'] as String?) ?? 'default';
          final debounceStr = settings['reporter.debounce_seconds'] as String?;
          _debounceSeconds = int.tryParse(debounceStr ?? '') ?? 10;
          _customPrompt = (settings['reporter.custom_prompt'] as String?) ?? '';
          _personas = personas;
          _eventTypes = parsedEventTypes;
          _globalFilterJson = settings['reporter.filter.global'] as String?;
          _sessionFilterJson = settings['reporter.filter.session'] as String?;
          _autoTitleEnabled = (settings['autotitle.enabled'] as String?) == 'true';
          _autoTitleEmoji = (settings['autotitle.emoji'] as String?) != 'false';
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

  Future<void> _updateReporterSetting(String key, String value) async {
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
              const _SectionHeader('Voice'),
              SwitchListTile(
                title: const Text('Voice input'),
                subtitle: const Text('Show mic button in voice mode'),
                value: _voiceInputEnabled,
                onChanged: (value) async {
                  setState(() => _voiceInputEnabled = value);
                  VoiceService.instance.setVoiceInputEnabled(value);
                  if (value) {
                    final warning = await VoiceService.instance.checkSttAvailability();
                    if (warning != null && mounted) {
                      _showServiceWarning(warning);
                    }
                  }
                },
              ),
              SwitchListTile(
                title: const Text('Auto-read responses'),
                subtitle: const Text('Speak new messages in voice mode'),
                value: _autoReadEnabled,
                onChanged: (value) async {
                  setState(() => _autoReadEnabled = value);
                  VoiceService.instance.setAutoReadEnabled(value);
                  if (value) {
                    final warning = await VoiceService.instance.checkTtsAvailability();
                    if (warning != null && mounted) {
                      _showServiceWarning(warning);
                    }
                  }
                },
              ),
              ListTile(
                leading: const Icon(Icons.speed),
                title: const Text('Speech rate'),
                subtitle: Slider(
                  value: _speechRate,
                  min: 0.1,
                  max: 1.0,
                  divisions: 9,
                  label: _speechRate.toStringAsFixed(1),
                  onChanged: (value) {
                    setState(() => _speechRate = value);
                    VoiceService.instance.setSpeechRate(value);
                    _debounceSample();
                  },
                ),
              ),
              ListTile(
                leading: const Icon(Icons.tune),
                title: const Text('Pitch'),
                subtitle: Slider(
                  value: _pitch,
                  min: 0.5,
                  max: 2.0,
                  divisions: 15,
                  label: _pitch.toStringAsFixed(1),
                  onChanged: (value) {
                    setState(() => _pitch = value);
                    VoiceService.instance.setPitch(value);
                    _debounceSample();
                  },
                ),
              ),
              ListTile(
                leading: const Icon(Icons.record_voice_over),
                title: const Text('Voice'),
                subtitle: Text(
                  _selectedVoice != null
                      ? VoiceService.displayName(_selectedVoice!['name'] ?? '')
                      : 'System default',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                ),
                trailing: const Icon(Icons.chevron_right, size: 20),
                onTap: _showVoicePicker,
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
                    _updateReporterSetting('autotitle.enabled', value ? 'true' : 'false');
                  },
                ),
                if (_autoTitleEnabled)
                  SwitchListTile(
                    secondary: const Icon(Icons.emoji_emotions_outlined),
                    title: const Text('Title emoji'),
                    subtitle: const Text('Prefix titles with a category emoji'),
                    value: _autoTitleEmoji,
                    onChanged: (value) {
                      setState(() => _autoTitleEmoji = value);
                      _updateReporterSetting('autotitle.emoji', value ? 'true' : 'false');
                    },
                  ),
              ],
              const _SectionHeader('AI Narrator'),
              if (_settingsLoaded) ...[
                ListTile(
                  leading: const Icon(Icons.person),
                  title: const Text('Narrator persona'),
                  subtitle: Text(
                    _personas
                        .where((p) => p['id'] == _activePersonaId)
                        .map((p) => '${p['name']} — ${p['description']}')
                        .firstOrNull ?? _activePersonaId,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                  ),
                  trailing: const Icon(Icons.chevron_right, size: 20),
                  onTap: _showPersonaPicker,
                ),
                ListTile(
                  leading: const Icon(Icons.timer),
                  title: const Text('Narration delay'),
                  subtitle: Text(
                    '${_debounceSeconds}s — batches events before narrating',
                    style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                  ),
                  trailing: const Icon(Icons.chevron_right, size: 20),
                  onTap: _showDebounceDialog,
                ),
                if (_activePersonaId == 'custom')
                  ListTile(
                    leading: const Icon(Icons.edit_note),
                    title: const Text('Custom prompt'),
                    subtitle: Text(
                      _customPrompt.isNotEmpty ? _customPrompt : 'No custom prompt set',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                    ),
                    trailing: const Icon(Icons.chevron_right, size: 20),
                    onTap: _showCustomPromptDialog,
                  ),
                if (_eventTypes.isNotEmpty)
                  ListTile(
                    leading: const Icon(Icons.filter_list),
                    title: const Text('Event filters'),
                    subtitle: Text(
                      'Choose which events trigger narration',
                      style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                    ),
                    trailing: const Icon(Icons.chevron_right, size: 20),
                    onTap: () {
                      Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) => EventFilterScreen(
                            eventTypes: _eventTypes,
                            globalFilterJson: _globalFilterJson,
                            sessionFilterJson: _sessionFilterJson,
                            onUpdate: _updateReporterSetting,
                          ),
                        ),
                      );
                    },
                  ),
              ],
            ],
          ),
        );
      },
    );
  }

  void _showPersonaPicker() {
    final allOptions = [
      ..._personas,
      {'id': 'custom', 'name': 'Custom', 'description': 'Your own narrator prompt'},
    ];

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (ctx) {
        return DraggableScrollableSheet(
          initialChildSize: 0.6,
          minChildSize: 0.3,
          maxChildSize: 0.85,
          expand: false,
          builder: (ctx, scrollController) {
            return SafeArea(
              child: Column(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(16),
                    child: Text('Narrator Persona', style: Theme.of(ctx).textTheme.titleSmall),
                  ),
                  Expanded(
                    child: ListView.builder(
                      controller: scrollController,
                      itemCount: allOptions.length,
                      itemBuilder: (ctx, index) {
                        final p = allOptions[index];
                        final id = p['id'] as String;
                        final isSelected = id == _activePersonaId;
                        return ListTile(
                          leading: Icon(
                            isSelected ? Icons.radio_button_checked : Icons.radio_button_off,
                            color: isSelected ? Theme.of(ctx).colorScheme.primary : null,
                          ),
                          title: Text(
                            p['name'] as String,
                            style: TextStyle(fontWeight: isSelected ? FontWeight.w600 : null),
                          ),
                          subtitle: Text(
                            p['description'] as String,
                            style: const TextStyle(fontSize: 12),
                          ),
                          onTap: () {
                            Navigator.pop(ctx);
                            setState(() => _activePersonaId = id);
                            _updateReporterSetting('reporter.persona', id);
                            if (id == 'custom') {
                              Future.delayed(const Duration(milliseconds: 300), _showCustomPromptDialog);
                            }
                          },
                        );
                      },
                    ),
                  ),
                ],
              ),
            );
          },
        );
      },
    );
  }

  void _showDebounceDialog() {
    int value = _debounceSeconds;
    showDialog(
      context: context,
      builder: (ctx) {
        return StatefulBuilder(
          builder: (ctx, setDialogState) {
            return AlertDialog(
              title: const Text('Narration Delay'),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'Wait ${value}s after the last event before generating narration.',
                    style: TextStyle(fontSize: 13, color: Theme.of(ctx).colorScheme.onSurfaceVariant),
                  ),
                  const SizedBox(height: 16),
                  Slider(
                    value: value.toDouble(),
                    min: 2,
                    max: 60,
                    divisions: 29,
                    label: '${value}s',
                    onChanged: (v) => setDialogState(() => value = v.round()),
                  ),
                ],
              ),
              actions: [
                TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
                FilledButton(
                  onPressed: () {
                    Navigator.pop(ctx);
                    setState(() => _debounceSeconds = value);
                    _updateReporterSetting('reporter.debounce_seconds', value.toString());
                  },
                  child: const Text('Save'),
                ),
              ],
            );
          },
        );
      },
    );
  }

  void _showCustomPromptDialog() {
    final controller = TextEditingController(text: _customPrompt);
    showDialog(
      context: context,
      builder: (ctx) {
        return AlertDialog(
          title: const Text('Custom Narrator Prompt'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Write a system prompt for your custom narrator personality.',
                style: TextStyle(fontSize: 13, color: Theme.of(ctx).colorScheme.onSurfaceVariant),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: controller,
                maxLines: 5,
                decoration: InputDecoration(
                  hintText: 'e.g. You are a zen monk who speaks in haiku...',
                  border: OutlineInputBorder(borderRadius: BorderRadius.circular(8)),
                ),
              ),
            ],
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
            FilledButton(
              onPressed: () {
                Navigator.pop(ctx);
                final prompt = controller.text.trim();
                setState(() => _customPrompt = prompt);
                _updateReporterSetting('reporter.custom_prompt', prompt);
              },
              child: const Text('Save'),
            ),
          ],
        );
      },
    );
  }

  void _showVoicePicker() async {
    final voices = await VoiceService.instance.getAvailableVoices();
    if (!mounted) return;

    if (voices.isEmpty) {
      _showServiceWarning('No voices available for the current language.');
      return;
    }

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (ctx) {
        return DraggableScrollableSheet(
          initialChildSize: 0.6,
          minChildSize: 0.3,
          maxChildSize: 0.85,
          expand: false,
          builder: (ctx, scrollController) {
            return SafeArea(
              child: Column(
                children: [
                  Padding(
                    padding: const EdgeInsets.all(16),
                    child: Text('Select Voice', style: Theme.of(ctx).textTheme.titleSmall),
                  ),
                  Expanded(
                    child: ListView.builder(
                      controller: scrollController,
                      itemCount: voices.length + 1,
                      itemBuilder: (ctx, index) {
                        // First item is "System default"
                        if (index == 0) {
                          final isSelected = _selectedVoice == null;
                          return ListTile(
                            leading: Icon(
                              isSelected ? Icons.radio_button_checked : Icons.radio_button_off,
                              color: isSelected ? Theme.of(ctx).colorScheme.primary : null,
                            ),
                            title: Text(
                              'System default',
                              style: TextStyle(fontWeight: isSelected ? FontWeight.w600 : null),
                            ),
                            subtitle: const Text('Use device default voice', style: TextStyle(fontSize: 12)),
                            onTap: () {
                              Navigator.pop(ctx);
                              setState(() => _selectedVoice = null);
                              VoiceService.instance.setSelectedVoice(null);
                            },
                          );
                        }
                        final voice = voices[index - 1];
                        final name = voice['name'] ?? '';
                        final locale = voice['locale'] ?? '';
                        final displayName = VoiceService.displayName(name);
                        final gender = VoiceService.guessGender(name);
                        final isSelected = _selectedVoice?['name'] == name;
                        return ListTile(
                          leading: Icon(
                            isSelected ? Icons.radio_button_checked : Icons.radio_button_off,
                            color: isSelected ? Theme.of(ctx).colorScheme.primary : null,
                          ),
                          title: Text(
                            displayName,
                            style: TextStyle(fontWeight: isSelected ? FontWeight.w600 : null),
                          ),
                          subtitle: Text(
                            gender != null ? '$locale  ·  $gender' : locale,
                            style: const TextStyle(fontSize: 12),
                          ),
                          trailing: IconButton(
                            icon: const Icon(Icons.play_arrow, size: 20),
                            tooltip: 'Preview',
                            onPressed: () => VoiceService.instance.previewVoice(voice),
                          ),
                          onTap: () {
                            Navigator.pop(ctx);
                            setState(() => _selectedVoice = voice);
                            VoiceService.instance.setSelectedVoice(voice);
                          },
                        );
                      },
                    ),
                  ),
                ],
              ),
            );
          },
        );
      },
    );
  }

  void _showServiceWarning(String message) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        icon: Icon(Icons.warning_amber_rounded, color: Theme.of(ctx).colorScheme.error, size: 32),
        title: const Text('Service unavailable'),
        content: Text(message),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('OK'),
          ),
        ],
      ),
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
