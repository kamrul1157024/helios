import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/host_connection.dart';
import 'package:helios/services/daemon_api_service.dart';
import 'package:helios/services/host_manager.dart';

HostConnection host(String id) => HostConnection(
      id: id,
      label: id,
      serverUrl: 'http://$id.local:7655',
      deviceId: 'device-$id',
      colorIndex: 0,
      addedAt: DateTime.utc(2026, 1, 1),
    );

void main() {
  group('offlineHostsForFilter', () {
    final laptop = host('laptop');
    final desktop = host('desktop');

    test('shows every offline host under the "All Hosts" filter', () {
      final visible = offlineHostsForFilter([laptop, desktop], null);
      expect(visible.map((h) => h.id), ['laptop', 'desktop']);
    });

    // The lists on screen are already filtered to the active host, so a banner
    // about another host is noise about content that is not there.
    test('hides other hosts when checked out to one', () {
      final visible = offlineHostsForFilter([laptop, desktop], 'laptop');
      expect(visible.map((h) => h.id), ['laptop']);
    });

    test('shows nothing when the active host is the one that is up', () {
      expect(offlineHostsForFilter([desktop], 'laptop'), isEmpty);
    });

    test('shows nothing when no host is offline', () {
      expect(offlineHostsForFilter([], 'laptop'), isEmpty);
      expect(offlineHostsForFilter([], null), isEmpty);
    });
  });

  group('isStreamStale', () {
    final now = DateTime.utc(2026, 8, 13, 18, 55);

    bool staleAfter(Duration silence) =>
        isStreamStale(now.subtract(silence), now);

    test('a stream inside the heartbeat window is alive', () {
      expect(staleAfter(const Duration(seconds: 29)), isFalse);
      // One missed heartbeat is not proof of death — the beat is every 30s.
      expect(staleAfter(const Duration(seconds: 74)), isFalse);
    });

    test('two missed heartbeats plus slack means dead', () {
      expect(staleAfter(const Duration(seconds: 76)), isTrue);
      // A phone that slept for an hour is the case this exists for.
      expect(staleAfter(const Duration(hours: 1)), isTrue);
    });

    test('a stream that never delivered bytes is not judged stale', () {
      expect(isStreamStale(null, now), isFalse);
    });
  });
}
