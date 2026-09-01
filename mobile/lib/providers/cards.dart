import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import '../models/notification.dart';
import '../services/daemon_api_service.dart';
import 'notification_ext.dart';

// ==================== Permission Card ====================

class PermissionCard extends StatefulWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;
  final Set<String> selected;
  final VoidCallback onSelectionChanged;

  const PermissionCard({
    super.key,
    required this.notification,
    required this.sse,
    required this.selected,
    required this.onSelectionChanged,
  });

  @override
  State<PermissionCard> createState() => _PermissionCardState();
}

class _PermissionCardState extends State<PermissionCard> {
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
        side: BorderSide(color: Colors.orange.withValues(alpha: 0.3), width: 1),
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
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.orange.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(
                      color: Colors.orange.withValues(alpha: 0.3),
                    ),
                  ),
                  child: const Text(
                    'permission',
                    style: TextStyle(fontSize: 11, color: Colors.orange),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.displayTitle,
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 14,
                    ),
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
              constraints: const BoxConstraints(maxHeight: 160),
              child: SingleChildScrollView(
                child: Text(
                  _displayInput(),
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
                  border: Border.all(
                    color: Theme.of(context).colorScheme.outlineVariant,
                  ),
                  borderRadius: BorderRadius.circular(8),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'Quick rules',
                      style: TextStyle(
                        fontSize: 11,
                        fontWeight: FontWeight.w600,
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                    ),
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
                                selected
                                    ? Icons.check_box
                                    : Icons.check_box_outline_blank,
                                size: 18,
                                color: Theme.of(context).colorScheme.primary,
                              ),
                              const SizedBox(width: 6),
                              Expanded(
                                child: Text(
                                  label,
                                  style: const TextStyle(fontSize: 12),
                                ),
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
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(8),
                  ),
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
                    onPressed: () =>
                        widget.sse.sendAction(n.id, {'action': 'deny'}),
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

  /// What the tool will actually do, laid out to be read.
  ///
  /// The notification's own detail is the command cut to 100 characters, which
  /// hides the end of the very thing being approved. Encoding the input as JSON
  /// instead is worse: a heredoc becomes one line of \n escapes. So the command
  /// is shown as it is, and any other input as one field per block with its
  /// multi-line values printed as the lines they are.
  String _displayInput() {
    final ti = n.payload?['tool_input'];
    if (ti is String && ti.isNotEmpty) return ti;
    if (ti is Map) {
      final cmd = ti['command'];
      if (cmd is String) return cmd;
      if (ti.isNotEmpty) {
        return ti.entries
            .map((e) {
              final v = e.value;
              if (v is String && v.contains('\n')) return '${e.key}:\n$v';
              return '${e.key}: ${v is String ? v : jsonEncode(v)}';
            })
            .join('\n\n');
      }
    }
    return n.displayDetail;
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

class QuestionCard extends StatefulWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const QuestionCard({
    super.key,
    required this.notification,
    required this.sse,
  });

  @override
  State<QuestionCard> createState() => _QuestionCardState();
}

class _QuestionCardState extends State<QuestionCard> {
  /// Question index → chosen option indices. Indices rather than labels: the
  /// daemon resolves them against the question it raised, and two options can
  /// share a label. A multi-select question keeps several; the rest hold one.
  final Map<int, Set<int>> _selections = {};

  /// Question index → what the user typed instead of picking.
  final Map<int, TextEditingController> _typed = {};
  bool _submitting = false;

  HeliosNotification get n => widget.notification;

  @override
  void dispose() {
    for (final controller in _typed.values) {
      controller.dispose();
    }
    super.dispose();
  }

  TextEditingController _controllerFor(int questionIndex) =>
      _typed.putIfAbsent(questionIndex, () {
        final c = TextEditingController();
        c.addListener(() => setState(() {}));
        return c;
      });

  String _textFor(int questionIndex) =>
      _typed[questionIndex]?.text.trim() ?? '';

  bool _answered(int questionIndex) =>
      (_selections[questionIndex]?.isNotEmpty ?? false) ||
      _textFor(questionIndex).isNotEmpty;

  /// The wire carries one text field for the whole set, so an answer past the
  /// first says which question it belongs to.
  String _writtenAnswers(List<dynamic> questions) {
    final lines = <String>[];
    for (var i = 0; i < questions.length; i++) {
      final text = _textFor(i);
      if (text.isEmpty) continue;
      if (questions.length == 1) return text;
      final q = questions[i];
      final header =
          (q is Map ? (q['header'] ?? q['question']) : null)?.toString() ??
          'Question ${i + 1}';
      lines.add('$header: $text');
    }
    return lines.join('\n');
  }

  Future<void> _submit(List<dynamic> questions) async {
    setState(() => _submitting = true);
    final selections = <Map<String, int>>[];
    for (final entry in _selections.entries) {
      for (final optionIndex in entry.value.toList()..sort()) {
        selections.add({
          'question_index': entry.key,
          'option_index': optionIndex,
        });
      }
    }
    selections.sort(
      (a, b) =>
          (a['question_index'] as int).compareTo(b['question_index'] as int),
    );

    final error = await widget.sse.sendActionError(n.id, {
      'action': 'answer',
      'selections': selections,
      'text': _writtenAnswers(questions),
    });
    if (!mounted) return;
    setState(() => _submitting = false);
    if (error != null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text("Couldn't answer: $error")));
    }
  }

  @override
  Widget build(BuildContext context) {
    final questions = n.questions ?? [];

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: Colors.blue.withValues(alpha: 0.3), width: 1),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.blue.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(
                      color: Colors.blue.withValues(alpha: 0.3),
                    ),
                  ),
                  child: const Text(
                    'question',
                    style: TextStyle(fontSize: 11, color: Colors.blue),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.displayTitle,
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 14,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            ...questions.asMap().entries.map((qe) {
              final q = qe.value;
              if (q is! Map) return const SizedBox.shrink();
              final questionIndex = qe.key;
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
                      Text(
                        header,
                        style: const TextStyle(
                          fontWeight: FontWeight.w600,
                          fontSize: 13,
                        ),
                      ),
                      const SizedBox(height: 2),
                    ],
                    Text(question, style: const TextStyle(fontSize: 13)),
                    const SizedBox(height: 6),
                    ...options.asMap().entries.map((oe) {
                      final optionIndex = oe.key;
                      final opt = oe.value;
                      final label =
                          (opt is Map ? opt['label'] : opt)?.toString() ?? '';
                      final description = opt is Map
                          ? opt['description']?.toString()
                          : null;
                      final isSelected =
                          _selections[questionIndex]?.contains(optionIndex) ??
                          false;
                      return InkWell(
                        onTap: _submitting
                            ? null
                            : () {
                                setState(() {
                                  final held =
                                      _selections[questionIndex] ?? <int>{};
                                  if (!multiSelect) {
                                    _selections[questionIndex] = {optionIndex};
                                  } else if (!held.remove(optionIndex)) {
                                    held.add(optionIndex);
                                    _selections[questionIndex] = held;
                                  } else {
                                    _selections[questionIndex] = held;
                                  }
                                });
                              },
                        child: Padding(
                          padding: const EdgeInsets.symmetric(vertical: 2),
                          child: Row(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Icon(
                                multiSelect
                                    ? (isSelected
                                          ? Icons.check_box
                                          : Icons.check_box_outline_blank)
                                    : (isSelected
                                          ? Icons.radio_button_checked
                                          : Icons.radio_button_unchecked),
                                size: 20,
                                color: Theme.of(context).colorScheme.primary,
                              ),
                              const SizedBox(width: 8),
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      label,
                                      style: const TextStyle(fontSize: 13),
                                    ),
                                    if (description != null &&
                                        description.isNotEmpty)
                                      Text(
                                        description,
                                        style: TextStyle(
                                          fontSize: 11.5,
                                          color: Theme.of(
                                            context,
                                          ).colorScheme.onSurfaceVariant,
                                        ),
                                      ),
                                  ],
                                ),
                              ),
                            ],
                          ),
                        ),
                      );
                    }),
                    const SizedBox(height: 4),
                    TextField(
                      controller: _controllerFor(questionIndex),
                      enabled: !_submitting,
                      style: const TextStyle(fontSize: 13),
                      decoration: const InputDecoration(
                        isDense: true,
                        labelText: 'Other',
                        hintText: 'Answer in your own words',
                      ),
                    ),
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
                // Every question, not just one: a gap comes back to Claude as
                // a question nobody answered.
                onPressed:
                    _submitting ||
                        !List.generate(
                          questions.length,
                          _answered,
                        ).every((ok) => ok)
                    ? null
                    : () => _submit(questions),
                child: Text(
                  questions.length > 1 ? 'Submit Answers' : 'Submit Answer',
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ==================== Elicitation Form Card (Stub) ====================

class ElicitationFormCard extends StatelessWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ElicitationFormCard({
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
        side: BorderSide(color: Colors.purple.withValues(alpha: 0.3), width: 1),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.purple.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(
                      color: Colors.purple.withValues(alpha: 0.3),
                    ),
                  ),
                  child: const Text(
                    'input',
                    style: TextStyle(fontSize: 11, color: Colors.purple),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.mcpServerName ?? 'MCP Server',
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 14,
                    ),
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

class TrustCard extends StatelessWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const TrustCard({super.key, required this.notification, required this.sse});

  @override
  Widget build(BuildContext context) {
    final n = notification;
    final cwd = n.payload?['cwd']?.toString() ?? n.cwd;

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: Colors.teal.withValues(alpha: 0.3), width: 1),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.teal.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(
                      color: Colors.teal.withValues(alpha: 0.3),
                    ),
                  ),
                  child: const Text(
                    'trust',
                    style: TextStyle(fontSize: 11, color: Colors.teal),
                  ),
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

class ElicitationUrlCard extends StatelessWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ElicitationUrlCard({
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
        side: BorderSide(color: Colors.purple.withValues(alpha: 0.3), width: 1),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.purple.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(
                      color: Colors.purple.withValues(alpha: 0.3),
                    ),
                  ),
                  child: const Text(
                    'auth',
                    style: TextStyle(fontSize: 11, color: Colors.purple),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.mcpServerName ?? 'MCP Server',
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 14,
                    ),
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
                    onPressed: () =>
                        sse.sendAction(n.id, {'action': 'decline'}),
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

// ==================== Error Card ====================

/// A turn that died on an API error. Retry sends "continue", which is what a
/// user types in the terminal to pick the turn up where it stopped.
class ErrorCard extends StatefulWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ErrorCard({super.key, required this.notification, required this.sse});

  @override
  State<ErrorCard> createState() => _ErrorCardState();
}

class _ErrorCardState extends State<ErrorCard> {
  Timer? _ticker;
  bool _sending = false;

  @override
  void initState() {
    super.initState();
    // Only a rate limit with a known reset time needs a countdown; everything
    // else is retryable immediately and has nothing to tick.
    if (_resetsInTheFuture) {
      _ticker = Timer.periodic(const Duration(seconds: 1), (_) {
        if (!mounted) return;
        setState(() {});
        if (!_resetsInTheFuture) {
          _ticker?.cancel();
          _ticker = null;
        }
      });
    }
  }

  @override
  void dispose() {
    _ticker?.cancel();
    super.dispose();
  }

  bool get _resetsInTheFuture {
    final reset = widget.notification.rateLimitResetAt;
    return reset != null && reset.isAfter(DateTime.now().toUtc());
  }

  /// Time until the limit lifts, rendered coarsely — a second-by-second
  /// countdown on a multi-hour window is noise.
  String get _remainingLabel {
    final reset = widget.notification.rateLimitResetAt;
    if (reset == null) return '';
    final left = reset.difference(DateTime.now().toUtc());
    if (left.inHours >= 1) {
      return 'Retry in ${left.inHours}h ${left.inMinutes % 60}m';
    }
    if (left.inMinutes >= 1) return 'Retry in ${left.inMinutes}m';
    return 'Retry in ${left.inSeconds}s';
  }

  Future<void> _send(Map<String, dynamic> body) async {
    setState(() => _sending = true);
    final ok = await widget.sse.sendAction(widget.notification.id, body);
    if (!mounted) return;
    setState(() => _sending = false);
    if (!ok) {
      // The daemon rejects a retry when the session has no live terminal.
      // Waking one goes through the composer's send path, not this action.
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text('Retry failed — send a prompt to wake the session.'),
        ),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final n = widget.notification;
    final theme = Theme.of(context);
    final blocked = _resetsInTheFuture;
    final accent = n.isRateLimit ? Colors.orange : theme.colorScheme.error;

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: BorderSide(color: accent.withValues(alpha: 0.3), width: 1),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 2,
                  ),
                  decoration: BoxDecoration(
                    color: accent.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(4),
                    border: Border.all(color: accent.withValues(alpha: 0.3)),
                  ),
                  child: Text(
                    n.isRateLimit ? 'rate limit' : 'error',
                    style: TextStyle(fontSize: 11, color: accent),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    n.displayTitle,
                    style: const TextStyle(
                      fontWeight: FontWeight.w600,
                      fontSize: 14,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(10),
              decoration: BoxDecoration(
                color: theme.colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Text(
                n.errorText?.isNotEmpty == true
                    ? n.errorText!
                    : n.displayDetail,
                style: TextStyle(
                  fontFamily: 'monospace',
                  fontSize: 12,
                  color: theme.colorScheme.onSurface,
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
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: FilledButton(
                    onPressed: (blocked || _sending)
                        ? null
                        : () => _send({'action': 'retry'}),
                    child: Text(blocked ? _remainingLabel : 'Retry'),
                  ),
                ),
                const SizedBox(width: 8),
                Expanded(
                  child: OutlinedButton(
                    onPressed: _sending
                        ? null
                        : () => _send({'action': 'dismiss'}),
                    child: const Text('Dismiss'),
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
