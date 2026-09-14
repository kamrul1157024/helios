import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

class ThemeProvider extends ChangeNotifier {
  static const _key = 'theme_mode';
  static const _compactKey = 'compact_sessions';
  static const _titleSizeKey = 'session_title_size';

  /// What the session title has always been drawn at, and the range either side
  /// of it: below the floor a list stops being scannable, above the ceiling two
  /// rows fill the screen.
  static const defaultTitleSize = 14.0;
  static const minTitleSize = 10.0;
  static const maxTitleSize = 24.0;

  ThemeMode _mode = ThemeMode.system;
  bool _compactSessions = true;
  double _titleSize = defaultTitleSize;

  ThemeMode get mode => _mode;

  /// One line a session: which agent, how it is doing, and what it is called.
  /// On unless it has been turned off — the list is read to pick a session out
  /// of, and the rest of a card is answered by opening it.
  bool get compactSessions => _compactSessions;

  /// The size of the name on each card. The rest of the card keeps its own
  /// sizes: the title is what the list is read for, and it is the only part
  /// worth being able to set.
  double get titleSize => _titleSize;

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
    _titleSize = (prefs.getDouble(_titleSizeKey) ?? defaultTitleSize).clamp(
      minTitleSize,
      maxTitleSize,
    );
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

  Future<void> setTitleSize(double size) async {
    final next = size.clamp(minTitleSize, maxTitleSize);
    if (_titleSize == next) return;
    _titleSize = next;
    notifyListeners();
    final prefs = await SharedPreferences.getInstance();
    await prefs.setDouble(_titleSizeKey, next);
  }
}
