import 'package:flutter_test/flutter_test.dart';
import 'package:provider/provider.dart';

import 'package:helios/main.dart';
import 'package:helios/providers/theme_provider.dart';
import 'package:helios/services/host_manager.dart';

void main() {
  testWidgets('App renders', (WidgetTester tester) async {
    // HeliosApp reads both providers while building, so the test has to supply
    // the same scope main() does.
    await tester.pumpWidget(
      MultiProvider(
        providers: [
          ChangeNotifierProvider(create: (_) => HostManager()),
          ChangeNotifierProvider(create: (_) => ThemeProvider()),
        ],
        child: const HeliosApp(),
      ),
    );
  });
}
