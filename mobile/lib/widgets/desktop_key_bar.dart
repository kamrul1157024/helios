import 'package:flutter/material.dart';

import '../utils/keysyms.dart';

/// Ctrl and Alt as the key bar holds them.
///
/// Sticky for one key: arm Ctrl, then press a letter on the phone keyboard or
/// a key on the bar, and the chord goes out with Ctrl held, after which Ctrl
/// lets go. A phone keyboard has no key that stays down, so this is the only
/// way to reach a shortcut it does not have a button for.
class DesktopModifiers extends ChangeNotifier {
  bool _ctrl = false;
  bool _alt = false;

  bool get ctrl => _ctrl;
  bool get alt => _alt;

  void toggleCtrl() {
    _ctrl = !_ctrl;
    notifyListeners();
  }

  void toggleAlt() {
    _alt = !_alt;
    notifyListeners();
  }

  /// The chord for [keysym] with whatever is armed, and lets go of it.
  List<int> take(int keysym) {
    final chord = chordFor(keysym, ctrl: _ctrl, alt: _alt);
    _release();
    return chord;
  }

  /// One chord per character, the first of them with whatever is armed.
  List<List<int>> takeText(String text) {
    if (text.isEmpty) return const [];
    final first = String.fromCharCode(text.runes.first);
    final chords = [
      ...chordsForText(first, ctrl: _ctrl, alt: _alt),
      ...chordsForText(text.substring(first.length)),
    ];
    _release();
    return chords;
  }

  void _release() {
    if (!_ctrl && !_alt) return;
    _ctrl = false;
    _alt = false;
    notifyListeners();
  }
}

/// The keys a phone keyboard lacks and a desktop cannot be used without.
class DesktopKeyBar extends StatelessWidget {
  final DesktopModifiers modifiers;
  final bool keyboardOpen;
  final VoidCallback onToggleKeyboard;

  /// Sends one chord to the desktop.
  final ValueChanged<List<int>> onChord;

  const DesktopKeyBar({
    super.key,
    required this.modifiers,
    required this.keyboardOpen,
    required this.onToggleKeyboard,
    required this.onChord,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Material(
      color: theme.colorScheme.surfaceContainerHigh,
      child: SafeArea(
        top: false,
        child: SizedBox(
          height: 44,
          child: ListenableBuilder(
            listenable: modifiers,
            builder: (context, _) => ListView(
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 6),
              children: [
                _KeyCap(
                  label: keyboardOpen ? 'hide' : 'kbd',
                  icon: keyboardOpen
                      ? Icons.keyboard_hide_outlined
                      : Icons.keyboard_outlined,
                  tooltip: keyboardOpen ? 'Hide keyboard' : 'Show keyboard',
                  onTap: onToggleKeyboard,
                ),
                _KeyCap(label: 'esc', onTap: () => _send(keyEscape)),
                _KeyCap(label: 'tab', onTap: () => _send(keyTab)),
                _KeyCap(
                  label: 'ctrl',
                  active: modifiers.ctrl,
                  onTap: modifiers.toggleCtrl,
                ),
                _KeyCap(
                  label: 'alt',
                  active: modifiers.alt,
                  onTap: modifiers.toggleAlt,
                ),
                _KeyCap(
                  label: '←',
                  icon: Icons.keyboard_arrow_left,
                  onTap: () => _send(keyLeft),
                ),
                _KeyCap(
                  label: '↑',
                  icon: Icons.keyboard_arrow_up,
                  onTap: () => _send(keyUp),
                ),
                _KeyCap(
                  label: '↓',
                  icon: Icons.keyboard_arrow_down,
                  onTap: () => _send(keyDown),
                ),
                _KeyCap(
                  label: '→',
                  icon: Icons.keyboard_arrow_right,
                  onTap: () => _send(keyRight),
                ),
                // Sent outright, rather than making the person arm Ctrl and
                // then find the letter: copy and paste are most of what a
                // phone does with a desktop's keyboard.
                _KeyCap(label: '^C', onTap: () => _shortcut(0x63)),
                _KeyCap(label: '^V', onTap: () => _shortcut(0x76)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _send(int keysym) => onChord(modifiers.take(keysym));

  /// Ctrl plus a letter, with Alt too when it is armed.
  void _shortcut(int letter) {
    final alt = modifiers.alt;
    modifiers.take(letter);
    onChord(chordFor(letter, ctrl: true, alt: alt));
  }
}

class _KeyCap extends StatelessWidget {
  final String label;
  final IconData? icon;
  final String? tooltip;
  final bool active;
  final VoidCallback onTap;

  const _KeyCap({
    required this.label,
    required this.onTap,
    this.icon,
    this.tooltip,
    this.active = false,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colour = active
        ? theme.colorScheme.onPrimaryContainer
        : theme.colorScheme.onSurface;
    final cap = Padding(
      padding: const EdgeInsets.symmetric(horizontal: 3, vertical: 6),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          alignment: Alignment.center,
          constraints: const BoxConstraints(minWidth: 44),
          padding: const EdgeInsets.symmetric(horizontal: 10),
          decoration: BoxDecoration(
            color: active
                ? theme.colorScheme.primaryContainer
                : theme.colorScheme.surface,
            borderRadius: BorderRadius.circular(6),
          ),
          child: icon != null
              ? Icon(icon, size: 18, color: colour, semanticLabel: label)
              : Text(label, style: TextStyle(fontSize: 13, color: colour)),
        ),
      ),
    );
    return tooltip == null ? cap : Tooltip(message: tooltip!, child: cap);
  }
}

/// An invisible field that holds the phone keyboard open and turns what is
/// typed into keys.
///
/// The field always holds two zero-width spaces with the cursor after them, so
/// a backspace has something to delete even when nothing has been typed — the
/// deletion is how the field learns the key was pressed. Each change is read
/// as a difference against that, sent, and the field is put back.
class DesktopKeyboardSink extends StatefulWidget {
  final FocusNode focusNode;
  final ValueChanged<String> onText;
  final VoidCallback onBackspace;

  const DesktopKeyboardSink({
    super.key,
    required this.focusNode,
    required this.onText,
    required this.onBackspace,
  });

  @override
  State<DesktopKeyboardSink> createState() => _DesktopKeyboardSinkState();
}

class _DesktopKeyboardSinkState extends State<DesktopKeyboardSink> {
  static const _sentinel = '\u200B\u200B';
  final _controller = TextEditingController();

  @override
  void initState() {
    super.initState();
    _reset();
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _reset() {
    _controller.value = const TextEditingValue(
      text: _sentinel,
      selection: TextSelection.collapsed(offset: _sentinel.length),
    );
  }

  void _onChanged(String value) {
    if (value.startsWith(_sentinel)) {
      final added = value.substring(_sentinel.length);
      if (added.isNotEmpty) widget.onText(added);
    } else if (_sentinel.startsWith(value)) {
      for (var i = value.length; i < _sentinel.length; i++) {
        widget.onBackspace();
      }
    } else {
      final typed = value.replaceAll('\u200B', '');
      if (typed.isNotEmpty) widget.onText(typed);
    }
    _reset();
  }

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 1,
      height: 1,
      child: Opacity(
        opacity: 0,
        child: TextField(
          controller: _controller,
          focusNode: widget.focusNode,
          onChanged: _onChanged,
          autocorrect: false,
          enableSuggestions: false,
          // A password keyboard commits each key as it is pressed. A text
          // keyboard holds a word back while it guesses, and the desktop
          // would see nothing until the space bar.
          keyboardType: TextInputType.visiblePassword,
          // Enter is a key, not the end of an edit: it goes to the desktop
          // as Return, and the keyboard stays where it is.
          textInputAction: TextInputAction.go,
          onSubmitted: (_) => widget.onText('\n'),
          onEditingComplete: () {},
          showCursor: false,
          enableInteractiveSelection: false,
          decoration: const InputDecoration.collapsed(hintText: ''),
        ),
      ),
    );
  }
}
