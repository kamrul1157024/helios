/// The host picker has to reach the lists.
///
/// `hostManagerProvider` holds one long-lived manager, so a provider that
/// watches it is told nothing when the picker moves — the instance is the same
/// before and after. That looked fine for a long time because the session list
/// wraps itself in a `Consumer<HostManager>` and rebuilds for its own reasons.
/// The channels and schedules tabs do not, and they showed the host you had
/// switched away from.
library;

import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart' as rp;

import 'package:helios/models/host_connection.dart';
import 'package:helios/providers/daemon_providers.dart';
import 'package:helios/services/host_manager.dart';

HostConnection _host(String id) => HostConnection(
  id: id,
  label: id,
  serverUrl: 'http://localhost:1',
  deviceId: 'd-$id',
  colorIndex: 0,
  addedAt: DateTime(2026),
);

/// The manager with the network taken out: the picker's state and the
/// announcement it makes, which is all these providers read.
class _Hosts extends HostManager {
  _Hosts(this._hosts);

  List<HostConnection> _hosts;
  String? _active;

  @override
  List<HostConnection> get hosts => List.unmodifiable(_hosts);

  @override
  String? get activeHostId => _active;

  @override
  Future<void> setActiveHost(String? hostId) async {
    _active = hostId;
    notifyListeners();
  }

  void pair(HostConnection host) {
    _hosts = [..._hosts, host];
    notifyListeners();
  }

  /// Something the manager announces that is none of the picker's business.
  void reportConnectionChange() => notifyListeners();
}

void main() {
  late _Hosts hosts;
  late rp.ProviderContainer container;

  setUp(() {
    hosts = _Hosts([_host('a'), _host('b')]);
    container = rp.ProviderContainer(
      overrides: [hostManagerProvider.overrideWithValue(hosts)],
    );
    addTearDown(container.dispose);
  });

  List<String> visible() =>
      container.read(visibleHostsProvider).map((h) => h.id).toList();

  test('all hosts are in view until one is picked', () {
    expect(visible(), ['a', 'b']);
  });

  test('picking a host narrows the view to it', () async {
    container.listen(visibleHostsProvider, (_, _) {});
    expect(visible(), ['a', 'b']);

    await hosts.setActiveHost('b');

    expect(visible(), ['b']);
  });

  test('switching between hosts is followed, not just the first pick', () async {
    container.listen(visibleHostsProvider, (_, _) {});
    await hosts.setActiveHost('a');
    expect(visible(), ['a']);

    await hosts.setActiveHost('b');

    expect(visible(), ['b']);
  });

  test('going back to all hosts widens it again', () async {
    container.listen(visibleHostsProvider, (_, _) {});
    await hosts.setActiveHost('a');

    await hosts.setActiveHost(null);

    expect(visible(), ['a', 'b']);
  });

  test('pairing a machine puts it in view', () async {
    container.listen(visibleHostsProvider, (_, _) {});

    hosts.pair(_host('c'));

    expect(visible(), ['a', 'b', 'c']);
  });

  // The manager announces connections coming and going on the same channel.
  // Rebuilding every list on those would rebuild them on every heartbeat.
  test('a connection changing does not rebuild the view', () async {
    var rebuilds = 0;
    container.listen(visibleHostsProvider, (_, _) => rebuilds++);

    hosts.reportConnectionChange();
    hosts.reportConnectionChange();

    expect(rebuilds, 0);
  });
}
