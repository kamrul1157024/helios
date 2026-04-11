import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:provider/provider.dart';
import '../models/host_connection.dart';
import '../services/host_manager.dart';
import '../services/daemon_api_service.dart';
import '../services/notification_service.dart';
import '../providers/card_registry.dart' as registry;
import 'setup_screen.dart';
import 'sessions_screen.dart';
import 'new_session_sheet.dart';
import 'dashboard_screen.dart';
import 'settings_screen.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with WidgetsBindingObserver {
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
    if (event.type != 'notification') return;
    if (event.data is! Map) {
      debugPrint('[HomeScreen] notification data is not Map: ${event.data.runtimeType}');
      return;
    }

    final data = event.data as Map;
    final type = data['type']?.toString() ?? '';
    final id = data['id']?.toString() ?? '';
    debugPrint('[HomeScreen] notification: notifType=$type id=$id');

    final host = _hm.hostById(hostId);
    final hostLabel = _hm.hosts.length > 1 ? (host?.label ?? '') : '';
    final prefix = hostLabel.isNotEmpty ? '$hostLabel — ' : '';

    // Encode hostId into notification payload for routing on tap
    final payload = jsonEncode({'hostId': hostId, 'notificationId': id});

    if (type == 'claude.permission') {
      debugPrint('[HomeScreen] showing OS permission notification');
      NotificationService.instance.showPermissionNotification(
        id: payload,
        toolName: '$prefix${data['title'] ?? 'Unknown tool'}',
        detail: data['detail']?.toString() ?? 'Permission requested',
      );
    } else if (type == 'claude.question') {
      debugPrint('[HomeScreen] showing OS question notification');
      NotificationService.instance.showNotification(
        id: payload,
        title: '${prefix}Claude has a question',
        body: data['detail']?.toString() ?? 'Answer required',
      );
    } else if (type.startsWith('claude.elicitation')) {
      debugPrint('[HomeScreen] showing OS elicitation notification');
      NotificationService.instance.showNotification(
        id: payload,
        title: '$prefix${data['title'] ?? 'Input requested'}',
        body: data['detail']?.toString() ?? 'Input required',
      );
    }
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

      // Switch UI filter to this host
      _hm.setActiveHost(hostId);
    } catch (_) {
      // Legacy payload format (just notification ID) — ignore
    }
  }

  Widget _buildOfflineHostBanner(HostConnection host) {
    final theme = Theme.of(context);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      color: theme.colorScheme.errorContainer,
      child: Row(
        children: [
          Icon(Icons.cloud_off, size: 16, color: theme.colorScheme.onErrorContainer),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              '"${host.label}" is offline',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onErrorContainer),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          TextButton(
            onPressed: () {
              _hm.serviceFor(host.id)?.reconnect();
            },
            style: TextButton.styleFrom(
              visualDensity: VisualDensity.compact,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              padding: const EdgeInsets.symmetric(horizontal: 8),
            ),
            child: Text('Retry', style: TextStyle(fontSize: 12, color: theme.colorScheme.onErrorContainer)),
          ),
          TextButton(
            onPressed: () async {
              final confirmed = await showDialog<bool>(
                context: context,
                builder: (ctx) => AlertDialog(
                  title: const Text('Remove host?'),
                  content: Text('Remove "${host.label}"? You can re-pair later.'),
                  actions: [
                    TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Cancel')),
                    FilledButton(
                      onPressed: () => Navigator.pop(ctx, true),
                      style: FilledButton.styleFrom(backgroundColor: theme.colorScheme.error),
                      child: const Text('Remove'),
                    ),
                  ],
                ),
              );
              if (confirmed == true && mounted) {
                await _hm.removeHost(host.id);
              }
            },
            style: TextButton.styleFrom(
              visualDensity: VisualDensity.compact,
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              padding: const EdgeInsets.symmetric(horizontal: 8),
            ),
            child: Text('Remove', style: TextStyle(fontSize: 12, color: theme.colorScheme.error)),
          ),
        ],
      ),
    );
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
                  style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
                ),
              ),
              // "All Hosts" option
              ListTile(
                leading: Icon(
                  _hm.activeHostId == null ? Icons.radio_button_checked : Icons.radio_button_off,
                  color: theme.colorScheme.primary,
                ),
                title: const Text('All Hosts'),
                trailing: Text(
                  '${_hm.hosts.where((h) => _hm.serviceFor(h.id)?.connected == true).length}/${_hm.hosts.length}',
                  style: TextStyle(color: theme.colorScheme.onSurfaceVariant, fontSize: 13),
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
                    isSelected ? Icons.radio_button_checked : Icons.radio_button_off,
                    color: host.color,
                  ),
                  title: Row(
                    children: [
                      Container(
                        width: 10,
                        height: 10,
                        decoration: BoxDecoration(
                          shape: BoxShape.circle,
                          color: host.color.withValues(alpha: isConnected ? 1.0 : 0.3),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(child: Text(host.label)),
                    ],
                  ),
                  subtitle: Text(
                    host.serverUrl,
                    style: TextStyle(fontSize: 11, color: theme.colorScheme.onSurfaceVariant),
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
          Text(label, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
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
              message: '${host.label}: ${isConnected ? 'connected' : 'offline'}',
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

  Widget _buildNotifPermissionBanner() {
    final theme = Theme.of(context);
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      color: theme.colorScheme.errorContainer,
      child: Row(
        children: [
          Icon(Icons.notifications_off, size: 18, color: theme.colorScheme.onErrorContainer),
          const SizedBox(width: 10),
          Expanded(
            child: Text(
              'Notifications are disabled — you won\'t hear permission requests.',
              style: TextStyle(fontSize: 12, color: theme.colorScheme.onErrorContainer),
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

        final offlineHosts = hm.offlineHosts;

        final allNotifications = hm.allNotifications;
        final allSessions = hm.allSessions;
        final pendingCount = allNotifications.where((n) => registry.needsAction(n)).length;
        final activeSessionCount = allSessions.where((s) => s.isActive).length;

        return Scaffold(
          appBar: AppBar(
            title: _buildHostFilterChip(),
            centerTitle: true,
            actions: [
              _buildConnectionDots(),
              IconButton(
                icon: const Icon(Icons.settings_outlined),
                tooltip: 'Settings',
                onPressed: () {
                  Navigator.of(context).push(
                    MaterialPageRoute(builder: (_) => const SettingsScreen()),
                  );
                },
              ),
            ],
          ),
          body: Column(
            children: [
              if (_notifPermissionDenied) _buildNotifPermissionBanner(),
              ...offlineHosts.map((h) => _buildOfflineHostBanner(h)),
              Expanded(
                child: IndexedStack(
                  index: _currentIndex,
                  children: const [
                    SessionsScreen(),
                    DashboardScreen(),
                  ],
                ),
              ),
            ],
          ),
          floatingActionButton: _currentIndex == 0
              ? FloatingActionButton(
                  onPressed: _showNewSessionSheet,
                  tooltip: 'New Session',
                  child: const Icon(Icons.add),
                )
              : null,
          bottomNavigationBar: NavigationBar(
            selectedIndex: _currentIndex,
            onDestinationSelected: (index) => setState(() => _currentIndex = index),
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
