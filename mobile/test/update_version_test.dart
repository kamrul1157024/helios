import 'package:flutter_test/flutter_test.dart';
import 'package:helios/services/update_service.dart';

void main() {
  final service = UpdateService.instance;

  group('isNewer', () {
    test('a later release is newer', () {
      expect(service.isNewer('2.15.0', '2.14.1'), isTrue);
      expect(service.isNewer('3.0.0', '2.99.99'), isTrue);
    });

    test('the running version is not an update', () {
      expect(service.isNewer('2.15.0', '2.15.0'), isFalse);
      expect(service.isNewer('2.14.0', '2.15.0'), isFalse);
    });

    test('components compare as numbers, not text', () {
      expect(service.isNewer('2.10.0', '2.9.0'), isTrue);
    });

    // The regression this test exists for: a debug build calls itself
    // "0.2.0-dev", int.parse threw on it, and the catch turned that into "you
    // are up to date" — so no dev build was ever told about a release.
    test('a suffixed build version still compares', () {
      expect(service.isNewer('2.15.0', '0.2.0-dev'), isTrue);
      expect(service.isNewer('2.15.0', '2.15.0-dev'), isFalse);
    });
  });
}
