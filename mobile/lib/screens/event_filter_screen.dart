import 'dart:convert';
import 'package:flutter/material.dart';

/// Default event types enabled for global mode.
const _defaultGlobalTypes = {'stop', 'stop_failure', 'question', 'permission'};

/// Screen for configuring which event types trigger narration
/// in global vs session mode.
class EventFilterScreen extends StatefulWidget {
  final Map<String, List<Map<String, dynamic>>> eventTypes;
  final String? globalFilterJson;
  final String? sessionFilterJson;
  final Future<void> Function(String key, String value) onUpdate;

  const EventFilterScreen({
    super.key,
    required this.eventTypes,
    this.globalFilterJson,
    this.sessionFilterJson,
    required this.onUpdate,
  });

  @override
  State<EventFilterScreen> createState() => _EventFilterScreenState();
}

class _EventFilterScreenState extends State<EventFilterScreen>
    with SingleTickerProviderStateMixin {
  late TabController _tabController;

  // null means "all allowed" (session default)
  Set<String>? _globalFilter;
  Set<String>? _sessionFilter;

  /// Flattened list of all event types across all providers.
  late List<Map<String, dynamic>> _allTypes;

  @override
  void initState() {
    super.initState();
    _tabController = TabController(length: 2, vsync: this);
    _allTypes = widget.eventTypes.values.expand((v) => v).toList();
    _globalFilter = _parseFilter(widget.globalFilterJson) ??
        Set<String>.from(_defaultGlobalTypes);
    _sessionFilter = _parseFilter(widget.sessionFilterJson);
  }

  @override
  void dispose() {
    _tabController.dispose();
    super.dispose();
  }

  Set<String>? _parseFilter(String? json) {
    if (json == null || json.isEmpty) return null;
    try {
      final list = (jsonDecode(json) as List).cast<String>();
      return Set<String>.from(list);
    } catch (_) {
      return null;
    }
  }

  Set<String> _allTypeNames() {
    return _allTypes.map((t) => t['type'] as String).toSet();
  }

  Future<void> _saveFilter(String mode) async {
    final filter = mode == 'global' ? _globalFilter : _sessionFilter;
    final key = 'reporter.filter.$mode';
    if (filter == null) {
      final all = _allTypeNames().toList();
      await widget.onUpdate(key, jsonEncode(all));
    } else {
      await widget.onUpdate(key, jsonEncode(filter.toList()));
    }
  }

  void _toggleType(String mode, String type, bool enabled) {
    setState(() {
      if (mode == 'global') {
        _globalFilter ??= Set<String>.from(_allTypeNames());
        if (enabled) {
          _globalFilter!.add(type);
        } else {
          _globalFilter!.remove(type);
        }
      } else {
        _sessionFilter ??= Set<String>.from(_allTypeNames());
        if (enabled) {
          _sessionFilter!.add(type);
        } else {
          _sessionFilter!.remove(type);
        }
      }
    });
    _saveFilter(mode);
  }

  void _resetToDefaults(String mode) {
    setState(() {
      if (mode == 'global') {
        _globalFilter = Set<String>.from(_defaultGlobalTypes);
      } else {
        _sessionFilter = null;
      }
    });
    _saveFilter(mode);
  }

  bool _isEnabled(String mode, String type) {
    final filter = mode == 'global' ? _globalFilter : _sessionFilter;
    if (filter == null) return true;
    return filter.contains(type);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Event Filters'),
        bottom: TabBar(
          controller: _tabController,
          tabs: const [
            Tab(text: 'Global'),
            Tab(text: 'Session'),
          ],
        ),
      ),
      body: TabBarView(
        controller: _tabController,
        children: [
          _buildFilterList('global'),
          _buildFilterList('session'),
        ],
      ),
    );
  }

  Widget _buildFilterList(String mode) {
    final grouped = <String, List<Map<String, dynamic>>>{};
    for (final t in _allTypes) {
      final cat = t['category'] as String? ?? 'other';
      grouped.putIfAbsent(cat, () => []).add(t);
    }

    const categoryOrder = [
      'actions',
      'lifecycle',
      'tools',
      'context',
      'subagents',
      'other',
    ];
    const categoryLabels = {
      'actions': 'Actions',
      'lifecycle': 'Lifecycle',
      'tools': 'Tools',
      'context': 'Context',
      'subagents': 'Subagents',
      'other': 'Other',
    };

    final sortedCategories =
        categoryOrder.where(grouped.containsKey).toList();

    return ListView(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
          child: Text(
            mode == 'global'
                ? 'Events narrated when voice is on from the home screen.'
                : 'Events narrated when voice is on from a session.',
            style: TextStyle(
              fontSize: 13,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ),
        for (final cat in sortedCategories) ...[
          _CategoryHeader(categoryLabels[cat] ?? cat),
          for (final t in grouped[cat]!)
            SwitchListTile(
              title: Text(t['label'] as String),
              subtitle: Text(
                t['description'] as String,
                style: TextStyle(
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
              value: _isEnabled(mode, t['type'] as String),
              onChanged: (v) =>
                  _toggleType(mode, t['type'] as String, v),
            ),
        ],
        const SizedBox(height: 16),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: OutlinedButton.icon(
            onPressed: () => _resetToDefaults(mode),
            icon: const Icon(Icons.restore, size: 18),
            label: const Text('Reset to defaults'),
          ),
        ),
        const SizedBox(height: 32),
      ],
    );
  }
}

class _CategoryHeader extends StatelessWidget {
  const _CategoryHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Text(
        title,
        style: Theme.of(context).textTheme.labelLarge?.copyWith(
              color: Theme.of(context).colorScheme.primary,
            ),
      ),
    );
  }
}
