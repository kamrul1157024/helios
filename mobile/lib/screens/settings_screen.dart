import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../providers/theme_provider.dart';
import '../services/host_manager.dart';
import '../services/notification_service.dart';
import '../services/narration_service.dart';
import '../services/voice_service.dart';
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
  late bool _voiceInputEnabled;
  late bool _autoReadEnabled;
  late bool _toolCallTtsEnabled;
  late double _speechRate;
  late bool _aiNarrationEnabled;

  @override
  void initState() {
    super.initState();
    _soundEnabled = NotificationService.instance.soundEnabled;
    _vibrationEnabled = NotificationService.instance.vibrationEnabled;
    _voiceInputEnabled = VoiceService.instance.voiceInputEnabled;
    _autoReadEnabled = VoiceService.instance.autoReadEnabled;
    _toolCallTtsEnabled = VoiceService.instance.toolCallTtsEnabled;
    _speechRate = VoiceService.instance.speechRate;
    _aiNarrationEnabled = NarrationService.instance.aiNarrationEnabled;
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
              SwitchListTile(
                title: const Text('Read tool actions'),
                subtitle: const Text('Announce tool calls like Read, Edit, Bash'),
                value: _toolCallTtsEnabled,
                onChanged: (value) {
                  setState(() => _toolCallTtsEnabled = value);
                  VoiceService.instance.setToolCallTtsEnabled(value);
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
                  },
                ),
              ),
              SwitchListTile(
                title: const Text('AI narration'),
                subtitle: const Text('Use AI to generate natural narration'),
                value: _aiNarrationEnabled,
                onChanged: (value) {
                  setState(() => _aiNarrationEnabled = value);
                  NarrationService.instance.setAINarrationEnabled(value);
                },
              ),
              ListTile(
                leading: const Icon(Icons.edit_note),
                title: const Text('Narrator prompt'),
                subtitle: Text(
                  NarrationService.instance.customPrompt.isNotEmpty
                      ? NarrationService.instance.customPrompt
                      : 'Default (casual first-person)',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
                ),
                trailing: const Icon(Icons.chevron_right, size: 20),
                enabled: _aiNarrationEnabled,
                onTap: _aiNarrationEnabled ? _showNarratorPromptDialog : null,
              ),
            ],
          ),
        );
      },
    );
  }

  void _showNarratorPromptDialog() {
    final controller = TextEditingController(
      text: NarrationService.instance.customPrompt,
    );
    showDialog(
      context: context,
      builder: (ctx) {
        return AlertDialog(
          title: const Text('Narrator Prompt'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                'Customize how the AI narrator speaks. Leave empty for the default casual style.',
                style: TextStyle(
                  fontSize: 13,
                  color: Theme.of(ctx).colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: controller,
                maxLines: 4,
                decoration: InputDecoration(
                  hintText: 'e.g. You are a sarcastic British butler narrating code changes.',
                  border: OutlineInputBorder(borderRadius: BorderRadius.circular(8)),
                ),
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () {
                controller.text = '';
              },
              child: const Text('Reset'),
            ),
            const Spacer(),
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () {
                Navigator.pop(ctx);
                final prompt = controller.text.trim();
                NarrationService.instance.setCustomPrompt(prompt);
                setState(() {});
              },
              child: const Text('Save'),
            ),
          ],
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
