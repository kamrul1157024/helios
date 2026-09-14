import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

class ThemeProvider extends ChangeNotifier {
  static const _key = 'theme_mode';
  static const _compactKey = 'compact_sessions';
  ThemeMode _mode = ThemeMode.system;
  bool _compactSessions = true;

  ThemeMode get mode => _mode;

  /// One line a session: which agent, how it is doing, and what it is called.
  /// On unless it has been turned off — the list is read to pick a session out
  /// of, and the rest of a card is answered by opening it.
  bool get compactSessions => _compactSessions;

  ThemeProvider() {
    _load();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final value = prefs.getString(_key);
    if (value != null) {
      _mode = ThemeMode.values.firstWhere(
        (m) => m.name == value,
        orElse: () => ThemeMode.system,
      );
    }
    _compactSessions = prefs.getBool(_compactKey) ?? true;
    notifyListeners();
  }

  Future<void> setMode(ThemeMode mode) async {
    if (_mode == mode) return;
    _mode = mode;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_key, mode.name);
  }

  Future<void> setCompactSessions(bool compact) async {
    if (_compactSessions == compact) return;
    _compactSessions = compact;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_compactKey, compact);
  }
}
