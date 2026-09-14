/// Channels: several sessions and the person, with one conversation running
/// through them.
///
/// Grouped by host, as the session list is, because a channel belongs to the
/// daemon that holds it — its members are that daemon's sessions and its
/// messages never leave it.
///
/// See docs/specs/60-group-chat.md.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/channel.dart';
import '../providers/daemon_providers.dart';
import 'channel_detail_screen.dart';

class ChannelsScreen extends rp.ConsumerWidget {
  const ChannelsScreen({super.key});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final hosts = ref.watch(hostManagerProvider).hosts;

    if (hosts.isEmpty) {
      return const Center(child: Text('Pair a machine first.'));
    }

    return ListView(
      padding: const EdgeInsets.only(bottom: 88),
      children: [
        for (final host in hosts) ...[
          if (hosts.length > 1)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
              child: Text(
                host.label,
                style: Theme.of(context).textTheme.labelMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          _HostChannels(hostId: host.id),
        ],
      ],
    );
  }
}

class _HostChannels extends rp.ConsumerStatefulWidget {
  final String hostId;

  const _HostChannels({required this.hostId});

  @override
  rp.ConsumerState<_HostChannels> createState() => _HostChannelsState();
}

class _HostChannelsState extends rp.ConsumerState<_HostChannels> {
  /// Shut to begin with: the point of closing a conversation is not to be
  /// shown it. Opened by hand when somebody wants to read one back.
  bool _showClosed = false;

  @override
  Widget build(BuildContext context) {
    final channels = ref.watch(channelsProvider(widget.hostId));

    return channels.when(
      loading: () => const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: CircularProgressIndicator()),
      ),
      // A daemon older than channels answers 404, and one out-of-date machine
      // must not make the tab look broken on the others.
      error: (err, _) => Padding(
        padding: const EdgeInsets.all(16),
        child: Text(
          'Channels need a newer daemon on this machine.',
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ),
      data: (list) {
        if (list.isEmpty) {
          return const Padding(
            padding: EdgeInsets.all(24),
            child: Text('No channels yet.'),
          );
        }
        final open = list.where((c) => !c.archived).toList();
        final closed = list.where((c) => c.archived).toList();

        return Column(
          children: [
            for (final channel in open)
              _ChannelRow(hostId: widget.hostId, channel: channel),
            if (closed.isNotEmpty)
              ListTile(
                dense: true,
                leading: Icon(
                  _showClosed ? Icons.expand_more : Icons.chevron_right,
                  size: 20,
                ),
                title: Text(
                  'Closed (${closed.length})',
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                onTap: () => setState(() => _showClosed = !_showClosed),
              ),
            if (_showClosed)
              for (final channel in closed)
                _ChannelRow(hostId: widget.hostId, channel: channel),
          ],
        );
      },
    );
  }
}

class _ChannelRow extends rp.ConsumerWidget {
  final String hostId;
  final Channel channel;

  const _ChannelRow({required this.hostId, required this.channel});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final theme = Theme.of(context);
    final muted = theme.colorScheme.onSurfaceVariant;

    return ListTile(
      title: Text(
        channel.label,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        // A `#name` is a handle, and is coloured like one — the same accent an
        // `@mention` wears, so both read as something you can point at. A
        // member list is not a handle and stays in the ordinary text colour.
        style: channel.archived
            ? TextStyle(color: muted)
            : channel.isNamed
            ? TextStyle(
                color: theme.colorScheme.primary,
                fontWeight: FontWeight.w500,
              )
            : null,
      ),
      subtitle: Text(
        _summary(channel),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: theme.textTheme.bodySmall?.copyWith(color: muted),
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Somebody addressed you, which a count of general traffic buries.
          if (channel.mentions > 0 && !channel.archived)
            Padding(
              padding: const EdgeInsets.only(right: 6),
              child: Badge(
                backgroundColor: theme.colorScheme.primary,
                label: Text('@${channel.mentions}'),
              ),
            ),
          if (channel.unread > 0 && !channel.archived)
            Badge(label: Text('${channel.unread}')),
        ],
      ),
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) =>
              ChannelDetailScreen(hostId: hostId, channelId: channel.id),
        ),
      ),
      onLongPress: channel.isGeneral
          ? null
          : () => _showActions(context, ref, hostId, channel),
    );
  }

  static String _summary(Channel channel) {
    final count = channel.members.length;
    final sessions = '$count ${count == 1 ? 'session' : 'sessions'}';
    if (channel.archived) return 'closed · $sessions';
    // General's membership is everyone on the daemon, so the count is what
    // says something rather than the list — and it was nobody's to join.
    if (channel.isGeneral) return 'everyone · $sessions, and you';
    return '$sessions, and you';
  }
}

/// Rename, close and delete, on a long press.
///
/// General gets none of it: every session is in it and it is the one channel
/// that is always there, so neither closing nor deleting it is somebody's to
/// do — which is why the row does not offer the sheet at all.
Future<void> _showActions(
  BuildContext context,
  rp.WidgetRef ref,
  String hostId,
  Channel channel,
) async {
  final service = ref.read(serviceProvider(hostId));
  if (service == null) return;

  final messenger = ScaffoldMessenger.of(context);
  await showModalBottomSheet<void>(
    context: context,
    builder: (sheet) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          ListTile(
            leading: const Icon(Icons.edit_outlined),
            title: const Text('Rename'),
            // Worth saying before the tap rather than after: an unnamed
            // channel is found again by who is in it, and naming it ends that.
            subtitle: channel.name.isEmpty
                ? const Text(
                    'Naming it means asking for these sessions again starts a '
                    'new channel',
                  )
                : null,
            onTap: () async {
              Navigator.of(sheet).pop();
              final name = await _askName(context, channel);
              if (name == null || name.isEmpty) return;
              try {
                await service.renameChannel(channel.id, name);
                ref.invalidate(channelsProvider(hostId));
              } catch (err) {
                messenger.showSnackBar(SnackBar(content: Text('$err')));
              }
            },
          ),
          ListTile(
            leading: Icon(
              channel.archived ? Icons.unarchive_outlined : Icons.archive_outlined,
            ),
            title: Text(channel.archived ? 'Reopen' : 'Close'),
            subtitle: Text(
              channel.archived
                  ? 'Messages are delivered again'
                  : 'It stays readable, but takes no more messages',
            ),
            onTap: () async {
              Navigator.of(sheet).pop();
              try {
                await service.setChannelArchived(channel.id, !channel.archived);
                ref.invalidate(channelsProvider(hostId));
              } catch (err) {
                messenger.showSnackBar(SnackBar(content: Text('$err')));
              }
            },
          ),
          ListTile(
            leading: Icon(
              Icons.delete_outline,
              color: Theme.of(sheet).colorScheme.error,
            ),
            title: const Text('Delete'),
            subtitle: const Text('Everything said in it goes too'),
            onTap: () async {
              Navigator.of(sheet).pop();
              final sure = await _confirmDelete(context, channel);
              if (sure != true) return;
              try {
                await service.deleteChannel(channel.id);
                ref.invalidate(channelsProvider(hostId));
              } catch (err) {
                messenger.showSnackBar(SnackBar(content: Text('$err')));
              }
            },
          ),
        ],
      ),
    ),
  );
}

Future<String?> _askName(BuildContext context, Channel channel) {
  // Seeded with the name it has rather than what the row shows: an unnamed
  // channel is shown by its members, and offering that as the text to edit
  // would invite somebody to accept a name they never chose.
  final field = TextEditingController(text: channel.name);
  return showDialog<String>(
    context: context,
    builder: (dialog) => AlertDialog(
      title: const Text('Name this channel'),
      content: TextField(
        controller: field,
        autofocus: true,
        decoration: const InputDecoration(hintText: 'api-redesign'),
        onSubmitted: (value) => Navigator.of(dialog).pop(value.trim()),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(dialog).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(dialog).pop(field.text.trim()),
          child: const Text('Rename'),
        ),
      ],
    ),
  );
}

Future<bool?> _confirmDelete(BuildContext context, Channel channel) {
  return showDialog<bool>(
    context: context,
    builder: (dialog) => AlertDialog(
      title: Text('Delete ${channel.label}?'),
      content: const Text('Everything said in it goes with it.'),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(dialog).pop(false),
          child: const Text('Cancel'),
        ),
        FilledButton(
          style: FilledButton.styleFrom(
            backgroundColor: Theme.of(dialog).colorScheme.error,
          ),
          onPressed: () => Navigator.of(dialog).pop(true),
          child: const Text('Delete'),
        ),
      ],
    ),
  );
}
