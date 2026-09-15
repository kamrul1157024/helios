import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/session_group.dart';
import '../providers/grouping_providers.dart';
import '../utils/grouping.dart';
import 'group_header.dart';

/// How the list is arranged: whether it is grouped at all, and what orders the
/// directory groups.
///
/// The groups themselves are not managed here. They are made, renamed, moved
/// and deleted on the tree, where the one being pointed at is the one that
/// changes.
///
/// [unsupported] says the host's daemon has no groups. The manual choice still
/// shows, with the reason under it: hiding it would leave a phone that cannot
/// group and no explanation of why.
///
/// The session order lives here too, as it does on the desktop: two controls
/// that both arrange the list, sitting apart, is one question asked twice.
Future<void> showGroupingSheet(
  BuildContext context,
  rp.WidgetRef ref, {
  required bool unsupported,
  required String hostName,
  required bool manualOrder,
  required ValueChanged<bool> onManualOrder,
}) {
  return showModalBottomSheet<void>(
    context: context,
    builder: (sheetContext) => rp.Consumer(
      builder: (context, innerRef, _) {
        final prefs = innerRef.watch(groupingProvider);
        final writer = innerRef.read(groupingPrefsProvider.notifier);
        final theme = Theme.of(context);

        Widget choice({
          required String label,
          required String hint,
          required bool on,
          required VoidCallback onTap,
        }) => ListTile(
          leading: Icon(
            on ? Icons.radio_button_checked : Icons.radio_button_unchecked,
            color: on ? theme.colorScheme.primary : null,
          ),
          title: Text(label),
          subtitle: hint.isEmpty
              ? null
              : Text(hint, style: const TextStyle(fontSize: 12)),
          onTap: onTap,
        );

        return SafeArea(
          child: ListView(
            shrinkWrap: true,
            children: [
              const Padding(
                padding: EdgeInsets.fromLTRB(16, 16, 16, 4),
                child: Text(
                  'Group sessions by',
                  style: TextStyle(fontWeight: FontWeight.w600),
                ),
              ),
              choice(
                label: 'Off',
                hint: 'One flat list.',
                on: prefs.mode == GroupMode.off,
                onTap: () => writer.setMode(GroupMode.off),
              ),
              choice(
                label: 'Groups',
                hint:
                    'Groups you make and keep, nested as deep as you like. '
                    'Long-press a session to file it, long-press a group to '
                    'rename or delete it.',
                on: prefs.mode == GroupMode.manual,
                onTap: () => writer.setMode(GroupMode.manual),
              ),
              if (unsupported && prefs.mode == GroupMode.manual)
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                  child: Text(
                    '$hostName is running a daemon without groups. Update it '
                    'to make groups here.',
                    style: TextStyle(
                      fontSize: 12,
                      color: theme.colorScheme.error,
                    ),
                  ),
                ),
              choice(
                label: 'Directory',
                hint:
                    'One group per working directory, worked out from the '
                    'sessions. Nothing to set up, but it is a single level and '
                    'a session cannot be moved between directories.',
                on: prefs.mode == GroupMode.auto,
                onTap: () => writer.setMode(GroupMode.auto),
              ),
              // Directory only. A made group sits where it was put, and a
              // choice here would undo that the moment it landed.
              if (prefs.mode == GroupMode.auto) ...[
                const Divider(height: 1),
                const Padding(
                  padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
                  child: Text(
                    'Order groups by',
                    style: TextStyle(fontWeight: FontWeight.w600),
                  ),
                ),
                for (final (order, label) in const [
                  (GroupOrder.activity, 'Activity'),
                  (GroupOrder.name, 'Name A→Z'),
                  (GroupOrder.manual, 'Manual — as moved'),
                ])
                  choice(
                    label: label,
                    hint: '',
                    on: prefs.order == order,
                    onTap: () => writer.setOrder(order),
                  ),
              ],
              const Divider(height: 1),
              const Padding(
                padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
                child: Text(
                  'Order sessions by',
                  style: TextStyle(fontWeight: FontWeight.w600),
                ),
              ),
              choice(
                label: 'Activity',
                hint: 'Active first, then most recent.',
                on: !manualOrder,
                onTap: () {
                  Navigator.pop(sheetContext);
                  onManualOrder(false);
                },
              ),
              choice(
                label: 'Manual',
                hint: 'Hold the grip on a card to move it.',
                on: manualOrder,
                onTap: () {
                  Navigator.pop(sheetContext);
                  onManualOrder(true);
                },
              ),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    ),
  );
}

/// Picks a group out of the catalogue.
///
/// Returns the key chosen, an empty string for "no group", or null when the
/// sheet is dismissed. A group cannot be moved into its own subtree, so
/// [excludeSubtreeOf] takes that branch out of the list rather than offering it
/// and refusing afterwards.
Future<String?> showGroupPicker(
  BuildContext context, {
  required GroupCatalog catalog,
  String? current,
  String? excludeSubtreeOf,
  String title = 'Move to group',
  String rootLabel = 'No group',
  Future<String?> Function()? onCreate,
}) {
  bool excluded(String key) =>
      excludeSubtreeOf != null &&
      (key == excludeSubtreeOf || catalog.isDescendant(key, excludeSubtreeOf));

  return showModalBottomSheet<String>(
    context: context,
    isScrollControlled: true,
    builder: (ctx) {
      final theme = Theme.of(ctx);

      List<Widget> rowsUnder(String parent, int depth) => [
        for (final group in catalog.childrenOf(parent))
          if (!excluded(group.key)) ...[
            ListTile(
              contentPadding: EdgeInsets.only(left: 16.0 + depth * 16, right: 16),
              dense: true,
              leading: Container(
                width: 10,
                height: 10,
                decoration: BoxDecoration(
                  color: tintOf(group.key),
                  shape: BoxShape.circle,
                ),
              ),
              title: Text(group.name),
              trailing: group.key == current
                  ? Icon(Icons.check, color: theme.colorScheme.primary)
                  : null,
              onTap: () => Navigator.pop(ctx, group.key),
            ),
            ...rowsUnder(group.key, depth + 1),
          ],
      ];

      return SafeArea(
        child: ConstrainedBox(
          constraints: BoxConstraints(
            maxHeight: MediaQuery.of(ctx).size.height * 0.7,
          ),
          child: ListView(
            shrinkWrap: true,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Text(title, style: theme.textTheme.titleSmall),
              ),
              const Divider(height: 1),
              if (onCreate != null)
                ListTile(
                  dense: true,
                  leading: const Icon(Icons.create_new_folder_outlined),
                  title: const Text('New group…'),
                  onTap: () async {
                    final made = await onCreate();
                    if (made != null && ctx.mounted) Navigator.pop(ctx, made);
                  },
                ),
              ListTile(
                dense: true,
                leading: const Icon(Icons.remove_circle_outline),
                title: Text(rootLabel),
                trailing: (current ?? '').isEmpty
                    ? Icon(Icons.check, color: theme.colorScheme.primary)
                    : null,
                onTap: () => Navigator.pop(ctx, ''),
              ),
              ...rowsUnder('', 0),
              const SizedBox(height: 8),
            ],
          ),
        ),
      );
    },
  );
}

/// Asks for a group's name. Returns null when the dialog is dismissed or the
/// field is left empty — an unnamed group is not a group.
Future<String?> promptForGroupName(
  BuildContext context, {
  required String title,
  String initial = '',
  String action = 'Save',
}) async {
  final name = await showDialog<String>(
    context: context,
    builder: (ctx) =>
        _GroupNameDialog(title: title, initial: initial, action: action),
  );
  if (name == null || name.isEmpty) return null;
  return name;
}

/// The dialog owns the controller.
///
/// Disposing it beside the `await` instead looks equivalent and is not: the
/// route is still animating out, and the field it is still building reads a
/// controller that no longer exists.
class _GroupNameDialog extends StatefulWidget {
  final String title;
  final String initial;
  final String action;

  const _GroupNameDialog({
    required this.title,
    required this.initial,
    required this.action,
  });

  @override
  State<_GroupNameDialog> createState() => _GroupNameDialogState();
}

class _GroupNameDialogState extends State<_GroupNameDialog> {
  late final TextEditingController _controller = TextEditingController(
    text: widget.initial,
  );

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(widget.title),
      content: TextField(
        controller: _controller,
        autofocus: true,
        textCapitalization: TextCapitalization.sentences,
        decoration: const InputDecoration(hintText: 'Group name'),
        onSubmitted: (value) => Navigator.pop(context, value.trim()),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.pop(context, _controller.text.trim()),
          child: Text(widget.action),
        ),
      ],
    );
  }
}
