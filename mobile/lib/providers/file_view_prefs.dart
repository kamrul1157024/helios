import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// How the code viewer is laid out: wrapped or not, and at what size.
///
/// Both are a reader's habit rather than a property of one file, so they are
/// saved and every file opens the way the last one was left.
class FileViewPrefs {
  /// The range pinch moves the font through. Below 9 px a phone shows dots;
  /// above 20 px a line of code no longer fits a line of screen.
  static const minFontSize = 9.0;
  static const maxFontSize = 20.0;
  static const defaultFontSize = 12.0;

  final bool wrap;
  final double fontSize;

  const FileViewPrefs({this.wrap = false, this.fontSize = defaultFontSize});

  FileViewPrefs copyWith({bool? wrap, double? fontSize}) => FileViewPrefs(
    wrap: wrap ?? this.wrap,
    fontSize: fontSize ?? this.fontSize,
  );
}

class FileViewPrefsNotifier extends Notifier<FileViewPrefs> {
  static const _wrapKey = 'file_viewer_wrap';
  static const _fontKey = 'file_viewer_font_size';

  /// Set once the reader changes something, so a slow first read of the
  /// saved values does not undo it.
  bool _changed = false;

  @override
  FileViewPrefs build() {
    _load();
    return const FileViewPrefs();
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    if (_changed) return;
    state = FileViewPrefs(
      wrap: prefs.getBool(_wrapKey) ?? false,
      fontSize: (prefs.getDouble(_fontKey) ?? FileViewPrefs.defaultFontSize)
          .clamp(FileViewPrefs.minFontSize, FileViewPrefs.maxFontSize),
    );
  }

  Future<void> setWrap(bool wrap) async {
    _changed = true;
    state = state.copyWith(wrap: wrap);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_wrapKey, wrap);
  }

  /// Moves the size on screen without saving it, for every step of a pinch.
  void previewFontSize(double size) {
    _changed = true;
    state = state.copyWith(
      fontSize: size.clamp(
        FileViewPrefs.minFontSize,
        FileViewPrefs.maxFontSize,
      ),
    );
  }

  /// Saves the size the pinch ended on.
  Future<void> commitFontSize() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setDouble(_fontKey, state.fontSize);
  }
}

final fileViewPrefsProvider =
    NotifierProvider<FileViewPrefsNotifier, FileViewPrefs>(
      FileViewPrefsNotifier.new,
    );
