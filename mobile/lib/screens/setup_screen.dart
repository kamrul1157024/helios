import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:provider/provider.dart';
import '../services/auth_service.dart';
import 'dashboard_screen.dart';

class SetupScreen extends StatefulWidget {
  final String? deepLinkKey;
  final String? deepLinkServer;

  const SetupScreen({super.key, this.deepLinkKey, this.deepLinkServer});

  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

class _SetupScreenState extends State<SetupScreen> {
  final _urlController = TextEditingController();
  bool _scanning = !kIsWeb; // QR scanner on mobile, manual input on web
  bool _loading = false;
  String? _error;
  final List<String> _statusMessages = [];

  @override
  void initState() {
    super.initState();
    if (widget.deepLinkKey != null && widget.deepLinkServer != null) {
      // Launched via helios:// deep link — auto-setup
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _doSetup(widget.deepLinkKey!, widget.deepLinkServer!);
      });
    } else if (kIsWeb) {
      // On web, auto-setup from URL hash if key is present
      _tryAutoSetupFromUrl();
    }
  }

  void _tryAutoSetupFromUrl() {
    final uri = Uri.base;
    final fragment = uri.fragment; // everything after #
    if (fragment.contains('key=')) {
      final params = Uri.splitQueryString(
        fragment.contains('?') ? fragment.split('?').last : fragment,
      );
      final key = params['key'];
      if (key != null) {
        final serverUrl = '${uri.scheme}://${uri.host}${uri.hasPort ? ':${uri.port}' : ''}';
        _doSetup(key, serverUrl);
      }
    }
  }

  @override
  void dispose() {
    _urlController.dispose();
    super.dispose();
  }

  Future<void> _handleQR(String rawValue) async {
    if (_loading) return;

    final parsed = _parseSetupUrl(rawValue);
    if (parsed == null) return;

    await _doSetup(parsed.key, parsed.serverUrl);
  }

  Future<void> _handleManualSubmit() async {
    final input = _urlController.text.trim();
    if (input.isEmpty) return;

    final parsed = _parseSetupUrl(input);
    if (parsed == null) {
      setState(() => _error = 'Invalid setup URL. Paste the URL from the terminal QR code.');
      return;
    }

    await _doSetup(parsed.key, parsed.serverUrl);
  }

  Future<void> _doSetup(String key, String serverUrl) async {
    setState(() {
      _loading = true;
      _error = null;
      _statusMessages.clear();
      _scanning = false;
    });

    _addStatus('Importing key...');
    final auth = context.read<AuthService>();

    _addStatus('Registering device...');
    _addStatus('Authenticating...');

    final result = await auth.setup(key, serverUrl);

    if (result.ok) {
      _addStatus('Setup complete');
      if (mounted) {
        Navigator.of(context).pushReplacement(
          MaterialPageRoute(builder: (_) => const DashboardScreen()),
        );
      }
    } else {
      setState(() {
        _error = result.error;
        _loading = false;
      });
    }
  }

  void _addStatus(String msg) {
    setState(() => _statusMessages.add(msg));
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return _buildProgress();
    }

    if (_error != null) {
      return _buildError();
    }

    return Scaffold(
      appBar: AppBar(
        title: const Text('helios'),
        centerTitle: true,
      ),
      body: _scanning ? _buildScanner() : _buildManualInput(),
    );
  }

  Widget _buildScanner() {
    return Column(
      children: [
        Expanded(
          flex: 3,
          child: MobileScanner(
            onDetect: (capture) {
              final barcodes = capture.barcodes;
              for (final barcode in barcodes) {
                if (barcode.rawValue != null) {
                  _handleQR(barcode.rawValue!);
                  return;
                }
              }
            },
          ),
        ),
        Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            children: [
              Text(
                'Scan the QR code from your terminal',
                style: Theme.of(context).textTheme.bodyLarge,
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 8),
              Text(
                'Run helios start in your terminal to generate a QR code',
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 16),
              TextButton(
                onPressed: () => setState(() => _scanning = false),
                child: const Text('Paste URL manually'),
              ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _buildManualInput() {
    return Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            'helios',
            style: Theme.of(context).textTheme.headlineMedium?.copyWith(
                  fontWeight: FontWeight.bold,
                ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),
          Text(
            'Device Setup',
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 32),
          TextField(
            controller: _urlController,
            decoration: const InputDecoration(
              labelText: 'Setup URL',
              hintText: 'https://.../#/setup?key=...',
              border: OutlineInputBorder(),
            ),
            style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
            maxLines: 2,
          ),
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _handleManualSubmit,
            child: const Text('Connect'),
          ),
          if (!kIsWeb) ...[
            const SizedBox(height: 16),
            TextButton(
              onPressed: () => setState(() => _scanning = true),
              child: const Text('Scan QR code instead'),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildProgress() {
    return Scaffold(
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Card(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'helios',
                    style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                          fontWeight: FontWeight.bold,
                        ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'Setting up device...',
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                  const SizedBox(height: 16),
                  ..._statusMessages.map((msg) => Padding(
                        padding: const EdgeInsets.symmetric(vertical: 2),
                        child: Row(
                          children: [
                            Text('+', style: TextStyle(
                              color: Theme.of(context).colorScheme.primary,
                              fontFamily: 'monospace',
                            )),
                            const SizedBox(width: 8),
                            Expanded(
                              child: Text(msg, style: Theme.of(context).textTheme.bodySmall?.copyWith(
                                fontFamily: 'monospace',
                                color: Theme.of(context).colorScheme.onSurfaceVariant,
                              )),
                            ),
                          ],
                        ),
                      )),
                  const SizedBox(height: 12),
                  const Center(child: CircularProgressIndicator()),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildError() {
    final isKeyClaimed = _error?.contains('already been used') == true ||
        _error?.contains('key_already_claimed') == true;

    return Scaffold(
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Card(
            child: Padding(
              padding: const EdgeInsets.all(24),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    'helios',
                    style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                          fontWeight: FontWeight.bold,
                        ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    'Setup Failed',
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                  const SizedBox(height: 16),
                  Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: Theme.of(context).colorScheme.errorContainer,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: Text(
                      _error!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.onErrorContainer,
                        fontSize: 13,
                      ),
                    ),
                  ),
                  if (isKeyClaimed) ...[
                    const SizedBox(height: 12),
                    Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(
                        color: Theme.of(context).colorScheme.surfaceContainerHighest,
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: const Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text('What happened?', style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13)),
                          SizedBox(height: 4),
                          Text(
                            'Another device already scanned this QR code. Each QR code can only be used by one device.',
                            style: TextStyle(fontSize: 13),
                          ),
                          SizedBox(height: 8),
                          Text(
                            'Run helios start in your terminal to generate a new QR code.',
                            style: TextStyle(fontSize: 13),
                          ),
                        ],
                      ),
                    ),
                  ],
                  const SizedBox(height: 16),
                  ..._statusMessages.map((msg) => Padding(
                        padding: const EdgeInsets.symmetric(vertical: 1),
                        child: Text(msg, style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          fontFamily: 'monospace',
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        )),
                      )),
                  const SizedBox(height: 16),
                  SizedBox(
                    width: double.infinity,
                    child: OutlinedButton(
                      onPressed: () {
                        setState(() {
                          _error = null;
                          _loading = false;
                          _statusMessages.clear();
                          _scanning = !kIsWeb;
                        });
                      },
                      child: const Text('Try Again'),
                    ),
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

/// Parse various setup URL formats and extract key + server URL.
class _ParsedSetup {
  final String key;
  final String serverUrl;
  _ParsedSetup(this.key, this.serverUrl);
}

_ParsedSetup? _parseSetupUrl(String input) {
  // Format: helios://setup?key=abc123&server=https://example.com
  if (input.startsWith('helios://')) {
    try {
      final uri = Uri.parse(input.replaceFirst('helios://', 'https://'));
      final key = uri.queryParameters['key'];
      final server = uri.queryParameters['server'];
      if (key != null && server != null) return _ParsedSetup(key, server);
    } catch (_) {}
  }

  // Format: https://example.com/#/setup?key=abc123
  if (input.contains('#')) {
    try {
      final hashPart = input.split('#')[1];
      final queryPart = hashPart.contains('?') ? hashPart.split('?')[1] : '';
      final params = Uri.splitQueryString(queryPart);
      final key = params['key'];
      if (key != null) {
        final serverUrl = input.split('#')[0].replaceAll(RegExp(r'/$'), '');
        return _ParsedSetup(key, serverUrl);
      }
    } catch (_) {}
  }

  // Format: https://example.com/setup?key=abc123
  try {
    final uri = Uri.parse(input);
    final key = uri.queryParameters['key'];
    if (key != null) {
      final serverUrl = '${uri.scheme}://${uri.host}${uri.hasPort ? ':${uri.port}' : ''}';
      return _ParsedSetup(key, serverUrl);
    }
  } catch (_) {}

  return null;
}
