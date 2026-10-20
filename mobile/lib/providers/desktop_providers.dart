import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/desktop.dart';
import 'daemon_providers.dart';

/// A host's desktop, or null when its daemon has no desktop routes.
///
/// An `AsyncNotifier` because `desktop_state` carries the whole document: the
/// event is painted as it arrives instead of being turned into a refetch, which
/// is what makes the install steps tick over live.
class DesktopNotifier extends FamilyAsyncNotifier<DesktopStatus?, String> {
  @override
  Future<DesktopStatus?> build(String hostId) async {
    final service = ref.watch(serviceProvider(hostId));
    if (service == null) return null;
    return service.desktopStatus();
  }

  /// Turns the desktop on and takes the state the host answers with.
  Future<void> enable() async {
    final service = ref.read(serviceProvider(arg));
    if (service == null) return;
    state = AsyncData(await service.enableDesktop());
  }

  /// Shows the desktop as turned off, when a write was refused for it.
  void markTurnedOff() {
    final held = state.valueOrNull;
    if (held != null) state = AsyncData(held.turnedOff());
  }

  /// Takes the body of a `desktop_state` event as the new state.
  void apply(Map<String, dynamic> body) {
    state = AsyncData(DesktopStatus.fromJson(body));
  }
}

final desktopProvider =
    AsyncNotifierProvider.family<DesktopNotifier, DesktopStatus?, String>(
      DesktopNotifier.new,
    );

/// Whether a host has a desktop to offer, for the entry points.
///
/// False until the host has answered, and false for an old daemon or one that
/// cannot be reached: an entry that leads to an error is worse than none.
final desktopAvailableProvider = Provider.family<bool, String>(
  (ref, hostId) => ref.watch(desktopProvider(hostId)).valueOrNull != null,
);

/// The phone's screen-size and DPI choice for one host, kept in preferences.
class DesktopPrefsNotifier extends FamilyNotifier<DesktopDisplayPrefs, String> {
  static String keyFor(String hostId) => 'desktop_display_$hostId';

  /// Set once anything is written, so a slow first read of the saved value
  /// does not undo it.
  bool _changed = false;

  /// Completes once the saved value is in. The viewer waits on it before it
  /// connects, so the first connect uses the saved size, not the default.
  late Future<void> loaded;

  @override
  DesktopDisplayPrefs build(String hostId) {
    loaded = _load(hostId);
    return const DesktopDisplayPrefs();
  }

  Future<void> _load(String hostId) async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(keyFor(hostId));
    if (_changed || raw == null) return;
    try {
      state = DesktopDisplayPrefs.fromJson(
        jsonDecode(raw) as Map<String, dynamic>,
      );
    } on FormatException {
      // A value from a build that wrote something else. The default stands.
    }
  }

  Future<void> save(DesktopDisplayPrefs next) async {
    _changed = true;
    state = next;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(keyFor(arg), jsonEncode(next.toJson()));
  }
}

final desktopPrefsProvider =
    NotifierProvider.family<DesktopPrefsNotifier, DesktopDisplayPrefs, String>(
      DesktopPrefsNotifier.new,
    );
