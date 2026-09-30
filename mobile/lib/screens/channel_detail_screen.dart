/// One channel's conversation, and the thread hanging off a message.
///
/// The desktop puts a thread in a panel beside the conversation. A phone has
/// no beside, so it is a pushed screen — the same content, reached the same
/// way, from the reply line under the message it answers.
///
/// See docs/specs/60-group-chat.md.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../models/channel.dart';
import '../providers/channel_messages.dart';
import '../providers/daemon_providers.dart';
import '../utils/author_colour.dart';

class ChannelDetailScreen extends rp.ConsumerWidget {
  final String hostId;
  final String channelId;

  const ChannelDetailScreen({
    super.key,
    required this.hostId,
    required this.channelId,
  });

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final channels = ref.watch(channelsProvider(hostId));
    final channel = channels.value?.where((c) => c.id == channelId).firstOrNull;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          channel?.label ?? channelId,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        actions: [
          if (channel != null)
            TextButton(
              onPressed: () => _showMembers(context, hostId, channel),
              child: Text(_memberCount(channel)),
            ),
        ],
      ),
      body: Column(
        children: [
          // General behaves differently from every other channel, and the
          // difference is invisible until somebody posts and nothing happens.
          if (channel?.isGeneral ?? false)
            _Note(
              'The notice board. Every session is in it, and posting here '
              'interrupts nobody — agents read it when they look.',
            ),
          Expanded(
            child: _WindowedConversation(
              hostId: hostId,
              channelId: channelId,
              channel: channel,
              onOpenThread: (root) => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => ChannelThreadScreen(
                    hostId: hostId,
                    channelId: channelId,
                    root: root,
                  ),
                ),
              ),
            ),
          ),
          if (channel?.archived ?? false)
            _ClosedNote(hostId: hostId, channelId: channelId)
          else
            ChannelComposer(
              hostId: hostId,
              channelId: channelId,
              channel: channel,
              hint: 'Message the channel',
            ),
        ],
      ),
    );
  }
}

/// One thread: the message it hangs off, then its replies, and its own box.
class ChannelThreadScreen extends rp.ConsumerWidget {
  final String hostId;
  final String channelId;
  final String root;

  const ChannelThreadScreen({
    super.key,
    required this.hostId,
    required this.channelId,
    required this.root,
  });

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final channels = ref.watch(channelsProvider(hostId));
    final channel = channels.value?.where((c) => c.id == channelId).firstOrNull;
    final messages = ref.watch(
      channelThreadProvider((hostId, channelId, root)),
    );

    return Scaffold(
      appBar: AppBar(title: const Text('Thread')),
      body: Column(
        children: [
          Expanded(
            child: messages.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (err, _) => Center(child: Text('$err')),
              data: (list) => _Conversation(
                hostId: hostId,
                channel: channel,
                messages: list,
                // Everything here is already in a thread; there is no second
                // layer to open.
                onOpenThread: null,
              ),
            ),
          ),
          if (channel?.archived ?? false)
            _ClosedNote(hostId: hostId, channelId: channelId)
          else
            ChannelComposer(
              hostId: hostId,
              channelId: channelId,
              channel: channel,
              threadRoot: root,
              hint: 'Reply in the thread',
            ),
        ],
      ),
    );
  }
}

/// A channel's conversation as a bounded, paged window.
///
/// The newest page opens the channel; scrolling to the top pages older in, and
/// scrolling back to the bottom pages newer back after the window slid past its
/// cap. Prepending older is smooth in a reversed list on its own, but the trim
/// that holds the window's size removes messages from the bottom, so the reader's
/// place is held by hand: measure the extent the trim removed and step the offset
/// back by it. See ChannelMessagesNotifier.
class _WindowedConversation extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String channelId;
  final Channel? channel;
  final void Function(String root)? onOpenThread;

  const _WindowedConversation({
    required this.hostId,
    required this.channelId,
    required this.channel,
    required this.onOpenThread,
  });

  @override
  rp.ConsumerState<_WindowedConversation> createState() =>
      _WindowedConversationState();
}

class _WindowedConversationState
    extends rp.ConsumerState<_WindowedConversation> {
  final _controller = ScrollController();
  bool _loadingOlder = false;
  bool _loadingNewer = false;

  (String, String) get _key => (widget.hostId, widget.channelId);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  /// Pages older in, then trims the newest back to the cap without moving what
  /// the reader is looking at. Prepending alone leaves the offset be — it is
  /// measured from the bottom and the new messages land on top — so only the
  /// trim, which cuts from the bottom, needs compensating.
  Future<void> _loadOlder() async {
    if (_loadingOlder) return;
    _loadingOlder = true;
    try {
      final notifier = ref.read(channelMessagesProvider(_key).notifier);
      final older = await notifier.fetchOlder();
      if (older.isEmpty || !mounted) return;
      notifier.applyOlder(older);

      await WidgetsBinding.instance.endOfFrame;
      if (!mounted || !_controller.hasClients) return;
      final overflow = notifier.overflow;
      if (overflow <= 0) return;

      final before = _controller.position.maxScrollExtent;
      final pixels = _controller.position.pixels;
      notifier.trimNewest(overflow);
      await WidgetsBinding.instance.endOfFrame;
      if (!mounted || !_controller.hasClients) return;
      final after = _controller.position.maxScrollExtent;
      _controller.jumpTo((pixels - (before - after)).clamp(0.0, after));
    } finally {
      _loadingOlder = false;
    }
  }

  /// Pages newer back in. The reader is at the bottom to have reached for it, so
  /// the new messages land under the eye where they belong and nothing is moved;
  /// the oldest page falls off the top, which the offset never referenced.
  Future<void> _loadNewer() async {
    if (_loadingNewer) return;
    _loadingNewer = true;
    try {
      final notifier = ref.read(channelMessagesProvider(_key).notifier);
      final newer = await notifier.fetchNewer();
      if (newer.isEmpty || !mounted) return;
      notifier.applyNewer(newer);
    } finally {
      _loadingNewer = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    final window = ref.watch(channelMessagesProvider(_key));
    return window.when(
      // A channel event refetches the newest page; holding the conversation on
      // screen through that beats flashing a spinner over what was just read.
      skipLoadingOnReload: true,
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (err, _) => Center(child: Text('$err')),
      data: (data) => _Conversation(
        hostId: widget.hostId,
        channel: widget.channel,
        messages: data.messages,
        onOpenThread: widget.onOpenThread,
        controller: _controller,
        hasOlder: data.hasOlder,
        hasNewer: data.hasNewer,
        onReachOlder: _loadOlder,
        onReachNewer: _loadNewer,
      ),
    );
  }
}

class _Conversation extends StatelessWidget {
  final String hostId;
  final Channel? channel;
  final List<ChannelMessage> messages;
  final void Function(String root)? onOpenThread;
  final ScrollController? controller;

  /// Older messages exist above the top; a spinner sits there and, once built,
  /// calls [onReachOlder]. [hasNewer]/[onReachNewer] are the mirror at the
  /// bottom, for a window that slid past its newest. A thread passes neither: it
  /// is read whole.
  final bool hasOlder;
  final bool hasNewer;
  final Future<void> Function()? onReachOlder;
  final Future<void> Function()? onReachNewer;

  const _Conversation({
    required this.hostId,
    required this.channel,
    required this.messages,
    required this.onOpenThread,
    this.controller,
    this.hasOlder = false,
    this.hasNewer = false,
    this.onReachOlder,
    this.onReachNewer,
  });

  @override
  Widget build(BuildContext context) {
    if (messages.isEmpty) {
      return const Center(child: Text('Nothing said yet.'));
    }
    // reverse: true opens on the newest message and keeps it in view as more
    // arrive. The messages read oldest-first, so the index is counted back.
    //
    // A reversed list counts from the bottom: index 0 is the newest, so the
    // newer-page spinner leads and the older-page one trails.
    final leading = hasNewer ? 1 : 0;
    final trailing = hasOlder ? 1 : 0;
    return ListView.builder(
      controller: controller,
      reverse: true,
      padding: const EdgeInsets.symmetric(vertical: 12),
      itemCount: messages.length + leading + trailing,
      itemBuilder: (context, at) {
        if (hasNewer && at == 0) {
          WidgetsBinding.instance.addPostFrameCallback(
            (_) => onReachNewer?.call(),
          );
          return const _PageSpinner();
        }
        if (hasOlder && at == messages.length + leading + trailing - 1) {
          WidgetsBinding.instance.addPostFrameCallback(
            (_) => onReachOlder?.call(),
          );
          return const _PageSpinner();
        }
        final index = messages.length - 1 - (at - leading);
        final message = messages[index];
        // A run from one author shares a header: four messages from one agent
        // repeating its title four times is noise, and the repetition says
        // nothing the first line did not.
        final opens =
            index == 0 || messages[index - 1].author != message.author;
        return _MessageRow(
          message: message,
          channel: channel,
          opens: opens,
          onOpenThread: onOpenThread,
        );
      },
    );
  }
}

class _MessageRow extends StatelessWidget {
  final ChannelMessage message;
  final Channel? channel;
  final bool opens;
  final void Function(String root)? onOpenThread;

  const _MessageRow({
    required this.message,
    required this.channel,
    required this.opens,
    required this.onOpenThread,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final mine = message.fromPerson;
    final colour = authorColour(message.author, theme.brightness);

    final bubble = Container(
      constraints: BoxConstraints(
        maxWidth: MediaQuery.of(context).size.width * 0.85,
      ),
      padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 7),
      decoration: BoxDecoration(
        color: _bubbleColour(theme, mine),
        borderRadius: BorderRadius.only(
          topLeft: Radius.circular(opens && !mine ? 3 : 12),
          topRight: Radius.circular(opens && mine ? 3 : 12),
          bottomLeft: const Radius.circular(12),
          bottomRight: const Radius.circular(12),
        ),
        // A message that named the reader, so it can be found again on the
        // way back up.
        border: message.addressesUser
            ? Border(
                left: BorderSide(color: theme.colorScheme.primary, width: 2),
              )
            : null,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (opens)
            Padding(
              padding: const EdgeInsets.only(bottom: 2),
              child: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Flexible(
                    child: Text(
                      message.from,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.labelSmall?.copyWith(
                        color: colour ?? theme.colorScheme.onSurfaceVariant,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                  if (message.urgent) ...[
                    const SizedBox(width: 6),
                    _UrgentTag(),
                  ],
                  const SizedBox(width: 6),
                  Text(
                    _shortTime(message.createdAt),
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                      fontSize: 10,
                    ),
                  ),
                ],
              ),
            ),
          _MessageBody(message: message, channel: channel),
          if (onOpenThread != null && message.replyCount > 0)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: InkWell(
                onTap: () => onOpenThread!(message.id),
                child: Text(
                  _replyLine(message),
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: theme.colorScheme.primary,
                  ),
                ),
              ),
            ),
        ],
      ),
    );

    return Padding(
      padding: EdgeInsets.fromLTRB(12, opens ? 12 : 2, 12, 2),
      child: Row(
        mainAxisAlignment: mine
            ? MainAxisAlignment.end
            : MainAxisAlignment.start,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (!mine) ...[
            // A slot even when empty, so the bubbles of one run line up rather
            // than stepping left under the first.
            SizedBox(
              width: 28,
              child: opens
                  ? CircleAvatar(
                      radius: 13,
                      backgroundColor: (colour ?? theme.colorScheme.primary)
                          .withValues(alpha: 0.18),
                      child: Text(
                        authorInitials(message.from),
                        style: TextStyle(
                          fontSize: 10,
                          fontWeight: FontWeight.w600,
                          color: colour ?? theme.colorScheme.primary,
                        ),
                      ),
                    )
                  : null,
            ),
            const SizedBox(width: 8),
          ],
          Flexible(child: bubble),
        ],
      ),
    );
  }

  Color _bubbleColour(ThemeData theme, bool mine) {
    if (message.urgent) {
      return theme.colorScheme.errorContainer.withValues(alpha: 0.5);
    }
    if (mine) return theme.colorScheme.primary.withValues(alpha: 0.18);
    return theme.colorScheme.onSurface.withValues(alpha: 0.06);
  }
}

/// The body, with any handle it names drawn as a chip.
///
/// Decoration only. The daemon decided who the message named when it was
/// posted and the message carries that answer; nothing here changes who was
/// woken.
class _MessageBody extends StatelessWidget {
  final ChannelMessage message;
  final Channel? channel;

  const _MessageBody({required this.message, required this.channel});

  static final _token = RegExp(r'@([A-Za-z0-9][A-Za-z0-9_-]*)');

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final base = theme.textTheme.bodyMedium;

    final handles = <String>{
      'user',
      ...?channel?.members.map((id) => channel!.handleFor(id)),
    };

    final spans = <InlineSpan>[];
    var at = 0;
    for (final match in _token.allMatches(message.body)) {
      final token = match.group(1)!.toLowerCase();
      if (!handles.contains(token)) continue;
      if (match.start > at) {
        spans.add(TextSpan(text: message.body.substring(at, match.start)));
      }
      spans.add(
        WidgetSpan(
          alignment: PlaceholderAlignment.middle,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 3),
            decoration: BoxDecoration(
              color: theme.colorScheme.primary.withValues(alpha: 0.18),
              borderRadius: BorderRadius.circular(3),
            ),
            child: Text(
              match.group(0)!,
              style: base?.copyWith(
                color: theme.colorScheme.primary,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
        ),
      );
      at = match.end;
    }
    if (at < message.body.length) {
      spans.add(TextSpan(text: message.body.substring(at)));
    }

    return SelectableText.rich(TextSpan(style: base, children: spans));
  }
}

/// The marker at either end of the window while a page loads.
class _PageSpinner extends StatelessWidget {
  const _PageSpinner();

  @override
  Widget build(BuildContext context) {
    return const Center(
      child: Padding(
        padding: EdgeInsets.all(12),
        child: SizedBox(
          width: 18,
          height: 18,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      ),
    );
  }
}

class _UrgentTag extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5),
      decoration: BoxDecoration(
        color: theme.colorScheme.error,
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        'URGENT',
        style: TextStyle(fontSize: 9, color: theme.colorScheme.onError),
      ),
    );
  }
}

class _Note extends StatelessWidget {
  final String text;

  const _Note(this.text);

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      color: theme.colorScheme.surfaceContainerHighest.withValues(alpha: 0.4),
      child: Text(
        text,
        style: theme.textTheme.bodySmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}

/// Closed is read-only, so the box goes rather than being disabled: one that
/// takes text and then refuses it is worse than none at all.
class _ClosedNote extends rp.ConsumerWidget {
  final String hostId;
  final String channelId;

  const _ClosedNote({required this.hostId, required this.channelId});

  @override
  Widget build(BuildContext context, rp.WidgetRef ref) {
    final theme = Theme.of(context);
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
        child: Row(
          children: [
            Expanded(
              child: Text(
                'This channel is closed. It takes no more messages.',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
            TextButton(
              onPressed: () async {
                final service = ref.read(serviceProvider(hostId));
                if (service == null) return;
                await service.setChannelArchived(channelId, false);
                ref.invalidate(channelsProvider(hostId));
              },
              child: const Text('Reopen'),
            ),
          ],
        ),
      ),
    );
  }
}

/// The box, and the `@` menu it opens.
///
/// One widget for the channel and for a thread, because the only difference
/// between them is where the message lands.
class ChannelComposer extends rp.ConsumerStatefulWidget {
  final String hostId;
  final String channelId;
  final Channel? channel;
  final String threadRoot;
  final String hint;

  const ChannelComposer({
    super.key,
    required this.hostId,
    required this.channelId,
    required this.channel,
    required this.hint,
    this.threadRoot = '',
  });

  @override
  rp.ConsumerState<ChannelComposer> createState() => _ChannelComposerState();
}

class _ChannelComposerState extends rp.ConsumerState<ChannelComposer> {
  final _field = TextEditingController();
  bool _sending = false;
  String? _picking;

  static final _trailing = RegExp(r'@([A-Za-z0-9_-]*)$');

  @override
  void dispose() {
    _field.dispose();
    super.dispose();
  }

  Future<void> _post() async {
    final text = _field.text.trim();
    if (text.isEmpty || _sending) return;
    final service = ref.read(serviceProvider(widget.hostId));
    if (service == null) return;

    setState(() => _sending = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      await service.postToChannel(
        widget.channelId,
        text,
        threadRoot: widget.threadRoot,
      );
      _field.clear();
      ref.invalidate(channelsProvider(widget.hostId));
      ref.invalidate(
        channelMessagesProvider((widget.hostId, widget.channelId)),
      );
      if (widget.threadRoot.isNotEmpty) {
        ref.invalidate(
          channelThreadProvider((
            widget.hostId,
            widget.channelId,
            widget.threadRoot,
          )),
        );
      }
    } catch (err) {
      messenger.showSnackBar(SnackBar(content: Text('$err')));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  /// The handle is what the daemon resolves, so the menu inserts that rather
  /// than the title: what reads well and what wakes an agent are different
  /// strings.
  void _insert(String handle) {
    _field.text = _field.text.replaceFirst(_trailing, '@$handle ');
    _field.selection = TextSelection.collapsed(offset: _field.text.length);
    setState(() => _picking = null);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final channel = widget.channel;
    final needle = (_picking ?? '').toLowerCase();

    final offered = (channel?.members ?? const <String>[])
        .map(
          (id) => (
            id: id,
            handle: channel!.handleFor(id),
            title: channel.titles[id] ?? id,
          ),
        )
        .where(
          (m) =>
              needle.isEmpty ||
              m.handle.contains(needle) ||
              m.title.toLowerCase().contains(needle),
        )
        .toList();

    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (_picking != null && offered.isNotEmpty)
            Container(
              constraints: const BoxConstraints(maxHeight: 180),
              margin: const EdgeInsets.fromLTRB(12, 0, 12, 6),
              decoration: BoxDecoration(
                border: Border.all(color: theme.colorScheme.outlineVariant),
                borderRadius: BorderRadius.circular(8),
                color: theme.colorScheme.surface,
              ),
              child: ListView(
                shrinkWrap: true,
                children: [
                  for (final member in offered)
                    ListTile(
                      dense: true,
                      title: Text(
                        '@${member.handle}',
                        style: TextStyle(
                          color: authorColour(
                            'session:${member.id}',
                            theme.brightness,
                          ),
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      subtitle: Text(
                        member.title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                      onTap: () => _insert(member.handle),
                    ),
                ],
              ),
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 4, 8, 8),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: TextField(
                    controller: _field,
                    minLines: 1,
                    maxLines: 5,
                    textCapitalization: TextCapitalization.sentences,
                    decoration: InputDecoration(
                      hintText: widget.hint,
                      border: const OutlineInputBorder(),
                      isDense: true,
                    ),
                    onChanged: (text) {
                      // Opens on the @ and narrows as it is typed; a space
                      // ends it.
                      final match = _trailing.firstMatch(text);
                      setState(() => _picking = match?.group(1));
                    },
                  ),
                ),
                const SizedBox(width: 4),
                IconButton.filled(
                  onPressed: _sending ? null : _post,
                  icon: _sending
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.arrow_upward),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// Who is in the channel, with the handle each answers to — which is where a
/// handle belongs, beside the name it stands for, since it is what you type.
Future<void> _showMembers(
  BuildContext context,
  String hostId,
  Channel channel,
) {
  return showModalBottomSheet<void>(
    context: context,
    builder: (sheet) {
      final theme = Theme.of(sheet);
      return SafeArea(
        child: ListView(
          shrinkWrap: true,
          children: [
            ListTile(title: Text(_memberCount(channel))),
            const Divider(height: 1),
            for (final id in channel.members)
              ListTile(
                leading: CircleAvatar(
                  radius: 14,
                  backgroundColor:
                      (authorColour('session:$id', theme.brightness) ??
                              theme.colorScheme.primary)
                          .withValues(alpha: 0.18),
                  child: Text(
                    authorInitials(channel.titles[id] ?? id),
                    style: TextStyle(
                      fontSize: 10,
                      color: authorColour('session:$id', theme.brightness),
                    ),
                  ),
                ),
                title: Text(
                  channel.titles[id] ?? id,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                subtitle: Text('@${channel.handleFor(id)}'),
              ),
            const ListTile(
              leading: CircleAvatar(
                radius: 14,
                child: Text('you', style: TextStyle(fontSize: 9)),
              ),
              title: Text('user'),
              subtitle: Text('@user'),
            ),
          ],
        ),
      );
    },
  );
}

String _memberCount(Channel channel) {
  // The person counts: they are in the conversation.
  final count = channel.members.length + 1;
  return '$count ${count == 1 ? 'member' : 'members'}';
}

String _replyLine(ChannelMessage message) {
  final who = message.replyAuthors.join(', ');
  final replies = message.replyCount == 1
      ? '1 reply'
      : '${message.replyCount} replies';
  return who.isEmpty ? replies : '$replies · $who';
}

String _shortTime(String iso) {
  final at = DateTime.tryParse(iso);
  if (at == null) return '';
  final local = at.toLocal();
  final hour = local.hour.toString().padLeft(2, '0');
  final minute = local.minute.toString().padLeft(2, '0');
  return '$hour:$minute';
}
