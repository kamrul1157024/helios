import 'dart:async';
import 'dart:convert';
import 'dart:io' show Platform;
import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:permission_handler/permission_handler.dart';
// Both packages export Provider, ChangeNotifierProvider and Consumer.
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:provider/provider.dart';
import '../providers/daemon_providers.dart';
import '../models/host_connection.dart';
import '../services/host_manager.dart';
import '../services/daemon_api_service.dart';
import '../services/notification_service.dart';
import '../services/update_service.dart';
import '../providers/card_registry.dart' as registry;
import 'setup_screen.dart';
import 'new_schedule_sheet.dart';
import 'schedules_screen.dart';
import 'sessions_screen.dart';
import 'new_session_sheet.dart';
import 'dashboard_screen.dart';
import 'settings_screen.dart';

class HomeScreen extends rp.ConsumerStatefulWidget {
  const HomeScreen({super.key});

  @override
  rp.ConsumerState<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends rp.ConsumerState<HomeScreen>
    with WidgetsBindingObserver {
  late HostManager _hm;
  final Map<String, StreamSubscription<SSEEvent>> _eventSubs = {};
  int _currentIndex = 0;
  bool _notifPermissionDenied = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    _hm = context.read<HostManager>();
    _checkNotificationPermission();
    _checkForUpdate();
    NotificationService.instance.onAction = _handleNotificationAction;
    _subscribeToAllHosts();
  }

  void _subscribeToAllHosts() {
    for (final entry in _hm.hosts) {
      _subscribeToHost(entry.id);
    }
  }

  void _subscribeToHost(String hostId) {
    if (_eventSubs.containsKey(hostId)) return;
    final service = _hm.serviceFor(hostId);
    if (service == null) return;
    _eventSubs[hostId] = service.events.listen((event) {
      _handleSSEEvent(hostId, event);
    });
  }

  Future<void> _checkNotificationPermission() async {
    // permission_handler is not supported on macOS
    if (Platform.isMacOS) return;
    final granted = await NotificationService.instance.requestPermission();
    if (mounted) setState(() => _notifPermissionDenied = !granted);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _hm.resumeAll();
      _checkNotificationPermission();
    }
    // Don't stop SSE on pause — keep it alive for background notifications
  }

  void _handleSSEEvent(String hostId, SSEEvent event) {
    debugPrint('[HomeScreen] SSE event: type=${event.type} hostId=$hostId');

    // Answered in the terminal, in the desktop app, or on another device: the
    // tray copy here is now stale and nothing else retracts it.
    if (event.type == 'notification_resolved') {
      if (event.data is! Map) return;
      final id = (event.data as Map)['id']?.toString();
      if (id != null && id.isNotEmpty) {
        NotificationService.instance.cancel(
          NotificationService.notifKey(hostId, id),
        );
      }
      return;
    }

    if (event.type != 'notification') return;
    if (event.data is! Map) {
      debugPrint(
        '[HomeScreen] notification data is not Map: ${event.data.runtimeType}',
      );
      return;
    }

    final data = event.data as Map;
    final type = data['type']?.toString() ?? '';
    final id = data['id']?.toString() ?? '';
    final status = data['status']?.toString();
    debugPrint(
      '[HomeScreen] notification: notifType=$type id=$id status=$status',
    );

    final key = NotificationService.notifKey(hostId, id);
    final shouldRaise = registry.shouldRaiseNotification(
      type: type,
      status: status,
      alreadyPosted: NotificationService.instance.isPosted(key),
    );
    if (!shouldRaise) return;

    final host = _hm.hostById(hostId);
    final hostLabel = _hm.hosts.length > 1 ? (host?.label ?? '') : '';
    final prefix = hostLabel.isNotEmpty ? '$hostLabel — ' : '';

    // Encode hostId into notification payload for routing on tap
    final payload = jsonEncode({'hostId': hostId, 'notificationId': id});

    final notifSvc = NotificationService.instance;
    final silent = !notifSvc.isAlertEnabled(type);

    // Keyed on the kind of request — the part of the type after the provider
    // prefix — so a permission request raises a permission notification
    // whoever asked.
    //
    // The default arm is the point of this. It used to be an if/else chain
    // over seven literal claude.* types with no final else, so a notification
    // from any other provider raised nothing at all: the agent waited for an
    // answer and the phone never buzzed. Never fall through in silence.
    final kind = registry.kindOfType(type);
    final title = data['title']?.toString();
    final detail = data['detail']?.toString();

    if (kind == 'permission') {
      debugPrint('[HomeScreen] showing OS permission notification');
      notifSvc.showPermissionNotification(
        id: payload,
        key: key,
        toolName: '$prefix${title ?? 'Unknown tool'}',
        detail: detail ?? 'Permission requested',
        silent: silent,
      );
      return;
    }

    debugPrint('[HomeScreen] showing OS notification for kind=$kind');
    notifSvc.showNotification(
      id: payload,
      key: key,
      title: prefix + (title ?? registry.labelForKind(kind)),
      body: detail ?? registry.bodyForKind(kind),
      silent: silent,
    );
  }

  void _handleNotificationAction(String rawPayload, String action) {
    // Parse hostId from the notification payload
    try {
      final payload = jsonDecode(rawPayload);
      final hostId = payload['hostId'] as String?;
      final notificationId = payload['notificationId'] as String?;
      if (hostId == null || notificationId == null) return;

      final service = _hm.serviceFor(hostId);
      if (service == null) return;

      if (action == 'approve') {
        service.sendAction(notificationId, {'action': 'approve'});
      } else if (action == 'deny') {
        service.sendAction(notificationId, {'action': 'deny'});
      }

      // Answering from the notification's own buttons does not otherwise
      // retract it — the tray copy would sit there already answered.
      NotificationService.instance.cancel(
        NotificationService.notifKey(hostId, notificationId),
      );

      // Switch UI filter to this host
      _hm.setActiveHost(hostId);
    } catch (_) {
      // Legacy payload format (just notification ID) — ignore
    }
  }

  Widget _buildOfflineHostBanner(HostConnection host) {
    final theme = Theme.of(context);
    final fg = theme.colorScheme.onErrorContainer;
    return Material(
      color: theme.colorScheme.errorContainer,
      child: InkWell(
        onTap: () => _hm.serviceFor(host.id)?.reconnect(),
        onLongPress: () => _confirmRemoveHost(host),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 3),
          child: Row(
            children: [
              Icon(Icons.cloud_off, size: 13, color: fg),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  '${host.label} offline',
                  style: TextStyle(fontSize: 11, color: fg),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              Text(
                'Retry',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  color: fg,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _confirmRemoveHost(HostConnection host) async {
    final theme = Theme.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Remove host?'),
        content: Text('Remove "${host.label}"? You can re-pair later.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: FilledButton.styleFrom(
              backgroundColor: theme.colorScheme.error,
            ),
            child: const Text('Remove'),
          ),
        ],
      ),
    );
    if (confirmed == true && mounted) {
      await _hm.removeHost(host.id);
    }
  }

  void _showNewSessionSheet() {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      builder: (_) => ChangeNotifierProvider.value(
        value: _hm,
        child: const NewSessionSheet(),
      ),
    );
  }

  void _showHostSelector() {
    final theme = Theme.of(context);

    showModalBottomSheet(
      context: context,
      builder: (ctx) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                child: Text(
                  'Select Host',
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              // "All Hosts" option
              ListTile(
                leading: Icon(
                  _hm.activeHostId == null
                      ? Icons.radio_button_checked
                      : Icons.radio_button_off,
                  color: theme.colorScheme.primary,
                ),
                title: const Text('All Hosts'),
                trailing: Text(
                  '${_hm.hosts.where((h) => _hm.serviceFor(h.id)?.connected == true).length}/${_hm.hosts.length}',
                  style: TextStyle(
                    color: theme.colorScheme.onSurfaceVariant,
                    fontSize: 13,
                  ),
                ),
                onTap: () {
                  Navigator.pop(ctx);
                  _hm.setActiveHost(null);
                },
              ),
              const Divider(height: 1),
              // Per-host options
              ...(_hm.hosts.map((host) {
                final service = _hm.serviceFor(host.id);
                final isConnected = service?.connected == true;
                final isSelected = _hm.activeHostId == host.id;

                return ListTile(
                  leading: Icon(
                    isSelected
                        ? Icons.radio_button_checked
                        : Icons.radio_button_off,
                    color: host.color,
                  ),
                  title: Row(
                    children: [
                      Container(
                        width: 10,
                        height: 10,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          color: host.color.withValues(
                            alpha: isConnected ? 1.0 : 0.3,
                          ),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(child: Text(host.label)),
                    ],
                  ),
                  subtitle: Text(
                    host.serverUrl,
                    style: TextStyle(
                      fontSize: 11,
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                  onTap: () {
                    Navigator.pop(ctx);
                    _hm.setActiveHost(host.id);
                  },
                );
              })),
              const Divider(height: 1),
              ListTile(
                leading: Icon(Icons.add, color: theme.colorScheme.primary),
                title: const Text('Add new host'),
                onTap: () {
                  Navigator.pop(ctx);
                  Navigator.of(context).push(
                    MaterialPageRoute(builder: (_) => const SetupScreen()),
                  );
                },
              ),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  /// Steps to the next or previous host. "All Hosts" is not a stop on the way:
  /// a swipe from it lands on whichever end the swipe came from.
  void _cycleHost(int delta) {
    final hosts = _hm.hosts;
    if (hosts.length < 2) return;
    final current = hosts.indexWhere((h) => h.id == _hm.activeHostId);
    final next = current < 0
        ? (delta > 0 ? 0 : hosts.length - 1)
        : (current + delta) % hosts.length;
    _hm.setActiveHost(hosts[next].id);
  }

  void _onAppBarSwipe(DragEndDetails details) {
    final velocity = details.primaryVelocity ?? 0;
    if (velocity.abs() < 100) return;
    _cycleHost(velocity < 0 ? 1 : -1);
  }

  Widget _buildHostFilterChip() {
    if (_hm.hosts.length <= 1) {
      return const Text('helios');
    }

    final label = _hm.activeHostId == null
        ? 'All Hosts'
        : (_hm.activeHost?.label ?? 'helios');

    return GestureDetector(
      onTap: _showHostSelector,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            label,
            style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
          ),
          const SizedBox(width: 4),
          const Icon(Icons.arrow_drop_down, size: 20),
        ],
      ),
    );
  }

  Widget _buildConnectionDots() {
    return Padding(
      padding: const EdgeInsets.only(right: 8),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: _hm.hosts.map((host) {
          final isConnected = _hm.serviceFor(host.id)?.connected == true;
          return Padding(
            padding: const EdgeInsets.only(left: 3),
            child: Tooltip(
              message:
                  '${host.label}: ${isConnected ? 'connected' : 'offline'}',
              child: Icon(
                Icons.circle,
                size: 10,
                color: host.color.withValues(alpha: isConnected ? 1.0 : 0.3),
              ),
            ),
          );
        }).toList(),
      ),
    );
  }

  /// Says what a new release changed, once.
  ///
  /// This was a banner carrying a version number and nothing else, so finding
  /// out what had arrived meant leaving for GitHub — and somebody three
  /// releases behind read the same line as somebody one behind. Dismissing is
  /// remembered per version, so the next release gets one mention and this one
  /// gets none. The settings screen still offers the download to anybody who
  /// waved it away.
  Future<void> _checkForUpdate() async {
    final info = await UpdateService.instance.checkForUpdate();
    if (info == null || !mounted) return;
    if (await UpdateService.instance.isDismissed(info.latestVersion)) return;
    if (!mounted) return;
    // Closing by any route counts as read: a dialog that comes back tomorrow
    // because it was dismissed with the back button is one nobody trusts.
    await showDialog<void>(
      context: context,
      builder: (ctx) => _ReleaseNotesDialog(update: info),
    );
    await UpdateService.instance.dismiss(info.latestVersion);
  }

  Widget _buildNotifPermissionBanner() {
    final theme = Theme.of(context);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      color: theme.colorScheme.errorContainer,
      child: Row(
        children: [
          Icon(
            Icons.notifications_off,
            size: 18,
            color: theme.colorScheme.onErrorContainer,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'Notifications are disabled — you won\'t hear permission requests.',
              style: TextStyle(
                fontSize: 12,
                color: theme.colorScheme.onErrorContainer,
              ),
            ),
          ),
          TextButton(
            onPressed: () => openAppSettings(),
            child: const Text('Enable', style: TextStyle(fontSize: 12)),
          ),
        ],
      ),
    );
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    for (final sub in _eventSubs.values) {
      sub.cancel();
    }
    _eventSubs.clear();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<HostManager>(
      builder: (context, hm, _) {
        // Subscribe to any newly added hosts
        for (final host in hm.hosts) {
          _subscribeToHost(host.id);
        }

        final offlineHosts = hm.visibleOfflineHosts;

        final allNotifications =
            ref.watch(allHostNotificationsProvider).valueOrNull ?? const [];
        final allSessions =
            ref.watch(allHostSessionsProvider).valueOrNull ?? const [];
        final pendingCount = allNotifications
            .where((n) => registry.needsAction(n))
            .length;
        final activeSessionCount = allSessions.where((s) => s.isActive).length;

        return Scaffold(
          appBar: PreferredSize(
            preferredSize: const Size.fromHeight(kToolbarHeight),
            child: GestureDetector(
              behavior: HitTestBehavior.translucent,
              onHorizontalDragEnd: _onAppBarSwipe,
              child: AppBar(
                title: _buildHostFilterChip(),
                centerTitle: true,
                actions: [
                  _buildConnectionDots(),
                  IconButton(
                    icon: const Icon(Icons.settings_outlined),
                    tooltip: 'Settings',
                    onPressed: () {
                      Navigator.of(context).push(
                        MaterialPageRoute(
                          builder: (_) => const SettingsScreen(),
                        ),
                      );
                    },
                  ),
                ],
              ),
            ),
          ),
          body: Column(
            children: [
              if (_notifPermissionDenied) _buildNotifPermissionBanner(),
              Expanded(
                child: IndexedStack(
                  index: _currentIndex,
                  children: const [
                    SessionsScreen(),
                    SchedulesScreen(),
                    DashboardScreen(),
                  ],
                ),
              ),
              ...offlineHosts.map((h) => _buildOfflineHostBanner(h)),
            ],
          ),
          floatingActionButton: switch (_currentIndex) {
            0 => FloatingActionButton(
              onPressed: _showNewSessionSheet,
              tooltip: 'New Session',
              child: const Icon(Icons.add),
            ),
            1 => FloatingActionButton(
              onPressed: () {
                final hosts = ref.read(hostManagerProvider).hosts;
                if (hosts.isEmpty) return;
                // The fork: describe it and let an agent build it, or fill in
                // the form. Most people want the first.
                showNewScheduleSheet(context, hosts.first.id);
              },
              tooltip: 'New schedule',
              child: const Icon(Icons.add_alarm),
            ),
            _ => null,
          },
          bottomNavigationBar: NavigationBar(
            selectedIndex: _currentIndex,
            onDestinationSelected: (index) =>
                setState(() => _currentIndex = index),
            destinations: [
              NavigationDestination(
                icon: Badge(
                  isLabelVisible: activeSessionCount > 0,
                  label: Text('$activeSessionCount'),
                  child: const Icon(Icons.terminal),
                ),
                selectedIcon: Badge(
                  isLabelVisible: activeSessionCount > 0,
                  label: Text('$activeSessionCount'),
                  child: const Icon(Icons.terminal),
                ),
                label: 'Sessions',
              ),
              const NavigationDestination(
                icon: Icon(Icons.schedule_outlined),
                selectedIcon: Icon(Icons.schedule),
                label: 'Schedules',
              ),
              NavigationDestination(
                icon: Badge(
                  isLabelVisible: pendingCount > 0,
                  label: Text('$pendingCount'),
                  child: const Icon(Icons.notifications_outlined),
                ),
                selectedIcon: Badge(
                  isLabelVisible: pendingCount > 0,
                  label: Text('$pendingCount'),
                  child: const Icon(Icons.notifications),
                ),
                label: 'Notifications',
              ),
            ],
          ),
        );
      },
    );
  }
}

/// What arrived, once: one section per release, newest first, with the body
/// GitHub was given rendered rather than linked to.
class _ReleaseNotesDialog extends StatelessWidget {
  const _ReleaseNotesDialog({required this.update});

  final UpdateInfo update;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AlertDialog(
      title: Text('Helios ${update.latestVersion} is out'),
      content: SizedBox(
        width: double.maxFinite,
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              // The half an app update leaves out: each paired machine runs its
              // own daemon, and that is what runs the sessions.
              Text(
                'Update the daemon on each paired machine too — that is what runs the '
                'sessions. Updating any part of it keeps running sessions alive.',
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 12),
              for (final note in update.notes) _ReleaseSection(note: note),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Got it'),
        ),
        FilledButton(
          onPressed: () {
            Navigator.pop(context);
            UpdateService.instance.install(update);
          },
          child: Text(update.canDirectInstall ? 'Install' : 'Download'),
        ),
      ],
    );
  }
}

class _ReleaseSection extends StatelessWidget {
  const _ReleaseSection({required this.note});

  final ReleaseNote note;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final date = note.publishedAt;
    return Padding(
      padding: const EdgeInsets.only(bottom: 14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.baseline,
            textBaseline: TextBaseline.alphabetic,
            children: [
              Text(note.version, style: theme.textTheme.titleSmall),
              if (date != null) ...[
                const SizedBox(width: 8),
                Text(
                  '${date.day}/${date.month}/${date.year}',
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ],
          ),
          const SizedBox(height: 4),
          if (note.body.isEmpty)
            Text(
              'No notes for this one.',
              style: theme.textTheme.bodySmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            )
          else
            MarkdownBody(data: note.body, shrinkWrap: true),
        ],
      ),
    );
  }
}
