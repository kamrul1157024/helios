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

  // The dialog shows the notes for every release the reader skipped, so the list
  // it is handed is the whole feature: one short and a version's news is lost.
  group('releasesSince', () {
    final service = UpdateService.instance;
    List<Map<String, dynamic>> releases() => [
      {'tag_name': 'v2.13.0'},
      {'tag_name': 'v2.15.0'},
      {'tag_name': 'v2.14.0'},
    ];

    test('keeps what is newer, newest first', () {
      final kept = service.releasesSince(releases(), '2.13.0');
      expect(kept.map((r) => r['tag_name']), ['v2.15.0', 'v2.14.0']);
    });

    test('keeps nothing when the newest is already running', () {
      expect(service.releasesSince(releases(), '2.15.0'), isEmpty);
    });

    test('orders by version rather than by what the API returned', () {
      final shuffled = [
        {'tag_name': 'v2.9.0'},
        {'tag_name': 'v2.10.0'},
        {'tag_name': 'v2.9.5'},
      ];
      expect(
        service.releasesSince(shuffled, '2.8.0').map((r) => r['tag_name']),
        ['v2.10.0', 'v2.9.5', 'v2.9.0'],
      );
    });

    // A dev build reports "0.2.0-dev", and every published release is newer.
    test('a dev build is offered everything', () {
      expect(service.releasesSince(releases(), '0.2.0-dev').length, 3);
    });
  });
}
