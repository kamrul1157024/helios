import 'package:flutter_test/flutter_test.dart';
import 'package:helios/main.dart';

void main() {
  testWidgets('App renders', (WidgetTester tester) async {
    await tester.pumpWidget(const HeliosApp());
  });
}
