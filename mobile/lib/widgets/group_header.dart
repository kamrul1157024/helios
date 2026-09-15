import 'package:flutter/material.dart';

import '../utils/grouping.dart';

/// A stable colour for a group, drawn from its key.
///
/// Twelve hues rather than the whole wheel: adjacent hues are not
/// distinguishable at this size, so a continuous hash spends its range on
/// differences nobody can see. Saturation and lightness are fixed so no group
/// gets a badge that shouts.
Color tintOf(String key) {
  var hash = 0;
  for (final unit in key.codeUnits) {
    hash = (hash * 31 + unit) % 4096;
  }
  return HSLColor.fromAHSL(1, (hash % 12) * 30.0, 0.62, 0.62).toColor();
}

/// The row that stands for a group: its colour, its name, and how many
/// sessions are under it.
///
/// Tapping folds it. A long press opens whatever the caller offers, which is
/// nothing on Ungrouped and nothing on a directory — one is synthetic and the
/// other's key is a path the daemon never stored.
class GroupHeader extends StatelessWidget {
  final GroupNode node;
  final int depth;
  final bool folded;
  final VoidCallback onTap;
  final VoidCallback? onMenu;

  /// Set while a session is being dragged over this header.
  final bool highlighted;

  const GroupHeader({
    super.key,
    required this.node,
    required this.depth,
    required this.folded,
    required this.onTap,
    this.onMenu,
    this.highlighted = false,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tint = node.isUngrouped
        ? theme.colorScheme.outline
        : tintOf(node.key);

    return InkWell(
      onTap: onTap,
      onLongPress: onMenu,
      child: Container(
        color: highlighted
            ? theme.colorScheme.primary.withValues(alpha: 0.12)
            : null,
        padding: EdgeInsets.only(
          left: 4.0 + depth * 14,
          right: 8,
          top: 6,
          bottom: 6,
        ),
        child: Row(
          children: [
            Icon(
              folded ? Icons.chevron_right : Icons.expand_more,
              size: 18,
              color: theme.colorScheme.onSurfaceVariant,
            ),
            const SizedBox(width: 2),
            Container(
              width: 8,
              height: 8,
              decoration: BoxDecoration(color: tint, shape: BoxShape.circle),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                node.name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  letterSpacing: 0.3,
                  color: node.isUngrouped
                      ? theme.colorScheme.onSurfaceVariant
                      : theme.colorScheme.onSurface,
                ),
              ),
            ),
            const SizedBox(width: 8),
            // The subtree, not the level: a folded group that says 2 while
            // holding twelve is worse than no count at all.
            Text(
              '${node.total}',
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            if (onMenu != null)
              IconButton(
                icon: const Icon(Icons.more_horiz, size: 18),
                visualDensity: VisualDensity.compact,
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
                tooltip: 'Group options',
                onPressed: onMenu,
              ),
          ],
        ),
      ),
    );
  }
}
