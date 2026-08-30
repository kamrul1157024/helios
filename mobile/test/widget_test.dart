import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;
import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'package:helios/main.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/providers/theme_provider.dart';
import 'package:helios/services/host_manager.dart';

void main() {
  testWidgets('App renders', (WidgetTester tester) async {
    // HeliosApp reads from both trees while building, so the test has to supply
    // the same pair of scopes main() does — including the HostManager override,
    // which is what lets a Riverpod provider reach the per-host services.
    final hostManager = HostManager();
    addTearDown(hostManager.dispose);

    await tester.pumpWidget(
      rp.ProviderScope(
        overrides: [hostManagerProvider.overrideWithValue(hostManager)],
        child: MultiProvider(
          providers: [
            ChangeNotifierProvider.value(value: hostManager),
            ChangeNotifierProvider(create: (_) => ThemeProvider()),
          ],
          child: const HeliosApp(),
        ),
      ),
    );
  });
}
