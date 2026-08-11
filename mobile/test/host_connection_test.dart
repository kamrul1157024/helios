import 'package:flutter_test/flutter_test.dart';

import 'package:helios/models/host_connection.dart';

void main() {
  group('isTailnetUrl', () {
    test('matches MagicDNS names', () {
      expect(HostConnection.isTailnetUrl('http://box.tail1234.ts.net:7655'), isTrue);
      expect(HostConnection.isTailnetUrl('https://box.tail1234.ts.net'), isTrue);
      expect(HostConnection.isTailnetUrl('HTTP://BOX.TAIL1234.TS.NET'), isTrue);
    });

    test('does not match lookalike hosts', () {
      expect(HostConnection.isTailnetUrl('https://example.com'), isFalse);
      expect(HostConnection.isTailnetUrl('https://ts.net.example.com'), isFalse);
      expect(HostConnection.isTailnetUrl('https://notts.net'), isFalse);
      expect(HostConnection.isTailnetUrl(''), isFalse);
    });

    // The check reads the host component, so a ts.net elsewhere in the URL is
    // not a tailnet address — treating it as one would tell people to switch
    // on a VPN that has nothing to do with their problem.
    test('ignores ts.net outside the host component', () {
      expect(HostConnection.isTailnetUrl('https://example.com/box.ts.net'), isFalse);
      expect(HostConnection.isTailnetUrl('https://example.com?h=box.ts.net'), isFalse);
    });
  });

  test('serverUrl survives a JSON round trip', () {
    final host = HostConnection(
      id: 'id-1',
      label: 'Laptop',
      serverUrl: 'http://box.tail1234.ts.net:7655',
      deviceId: 'device-1',
      colorIndex: 2,
      addedAt: DateTime.utc(2026, 1, 1),
    );

    final restored = HostConnection.fromJson(host.toJson());
    expect(restored.serverUrl, host.serverUrl);
    expect(restored.id, host.id);
  });
}
