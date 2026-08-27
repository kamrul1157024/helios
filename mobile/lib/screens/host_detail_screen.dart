import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../services/daemon_api_service.dart';
import '../services/host_manager.dart';

class HostDetailScreen extends StatefulWidget {
  final String hostId;

  const HostDetailScreen({super.key, required this.hostId});

  @override
  State<HostDetailScreen> createState() => _HostDetailScreenState();
}

class _HostDetailScreenState extends State<HostDetailScreen> {
  late TextEditingController _labelController;
  late TextEditingController _urlController;

  /// Where the budget thumb sits mid-drag. Null when nothing is being dragged,
  /// so the service's value shows. Each step the thumb passes through would
  /// otherwise be a request the host has to answer.
  double? _budgetDrag;

  @override
  void initState() {
    super.initState();
    final hm = context.read<HostManager>();
    final host = hm.hostById(widget.hostId);
    _labelController = TextEditingController(text: host?.label ?? '');
    _urlController = TextEditingController(text: host?.serverUrl ?? '');

    final service = hm.serviceFor(widget.hostId);
    if (service != null && service.connected) service.fetchHostSettings();
  }

  @override
  void dispose() {
    _labelController.dispose();
    _urlController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        final host = hm.hostById(widget.hostId);
        if (host == null) {
          return Scaffold(
            appBar: AppBar(title: const Text('Host')),
            body: const Center(child: Text('Host not found')),
          );
        }

        final isConnected = hm.serviceFor(host.id)?.connected == true;
        final theme = Theme.of(context);

        return Scaffold(
          appBar: AppBar(title: Text(host.label)),
          body: ListView(
            padding: const EdgeInsets.all(16),
            children: [
              // Color picker
              Text('Color', style: theme.textTheme.labelLarge),
              const SizedBox(height: 8),
              Wrap(
                spacing: 12,
                children: List.generate(HostConnection.hostColors.length, (i) {
                  final color = HostConnection.hostColors[i];
                  final isSelected = host.colorIndex % HostConnection.hostColors.length == i;
                  return GestureDetector(
                    onTap: () => hm.updateHostColor(host.id, i),
                    child: Container(
                      width: 36,
                      height: 36,
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        color: color,
                        border: isSelected
                            ? Border.all(color: theme.colorScheme.onSurface, width: 3)
                            : null,
                      ),
                      child: isSelected
                          ? const Icon(Icons.check, color: Colors.white, size: 18)
                          : null,
                    ),
                  );
                }),
              ),
              const SizedBox(height: 24),

              // Label
              Text('Label', style: theme.textTheme.labelLarge),
              const SizedBox(height: 8),
              TextField(
                controller: _labelController,
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  hintText: 'e.g. Work MacBook',
                ),
                onSubmitted: (value) {
                  if (value.trim().isNotEmpty) {
                    hm.updateHostLabel(host.id, value.trim());
                  }
                },
              ),
              const SizedBox(height: 4),
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  onPressed: () {
                    final value = _labelController.text.trim();
                    if (value.isNotEmpty) {
                      hm.updateHostLabel(host.id, value);
                      ScaffoldMessenger.of(context).showSnackBar(
                        const SnackBar(content: Text('Label updated')),
                      );
                    }
                  },
                  child: const Text('Save'),
                ),
              ),
              const SizedBox(height: 16),

              // Server URL. Editable because most tunnel providers hand out a
              // fresh URL on every restart, and re-pairing to follow one would
              // throw away this device's approval and its history.
              Text('Server URL', style: theme.textTheme.labelLarge),
              const SizedBox(height: 8),
              TextField(
                controller: _urlController,
                keyboardType: TextInputType.url,
                autocorrect: false,
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  hintText: 'https://my-machine.tailnet.ts.net',
                ),
                onSubmitted: (value) => _saveUrl(hm, value),
              ),
              const SizedBox(height: 4),
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  onPressed: () => _saveUrl(hm, _urlController.text),
                  child: const Text('Save'),
                ),
              ),
              if (HostConnection.isTailnetUrl(host.serverUrl))
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: Text(
                    'Tailnet address — reachable only while Tailscale is connected.',
                    style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
                  ),
                ),
              const SizedBox(height: 16),

              // Info fields
              _infoRow('Device ID', host.deviceId, theme),
              _infoRow('Status', isConnected ? 'Connected' : 'Offline', theme,
                  valueColor: isConnected ? Colors.green : theme.colorScheme.onSurfaceVariant),
              _infoRow('Paired', _formatDate(host.addedAt), theme),

              const SizedBox(height: 24),
              ..._buildHostSettings(hm.serviceFor(host.id), isConnected, theme),

              const SizedBox(height: 32),

              // Disconnect button
              SizedBox(
                width: double.infinity,
                child: OutlinedButton(
                  onPressed: () => _confirmDisconnect(hm, host),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: theme.colorScheme.error,
                    side: BorderSide(color: theme.colorScheme.error),
                  ),
                  child: const Text('Disconnect & Remove'),
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  /// The host's own settings, not this device's. The daemon stores them, so
  /// they belong on the page for one host rather than on a settings screen
  /// that has to guess which machine is meant.
  List<Widget> _buildHostSettings(
    DaemonAPIService? service,
    bool isConnected,
    ThemeData theme,
  ) {
    final header = Text('Host settings', style: theme.textTheme.labelLarge);

    // Nothing read yet. A spinner while the host is answering, and a plain
    // statement when it cannot: an offline host that spins forever reads as a
    // broken screen.
    if (service == null || !service.hostSettingsLoaded) {
      return [
        header,
        const SizedBox(height: 8),
        if (isConnected)
          const Row(
            children: [
              SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2)),
              SizedBox(width: 12),
              Text('Loading…'),
            ],
          )
        else
          Text(
            'Offline — settings unavailable until this host reconnects.',
            style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
          ),
      ];
    }

    // Loaded but unreachable. The last known values still say what the host is
    // set to, so they are shown and locked rather than hidden.
    final fraction = _budgetDrag ?? service.budgetFraction;

    return [
      header,
      if (!isConnected)
        Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Text(
            'Offline — showing the last known values. Reconnect to change them.',
            style: TextStyle(fontSize: 12, color: theme.colorScheme.onSurfaceVariant),
          ),
        ),
      const SizedBox(height: 8),
      SwitchListTile(
        contentPadding: EdgeInsets.zero,
        secondary: const Icon(Icons.title),
        title: const Text('Auto title'),
        subtitle: const Text('Generate session titles automatically'),
        value: service.autoTitleEnabled,
        onChanged: isConnected
            ? (value) => _saveHostSetting(
                  () => service.setAutoTitleEnabled(value),
                  'Auto title',
                )
            : null,
      ),
      if (service.autoTitleEnabled)
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          secondary: const Icon(Icons.emoji_emotions_outlined),
          title: const Text('Title icon'),
          subtitle: const Text('Needs a Nerd Font — boxes without one'),
          value: service.autoTitleEmoji,
          onChanged: isConnected
              ? (value) => _saveHostSetting(
                    () => service.setAutoTitleEmoji(value),
                    'Title icon',
                  )
              : null,
        ),
      SwitchListTile(
        contentPadding: EdgeInsets.zero,
        secondary: const Icon(Icons.memory),
        title: const Text('Save memory'),
        subtitle: const Text(
          'Stops the agents you have not opened lately. Opening one starts '
          'it again, with the conversation intact.',
        ),
        value: service.evictEnabled,
        onChanged: isConnected
            ? (value) => _saveHostSetting(
                  () => service.setEvictEnabled(value),
                  'Save memory',
                )
            : null,
      ),
      if (service.evictEnabled)
        ListTile(
          contentPadding: EdgeInsets.zero,
          title: Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              const Text('Memory limit'),
              Text(
                '${(fraction * 100).round()}% of host RAM',
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
            ],
          ),
          subtitle: Slider(
            value: fraction.clamp(DaemonAPIService.budgetMin, DaemonAPIService.budgetMax),
            min: DaemonAPIService.budgetMin,
            max: DaemonAPIService.budgetMax,
            divisions: 17,
            label: '${(fraction * 100).round()}%',
            onChanged: isConnected ? (value) => setState(() => _budgetDrag = value) : null,
            onChangeEnd: isConnected
                ? (value) async {
                    await _saveHostSetting(
                      () => service.setBudgetFraction(value),
                      'Memory limit',
                    );
                    if (mounted) setState(() => _budgetDrag = null);
                  }
                : null,
          ),
        ),
    ];
  }

  /// Writes one setting and says so when it fails. The service puts the old
  /// value back on its own; without a message the control would flick back
  /// with no reason given.
  Future<void> _saveHostSetting(Future<bool> Function() write, String label) async {
    final ok = await write();
    if (!mounted || ok) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text('Could not save $label — the host did not answer')),
    );
  }

  /// Validate and apply a new server URL. Only http and https are accepted:
  /// anything else is a typo that would otherwise leave the host unreachable
  /// with no explanation.
  Future<void> _saveUrl(HostManager hm, String value) async {
    final trimmed = value.trim();
    final uri = Uri.tryParse(trimmed);
    final valid = uri != null &&
        (uri.scheme == 'http' || uri.scheme == 'https') &&
        uri.host.isNotEmpty;

    if (!valid) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Enter a full URL, e.g. https://host.tailnet.ts.net')),
      );
      return;
    }

    await hm.updateHostUrl(widget.hostId, trimmed);
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Server URL updated — reconnecting')),
    );
  }

  Widget _infoRow(String label, String value, ThemeData theme, {Color? valueColor}) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 100,
            child: Text(label, style: TextStyle(fontSize: 13, color: theme.colorScheme.onSurfaceVariant)),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(
                fontSize: 13,
                fontFamily: 'monospace',
                color: valueColor ?? theme.colorScheme.onSurface,
              ),
            ),
          ),
        ],
      ),
    );
  }

  String _formatDate(DateTime date) {
    final diff = DateTime.now().difference(date);
    if (diff.inDays == 0) return 'today';
    if (diff.inDays == 1) return 'yesterday';
    if (diff.inDays < 30) return '${diff.inDays} days ago';
    return '${date.month}/${date.day}/${date.year}';
  }

  void _confirmDisconnect(HostManager hm, HostConnection host) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Disconnect host'),
        content: Text('Remove "${host.label}" and delete stored credentials? You can re-pair later.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(
            onPressed: () async {
              Navigator.pop(ctx);
              await hm.removeHost(host.id);
              if (mounted) Navigator.of(context).pop();
            },
            style: FilledButton.styleFrom(backgroundColor: Theme.of(ctx).colorScheme.error),
            child: const Text('Disconnect'),
          ),
        ],
      ),
    );
  }
}
