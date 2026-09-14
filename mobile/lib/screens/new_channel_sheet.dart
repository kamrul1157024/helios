/// Starting a channel: pick the sessions, and say what it is about.
///
/// The desktop starts one from a multi-select in its session list. A phone has
/// no selection bar to hang that off, so this is a sheet with tick boxes —
/// the shape the spec called for.
///
/// The opening message is offered here rather than after, because the daemon
/// puts it in the joining prompt: the members are told why they are in a
/// channel without having to go and fetch it.
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import '../providers/daemon_providers.dart';
import 'channel_detail_screen.dart';

class NewChannelSheet extends rp.ConsumerStatefulWidget {
  const NewChannelSheet({super.key});

  @override
  rp.ConsumerState<NewChannelSheet> createState() => _NewChannelSheetState();
}

class _NewChannelSheetState extends rp.ConsumerState<NewChannelSheet> {
  final _name = TextEditingController();
  final _opening = TextEditingController();
  final _picked = <String>{};
  String _hostId = '';
  bool _starting = false;

  @override
  void dispose() {
    _name.dispose();
    _opening.dispose();
    super.dispose();
  }

  Future<void> _start() async {
    if (_picked.isEmpty || _starting) return;
    final service = ref.read(serviceProvider(_hostId));
    if (service == null) return;

    setState(() => _starting = true);
    final navigator = Navigator.of(context);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final (channel, existing) = await service.createChannel(
        members: _picked.toList(),
        name: _name.text.trim(),
        message: _opening.text.trim(),
      );
      ref.invalidate(channelsProvider(_hostId));
      navigator.pop();
      navigator.push(
        MaterialPageRoute(
          builder: (_) =>
              ChannelDetailScreen(hostId: _hostId, channelId: channel.id),
        ),
      );
      if (existing) {
        // An unnamed channel is its members, so asking for the same set twice
        // opens the one they have. Being shown an existing conversation when
        // you asked for a new one is otherwise unexplained.
        messenger.showSnackBar(
          const SnackBar(content: Text('These sessions already had a channel')),
        );
      }
    } catch (err) {
      messenger.showSnackBar(SnackBar(content: Text('$err')));
    } finally {
      if (mounted) setState(() => _starting = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final hosts = ref.watch(hostManagerProvider).hosts;
    if (hosts.isEmpty) {
      return const Padding(
        padding: EdgeInsets.all(24),
        child: Text('Pair a machine first.'),
      );
    }
    if (_hostId.isEmpty) _hostId = hosts.first.id;

    final sessions = ref.watch(sessionsProvider(allSessionsKey(_hostId)));
    final inset = MediaQuery.of(context).viewInsets.bottom;

    return Padding(
      padding: EdgeInsets.only(bottom: inset),
      child: DraggableScrollableSheet(
        initialChildSize: 0.8,
        maxChildSize: 0.95,
        expand: false,
        builder: (context, scroll) => Column(
          children: [
            AppBar(
              title: const Text('New channel'),
              automaticallyImplyLeading: false,
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: const Text('Cancel'),
                ),
              ],
            ),
            Expanded(
              child: ListView(
                controller: scroll,
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
                children: [
                  TextField(
                    controller: _name,
                    decoration: const InputDecoration(
                      labelText: 'Name (optional)',
                      hintText: 'api-redesign',
                      helperText:
                          'Left empty, the channel is its members — asking for '
                          'the same sessions again opens this one.',
                      helperMaxLines: 3,
                      border: OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 12),
                  TextField(
                    controller: _opening,
                    minLines: 2,
                    maxLines: 4,
                    decoration: const InputDecoration(
                      labelText: 'Say something to open with (optional)',
                      helperText:
                          'Carried in the joining prompt, so the members are '
                          'told why they are here.',
                      helperMaxLines: 3,
                      border: OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 16),
                  Text(
                    'Who is in it',
                    style: Theme.of(context).textTheme.labelLarge,
                  ),
                  sessions.when(
                    loading: () => const Padding(
                      padding: EdgeInsets.all(24),
                      child: Center(child: CircularProgressIndicator()),
                    ),
                    error: (err, _) => Padding(
                      padding: const EdgeInsets.all(16),
                      child: Text('$err'),
                    ),
                    data: (list) {
                      // A terminated session stays in a channel as the author
                      // of what it said, but there is no reason to start one
                      // with it: nothing would be delivered.
                      final live = list
                          .where((s) => s.status != 'terminated')
                          .toList();
                      if (live.isEmpty) {
                        return const Padding(
                          padding: EdgeInsets.all(16),
                          child: Text('No sessions to put in one.'),
                        );
                      }
                      return Column(
                        children: [
                          for (final session in live)
                            CheckboxListTile(
                              dense: true,
                              value: _picked.contains(session.sessionId),
                              title: Text(
                                session.displayTitle,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                              subtitle: Text(
                                session.cwd,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                              ),
                              onChanged: (on) => setState(() {
                                if (on ?? false) {
                                  _picked.add(session.sessionId);
                                } else {
                                  _picked.remove(session.sessionId);
                                }
                              }),
                            ),
                        ],
                      );
                    },
                  ),
                ],
              ),
            ),
            SafeArea(
              child: Padding(
                padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
                child: SizedBox(
                  width: double.infinity,
                  child: FilledButton(
                    onPressed: _picked.isEmpty || _starting ? null : _start,
                    child: Text(
                      _starting
                          ? 'Starting…'
                          : 'Start with ${_picked.length} '
                                '${_picked.length == 1 ? 'session' : 'sessions'}',
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
