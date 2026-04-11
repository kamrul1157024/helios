import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'package:provider/provider.dart';
import '../services/host_manager.dart';
import 'home_screen.dart';

class SetupScreen extends StatefulWidget {
  final String? deepLinkToken;
  final String? deepLinkServer;

  const SetupScreen({super.key, this.deepLinkToken, this.deepLinkServer});

  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

class _SetupScreenState extends State<SetupScreen> {
  final _urlController = TextEditingController();
  bool _scanning = !kIsWeb;
  bool _loading = false;
  String? _error;
  final List<String> _statusMessages = [];

  @override
  void initState() {
    super.initState();
    if (widget.deepLinkToken != null && widget.deepLinkServer != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        _doSetup(widget.deepLinkToken!, widget.deepLinkServer!);
      });
    }
  }

  @override
  void dispose() {
    _urlController.dispose();
    super.dispose();
  }

  Future<void> _handleQR(String rawValue) async {
    if (_loading) return;
    final parsed = _parsePairingUrl(rawValue);
    if (parsed == null) return;
    await _doSetup(parsed.token, parsed.serverUrl);
  }

  Future<void> _handleManualSubmit() async {
    final input = _urlController.text.trim();
    if (input.isEmpty) return;
    final parsed = _parsePairingUrl(input);
    if (parsed == null) {
      setState(() => _error = 'Invalid pairing URL. Scan the QR code from the terminal.');
      return;
    }
    await _doSetup(parsed.token, parsed.serverUrl);
  }

  Future<void> _doSetup(String token, String serverUrl) async {
    setState(() {
      _loading = true;
      _error = null;
      _statusMessages.clear();
      _scanning = false;
    });

    final hm = context.read<HostManager>();
    final hasExistingHosts = hm.hosts.isNotEmpty;

    final result = await hm.addHost(token, serverUrl, onStatus: _addStatus);

    if (result.ok) {
      _addStatus('Approved!');
      if (mounted) {
        if (hasExistingHosts) {
          // Return to existing HomeScreen
          Navigator.of(context).pop();
        } else {
          // First host — navigate to HomeScreen
          Navigator.of(context).pushReplacement(
            MaterialPageRoute(builder: (_) => const HomeScreen()),
          );
        }
      }
    } else {
      setState(() {
        _error = result.error;
        _loading = false;
      });
    }
  }

  void _addStatus(String msg) {
    if (mounted) setState(() => _statusMessages.add(msg));
  }

  @override
  Widget build(BuildContext context) {
    if (_loading) return _buildProgress();
    if (_error != null) return _buildError();

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
            style: Theme.of(context).textTheme.headlineMedium?.copyWith(fontWeight: FontWeight.bold),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),
          Text(
            'Add Host',
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 32),
          TextField(
            controller: _urlController,
            decoration: const InputDecoration(
              labelText: 'Pairing URL',
              hintText: 'helios://pair?url=...&token=...',
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
    final hm = context.watch<HostManager>();
    final isPending = hm.isPendingApproval;

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
                    style: Theme.of(context).textTheme.headlineSmall?.copyWith(fontWeight: FontWeight.bold),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    isPending ? 'Waiting for approval...' : 'Setting up device...',
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                  const SizedBox(height: 16),
                  ..._statusMessages.map((msg) => Padding(
                        padding: const EdgeInsets.symmetric(vertical: 2),
                        child: Row(
                          children: [
                            Text('+',
                                style: TextStyle(
                                    color: Theme.of(context).colorScheme.primary, fontFamily: 'monospace')),
                            const SizedBox(width: 8),
                            Expanded(
                              child: Text(msg,
                                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                                        fontFamily: 'monospace',
                                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                                      )),
                            ),
                          ],
                        ),
                      )),
                  if (isPending) ...[
                    const SizedBox(height: 16),
                    Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(
                        color: Theme.of(context).colorScheme.primaryContainer,
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: Row(
                        children: [
                          Icon(Icons.phone_android,
                              color: Theme.of(context).colorScheme.onPrimaryContainer, size: 20),
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
                  ],
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
    final isTokenExpired =
        _error?.contains('expired') == true || _error?.contains('already been used') == true;

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
                  Text('helios',
                      style: Theme.of(context).textTheme.headlineSmall?.copyWith(fontWeight: FontWeight.bold)),
                  const SizedBox(height: 4),
                  Text('Setup Failed',
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                            color: Theme.of(context).colorScheme.onSurfaceVariant,
                          )),
                  const SizedBox(height: 16),
                  Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: Theme.of(context).colorScheme.errorContainer,
                      borderRadius: BorderRadius.circular(8),
                    ),
                    child: Text(_error!,
                        style: TextStyle(color: Theme.of(context).colorScheme.onErrorContainer, fontSize: 13)),
                  ),
                  if (isTokenExpired) ...[
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
                          Text('What happened?',
                              style: TextStyle(fontWeight: FontWeight.w600, fontSize: 13)),
                          SizedBox(height: 4),
                          Text(
                              'The pairing QR code has expired or was already used. Each QR code can only be scanned once and expires after 2 minutes.',
                              style: TextStyle(fontSize: 13)),
                          SizedBox(height: 8),
                          Text(
                              'A new QR code is automatically generated in the terminal. Scan the latest one.',
                              style: TextStyle(fontSize: 13)),
                        ],
                      ),
                    ),
                  ],
                  const SizedBox(height: 16),
                  ..._statusMessages.map((msg) => Padding(
                        padding: const EdgeInsets.symmetric(vertical: 1),
                        child: Text(msg,
                            style: Theme.of(context).textTheme.bodySmall?.copyWith(
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

class _ParsedPairing {
  final String token;
  final String serverUrl;
  _ParsedPairing(this.token, this.serverUrl);
}

_ParsedPairing? _parsePairingUrl(String input) {
  if (input.startsWith('helios://')) {
    try {
      final uri = Uri.parse(input.replaceFirst('helios://', 'https://'));
      final token = uri.queryParameters['token'];
      final url = uri.queryParameters['url'];
      if (token != null && url != null) return _ParsedPairing(token, url);
    } catch (_) {}
  }
  return null;
}
