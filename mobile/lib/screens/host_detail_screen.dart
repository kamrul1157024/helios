import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../services/host_manager.dart';

class HostDetailScreen extends StatefulWidget {
  final String hostId;

  const HostDetailScreen({super.key, required this.hostId});

  @override
  State<HostDetailScreen> createState() => _HostDetailScreenState();
}

class _HostDetailScreenState extends State<HostDetailScreen> {
  late TextEditingController _labelController;

  @override
  void initState() {
    super.initState();
    final hm = context.read<HostManager>();
    final host = hm.hostById(widget.hostId);
    _labelController = TextEditingController(text: host?.label ?? '');
  }

  @override
  void dispose() {
    _labelController.dispose();
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

              // Info fields
              _infoRow('Server URL', host.serverUrl, theme),
              _infoRow('Device ID', host.deviceId, theme),
              _infoRow('Status', isConnected ? 'Connected' : 'Offline', theme,
                  valueColor: isConnected ? Colors.green : theme.colorScheme.onSurfaceVariant),
              _infoRow('Paired', _formatDate(host.addedAt), theme),

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
