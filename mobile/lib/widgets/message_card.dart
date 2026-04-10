import 'package:flutter/material.dart';
import '../models/message.dart';

class MessageCard extends StatelessWidget {
  final Message message;

  const MessageCard({super.key, required this.message});

  @override
  Widget build(BuildContext context) {
    switch (message.role) {
      case 'user':
        return _UserMessageCard(message: message);
      case 'assistant':
        return _AssistantMessageCard(message: message);
      case 'tool_use':
        return _ToolUseCard(message: message);
      case 'tool_result':
        return _ToolResultCard(message: message);
      default:
        return const SizedBox.shrink();
    }
  }
}

class _UserMessageCard extends StatelessWidget {
  final Message message;
  const _UserMessageCard({required this.message});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        constraints: BoxConstraints(maxWidth: MediaQuery.of(context).size.width * 0.8),
        margin: const EdgeInsets.only(bottom: 8),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: theme.colorScheme.primaryContainer,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(16),
            bottomRight: Radius.circular(4),
          ),
        ),
        child: Text(
          message.content ?? '',
          style: TextStyle(
            fontSize: 14,
            color: theme.colorScheme.onPrimaryContainer,
          ),
        ),
      ),
    );
  }
}

class _AssistantMessageCard extends StatelessWidget {
  final Message message;
  const _AssistantMessageCard({required this.message});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final content = message.content ?? '';
    if (content.isEmpty) return const SizedBox.shrink();

    return Align(
      alignment: Alignment.centerLeft,
      child: Container(
        constraints: BoxConstraints(maxWidth: MediaQuery.of(context).size.width * 0.85),
        margin: const EdgeInsets.only(bottom: 8),
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerHighest,
          borderRadius: const BorderRadius.only(
            topLeft: Radius.circular(16),
            topRight: Radius.circular(16),
            bottomLeft: Radius.circular(4),
            bottomRight: Radius.circular(16),
          ),
        ),
        child: SelectableText(
          content,
          style: TextStyle(
            fontSize: 14,
            color: theme.colorScheme.onSurface,
          ),
        ),
      ),
    );
  }
}

class _ToolUseCard extends StatefulWidget {
  final Message message;
  const _ToolUseCard({required this.message});

  @override
  State<_ToolUseCard> createState() => _ToolUseCardState();
}

class _ToolUseCardState extends State<_ToolUseCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final meta = widget.message.metadata;
    final hasDetails = meta != null && meta.isNotEmpty;

    return GestureDetector(
      onTap: hasDetails ? () => setState(() => _expanded = !_expanded) : null,
      child: Container(
        margin: const EdgeInsets.only(bottom: 4),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: theme.colorScheme.tertiaryContainer.withValues(alpha: 0.3),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(
            color: theme.colorScheme.tertiaryContainer,
            width: 1,
          ),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(
                  _toolIcon(widget.message.tool ?? ''),
                  size: 14,
                  color: theme.colorScheme.tertiary,
                ),
                const SizedBox(width: 8),
                Text(
                  widget.message.tool ?? 'Tool',
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: theme.colorScheme.tertiary,
                  ),
                ),
                if (widget.message.summary != null && widget.message.summary!.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      widget.message.summary!,
                      style: TextStyle(
                        fontSize: 12,
                        fontFamily: 'monospace',
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
                if (hasDetails)
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 16,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
              ],
            ),
            if (_expanded && hasDetails) ...[
              const SizedBox(height: 6),
              Container(
                width: double.infinity,
                padding: const EdgeInsets.all(8),
                decoration: BoxDecoration(
                  color: theme.colorScheme.surface,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: SelectableText(
                  _formatMetadata(meta),
                  style: TextStyle(
                    fontSize: 11,
                    fontFamily: 'monospace',
                    color: theme.colorScheme.onSurface,
                  ),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }

  String _formatMetadata(Map<String, dynamic> meta) {
    final lines = <String>[];
    for (final MapEntry(:key, :value) in meta.entries) {
      if (value is String) {
        if (value.length > 500) {
          lines.add('$key: ${value.substring(0, 500)}...');
        } else {
          lines.add('$key: $value');
        }
      } else {
        lines.add('$key: $value');
      }
    }
    return lines.join('\n');
  }

  IconData _toolIcon(String tool) {
    switch (tool) {
      case 'Read':
        return Icons.description;
      case 'Write':
        return Icons.edit_document;
      case 'Edit':
        return Icons.edit;
      case 'Bash':
        return Icons.terminal;
      case 'Glob':
        return Icons.search;
      case 'Grep':
        return Icons.search;
      case 'Agent':
        return Icons.smart_toy;
      default:
        return Icons.build;
    }
  }
}

class _ToolResultCard extends StatelessWidget {
  final Message message;
  const _ToolResultCard({required this.message});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final success = message.success ?? true;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Row(
        children: [
          Icon(
            success ? Icons.check_circle : Icons.error,
            size: 12,
            color: success ? Colors.green : theme.colorScheme.error,
          ),
          const SizedBox(width: 6),
          Text(
            message.tool != null ? '${message.tool} ${success ? "done" : "failed"}' : (success ? 'done' : 'failed'),
            style: TextStyle(
              fontSize: 11,
              color: theme.colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      ),
    );
  }
}
