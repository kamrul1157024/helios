import 'dart:async';
import 'package:app_links/app_links.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'services/auth_service.dart';
import 'services/sse_service.dart';
import 'services/notification_service.dart';
import 'screens/setup_screen.dart';
import 'screens/dashboard_screen.dart';

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
  String? _deepLinkKey;
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
    if (uri.scheme == 'helios' && uri.host == 'setup') {
      final key = uri.queryParameters['key'];
      final server = uri.queryParameters['server'];
      if (key != null && server != null) {
        setState(() {
          _deepLinkKey = key;
          _deepLinkServer = server;
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
          return const DashboardScreen();
        }
        return SetupScreen(
          deepLinkKey: _deepLinkKey,
          deepLinkServer: _deepLinkServer,
        );
      },
    );
  }
}
