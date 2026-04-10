import 'dart:async';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import '../services/auth_service.dart';
import '../services/sse_service.dart';
import '../services/notification_service.dart';
import '../providers/card_registry.dart' as registry;
import 'setup_screen.dart';
import 'sessions_screen.dart';
import 'dashboard_screen.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with WidgetsBindingObserver {
  late SSEService _sse;
  StreamSubscription<SSEEvent>? _eventSub;
  int _currentIndex = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);

    _sse = context.read<SSEService>();
    final auth = context.read<AuthService>();
    _sse.attach(auth);
    _sse.fetchNotifications();
    _sse.fetchSessions();
    _sse.start();

    NotificationService.instance.requestPermission();
    NotificationService.instance.onAction = _handleNotificationAction;
    _eventSub = _sse.events.listen(_handleSSEEvent);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _sse.fetchNotifications();
      _sse.fetchSessions();
    }
  }

  void _handleSSEEvent(SSEEvent event) {
    if (event.type == 'notification' && event.data is Map) {
      final data = event.data as Map;
      final type = data['type']?.toString() ?? '';
      final id = data['id']?.toString() ?? '';

      if (type == 'claude.permission') {
        NotificationService.instance.showPermissionNotification(
          id: id,
          toolName: data['title']?.toString() ?? 'Unknown tool',
          detail: data['detail']?.toString() ?? 'Permission requested',
        );
      } else if (type == 'claude.question') {
        NotificationService.instance.showNotification(
          id: id,
          title: 'Claude has a question',
          body: data['detail']?.toString() ?? 'Answer required',
        );
      } else if (type.startsWith('claude.elicitation')) {
        NotificationService.instance.showNotification(
          id: id,
          title: data['title']?.toString() ?? 'Input requested',
          body: data['detail']?.toString() ?? 'Input required',
        );
      }
    }
  }

  void _handleNotificationAction(String notificationId, String action) {
    if (action == 'approve') {
      _sse.sendAction(notificationId, {'action': 'approve'});
    } else if (action == 'deny') {
      _sse.sendAction(notificationId, {'action': 'deny'});
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _eventSub?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('helios'),
        centerTitle: true,
        actions: [
          Consumer<SSEService>(
            builder: (_, sse, _) => Padding(
              padding: const EdgeInsets.only(right: 8),
              child: Icon(
                Icons.circle,
                size: 10,
                color: sse.connected ? Colors.green : Colors.grey,
              ),
            ),
          ),
          PopupMenuButton<String>(
            onSelected: (value) async {
              if (value == 'logout') {
                _sse.stop();
                final auth = context.read<AuthService>();
                final nav = Navigator.of(context);
                await auth.logout();
                if (mounted) {
                  nav.pushReplacement(
                    MaterialPageRoute(builder: (_) => const SetupScreen()),
                  );
                }
              }
            },
            itemBuilder: (_) => [
              const PopupMenuItem(value: 'logout', child: Text('Disconnect')),
            ],
          ),
        ],
      ),
      body: IndexedStack(
        index: _currentIndex,
        children: const [
          SessionsScreen(),
          DashboardScreen(),
        ],
      ),
      bottomNavigationBar: Consumer<SSEService>(
        builder: (context, sse, _) {
          final pendingCount = sse.notifications.where((n) => registry.needsAction(n)).length;
          final activeSessionCount = sse.sessions.where((s) => s.isActive).length;

          return NavigationBar(
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
          );
        },
      ),
    );
  }
}
