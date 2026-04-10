import 'dart:async';
import 'dart:convert';
import 'package:app_links/app_links.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'services/auth_service.dart';
import 'services/sse_service.dart';
import 'services/notification_service.dart';
import 'screens/setup_screen.dart';
import 'screens/home_screen.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await NotificationService.instance.init();

  runApp(
    MultiProvider(
      providers: [
        ChangeNotifierProvider(create: (_) => AuthService()),
        ChangeNotifierProvider(create: (_) => SSEService()),
      ],
      child: const HeliosApp(),
    ),
  );
}

class HeliosApp extends StatelessWidget {
  const HeliosApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Helios',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        colorSchemeSeed: Colors.blue,
        useMaterial3: true,
        brightness: Brightness.light,
      ),
      darkTheme: ThemeData(
        colorSchemeSeed: Colors.blue,
        useMaterial3: true,
        brightness: Brightness.dark,
      ),
      themeMode: ThemeMode.system,
      home: const AuthGate(),
    );
  }
}

class AuthGate extends StatefulWidget {
  const AuthGate({super.key});

  @override
  State<AuthGate> createState() => _AuthGateState();
}

class _AuthGateState extends State<AuthGate> {
  late AppLinks _appLinks;
  String? _deepLinkToken;
  String? _deepLinkServer;
  StreamSubscription<Uri>? _linkSub;

  @override
  void initState() {
    super.initState();
    _appLinks = AppLinks();
    _checkAuth();
    _handleDeepLinks();
  }

  @override
  void dispose() {
    _linkSub?.cancel();
    super.dispose();
  }

  Future<void> _checkAuth() async {
    final auth = context.read<AuthService>();
    await auth.loadStoredCredentials();
  }

  Future<void> _handleDeepLinks() async {
    // Handle link that launched the app
    final initialUri = await _appLinks.getInitialLink();
    if (initialUri != null) _processUri(initialUri);

    // Handle links while app is running
    _linkSub = _appLinks.uriLinkStream.listen(_processUri);
  }

  void _processUri(Uri uri) {
    if (uri.scheme == 'helios' && uri.host == 'pair') {
      final token = uri.queryParameters['token'];
      final url = uri.queryParameters['url'];
      if (token != null && url != null) {
        setState(() {
          _deepLinkToken = token;
          _deepLinkServer = url;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<AuthService>(
      builder: (context, auth, _) {
        if (auth.isLoading) {
          return const Scaffold(
            body: Center(child: CircularProgressIndicator()),
          );
        }
        if (auth.isAuthenticated) {
          return const HomeScreen();
        }
        if (auth.isPendingApproval) {
          return const _PendingApprovalScreen();
        }
        return SetupScreen(
          deepLinkToken: _deepLinkToken,
          deepLinkServer: _deepLinkServer,
        );
      },
    );
  }
}

/// Shown when the app restores a device that is still pending approval.
class _PendingApprovalScreen extends StatefulWidget {
  const _PendingApprovalScreen();

  @override
  State<_PendingApprovalScreen> createState() => _PendingApprovalScreenState();
}

class _PendingApprovalScreenState extends State<_PendingApprovalScreen> {
  @override
  void initState() {
    super.initState();
    _resumePolling();
  }

  Future<void> _resumePolling() async {
    final auth = context.read<AuthService>();
    // Poll until approved or rejected
    while (mounted && auth.isPendingApproval) {
      await Future.delayed(const Duration(seconds: 2));
      if (!mounted) return;
      try {
        final resp = await auth.authGet('/api/auth/device/me');
        if (resp.statusCode == 200) {
          final data = jsonDecode(resp.body);
          final status = data['status'] as String?;
          if (status == 'active') {
            auth.markAuthenticated();
            return;
          }
          if (status == 'revoked') {
            auth.logout();
            return;
          }
        } else if (resp.statusCode == 401 || resp.statusCode == 403) {
          auth.logout();
          return;
        }
      } catch (_) {
        // Network error — keep trying
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Card(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    'helios',
                    style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                          fontWeight: FontWeight.bold,
                        ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'Waiting for approval...',
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                  const SizedBox(height: 24),
                  const CircularProgressIndicator(),
                  const SizedBox(height: 24),
                  Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: Theme.of(context).colorScheme.primaryContainer,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: Row(
                      children: [
                        Icon(Icons.terminal, color: Theme.of(context).colorScheme.onPrimaryContainer, size: 20),
                        const SizedBox(width: 8),
                        Expanded(
                          child: Text(
                            'Press "y" in the terminal to approve this device.',
                            style: TextStyle(
                              color: Theme.of(context).colorScheme.onPrimaryContainer,
                              fontSize: 13,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                  const SizedBox(height: 16),
                  TextButton(
                    onPressed: () {
                      context.read<AuthService>().logout();
                    },
                    child: const Text('Cancel'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
