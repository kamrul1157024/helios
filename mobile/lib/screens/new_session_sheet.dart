import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../models/provider.dart';
import '../services/host_manager.dart';
import '../services/daemon_api_service.dart';

class NewSessionSheet extends StatefulWidget {
  const NewSessionSheet({super.key});

  @override
  State<NewSessionSheet> createState() => _NewSessionSheetState();
}

class _NewSessionSheetState extends State<NewSessionSheet> {
  final _cwdController = TextEditingController();

  String? _selectedHostId;
  ProviderInfo? _selectedProvider;
  ModelInfo? _selectedModel;
  List<ModelInfo> _models = [];
  bool _loadingModels = false;
  bool _refreshingModels = false;
  bool _creating = false;
  bool _showCustomCwd = false;
  bool _skipPermissions = false;

  @override
  void initState() {
    super.initState();
    final hm = context.read<HostManager>();
    // Default to the active host, or first host
    _selectedHostId = hm.activeHostId ?? hm.hosts.firstOrNull?.id;
    _initProvider();
  }

  DaemonAPIService? get _service {
    if (_selectedHostId == null) return null;
    return context.read<HostManager>().serviceFor(_selectedHostId!);
  }

  void _initProvider() {
    final sse = _service;
    if (sse == null) return;
    if (sse.providers.isNotEmpty) {
      _selectedProvider = sse.providers.first;
      _loadModels();
    } else {
      sse.fetchProviders().then((_) {
        if (mounted && sse.providers.isNotEmpty) {
          setState(() => _selectedProvider = sse.providers.first);
          _loadModels();
        }
      });
    }
  }

  Future<void> _loadModels() async {
    if (_selectedProvider == null) return;
    final sse = _service;
    if (sse == null) return;
    setState(() => _loadingModels = true);
    final models = await sse.fetchModels(_selectedProvider!.id);
    if (mounted) {
      setState(() {
        _models = models;
        _loadingModels = false;
        if (_models.isNotEmpty && _selectedModel == null) {
          _selectedModel = _models.first;
        }
      });
    }
  }

  Future<void> _refreshModels() async {
    if (_selectedProvider == null) return;
    final sse = _service;
    if (sse == null) return;
    setState(() => _refreshingModels = true);
    final models = await sse.fetchModels(_selectedProvider!.id, forceRefresh: true);
    if (mounted) {
      setState(() {
        _models = models;
        _refreshingModels = false;
        if (_selectedModel != null &&
            !_models.any((m) => m.id == _selectedModel!.id)) {
          _selectedModel = _models.isNotEmpty ? _models.first : null;
        }
      });
    }
  }

  Future<void> _createSession() async {
    if (_selectedProvider == null) return;

    final sse = _service;
    if (sse == null) return;

    setState(() => _creating = true);
    final cwd = _cwdController.text.trim();
    final ok = await sse.createSession(
      provider: _selectedProvider!.id,
      model: _selectedModel?.id,
      cwd: cwd.isNotEmpty ? cwd : null,
      dangerouslySkipPermissions: _skipPermissions,
    );

    if (mounted) {
      if (ok) {
        Navigator.of(context).pop(true);
      } else {
        setState(() => _creating = false);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('Failed to create session'),
            duration: Duration(seconds: 2),
          ),
        );
      }
    }
  }

  @override
  void dispose() {
    _cwdController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final hm = context.read<HostManager>();
    final bottomInset = MediaQuery.of(context).viewInsets.bottom;

    return Padding(
      padding: EdgeInsets.only(
        left: 20,
        right: 20,
        top: 16,
        bottom: bottomInset + 16,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Handle bar
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.3),
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 16),

          // Title
          Text(
            'New Session',
            style: theme.textTheme.titleMedium?.copyWith(
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(height: 20),

          // Host selector (only when multiple hosts)
          if (hm.hosts.length > 1) ...[
            _buildHostDropdown(theme, hm),
            const SizedBox(height: 16),
          ],

          // Provider dropdown
          _buildProviderDropdown(theme),
          const SizedBox(height: 16),

          // Model dropdown + refresh
          _buildModelRow(theme),
          const SizedBox(height: 16),

          // CWD section
          _buildCwdSection(theme),
          const SizedBox(height: 16),

          // Skip permissions toggle
          SwitchListTile(
            title: const Text(
              'Skip permissions',
              style: TextStyle(fontSize: 14),
            ),
            subtitle: Text(
              'Use --dangerously-skip-permissions',
              style: TextStyle(
                fontSize: 11,
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            value: _skipPermissions,
            onChanged: (v) => setState(() => _skipPermissions = v),
            contentPadding: EdgeInsets.zero,
            dense: true,
          ),
          const SizedBox(height: 12),

          // Start button
          SizedBox(
            width: double.infinity,
            height: 48,
            child: FilledButton(
              onPressed: _creating ? null : _createSession,
              child: _creating
                  ? const SizedBox(
                      width: 20,
                      height: 20,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: Colors.white,
                      ),
                    )
                  : const Text('Start Session'),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildHostDropdown(ThemeData theme, HostManager hm) {
    return DropdownButtonFormField<String>(
      initialValue: _selectedHostId,
      decoration: InputDecoration(
        labelText: 'Host',
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      ),
      items: hm.hosts.map((host) {
        final isConnected = hm.serviceFor(host.id)?.connected == true;
        return DropdownMenuItem(
          value: host.id,
          child: Row(
            children: [
              Container(
                width: 10,
                height: 10,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: host.color.withValues(alpha: isConnected ? 1.0 : 0.3),
                ),
              ),
              const SizedBox(width: 8),
              Text(host.label),
            ],
          ),
        );
      }).toList(),
      onChanged: (value) {
        if (value == null || value == _selectedHostId) return;
        setState(() {
          _selectedHostId = value;
          _selectedProvider = null;
          _selectedModel = null;
          _models = [];
        });
        _initProvider();
      },
    );
  }

  Widget _buildProviderDropdown(ThemeData theme) {
    final sse = _service;
    final providers = sse?.providers ?? [];

    return DropdownButtonFormField<String>(
      initialValue: _selectedProvider?.id,
      decoration: InputDecoration(
        labelText: 'Provider',
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
        ),
        contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      ),
      items: providers.map((p) {
        return DropdownMenuItem(
          value: p.id,
          child: Text(p.name),
        );
      }).toList(),
      onChanged: (value) {
        if (value == null) return;
        final p = providers.firstWhere((p) => p.id == value);
        setState(() {
          _selectedProvider = p;
          _selectedModel = null;
          _models = [];
        });
        _loadModels();
      },
    );
  }

  Widget _buildModelRow(ThemeData theme) {
    return Row(
      children: [
        Expanded(
          child: DropdownButtonFormField<String>(
            initialValue: _selectedModel?.id,
            decoration: InputDecoration(
              labelText: 'Model',
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
              ),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 12,
              ),
            ),
            items: _loadingModels
                ? []
                : _models.map((m) {
                    return DropdownMenuItem(
                      value: m.id,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(m.name, style: const TextStyle(fontSize: 14)),
                          Text(
                            m.description,
                            style: TextStyle(
                              fontSize: 11,
                              color: theme.colorScheme.onSurfaceVariant,
                            ),
                          ),
                        ],
                      ),
                    );
                  }).toList(),
            onChanged: (value) {
              if (value == null) return;
              setState(() {
                _selectedModel = _models.firstWhere((m) => m.id == value);
              });
            },
            hint: _loadingModels
                ? const Text('Loading...')
                : const Text('Select model'),
            isExpanded: true,
          ),
        ),
        const SizedBox(width: 8),
        IconButton(
          onPressed: _refreshingModels ? null : _refreshModels,
          icon: _refreshingModels
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Icon(Icons.refresh, size: 22),
          tooltip: 'Refresh models',
        ),
      ],
    );
  }

  Widget _buildCwdSection(ThemeData theme) {
    final sse = _service;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Working Directory (optional)',
          style: TextStyle(
            fontSize: 12,
            color: theme.colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: 8),
        FutureBuilder<List<DirectoryInfo>>(
          future: sse?.fetchDirectories() ?? Future.value([]),
          builder: (context, snapshot) {
            final dirs = snapshot.data ?? [];
            return Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                ...dirs.take(8).map((d) {
                  final isSelected = _cwdController.text == d.cwd;
                  return FilterChip(
                    avatar: d.activeCount > 0
                        ? Container(
                            width: 8,
                            height: 8,
                            decoration: const BoxDecoration(shape: BoxShape.circle, color: Colors.green),
                          )
                        : null,
                    label: Text(
                      d.project.isNotEmpty ? d.project : d.shortCwd,
                      style: const TextStyle(fontSize: 12, fontFamily: 'monospace'),
                    ),
                    selected: isSelected,
                    onSelected: (selected) {
                      setState(() {
                        if (selected) {
                          _cwdController.text = d.cwd;
                          _showCustomCwd = false;
                        } else {
                          _cwdController.clear();
                        }
                      });
                    },
                  );
                }),
                FilterChip(
                  label: const Text('Custom...', style: TextStyle(fontSize: 12)),
                  selected: _showCustomCwd,
                  onSelected: (selected) {
                    setState(() {
                      _showCustomCwd = selected;
                      if (!selected) _cwdController.clear();
                    });
                  },
                ),
              ],
            );
          },
        ),
        if (_showCustomCwd) ...[
          const SizedBox(height: 8),
          TextField(
            controller: _cwdController,
            decoration: InputDecoration(
              hintText: 'e.g. /home/user/project',
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(12),
              ),
              contentPadding: const EdgeInsets.symmetric(
                horizontal: 14,
                vertical: 10,
              ),
              isDense: true,
            ),
            style: const TextStyle(fontSize: 13, fontFamily: 'monospace'),
          ),
        ],
      ],
    );
  }
}
