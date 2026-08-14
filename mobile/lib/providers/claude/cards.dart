import 'dart:async';
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
  /// Question index → chosen option index. Indices rather than labels: the
  /// daemon resolves them against the question it raised, and two options can
  /// share a label.
  final Map<int, int> _selections = {};
  bool _submitting = false;

  HeliosNotification get n => widget.notification;

  Future<void> _submit() async {
    setState(() => _submitting = true);
    final error = await widget.sse.sendActionError(n.id, {
      'action': 'answer',
      'selections': _selections.entries
          .map((e) => {'question_index': e.key, 'option_index': e.value})
          .toList()
        ..sort((a, b) =>
            (a['question_index'] as int).compareTo(b['question_index'] as int)),
    });
    if (!mounted) return;
    setState(() => _submitting = false);
    if (error != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text("Couldn't answer: $error")),
      );
    }
  }

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
                      Text(header, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 13)),
                      const SizedBox(height: 2),
                    ],
                    Text(question, style: const TextStyle(fontSize: 13)),
                    const SizedBox(height: 6),
                    ...options.asMap().entries.map((oe) {
                      final optionIndex = oe.key;
                      final opt = oe.value;
                      final label = (opt is Map ? opt['label'] : opt)?.toString() ?? '';
                      final isSelected = _selections[questionIndex] == optionIndex;
                      return InkWell(
                        onTap: _submitting
                            ? null
                            : () {
                                setState(() {
                                  _selections[questionIndex] = optionIndex;
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
                              Expanded(child: Text(label, style: const TextStyle(fontSize: 13))),
                            ],
                          ),
                        ),
                      );
                    }),
                    // Helios answers with one choice per question on every
                    // surface, the terminal overlay included.
                    if (multiSelect) ...[
                      const SizedBox(height: 4),
                      Text(
                        'Claude will take several answers here, but helios sends one.',
                        style: TextStyle(
                          fontSize: 11,
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ],
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
                onPressed: _submitting || _selections.length != questions.length
                    ? null
                    : _submit,
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

// ==================== Error Card ====================

/// A turn that died on an API error. Retry sends "continue", which is what a
/// user types in the terminal to pick the turn up where it stopped.
class ClaudeErrorCard extends StatefulWidget {
  final HeliosNotification notification;
  final DaemonAPIService sse;

  const ClaudeErrorCard({
    super.key,
    required this.notification,
    required this.sse,
  });

  @override
  State<ClaudeErrorCard> createState() => _ClaudeErrorCardState();
}

class _ClaudeErrorCardState extends State<ClaudeErrorCard> {
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
    if (left.inHours >= 1) return 'Retry in ${left.inHours}h ${left.inMinutes % 60}m';
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
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
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
                    n.claudeDisplayTitle,
                    style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 14),
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
                n.errorText?.isNotEmpty == true ? n.errorText! : n.displayDetail,
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
                    onPressed:
                        _sending ? null : () => _send({'action': 'dismiss'}),
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
