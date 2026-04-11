import 'dart:convert';
import 'package:flutter/material.dart';
import '../../models/notification.dart';
import '../../services/daemon_api_service.dart';
import 'notification_ext.dart';

// ==================== Permission Card ====================

class ClaudePermissionCard extends StatefulWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;
  final Set<String> selected;
  final VoidCallback onSelectionChanged;

  const ClaudePermissionCard({
    super.key,
    required this.notification,
    required this.sse,
    required this.selected,
    required this.onSelectionChanged,
  });

  @override
  State<ClaudePermissionCard> createState() => _ClaudePermissionCardState();
}

class _ClaudePermissionCardState extends State<ClaudePermissionCard> {
  final Map<String, TextEditingController> _editControllers = {};
  bool _isEditing = false;
  int? _selectedPermissionIdx;

  HeliosNotification get n => widget.notification;

  @override
  void dispose() {
    for (final c in _editControllers.values) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final suggestions = n.permissionSuggestions;

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: Colors.orange.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Checkbox(
                  value: widget.selected.contains(n.id),
                  onChanged: (v) {
                    if (v == true) {
                      widget.selected.add(n.id);
                    } else {
                      widget.selected.remove(n.id);
                    }
                    widget.onSelectionChanged();
                  },
                ),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: Colors.orange.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.orange.withValues(alpha: 0.3)),
                  ),
                  child: const Text('permission', style: TextStyle(fontSize: 11, color: Colors.orange)),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.claudeDisplayTitle,
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(8),
              ),
              constraints: const BoxConstraints(maxHeight: 100),
              child: SingleChildScrollView(
                child: Text(
                  n.displayDetail,
                  style: TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 12,
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
                ),
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: Text(
                    n.cwd,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                Text(
                  n.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            // Quick rules
            if (suggestions != null && suggestions.isNotEmpty) ...[
              const SizedBox(height: 12),
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(
                  border: Border.all(color: Theme.of(context).colorScheme.outlineVariant),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('Quick rules', style: TextStyle(
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    )),
                    const SizedBox(height: 4),
                    ...List.generate(suggestions.length, (i) {
                      final sug = suggestions[i];
                      final label = _formatSuggestion(sug);
                      final selected = _selectedPermissionIdx == i;
                      return InkWell(
                        onTap: () {
                          setState(() {
                            _selectedPermissionIdx = selected ? null : i;
                          });
                        },
                        child: Padding(
                          padding: const EdgeInsets.symmetric(vertical: 2),
                          child: Row(
                            children: [
                              Icon(
                                selected ? Icons.check_box : Icons.check_box_outline_blank,
                                size: 18,
                                color: Theme.of(context).colorScheme.primary,
                              ),
                              const SizedBox(width: 6),
                              Expanded(
                                child: Text(label, style: const TextStyle(fontSize: 12)),
                              ),
                            ],
                          ),
                        ),
                      );
                    }),
                  ],
                ),
              ),
            ],
            // Edit input
            if (_isEditing) ...[
              const SizedBox(height: 8),
              TextField(
                controller: _editControllers.putIfAbsent(
                  n.id,
                  () => TextEditingController(text: _getEditableInput()),
                ),
                maxLines: 3,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
                decoration: InputDecoration(
                  labelText: 'Edit command',
                  border: OutlineInputBorder(borderRadius: BorderRadius.circular(8)),
                  isDense: true,
                ),
              ),
            ],
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: FilledButton(
                    onPressed: _approve,
                    child: const Text('Approve'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: FilledButton(
                    onPressed: () => widget.sse.sendAction(n.id, {'action': 'deny'}),
                    style: FilledButton.styleFrom(
                      backgroundColor: Theme.of(context).colorScheme.error,
                      foregroundColor: Theme.of(context).colorScheme.onError,
                    ),
                    child: const Text('Deny'),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 4),
            Center(
              child: TextButton(
                onPressed: () {
                  setState(() {
                    _isEditing = !_isEditing;
                  });
                },
                child: Text(
                  _isEditing ? 'Cancel editing' : 'Edit before approving',
                  style: const TextStyle(fontSize: 12),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _approve() {
    final body = <String, dynamic>{'action': 'approve'};

    if (_isEditing && _editControllers.containsKey(n.id)) {
      final edited = _editControllers[n.id]!.text;
      final original = _getEditableInput();
      if (edited != original) {
        try {
          body['updated_input'] = jsonDecode(edited);
        } catch (_) {
          body['updated_input'] = {'command': edited};
        }
      }
    }

    if (_selectedPermissionIdx != null) {
      body['apply_permission'] = _selectedPermissionIdx;
    }

    widget.sse.sendAction(n.id, body);
  }

  String _getEditableInput() {
    final ti = n.payload?['tool_input'];
    if (ti is String) return ti;
    if (ti is Map) {
      final cmd = ti['command'];
      if (cmd is String) return cmd;
      return jsonEncode(ti);
    }
    return '';
  }

  String _formatSuggestion(dynamic sug) {
    if (sug is! Map) return sug.toString();
    final rules = sug['rules'] as List?;
    if (rules == null || rules.isEmpty) return 'Always allow';
    final rule = rules.first;
    final toolName = rule['toolName']?.toString() ?? '';
    final content = rule['ruleContent']?.toString() ?? '';
    if (content.isNotEmpty) {
      return 'Always allow $toolName($content)';
    }
    return 'Always allow $toolName';
  }
}

// ==================== Question Card ====================

class ClaudeQuestionCard extends StatefulWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ClaudeQuestionCard({
    super.key,
    required this.notification,
    required this.sse,
  });

  @override
  State<ClaudeQuestionCard> createState() => _ClaudeQuestionCardState();
}

class _ClaudeQuestionCardState extends State<ClaudeQuestionCard> {
  final Map<String, String> _answers = {};

  HeliosNotification get n => widget.notification;

  @override
  Widget build(BuildContext context) {
    final questions = n.questions ?? [];

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: Colors.blue.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: Colors.blue.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.blue.withValues(alpha: 0.3)),
                  ),
                  child: const Text('question', style: TextStyle(fontSize: 11, color: Colors.blue)),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.claudeDisplayTitle,
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            ...questions.map((q) {
              if (q is! Map) return const SizedBox.shrink();
              final question = q['question']?.toString() ?? '';
              final header = q['header']?.toString();
              final options = (q['options'] as List?) ?? [];
              final multiSelect = q['multiSelect'] == true;

              return Padding(
                padding: const EdgeInsets.only(bottom: 12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (header != null) ...[
                      Text(header, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 13)),
                      const SizedBox(height: 2),
                    ],
                    Text(question, style: const TextStyle(fontSize: 13)),
                    const SizedBox(height: 6),
                    ...options.map((opt) {
                      final label = (opt is Map ? opt['label'] : opt)?.toString() ?? '';
                      if (multiSelect) {
                        final currentAnswers = (_answers[question] ?? '').split(', ').where((s) => s.isNotEmpty).toSet();
                        final isSelected = currentAnswers.contains(label);
                        return InkWell(
                          onTap: () {
                            setState(() {
                              if (isSelected) {
                                currentAnswers.remove(label);
                              } else {
                                currentAnswers.add(label);
                              }
                              _answers[question] = currentAnswers.join(', ');
                            });
                          },
                          child: Padding(
                            padding: const EdgeInsets.symmetric(vertical: 2),
                            child: Row(
                              children: [
                                Icon(
                                  isSelected ? Icons.check_box : Icons.check_box_outline_blank,
                                  size: 20,
                                  color: Theme.of(context).colorScheme.primary,
                                ),
                                const SizedBox(width: 8),
                                Text(label, style: const TextStyle(fontSize: 13)),
                              ],
                            ),
                          ),
                        );
                      } else {
                        final isSelected = _answers[question] == label;
                        return InkWell(
                          onTap: () {
                            setState(() {
                              _answers[question] = label;
                            });
                          },
                          child: Padding(
                            padding: const EdgeInsets.symmetric(vertical: 2),
                            child: Row(
                              children: [
                                Icon(
                                  isSelected ? Icons.radio_button_checked : Icons.radio_button_unchecked,
                                  size: 20,
                                  color: Theme.of(context).colorScheme.primary,
                                ),
                                const SizedBox(width: 8),
                                Text(label, style: const TextStyle(fontSize: 13)),
                              ],
                            ),
                          ),
                        );
                      }
                    }),
                  ],
                ),
              );
            }),
            Row(
              children: [
                Expanded(
                  child: Text(
                    n.cwd,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                Text(
                  n.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            SizedBox(
              width: double.infinity,
              child: FilledButton(
                onPressed: _answers.isNotEmpty
                    ? () => widget.sse.sendAction(n.id, {'action': 'answer', 'answers': _answers})
                    : null,
                child: Text(questions.length > 1 ? 'Submit Answers' : 'Submit Answer'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ==================== Elicitation Form Card (Stub) ====================

class ClaudeElicitationFormCard extends StatelessWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ClaudeElicitationFormCard({
    super.key,
    required this.notification,
    required this.sse,
  });

  @override
  Widget build(BuildContext context) {
    final n = notification;
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: Colors.purple.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: Colors.purple.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.purple.withValues(alpha: 0.3)),
                  ),
                  child: const Text('input', style: TextStyle(fontSize: 11, color: Colors.purple)),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.mcpServerName ?? 'MCP Server',
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Text(
              n.elicitationMessage ?? n.displayDetail,
              style: const TextStyle(fontSize: 13),
            ),
            const SizedBox(height: 12),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Text(
                'Form input not yet supported.\nDecline to let the agent continue.',
                style: TextStyle(
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
                textAlign: TextAlign.center,
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: Text(
                    n.cwd,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                Text(
                  n.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            SizedBox(
              width: double.infinity,
              child: FilledButton(
                onPressed: () => sse.sendAction(n.id, {'action': 'decline'}),
                style: FilledButton.styleFrom(
                  backgroundColor: Theme.of(context).colorScheme.error,
                  foregroundColor: Theme.of(context).colorScheme.onError,
                ),
                child: const Text('Decline'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ==================== Elicitation URL Card ====================

// ==================== Trust Card ====================

class ClaudeTrustCard extends StatelessWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ClaudeTrustCard({
    super.key,
    required this.notification,
    required this.sse,
  });

  @override
  Widget build(BuildContext context) {
    final n = notification;
    final cwd = n.payload?['cwd']?.toString() ?? n.cwd;

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: Colors.teal.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: Colors.teal.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.teal.withValues(alpha: 0.3)),
                  ),
                  child: const Text('trust', style: TextStyle(fontSize: 11, color: Colors.teal)),
                ),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    'Workspace Trust Required',
                    style: TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            const Text(
              'Claude is asking to trust the files in this workspace before proceeding.',
              style: TextStyle(fontSize: 13),
            ),
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Text(
                cwd,
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 12,
                  color: Theme.of(context).colorScheme.onSurface,
                ),
              ),
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                const Spacer(),
                Text(
                  n.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: FilledButton(
                    onPressed: () => sse.sendAction(n.id, {'action': 'trust'}),
                    child: const Text('Trust & Proceed'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: FilledButton(
                    onPressed: () => sse.sendAction(n.id, {'action': 'deny'}),
                    style: FilledButton.styleFrom(
                      backgroundColor: Theme.of(context).colorScheme.error,
                      foregroundColor: Theme.of(context).colorScheme.onError,
                    ),
                    child: const Text('Deny'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

// ==================== Elicitation URL Card ====================

class ClaudeElicitationUrlCard extends StatelessWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ClaudeElicitationUrlCard({
    super.key,
    required this.notification,
    required this.sse,
  });

  @override
  Widget build(BuildContext context) {
    final n = notification;
    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(
          color: Colors.purple.withValues(alpha: 0.3),
          width: 1,
        ),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                  decoration: BoxDecoration(
                    color: Colors.purple.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: Colors.purple.withValues(alpha: 0.3)),
                  ),
                  child: const Text('auth', style: TextStyle(fontSize: 11, color: Colors.purple)),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.mcpServerName ?? 'MCP Server',
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Text(
              n.elicitationMessage ?? n.displayDetail,
              style: const TextStyle(fontSize: 13),
            ),
            const SizedBox(height: 12),
            if (n.elicitationUrl != null)
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.surfaceContainerHighest,
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Text(
                  n.elicitationUrl!,
                  style: TextStyle(
                    fontFamily: 'monospace',
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurface,
                  ),
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: Text(
                    n.cwd,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 11,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                Text(
                  n.timeAgo,
                  style: TextStyle(
                    fontSize: 11,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: FilledButton(
                    onPressed: () => sse.sendAction(n.id, {'action': 'accept'}),
                    child: const Text('Done'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: FilledButton(
                    onPressed: () => sse.sendAction(n.id, {'action': 'decline'}),
                    style: FilledButton.styleFrom(
                      backgroundColor: Theme.of(context).colorScheme.error,
                      foregroundColor: Theme.of(context).colorScheme.onError,
                    ),
                    child: const Text('Decline'),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
