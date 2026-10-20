import 'package:flutter/material.dart';

/// The sheet that asks the session's agent about what is on screen.
///
/// Shared by the file viewer and the diff viewer, which used to carry a copy
/// each — and the same two faults each: no sign that anything was being sent,
/// and no word when it failed (spec 65, F6).
///
/// [send] posts the question and answers null when the agent took it, or the
/// reason it did not. While it runs the send button is a spinner and the field
/// is locked. On success the sheet closes and [onSent] runs — the caller pops
/// to the session there. On failure the sheet closes too, and a snackbar says
/// why, with an Edit action that opens the sheet again holding the question.
Future<void> showAskAISheet(
  BuildContext context, {
  required String label,
  required String hint,
  required Future<String?> Function(String question) send,
  required VoidCallback onSent,
  String initialText = '',
}) async {
  final messenger = ScaffoldMessenger.of(context);
  final outcome = await showModalBottomSheet<(String, String?)>(
    context: context,
    isScrollControlled: true,
    builder: (ctx) => _AskAISheet(
      label: label,
      hint: hint,
      initialText: initialText,
      send: send,
    ),
  );
  if (outcome == null) return;
  final (question, failure) = outcome;
  if (failure == null) {
    onSent();
    return;
  }
  // One failure on screen at a time: a second one replaces the first rather
  // than queueing behind it.
  dismissAskAIFailure();
  messenger.hideCurrentSnackBar();
  final shown = messenger.showSnackBar(
    SnackBar(
      content: Text(failure),
      behavior: SnackBarBehavior.floating,
      // A snackbar with an action stays until dismissed on recent Flutter,
      // and the messenger is the app's, so it would follow the reader from
      // screen to screen. Six seconds is long enough to tap Edit.
      duration: const Duration(seconds: 6),
      persist: false,
      action: SnackBarAction(
        label: 'Edit',
        onPressed: () {
          if (!context.mounted) return;
          showAskAISheet(
            context,
            label: label,
            hint: hint,
            send: send,
            onSent: onSent,
            initialText: question,
          );
        },
      ),
    ),
  );
  _failure = (messenger: messenger, controller: shown);
  shown.closed.then((_) {
    if (_failure?.controller == shown) _failure = null;
  });
}

/// The failure snackbar on screen, if any, and the messenger showing it.
({
  ScaffoldMessengerState messenger,
  ScaffoldFeatureController<SnackBar, SnackBarClosedReason> controller,
})?
_failure;

/// Takes down the failure snackbar. The screens that offer Ask AI call this
/// as they go, so a failure about one file is not still up over the next.
void dismissAskAIFailure() {
  final failure = _failure;
  if (failure == null) return;
  _failure = null;
  if (failure.messenger.mounted) failure.messenger.hideCurrentSnackBar();
}

class _AskAISheet extends StatefulWidget {
  final String label;
  final String hint;
  final String initialText;
  final Future<String?> Function(String question) send;

  const _AskAISheet({
    required this.label,
    required this.hint,
    required this.initialText,
    required this.send,
  });

  @override
  State<_AskAISheet> createState() => _AskAISheetState();
}

class _AskAISheetState extends State<_AskAISheet> {
  late final _controller = TextEditingController(text: widget.initialText);
  bool _sending = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  /// Pops with the question and, on failure, the reason — which is what
  /// [showAskAISheet] reads. Dismissing the sheet pops with null.
  Future<void> _submit() async {
    final question = _controller.text.trim();
    if (question.isEmpty || _sending) return;
    setState(() => _sending = true);
    final error = await widget.send(question);
    if (!mounted) return;
    Navigator.of(context).pop((question, error));
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final accent = theme.colorScheme.primary;
    return Padding(
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(context).viewInsets.bottom,
      ),
      child: SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(Icons.insert_drive_file, size: 20, color: accent),
                  const SizedBox(width: 6),
                  Flexible(
                    child: Text(
                      widget.label,
                      style: TextStyle(
                        fontSize: 13,
                        fontFamily: 'monospace',
                        fontWeight: FontWeight.w600,
                        color: accent,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _controller,
                autofocus: true,
                enabled: !_sending,
                minLines: 1,
                maxLines: 4,
                style: const TextStyle(fontSize: 14),
                decoration: InputDecoration(
                  hintText: widget.hint,
                  hintStyle: TextStyle(
                    fontSize: 14,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                  border: OutlineInputBorder(
                    borderRadius: BorderRadius.circular(10),
                  ),
                  contentPadding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 10,
                  ),
                  suffixIcon: _sending
                      ? const Padding(
                          padding: EdgeInsets.all(14),
                          child: SizedBox(
                            width: 20,
                            height: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          ),
                        )
                      : IconButton(
                          icon: const Icon(Icons.send, size: 20),
                          tooltip: 'Send',
                          onPressed: _submit,
                        ),
                ),
                onSubmitted: (_) => _submit(),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
